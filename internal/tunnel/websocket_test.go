package tunnel

import (
 "bufio"
 "bytes"
 "context"
 "crypto/sha1"
 "crypto/tls"
 "encoding/base64"
 "encoding/binary"
 "fmt"
 "io"
 "net"
 "net/http"
 "net/http/httptest"
 "net/http/httputil"
 "strings"
 "sync/atomic"
 "testing"
 "time"
 "golang.org/x/net/http2"
)

const wsTestKey = "dGhlIHNhbXBsZSBub25jZQ=="
func wsTestAccept(key string) string {
 sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
 return base64.StdEncoding.EncodeToString(sum[:])
}

// Real TLS + HTTP/2, with a live request body, catches bugs a recorder misses.
func wsTestTunnel(t *testing.T, origin http.Handler) (*http.Client, string, <-chan struct{}) {
 t.Helper()
 local := httptest.NewServer(origin)
 t.Cleanup(local.Close)
 tun := &tunnel{origin: strings.TrimPrefix(local.URL, "http://")}
 tun.proxy = &httputil.ReverseProxy{Rewrite: tun.rewrite, ModifyResponse: modifyResponse}
 done := make(chan struct{}, 64)
 edge := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  defer func() { done <- struct{}{} }()
  if r.ProtoMajor != 2 { t.Errorf("test must exercise HTTP/2, got %s", r.Proto) }
  tun.ServeHTTP(w, r)
 }))
 edge.EnableHTTP2 = true
 if err := http2.ConfigureServer(edge.Config, &http2.Server{}); err != nil { t.Fatal(err) }
 edge.StartTLS()
 t.Cleanup(edge.Close)
 transport := &http2.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // isolated httptest certificate
 t.Cleanup(transport.CloseIdleConnections)
 return &http.Client{Transport: transport}, edge.URL, done
}

func wsTestRequest(t *testing.T, url string) (*http.Request, *io.PipeWriter, context.CancelFunc) {
 t.Helper()
 ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
 reader, writer := io.Pipe()
 req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, reader)
 if err != nil { t.Fatal(err) }
 req.Host = "public.example.test"
 req.Header.Set(headerUpgrade, "websocket")
 req.Header.Set("Sec-WebSocket-Key", wsTestKey)
 req.Header.Set("Sec-WebSocket-Protocol", "chat.v1, other")
 req.Header.Set("Origin", "https://public.example.test")
 req.Header.Set("Authorization", "Bearer test-only")
 req.Header.Set("Cookie", "session=test-only")
 req.Header.Set("Proxy-Authorization", "Basic must-not-leak")
 t.Cleanup(func() { cancel(); _ = writer.Close(); _ = reader.Close() })
 return req, writer, cancel
}

func wsTestUpgrade(t *testing.T, w http.ResponseWriter, r *http.Request, header http.Header, initial []byte) (net.Conn, *bufio.ReadWriter) {
 t.Helper()
 conn, rw, err := w.(http.Hijacker).Hijack()
 if err != nil { t.Error(err); return nil, nil }
 _ = conn.SetDeadline(time.Now().Add(15 * time.Second))
 if header == nil { header = http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}, "Sec-Websocket-Accept": {wsTestAccept(r.Header.Get("Sec-WebSocket-Key"))}} }
 _, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
 _ = header.Write(rw)
 _, _ = rw.WriteString("\r\n")
 _, _ = rw.Write(initial) // first frame in the same read as 101
 if err := rw.Flush(); err != nil { t.Error(err); _ = conn.Close(); return nil, nil }
 return conn, rw
}

func wsTestFrame(flags byte, masked bool, payload []byte) []byte {
 b := []byte{flags, 0}
 switch n := len(payload); {
 case n < 126: b[1] = byte(n)
 case n <= 65535: b[1] = 126; b = binary.BigEndian.AppendUint16(b, uint16(n))
 default: b[1] = 127; b = binary.BigEndian.AppendUint64(b, uint64(n))
 }
 if !masked { return append(b, payload...) }
 b[1] |= 0x80
 mask := []byte{0x37, 0xfa, 0x21, 0x3d}
 b = append(b, mask...)
 for i, c := range payload { b = append(b, c^mask[i%4]) }
 return b
}

