package httpapi

import (
	"context"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/gin-gonic/gin"
	"mobile_autorun/internal/device"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGinWebSocketUpgradeAndRPC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := device.NewGateway()
	r := gin.New()
	r.GET("/connect", func(c *gin.Context) {
		conn, e := acceptSocket(c.Writer, c.Request)
		if e != nil {
			t.Error(e)
			return
		}
		g.Serve(context.WithoutCancel(c.Request.Context()), "peer", conn)
	})
	s := httptest.NewServer(r)
	defer s.Close()
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	c, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(s.URL, "http")+"/connect", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.CloseNow()
	go func() {
		var f device.Frame
		if wsjson.Read(ctx, c, &f) == nil {
			_ = wsjson.Write(ctx, c, device.Frame{ID: f.ID, Status: "success", Result: []byte(`"ok"`)})
		}
	}()
	// Dial can finish just before Serve registers the peer.
	for !g.Online("peer") {
		select {
		case <-ctx.Done():
			t.Fatal("peer never registered")
		case <-time.After(time.Millisecond):
		}
	}
	b, e := g.Call(ctx, "peer", "version", map[string]any{})
	if e != nil || string(b) != `"ok"` {
		t.Fatalf("RPC: %s %v", b, e)
	}
}
