package device

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"mobile_autorun/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestModelErrorCodesAreWhitelisted(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		login         bool
		code          string
	}{
		{"known", `{"code":"workbuddy_login_required","message":"secret-fixture-do-not-forward"}`, true, ""},
		{"unknown", `{"code":"secret-fixture-do-not-forward","message":"secret-fixture-do-not-forward"}`, false, ""},
		{"raw", `"secret-fixture-do-not-forward"`, false, ""},
		{"transient", `{"code":"model_network_error","message":"secret-fixture-do-not-forward"}`, false, "model_network_error"},
		{"invalid_decision", `{"code":"model_invalid_decision","message":"secret-fixture-do-not-forward"}`, false, "model_invalid_decision"},
		{"auth", `{"code":"model_auth_required","message":"secret-fixture-do-not-forward"}`, false, "model_auth_required"},
		{"old_connector", `{"code":"cli_call_failed","message":"secret-fixture-do-not-forward"}`, false, "model_cli_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			g := NewGateway()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				g.Serve(r.Context(), "connector", conn)
			}))
			defer server.Close()
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			for !g.Online("connector") {
				select {
				case <-ctx.Done():
					t.Fatal("connector did not connect")
				case <-time.After(time.Millisecond):
				}
			}
			result := make(chan error, 1)
			go func() { _, err := g.Call(ctx, "connector", "model/next", map[string]any{}); result <- err }()
			var frame Frame
			if err := wsjson.Read(ctx, conn, &frame); err != nil {
				t.Fatal(err)
			}
			if err := wsjson.Write(ctx, conn, Frame{ID: frame.ID, Status: "error", Error: json.RawMessage(tc.payload)}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err == nil || errors.Is(err, domain.ErrWorkBuddyLoginRequired) != tc.login || strings.Contains(err.Error(), "secret-fixture") {
					t.Fatalf("unsafe/error mismatch: %v", err)
				}
				if tc.code != "" {
					var diagnostic *domain.ModelError
					if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
						t.Fatalf("diagnostic mismatch: %v", err)
					}
				} else if !tc.login && err.Error() != "remote_call_failed" {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("no error response")
			}
		})
	}
}
