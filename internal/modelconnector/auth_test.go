package modelconnector

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/model"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWorkBuddyAuthenticationNoticeStopsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX fixture executable")
	}
	for _, notice := range []string{"Authentication required. Please use /login command to sign in to your account", "Authentication failed. Please use /login command to sign in to your account"} {
		t.Run(strings.Fields(notice)[1], func(t *testing.T) {
			event, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": notice}}}})
			script := filepath.Join(t.TempDir(), "cli.sh")
			scriptText := "#!/bin/sh\nprintf '%s\\n' '" + string(event) + "'\nsleep 30\n"
			if strings.Contains(notice, "Authentication failed") {
				// The CLI leader exits; the descendant keeps both pipes open.
				scriptText = "#!/bin/sh\nsleep 30 &\nprintf '%s\\n' '" + string(event) + "'\nexit 0\n"
			}
			if err := os.WriteFile(script, []byte(scriptText), 0700); err != nil {
				t.Fatal(err)
			}
			var frame bytes.Buffer
			if err := png.Encode(&frame, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			start := time.Now()
			cli := CLI{Config: Config{WorkBuddyNode: "/bin/sh", WorkBuddyEntry: script}}
			result, err := cli.Next(ctx, model.Request{Profile: domain.Profile{Provider: "workbuddy_cli"}, ObservationID: "fixture", Image: base64.StdEncoding.EncodeToString(frame.Bytes())})
			if !errors.Is(err, domain.ErrWorkBuddyLoginRequired) || result.Decision.Tool != "" || time.Since(start) > 3*time.Second {
				t.Fatalf("err=%v decision=%+v duration=%v", err, result.Decision, time.Since(start))
			}
		})
	}
}

func TestWorkBuddyAuthStreamHandlesFragmentsAndIgnoresOtherMessages(t *testing.T) {
	cancelled := 0
	w := workBuddyOutput{buffer: &limitedBuffer{max: 4096}, cancel: func() { cancelled++ }}
	user := `{"type":"user","message":{"content":[{"type":"text","text":"Authentication required. Please use /login command"}]}}` + "\n"
	if _, err := w.Write([]byte(user)); err != nil {
		t.Fatal(err)
	}
	if cancelled != 0 {
		t.Fatal("user echo was treated as authentication failure")
	}
	parts := []string{`{"type":"assistant","message":{"content":[{"type":"te`, `xt","text":"Authentication required. Please use /login command"}]}}`, "\n"}
	for i, p := range parts {
		if _, err := w.Write([]byte(p)); err != nil {
			t.Fatal(err)
		}
		if i < len(parts)-1 && cancelled != 0 {
			t.Fatal("incomplete event was interpreted")
		}
	}
	if cancelled != 1 || !errors.Is(w.failure, domain.ErrWorkBuddyLoginRequired) {
		t.Fatalf("cancelled=%d failure=%v", cancelled, w.failure)
	}
}
