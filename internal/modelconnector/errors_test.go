package modelconnector

import (
	"bytes"
	"context"
	"encoding/base64"
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
)

func TestCLIDiagnosticsUseFixedCodesOnly(t *testing.T) {
	for _, tc := range []struct {
		message, code string
		retry         bool
	}{
		{"stream disconnected before completion", "model_network_error", true},
		{"429 Too Many Requests", "model_rate_limited", true},
		{"401 Unauthorized", "model_auth_required", false},
		{"You exceeded your current quota", "model_quota_exhausted", false},
		{"unknown failure secret-fixture", "model_cli_failed", false},
	} {
		for _, event := range []string{`{"type":"error","message":"` + tc.message + ` secret-fixture"}`, `{"type":"turn.failed","error":{"message":"` + tc.message + ` secret-fixture"}}`} {
			e := classifyCLIError([]byte(event), nil)
			if e.Code != tc.code || e.Retryable != tc.retry || strings.Contains(e.Error(), "secret-fixture") {
				t.Fatalf("unsafe classifier: %v", e)
			}
		}
	}
	// A successful assistant message containing these words is not diagnostic.
	if e := classifyCLIError([]byte(`{"type":"item.completed","item":{"text":"401 Unauthorized"}}`), nil); e.Code != "model_cli_failed" {
		t.Fatal(e)
	}
}

func TestRealCLIProcessFailureAndMalformedDecision(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures")
	}
	for _, tc := range []struct{ name, script, code string }{
		{"network_exit", "printf '%s\\n' '{\"type\":\"turn.failed\",\"error\":{\"message\":\"stream disconnected secret-fixture\"}}'\nexit 1\n", "model_network_error"},
		{"authentication_exit", "printf '%s\\n' '401 Unauthorized secret-fixture' >&2\nexit 1\n", "model_auth_required"},
		{"invalid_decision", "while [ \"$1\" != \"--output-last-message\" ]; do shift; done\nshift\nprintf '%s' '{\"tool\":\"phone_action\",\"observation_id\":\"fixture\",\"reason\":\"test\",\"action\":{\"kind\":\"tap\",\"x\":2000}}' > \"$1\"\nprintf '%s\\n' '{\"type\":\"turn.completed\"}'\n", "model_invalid_decision"},
		{"missing_decision", "printf '%s\\n' '{\"type\":\"turn.completed\"}'\n", "model_output_missing"},
		{"host_tool", "printf '%s\\n' '{\"type\":\"item.completed\",\"item\":{\"type\":\"command_execution\"}}' '{\"type\":\"turn.completed\"}'\n", "model_host_tool_rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cli")
			if e := os.WriteFile(path, []byte("#!/bin/sh\n"+tc.script), 0700); e != nil {
				t.Fatal(e)
			}
			var frame bytes.Buffer
			if e := png.Encode(&frame, image.NewRGBA(image.Rect(0, 0, 16, 16))); e != nil {
				t.Fatal(e)
			}
			_, e := (&CLI{Config: Config{CodexBinary: path}}).Next(context.Background(), model.Request{Profile: domain.Profile{Provider: "codex_cli"}, ObservationID: "fixture", Image: base64.StdEncoding.EncodeToString(frame.Bytes())})
			var diagnostic *domain.ModelError
			if !errors.As(e, &diagnostic) || diagnostic.Code != tc.code || strings.Contains(e.Error(), "secret-fixture") {
				t.Fatalf("unsafe/missing diagnostic: %v", e)
			}
		})
	}
}