func wsTestReadFrame(r io.Reader) (flags byte, masked bool, payload []byte, err error) {
 var h [2]byte
 if _, err = io.ReadFull(r, h[:]); err != nil { return }
 flags, masked = h[0], h[1]&0x80 != 0
 n := uint64(h[1] & 127)
 var length [8]byte
 if n == 126 { _, err = io.ReadFull(r, length[:2]); n = uint64(binary.BigEndian.Uint16(length[:2]))
 } else if n == 127 { _, err = io.ReadFull(r, length[:]); n = binary.BigEndian.Uint64(length[:]) }
 if err != nil { return }
 if n > 8<<20 { err = fmt.Errorf("test frame too large: %d", n); return }
 var mask [4]byte
 if masked { if _, err = io.ReadFull(r, mask[:]); err != nil { return } }
 payload = make([]byte, int(n))
 _, err = io.ReadFull(r, payload)
 if masked { for i := range payload { payload[i] ^= mask[i%4] } }
 return
}

func wsTestEcho(t *testing.T) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  if r.URL.Path == "/health" { _, _ = io.WriteString(w, "healthy"); return }
  for key, want := range map[string]string{"Upgrade": "websocket", "Connection": "Upgrade", "Sec-WebSocket-Version": "13", "Authorization": "Bearer test-only", "Cookie": "session=test-only", "Origin": "https://public.example.test"} {
   if r.Header.Get(key) != want { t.Errorf("origin %s = %q, want %q", key, r.Header.Get(key), want) }
  }
  if r.Host != "public.example.test" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
   t.Errorf("invalid origin handshake: host=%s length=%d encoding=%v", r.Host, r.ContentLength, r.TransferEncoding)
  }
  if r.Header.Get(headerUpgrade) != "" || r.Header.Get("Proxy-Authorization") != "" { t.Error("internal/proxy credentials leaked to origin") }
  header := http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}, "Sec-Websocket-Accept": {wsTestAccept(r.Header.Get("Sec-WebSocket-Key"))}, "Sec-Websocket-Protocol": {"chat.v1"}, "Set-Cookie": {"a=1", "b=2"}}
  conn, rw := wsTestUpgrade(t, w, r, header, wsTestFrame(0x81, false, []byte("push:"+r.RequestURI)))
  if conn == nil { return }
  defer conn.Close()
  for {
   flags, masked, payload, err := wsTestReadFrame(rw)
   if err != nil { return }
   if !masked { t.Error("client frame masking lost"); return }
   if flags&15 == 9 { flags = 0x8a }
   if _, err = rw.Write(wsTestFrame(flags, false, payload)); err != nil { return }
   if err = rw.Flush(); err != nil || flags&15 == 8 { return }
  }
 })
}

func wsTestOpen(t *testing.T, client *http.Client, url string) (*http.Response, *io.PipeWriter, context.CancelFunc) {
 t.Helper()
 req, writer, cancel := wsTestRequest(t, url)
 resp, err := client.Do(req)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func() { _ = resp.Body.Close() })
 if resp.StatusCode != http.StatusOK { t.Fatalf("upgrade status = %d, want 200", resp.StatusCode) }
 // After RoundTrip returned, cancelling a context with an idle custom pipe
 // alone doesn't emit RST_STREAM. Closing the response models a real edge
 // stream cancellation, rather than leaving the test client silently open.
 return resp, writer, func() { cancel(); _ = resp.Body.Close() }
}

func wsTestWait(t *testing.T, done <-chan struct{}, description string) {
 t.Helper()
 select { case <-done: case <-time.After(3 * time.Second): t.Fatal("not released: " + description) }
}

