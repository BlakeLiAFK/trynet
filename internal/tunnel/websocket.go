package tunnel

import (
	"context"
	"crypto/sha1" // RFC 6455 handshake, not a password or integrity hash.
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"
)

// The edge carries RFC 6455 frames in a full-duplex HTTP/2 request/response
// body. Only the loopback origin uses an HTTP/1.1 Upgrade handshake. Do NOT
// give the edge stream to ReverseProxy: HTTP/2 has neither 101 nor Hijacker.
var websocketTransport = &http.Transport{
	Proxy:                 nil, // The configured origin is local; never use an environment proxy.
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	ResponseHeaderTimeout: 15 * time.Second,
	IdleConnTimeout:       90 * time.Second,
	DisableCompression:    true,
	MaxResponseHeaderBytes: 1 << 20,
}

var websocketBuffers = sync.Pool{New: func() any {
	b := make([]byte, 32*1024)
	return &b
}}

func (t *tunnel) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		websocketError(w, http.StatusMethodNotAllowed, "WebSocket requires GET")
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 16 {
		websocketError(w, http.StatusBadRequest, "invalid WebSocket key")
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		websocketError(w, http.StatusInternalServerError, "streaming response unavailable")
		return
	}

	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.Proto, out.ProtoMajor, out.ProtoMinor = "HTTP/1.1", 1, 1
	// r.Body is the frame stream, NOT a request payload. Sending it as the
	// origin handshake body deadlocks (the client waits for the handshake).
	out.Body, out.GetBody, out.Trailer = nil, nil, nil
	out.ContentLength, out.TransferEncoding = 0, nil
	stripWebsocketHopHeaders(out.Header)
	for _, name := range []string{headerUpgrade, headerTCPProxySrc, "Content-Length", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		out.Header.Del(name)
	}
	t.rewrite(&httputil.ProxyRequest{In: r, Out: out})
	// Cloudflare removes hop-by-hop headers before sending the HTTP/2 stream.
	out.Header.Set("Connection", "Upgrade")
	out.Header.Set("Upgrade", "websocket")
	out.Header.Set("Sec-WebSocket-Version", "13")

	resp, err := websocketTransport.RoundTrip(out)
	if err != nil {
		websocketError(w, http.StatusBadGateway, "WebSocket origin unavailable")
		return
	}
	defer resp.Body.Close()
	var origin io.ReadWriteCloser
	if resp.StatusCode == http.StatusSwitchingProtocols {
		var ok bool
		origin, ok = resp.Body.(io.ReadWriteCloser)
		if !ok || !validWebsocketResponse(resp.Header, key, out.Header) {
			websocketError(w, http.StatusBadGateway, "invalid WebSocket origin handshake")
			return
		}
		// HTTP/2 represents a successful upgrade as 200. The serialized
		// Connection/Upgrade/Accept headers tell the edge to emit 101 publicly.
		resp.StatusCode = http.StatusOK
		resp.Header.Del("Content-Length")
	} else {
		stripWebsocketHopHeaders(resp.Header)
	}
	_ = modifyResponse(resp)
	for name, values := range resp.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(resp.StatusCode)
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}
	writer := websocketFlusher{w: w, controller: controller}
	if origin == nil {
		// Preserve origin rejections (e.g. 401/403/404), including their bodies
		// and headers. Never turn an ordinary HTTP response into an upgrade.
		_, _ = io.Copy(writer, resp.Body)
		return
	}
	pipeWebsocket(r.Context(), r.Body, writer, origin, controller)
}

func stripWebsocketHopHeaders(h http.Header) {
	for _, value := range h.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			h.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		h.Del(name)
	}
}

func websocketHeaderToken(h http.Header, name, token string) bool {
	for _, value := range h.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func validWebsocketResponse(h http.Header, key string, request http.Header) bool {
	digest := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if !websocketHeaderToken(h, "Connection", "upgrade") ||
		!strings.EqualFold(h.Get("Upgrade"), "websocket") ||
		h.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(digest[:]) {
		return false
	}
	// Subprotocol names are case-sensitive (unlike Connection tokens).
	if selected := h.Get("Sec-WebSocket-Protocol"); selected != "" {
		for _, offered := range request.Values("Sec-WebSocket-Protocol") {
			for _, token := range strings.Split(offered, ",") {
				if strings.TrimSpace(token) == selected {
					return true
				}
			}
		}
		return false
	}
	return true
}

func websocketError(w http.ResponseWriter, status int, message string) {
	h := http.Header{"Content-Type": {"text/plain; charset=utf-8"}, "X-Content-Type-Options": {"nosniff"}}
	w.Header().Set(headerRespHeaders, serializeHeaders(h))
	w.Header().Set(headerRespMeta, `{"src":"cloudflared"}`)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, message+"\n")
}

type websocketFlusher struct {
	w          io.Writer
	controller *http.ResponseController
}

func (w websocketFlusher) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if err == nil {
		err = w.controller.Flush()
	}
	return n, err
}

func pipeWebsocket(ctx context.Context, body io.ReadCloser, writer io.Writer, origin io.ReadWriteCloser, controller *http.ResponseController) {
	// Two bounded buffers per active stream, independent of message size.
	// Frames, masking, fragmentation, compression, ping/pong and close codes
	// are passed byte-for-byte; endpoints retain protocol/auth ownership.
	done := make(chan bool, 2)
	copyDirection := func(dst io.Writer, src io.Reader, toClient bool) {
		buf := websocketBuffers.Get().(*[]byte)
		defer websocketBuffers.Put(buf)
		_, _ = io.CopyBuffer(dst, src, *buf)
		done <- toClient
	}
	go copyDirection(origin, body, false)
	go copyDirection(writer, origin, true)
	remaining, writerDone := 2, false
	select {
	case writerDone = <-done:
		remaining--
	case <-ctx.Done():
	}
	// A client that no longer reads can block the HTTP/2 flow-control window.
	// A write deadline wakes that goroutine on cancellation/client EOF. Do not
	// set it after the origin's final flush: that would reset a valid close.
	if !writerDone {
		_ = controller.SetWriteDeadline(time.Now())
	}
	_ = origin.Close()
	_ = body.Close()
	for ; remaining > 0; remaining-- {
		<-done
	}
	// Both copies have stopped before returning; no ResponseWriter use after
	// handler completion and no idle goroutine left holding the origin socket.
}
