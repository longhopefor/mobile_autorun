package model

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mobile_autorun/internal/domain"
	"strings"
	"testing"
	"unicode/utf8"
)

func visionFixture(t *testing.T, w, h int) string {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.SetRGBA(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), uint8((x + y) % 256), 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func TestVisionCopyPreservesEvidenceAndCoordinates(t *testing.T) {
	q := Request{Image: visionFixture(t, 1080, 2400), State: json.RawMessage(`{"a11y_tree":[{"text":"搜索","isVisibleToUser":true,"isEnabled":true,"isClickable":true,"boundsInScreen":{"left":800,"top":120,"right":1000,"bottom":200}}],"phone_state":{"packageName":"com.android.settings"}}`)}
	originalImage, originalState := q.Image, string(q.State)
	prepared, err := PrepareVision(q)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(prepared.Image)
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width != 960 || cfg.Height != 2133 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	if q.Image != originalImage || string(q.State) != originalState {
		t.Fatal("evidence mutated")
	}
	if prepared.Input.OriginalWidth != 1080 || prepared.Input.OriginalHeight != 2400 || prepared.Input.Width != 960 {
		t.Fatalf("metrics=%+v", prepared.Input)
	}
	if !strings.Contains(Prompt(prepared), "原图1080×2400") || !strings.Contains(string(prepared.State), `"left":800`) {
		t.Fatal("original coordinate context lost")
	}
	again, err := PrepareVision(prepared)
	if err != nil || again.Image != prepared.Image || again.Input.OriginalWidth != 1080 {
		t.Fatal("connector compressed twice or lost original metrics")
	}
	q.Profile.ImageMaxWidth = -1
	full, err := PrepareVision(q)
	if err != nil || full.Image != originalImage {
		t.Fatal("original-size option ignored")
	}
	q.Profile.ImageMaxWidth = 720
	small, err := PrepareVision(q)
	if err != nil || small.Input.Width != 720 || small.Input.Height != 1600 {
		t.Fatal("configured width ignored")
	}
}
func TestCompactStateIsBoundedValidUnicodeAndKeepsInputsFirst(t *testing.T) {
	nodes := []domain.UINode{}
	for i := 0; i < 200; i++ {
		nodes = append(nodes, domain.UINode{Text: strings.Repeat("中文标签", 100), Visible: true, Enabled: true})
	}
	nodes = append(nodes, domain.UINode{ResourceID: "query", Text: "正在输入", Hint: "搜索", Visible: true, Enabled: false, Editable: true, Focused: true})
	raw, _ := json.Marshal(map[string]any{"a11y_tree": nodes, "phone_state": map[string]any{"packageName": "app"}})
	compact := CompactState(raw)
	var v struct {
		Nodes []struct {
			ID       string `json:"id"`
			Disabled bool   `json:"disabled"`
		}
		Omitted int `json:"omitted_nodes"`
	}
	if len(compact) > 12000 || !utf8.Valid(compact) || json.Unmarshal(compact, &v) != nil || len(v.Nodes) == 0 || v.Nodes[0].ID != "query" || !v.Nodes[0].Disabled || v.Omitted == 0 {
		t.Fatalf("bad compact state: %s", compact)
	}
	if !bytes.Equal(compact, CompactState(compact)) {
		t.Fatal("compaction not idempotent")
	}
}
func TestVisionRejectsMalformedInputAndDoesNotUpscale(t *testing.T) {
	for _, s := range []string{"!base64", base64.StdEncoding.EncodeToString([]byte("not png"))} {
		if _, err := PrepareVision(Request{Image: s}); err == nil {
			t.Fatal("accepted malformed image")
		}
	}
	q := Request{Image: visionFixture(t, 32, 48)}
	prepared, err := PrepareVision(q)
	if err != nil || prepared.Image != q.Image || prepared.Input.Width != 32 {
		t.Fatal("small image upscaled")
	}
	q.Profile.ImageMaxWidth = 2
	if _, err := PrepareVision(q); err == nil {
		t.Fatal("invalid limit accepted")
	}
}