func TestWebSocketEchoOverHTTP2(t *testing.T) {
 client, url, done := wsTestTunnel(t, wsTestEcho(t))
 path := "/echo/a%2Fb?q=hello%20world"
 resp, writer, _ := wsTestOpen(t, client, url+path)
 headers, err := deserializeHeaders(resp.Header.Get(headerRespHeaders))
 if err != nil || headers.Get("Sec-WebSocket-Accept") != wsTestAccept(wsTestKey) || headers.Get("Sec-WebSocket-Protocol") != "chat.v1" || len(headers.Values("Set-Cookie")) != 2 {
  t.Fatalf("handshake serialization: %v, %v", headers, err)
 }
 if resp.Header.Get("Upgrade") != "" || resp.Header.Get("Connection") != "" || resp.Header.Get("Content-Length") != "" { t.Fatal("upgrade headers must be serialized, not sent as HTTP/2 headers") }
 _, _, first, err := wsTestReadFrame(resp.Body)
 if err != nil || string(first) != "push:"+path { t.Fatalf("buffered server-first frame/path lost: %q %v", first, err) }
 cases := []struct { name string; flags byte; payload []byte }{
  {"empty", 0x81, nil}, {"utf8", 0x81, []byte("你好，WebSocket 👋")},
  {"binary-16bit", 0x82, bytes.Repeat([]byte{0, 1, 0xff, 0xfe}, 64)},
  {"binary-1MiB", 0x82, bytes.Repeat([]byte{0, 0xff, 3, 7}, 256*1024)},
  {"fragment-start", 0x01, []byte("part one")}, {"fragment-ping", 0x89, []byte("ping")},
  {"fragment-middle", 0x00, []byte("part two")}, {"fragment-end", 0x80, []byte("part three")},
  {"close-code-reason", 0x88, append([]byte{3, 232}, []byte("done")...)},
 }
 for _, test := range cases {
  t.Run(test.name, func(t *testing.T) {
   errCh := make(chan error, 1)
   go func() { _, err := writer.Write(wsTestFrame(test.flags, true, test.payload)); errCh <- err }()
   flags, masked, data, err := wsTestReadFrame(resp.Body)
   wantFlags := test.flags
   if wantFlags == 0x89 { wantFlags = 0x8a }
   if err != nil || flags != wantFlags || masked || !bytes.Equal(data, test.payload) { t.Errorf("frame flags=%x masked=%v bytes=%d err=%v", flags, masked, len(data), err) }
   if err := <-errCh; err != nil { t.Fatal(err) }
  })
 }
 wsTestWait(t, done, "handler after close frame")
}

func TestWebSocketOriginRejections(t *testing.T) {
 for _, status := range []int{200, 401, 403, 404, 500} {
  t.Run(fmt.Sprint(status), func(t *testing.T) {
   client, url, done := wsTestTunnel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("WWW-Authenticate", "Bearer realm=test")
    w.Header().Add("Set-Cookie", "a=1"); w.Header().Add("Set-Cookie", "b=2")
    w.WriteHeader(status); _, _ = io.WriteString(w, "not upgraded")
   }))
   req, _, _ := wsTestRequest(t, url+"/denied")
   resp, err := client.Do(req)
   if err != nil { t.Fatal(err) }
   defer resp.Body.Close()
   body, err := io.ReadAll(resp.Body)
   h, decodeErr := deserializeHeaders(resp.Header.Get(headerRespHeaders))
   if err != nil || decodeErr != nil || resp.StatusCode != status || string(body) != "not upgraded" || len(h.Values("Set-Cookie")) != 2 || h.Get("WWW-Authenticate") != "Bearer realm=test" {
    t.Fatalf("rejection not preserved: %d %q %v %v %v", resp.StatusCode, body, h, err, decodeErr)
   }
   wsTestWait(t, done, "rejection handler")
  })
 }
}

func TestWebSocketInvalidOriginHandshake(t *testing.T) {
 for _, headerName := range []string{"Sec-WebSocket-Accept", "Upgrade", "Connection", "Sec-WebSocket-Protocol"} {
  t.Run(headerName, func(t *testing.T) {
   client, url, _ := wsTestTunnel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    h := http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}, "Sec-Websocket-Accept": {wsTestAccept(wsTestKey)}}
    h.Set(headerName, "invalid")
    conn, _ := wsTestUpgrade(t, w, r, h, nil)
    if conn != nil { defer conn.Close(); _, _ = io.Copy(io.Discard, conn) }
   }))
   req, _, _ := wsTestRequest(t, url)
   resp, err := client.Do(req)
   if err != nil { t.Fatal(err) }
   defer resp.Body.Close()
   if resp.StatusCode != 502 { t.Fatalf("got %d, want 502", resp.StatusCode) }
  })
 }
}

