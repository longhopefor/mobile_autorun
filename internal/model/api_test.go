package model

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"mobile_autorun/internal/domain"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIImageAndToolContract(t *testing.T) {
	t.Setenv("TEST_MODEL_KEY", "test-only")
	image := visionFixture(t, 1080, 2400)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-only" {
			t.Error("auth missing")
		}
		var v map[string]any
		if e := json.NewDecoder(r.Body).Decode(&v); e != nil {
			t.Fatal(e)
		}
		msgs := v["messages"].([]any)
		parts := msgs[1].(map[string]any)["content"].([]any)
		imageURL := parts[1].(map[string]any)["image_url"].(map[string]any)["url"].(string)
		const prefix = "data:image/png;base64,"
		if len(imageURL) < len(prefix) || imageURL[:len(prefix)] != prefix {
			t.Fatal("PNG image missing")
		}
		data, err := base64.StdEncoding.DecodeString(imageURL[len(prefix):])
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != 960 || cfg.Height != 2133 {
			t.Fatalf("model did not receive resized PNG: %+v %v", cfg, err)
		}
		d := domain.Decision{Tool: "phone_action", ObservationID: "o1", Action: &domain.Action{Kind: "tap", X: 100, Y: 200}}
		b, _ := json.Marshal(d)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"function": map[string]any{"name": "phone_action", "arguments": string(b)}}}}}}})
	}))
	defer server.Close()
	p := &API{}
	r, e := p.Next(context.Background(), Request{Profile: domain.Profile{BaseURL: server.URL, Model: "test", SecretEnv: "TEST_MODEL_KEY"}, ObservationID: "o1", Image: image})
	if e != nil {
		t.Fatal(e)
	}
	if r.Decision.Action.X != 100 {
		t.Fatal("invalid parsed tool")
	}
	if r.Input == nil || r.Input.OriginalWidth != 1080 {
		t.Fatal("input metrics missing")
	}
}

func TestBusinessToolsRequireExplicitRequest(t *testing.T) {
	q := Request{ObservationID: "o"}
	r := Result{Decision: domain.Decision{Tool: "record_business", ObservationID: "o", Record: &domain.BusinessRecord{Kind: "account", Key: "account"}}}
	if e := ValidateResult(q, r); e == nil {
		t.Fatal("general request enabled business tools")
	}
	q.BusinessTools = true
	if e := ValidateResult(q, r); e != nil {
		t.Fatal(e)
	}
	if len(ToolNames(Request{})) != 3 || len(ToolNames(q)) != 5 {
		t.Fatal("wrong tool availability")
	}
	if _, ok := RequestSchema(Request{})["properties"].(map[string]any)["record"]; ok {
		t.Fatal("legacy phone schema changed")
	}
}
