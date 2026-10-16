package mediaindex

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"mobile_autorun/internal/device"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Exercise capability negotiation over the real Gateway/WebSocket framing,
// including the exact unknown-method reply emitted by old Portal versions.
func TestGatewayMediaProtocol(t *testing.T) {
	for _, native := range []bool{true, false} {
		name := "old_portal"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			gateway := device.NewGateway()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				gateway.Serve(ctx, "phone", c)
			}))
			defer server.Close()
			phone, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer phone.CloseNow()
			go func() {
				for {
					var f device.Frame
					if wsjson.Read(ctx, phone, &f) != nil {
						return
					}
					response := device.Frame{ID: f.ID, Status: "success"}
					if !native {
						response.Status = "error"
						response.Error, _ = json.Marshal("Unknown method: " + f.Method)
					} else if f.Method == "media/capabilities" {
						response.Result = []byte(nativeCaps)
					} else {
						var q Request
						if json.Unmarshal(f.Params, &q) != nil {
							return
						}
						response.Result = reply(q.Paths, "external_primary")
					}
					if wsjson.Write(ctx, phone, response) != nil {
						return
					}
				}
			}()
			for !gateway.Online("phone") {
				select {
				case <-ctx.Done():
					t.Fatal("connection not registered")
				case <-time.After(time.Millisecond):
				}
			}
			legacy := &fakeCaller{online: true, call: func(_ string, p any) (json.RawMessage, error) { return reply(p.(Request).Paths, "external"), nil }}
			out, err := (Client{Portal: gateway, Legacy: legacy}).Index(ctx, "phone", []string{testPath})
			expected := "usb_legacy"
			if native {
				expected = "portal_native"
			}
			if err != nil || out.Transport != expected {
				t.Fatalf("%+v %v", out, err)
			}
			if native && len(legacy.methods) != 0 {
				t.Fatal("USB used for native phone")
			}
		})
	}
}