func TestWebSocketInvalidRequestsAndUnsupportedProtocols(t *testing.T) {
 var calls atomic.Int32
 client, url, _ := wsTestTunnel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
 cases := []struct { name, method, key, upgrade, tcp string; status int }{
  {"missing-key", "GET", "", "websocket", "", 400}, {"bad-key", "GET", "not-base64", "websocket", "", 400},
  {"short-key", "GET", "YWJj", "websocket", "", 400}, {"post", "POST", wsTestKey, "websocket", "", 405},
  {"tcp", "GET", wsTestKey, "", "tcp", 501}, {"websocket-with-tcp-marker", "GET", wsTestKey, "websocket", "tcp", 501},
  {"config", "GET", wsTestKey, "update-configuration", "", 501},
 }
 for _, test := range cases {
  t.Run(test.name, func(t *testing.T) {
   req, _, _ := wsTestRequest(t, url)
   req.Method = test.method
   req.Header.Set("Sec-WebSocket-Key", test.key); req.Header.Set(headerUpgrade, test.upgrade); req.Header.Set(headerTCPProxySrc, test.tcp)
   resp, err := client.Do(req)
   if err != nil { t.Fatal(err) }
   defer resp.Body.Close()
   if resp.StatusCode != test.status { t.Errorf("got %d, want %d", resp.StatusCode, test.status) }
  })
 }
 if calls.Load() != 0 { t.Fatal("invalid request reached origin") }
}

func TestWebSocketClientCancelReleasesOrigin(t *testing.T) {
 closed := make(chan struct{})
 client, url, done := wsTestTunnel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  defer close(closed)
  conn, rw := wsTestUpgrade(t, w, r, nil, nil)
  if conn != nil { defer conn.Close(); _, _ = io.Copy(io.Discard, rw) }
 }))
 _, _, cancel := wsTestOpen(t, client, url)
 cancel()
 wsTestWait(t, done, "cancelled handler"); wsTestWait(t, closed, "cancelled origin socket")
}

func TestWebSocketOriginCloseReleasesHandler(t *testing.T) {
 client, url, done := wsTestTunnel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  conn, _ := wsTestUpgrade(t, w, r, nil, nil); if conn != nil { _ = conn.Close() }
 }))
 resp, _, _ := wsTestOpen(t, client, url)
 if _, err := io.ReadAll(resp.Body); err != nil { t.Fatal(err) }
 wsTestWait(t, done, "origin-closed handler")
}

func TestWebSocketHandshakeCancellation(t *testing.T) {
 started, closed := make(chan struct{}), make(chan struct{})
 client, url, done := wsTestTunnel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(closed) }))
 req, _, cancel := wsTestRequest(t, url)
 result := make(chan error, 1)
 go func() { resp, err := client.Do(req); if resp != nil { _ = resp.Body.Close() }; result <- err }()
 wsTestWait(t, started, "origin request start"); cancel()
 if err := <-result; err == nil { t.Error("cancelled request succeeded") }
 wsTestWait(t, done, "cancelled handshake"); wsTestWait(t, closed, "cancelled origin handshake")
}

func TestWebSocketConcurrentWithHTTP(t *testing.T) {
 client, url, _ := wsTestTunnel(t, wsTestEcho(t))
 for i := 0; i < 16; i++ {
  resp, writer, _ := wsTestOpen(t, client, fmt.Sprintf("%s/echo/%d", url, i))
  _, _, _, err := wsTestReadFrame(resp.Body); if err != nil { t.Fatal(err) }
  message := []byte(fmt.Sprintf("connection-%d", i))
  if _, err = writer.Write(wsTestFrame(0x81, true, message)); err != nil { t.Fatal(err) }
  _, _, got, err := wsTestReadFrame(resp.Body)
  if err != nil || !bytes.Equal(got, message) { t.Fatalf("stream mixup %d: %q %v", i, got, err) }
 }
 resp, err := client.Get(url+"/health"); if err != nil { t.Fatal(err) }
 defer resp.Body.Close()
 body, err := io.ReadAll(resp.Body)
 if err != nil || string(body) != "healthy" || resp.StatusCode != 200 { t.Fatalf("HTTP blocked by open WebSockets: %q %v", body, err) }
}

func TestWebSocketCancelUnderBackpressure(t *testing.T) {
 closed := make(chan struct{})
 client, url, done := wsTestTunnel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  defer close(closed)
  conn, rw := wsTestUpgrade(t, w, r, nil, nil); if conn == nil { return }
  defer conn.Close()
  block := bytes.Repeat([]byte("x"), 32*1024)
  for i := 0; i < 2048; i++ { if _, err := rw.Write(block); err != nil { return }; if err := rw.Flush(); err != nil { return } }
 }))
 _, _, cancel := wsTestOpen(t, client, url)
 time.Sleep(200 * time.Millisecond) // intentionally leave HTTP/2 receive window full
 cancel()
 wsTestWait(t, done, "backpressured writer"); wsTestWait(t, closed, "backpressured origin")
}
