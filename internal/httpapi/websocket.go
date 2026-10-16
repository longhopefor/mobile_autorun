package httpapi

import (
	"bufio"
	"errors"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"net"
	"net/http"
)

// coder/websocket flushes Gin's 101 header before hijacking. Gin 1.11 rejects
// hijacking once headers are flushed; delegate the already-approved upgrade
// to the original HTTP writer, while preserving Gin's written status.
type upgradeWriter struct{ gin.ResponseWriter }

func (w upgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	raw, ok := w.ResponseWriter.(interface{ Unwrap() http.ResponseWriter })
	if !ok {
		return nil, nil, errors.New("websocket_writer_not_unwrappable")
	}
	h, ok := raw.Unwrap().(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("websocket_hijack_unavailable")
	}
	return h.Hijack()
}
func acceptSocket(w gin.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return websocket.Accept(upgradeWriter{w}, r, nil)
}
