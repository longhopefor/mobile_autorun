package materials

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mobile_autorun/internal/domain"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"
)

func TestRealCardsAndMP4(t *testing.T) {
	if _, e := loadFont(); e != nil {
		t.Skip("CJK font not installed: " + e.Error())
	}
	var photo bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 100, 120))
	im.Set(50, 50, color.RGBA{255, 0, 0, 255})
	_ = png.Encode(&photo, im)
	r := domain.BusinessRecord{Frames: []domain.Frame{{Title: "真实商品", Text: "规格来自商家", Scene: "桌面"}}}
	out, e := Render(r, [][]byte{photo.Bytes()})
	if e != nil {
		t.Fatal(e)
	}
	if len(out) != 3 {
		t.Fatal("missing material types")
	}
	for _, o := range out {
		cfg, e := png.DecodeConfig(bytes.NewReader(o.Data))
		if e != nil || cfg.Width != 960 || cfg.Height < 1280 {
			t.Fatalf("invalid PNG %s", o.Name)
		}
	}
	if _, e = exec.LookPath("ffmpeg"); e != nil {
		t.Skip("ffmpeg not installed; PNG verified")
	}
	ctx, done := context.WithTimeout(context.Background(), 30*time.Second)
	defer done()
	v, e := Video(ctx, out)
	if e != nil {
		t.Fatal(e)
	}
	if len(v.Data) < 12 || string(v.Data[4:8]) != "ftyp" {
		t.Fatal("not an MP4")
	}
}
func TestAIMediaContractAndInvalidOutput(t *testing.T) {
	var pixel bytes.Buffer
	_ = png.Encode(&pixel, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	invalid := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q map[string]any
		_ = json.NewDecoder(r.Body).Decode(&q)
		if q["kind"] != "scene" || q["preserve_product"] != true {
			t.Error("missing media contract")
		}
		b := pixel.Bytes()
		if invalid {
			b = []byte("not a PNG")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"outputs": []any{map[string]any{"name": "scene.png", "mime": "image/png", "data_base64": base64.StdEncoding.EncodeToString(b)}}})
	}))
	defer server.Close()
	t.Setenv("MATERIAL_GENERATOR_URL", server.URL)
	if _, e := GenerateMedia(context.Background(), "scene", domain.BusinessRecord{}, nil); e != nil {
		t.Fatal(e)
	}
	invalid = true
	if _, e := GenerateMedia(context.Background(), "scene", domain.BusinessRecord{}, nil); e == nil {
		t.Fatal("malformed generated scene accepted")
	}
}
