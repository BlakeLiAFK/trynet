package tunnel

import (
 "bytes"
 "compress/flate"
 "context"
 "io"
 "net"
 "net/http"
 "net/http/httptest"
 "testing"
)

func TestWebSocketPipeServerContextCancellation(t *testing.T) {
 ctx, cancel := context.WithCancel(context.Background())
 defer cancel()
 body, sender := io.Pipe()
 defer sender.Close()
 origin, peer := net.Pipe()
 defer peer.Close()
 done := make(chan struct{})
 go func() {
  pipeWebsocket(ctx, body, io.Discard, origin, http.NewResponseController(httptest.NewRecorder()))
  close(done)
 }()
 cancel()
 wsTestWait(t, done, "server-side context cancellation")
 if _, err := sender.Write([]byte("closed")); err == nil { t.Error("request body remains open") }
 if _, err := peer.Write([]byte("closed")); err == nil { t.Error("origin remains open") }
}

func TestWebSocketCompressionFramesPassUnmodified(t *testing.T) {
 // Real deflate bytes (RFC7692 tail removed). The proxy must preserve both
 // the RSV1 bit and payload, leaving extension negotiation to endpoints.
 payload := bytes.Repeat([]byte("compressible WebSocket text "), 8192)
 var compressed bytes.Buffer
 compressor, err := flate.NewWriter(&compressed, flate.BestSpeed)
 if err != nil { t.Fatal(err) }
 if _, err := compressor.Write(payload); err != nil { t.Fatal(err) }
 if err := compressor.Flush(); err != nil { t.Fatal(err) }
 raw := append([]byte(nil), compressed.Bytes()...)
 _ = compressor.Close()
 if !bytes.HasSuffix(raw, []byte{0,0,255,255}) { t.Fatal("invalid test deflate tail") }
 raw = raw[:len(raw)-4]
 client, url, done := wsTestTunnel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  if r.Header.Get("Sec-WebSocket-Extensions") != "permessage-deflate; client_no_context_takeover; server_no_context_takeover" { t.Error("extension offer lost") }
  h := http.Header{"Connection":{"Upgrade"},"Upgrade":{"websocket"},"Sec-Websocket-Accept":{wsTestAccept(wsTestKey)},"Sec-Websocket-Extensions":{"permessage-deflate; client_no_context_takeover; server_no_context_takeover"}}
  conn, rw := wsTestUpgrade(t,w,r,h,nil)
  if conn == nil { return }
  defer conn.Close()
  flags, masked, got, err := wsTestReadFrame(rw)
  if err != nil || flags != 0xc1 || !masked || !bytes.Equal(got,raw) { t.Errorf("compressed frame mutated: flags=%x masked=%v err=%v",flags,masked,err); return }
  _, _ = rw.Write(wsTestFrame(flags,false,got)); _ = rw.Flush()
 }))
 req, writer, _ := wsTestRequest(t,url)
 req.Header.Set("Sec-WebSocket-Extensions","permessage-deflate; client_no_context_takeover; server_no_context_takeover")
 resp, err := client.Do(req); if err != nil { t.Fatal(err) }
 defer resp.Body.Close()
 h, err := deserializeHeaders(resp.Header.Get(headerRespHeaders))
 if err != nil || h.Get("Sec-WebSocket-Extensions") != req.Header.Get("Sec-WebSocket-Extensions") { t.Fatalf("extension response lost: %v %v",h,err) }
 if _, err := writer.Write(wsTestFrame(0xc1,true,raw)); err != nil { t.Fatal(err) }
 flags,masked,got,err := wsTestReadFrame(resp.Body)
 if err != nil || flags != 0xc1 || masked || !bytes.Equal(got,raw) { t.Fatalf("compressed echo corrupted: flags=%x masked=%v err=%v",flags,masked,err) }
 // Supply the RFC7692 tail plus a final empty deflate block to decode EOF.
 reader := flate.NewReader(bytes.NewReader(append(got,0,0,255,255,1,0,0,255,255)))
 defer reader.Close()
 decoded,err := io.ReadAll(reader)
 if err != nil || !bytes.Equal(decoded,payload) { t.Fatalf("echo fails decompression: %v",err) }
 wsTestWait(t,done,"compressed stream close")
}
