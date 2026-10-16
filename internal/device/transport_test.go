package device

import (
	"context"
	"errors"
	"mobile_autorun/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestTransportSwitchInvalidatesObservationAndPendingCall(t *testing.T) {
	g := NewGateway()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err == nil {
			g.ServeTransport(r.Context(), "phone", c, r.URL.Query().Get("transport"))
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dial := func(transport string) *websocket.Conn {
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"?transport="+transport, nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	wait := func(transport string) {
		for g.Transport("phone") != transport {
			select {
			case <-ctx.Done():
				t.Fatal("transport never registered")
			case <-time.After(time.Millisecond):
			}
		}
	}
	usb := dial("usb_reverse")
	defer usb.CloseNow()
	wait("usb_reverse")
	revision := g.Revision("phone")
	result := make(chan error, 1)
	go func() { _, err := g.Call(ctx, "phone", "tap", map[string]int{"x": 1, "y": 1}); result <- err }()
	var frame Frame
	if err := wsjson.Read(ctx, usb, &frame); err != nil {
		t.Fatal(err)
	} // pending action is not acknowledged
	ws := dial("ws")
	defer ws.CloseNow()
	wait("ws")
	if g.Revision("phone") <= revision {
		t.Fatal("old observation revision reused after connection switch")
	}
	if _, err := g.CallAtRevision(ctx, "phone", "tap", map[string]int{"x": 1, "y": 1}, revision); !errors.Is(err, domain.ErrScreenChanged) {
		t.Fatalf("stale action accepted on replacement: %v", err)
	}
	if err := <-result; !errors.Is(err, domain.ErrOffline) {
		t.Fatalf("pending action survived switch: %v", err)
	}
	// The old session's cleanup must not remove the replacement session.
	if !g.Online("phone") || g.Transport("phone") != "ws" {
		t.Fatal("replacement session removed")
	}
	_ = ws.CloseNow()
	for g.Online("phone") {
		select {
		case <-ctx.Done():
			t.Fatal("closed connection stayed online")
		case <-time.After(time.Millisecond):
		}
	}
	if g.Transport("phone") != "" {
		t.Fatal("offline route reported as active")
	}
}
