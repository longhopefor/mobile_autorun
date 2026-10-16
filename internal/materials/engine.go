package materials

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"io"
	"mobile_autorun/internal/domain"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// The optional adapter is configured by the server administrator. Its contract
// deliberately returns bytes, not arbitrary URLs to be fetched by the server.
func GenerateMedia(ctx context.Context, kind string, r domain.BusinessRecord, photos [][]byte) ([]Output, error) {
	endpoint := os.Getenv("MATERIAL_GENERATOR_URL")
	if endpoint == "" {
		return nil, errors.New("ai_media_generator_not_configured")
	}
	u, e := url.Parse(endpoint)
	if e != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return nil, errors.New("invalid_media_generator_url")
	}
	refs := []string{}
	for _, b := range photos {
		refs = append(refs, base64.StdEncoding.EncodeToString(b))
	}
	payload, _ := json.Marshal(map[string]any{"kind": kind, "content": r, "reference_images": refs, "preserve_product": true, "watermark": false})
	req, e := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(payload))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("MATERIAL_GENERATOR_API_KEY"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := http.Client{Timeout: 180 * time.Second}
	res, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("media_generator_failed")
	}
	var v struct {
		Outputs []struct {
			Name string `json:"name"`
			MIME string `json:"mime"`
			Data string `json:"data_base64"`
		} `json:"outputs"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 64<<20)).Decode(&v); e != nil {
		return nil, errors.New("invalid_media_generator_response")
	}
	if len(v.Outputs) < 1 || len(v.Outputs) > 9 {
		return nil, errors.New("invalid_media_output_count")
	}
	out := []Output{}
	for _, o := range v.Outputs {
		b, e := base64.StdEncoding.DecodeString(o.Data)
		if e != nil || len(b) > 16<<20 || len(b) == 0 {
			return nil, errors.New("invalid_generated_media")
		}
		if kind == "scene" && (o.MIME != "image/png" && o.MIME != "image/jpeg") {
			return nil, errors.New("invalid_scene_mime")
		}
		if kind == "scene" {
			cfg, format, e := image.DecodeConfig(bytes.NewReader(b))
			if e != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 || (o.MIME == "image/png" && format != "png") || (o.MIME == "image/jpeg" && format != "jpeg") {
				return nil, errors.New("invalid_generated_scene_image")
			}
		}
		if kind == "video" && (o.MIME != "video/mp4" || len(b) < 12 || string(b[4:8]) != "ftyp") {
			return nil, errors.New("invalid_video_mime")
		}
		if strings.ContainsAny(o.Name, "/\\\x00") || o.Name == "" || len(o.Name) > 180 {
			return nil, errors.New("invalid_media_name")
		}
		out = append(out, Output{Name: "ai-" + kind + "-" + o.Name, MIME: o.MIME, Data: b})
	}
	return out, nil
}
