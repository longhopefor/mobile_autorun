package model

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"mobile_autorun/internal/domain"
	"unicode/utf8"

	"golang.org/x/image/draw"
)

// InputMetrics describes only the model copy. The evidence image stays untouched.
type InputMetrics struct {
	OriginalWidth  int `json:"original_width"`
	OriginalHeight int `json:"original_height"`
	Width          int `json:"width"`
	Height         int `json:"height"`
	OriginalBytes  int `json:"original_bytes"`
	Bytes          int `json:"bytes"`
	StateBytes     int `json:"state_bytes"`
	PromptBytes    int `json:"prompt_bytes"`
}

// PrepareVision runs at the provider boundary, after business guards have read
// the full state and original dimensions. PNG keeps older CLI connectors compatible.
func PrepareVision(q Request) (Request, error) {
	if q.Image == "" {
		return q, nil
	}
	if len(q.Image) > 28<<20 {
		return q, errors.New("image_too_large")
	}
	raw, err := base64.StdEncoding.DecodeString(q.Image)
	if err != nil {
		return q, errors.New("invalid_image")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 12000 || cfg.Height > 12000 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
		return q, errors.New("invalid_png")
	}
	metrics := InputMetrics{OriginalWidth: cfg.Width, OriginalHeight: cfg.Height, Width: cfg.Width, Height: cfg.Height, OriginalBytes: len(raw), Bytes: len(raw)}
	if q.Input != nil {
		metrics.OriginalWidth, metrics.OriginalHeight, metrics.OriginalBytes = q.Input.OriginalWidth, q.Input.OriginalHeight, q.Input.OriginalBytes
	}
	limit := q.Profile.ImageMaxWidth
	if limit == 0 {
		limit = 960
	}
	if limit != -1 && (limit < 320 || limit > 2560) {
		return q, errors.New("invalid_model_image_width")
	}
	if limit != -1 && cfg.Width > limit {
		src, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			return q, errors.New("invalid_png")
		}
		height := max(1, (cfg.Height*limit+cfg.Width/2)/cfg.Width)
		dst := image.NewRGBA(image.Rect(0, 0, limit, height))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
		var out bytes.Buffer
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		if err := encoder.Encode(&out, dst); err != nil {
			return q, err
		}
		q.Image = base64.StdEncoding.EncodeToString(out.Bytes())
		metrics.Width, metrics.Height, metrics.Bytes = limit, height, out.Len()
	}
	q.State = CompactState(q.State)
	q.Input = &metrics
	metrics.StateBytes, metrics.PromptBytes = len(q.State), len(Prompt(q))
	return q, nil
}

// CompactState preserves labels as data, prioritises inputs and interactive nodes,
// and never cuts a JSON document mid-byte. It is not used for device validation.
func CompactState(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var u domain.UI
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return json.RawMessage(`{"unavailable":"invalid_state"}`)
	}
	if _, ok := root["a11y_tree"]; ok {
		var err error
		u, err = domain.ParseUI(domain.Observation{State: raw})
		if err != nil {
			return json.RawMessage(`{"unavailable":"invalid_ui_tree"}`)
		}
	} else if _, ok := root["Nodes"]; ok {
		if json.Unmarshal(raw, &u) != nil {
			return json.RawMessage(`{"unavailable":"invalid_ui_tree"}`)
		}
	} else {
		if len(raw) <= 12000 {
			return raw
		}
		return json.RawMessage(`{"omitted":true,"reason":"state_too_large_use_screenshot"}`)
	}
	type node struct {
		ID        string        `json:"id,omitempty"`
		Text      string        `json:"text,omitempty"`
		Desc      string        `json:"description,omitempty"`
		Hint      string        `json:"hint,omitempty"`
		Bounds    domain.Bounds `json:"bounds"`
		Editable  bool          `json:"editable,omitempty"`
		Focused   bool          `json:"focused,omitempty"`
		Clickable bool          `json:"clickable,omitempty"`
		Disabled  bool          `json:"disabled,omitempty"`
	}
	nodes := []node{}
	omitted := 0
	for pass := 0; pass < 2; pass++ {
		for _, n := range u.Nodes {
			if !n.Visible {
				continue
			}
			priority := n.Editable || n.Focused || n.Clickable
			if priority != (pass == 0) || (!priority && n.Text == "" && n.Description == "" && n.Hint == "") {
				continue
			}
			if len(nodes) >= 80 {
				omitted++
				continue
			}
			nodes = append(nodes, node{ID: shortText(n.ResourceID, 160), Text: shortText(n.Text, 800), Desc: shortText(n.Description, 400), Hint: shortText(n.Hint, 160), Bounds: n.Bounds, Editable: n.Editable, Focused: n.Focused, Clickable: n.Clickable, Disabled: !n.Enabled})
		}
	}
	result := map[string]any{"package": u.Package, "activity": u.Activity, "keyboard": u.Keyboard, "focus": u.Focus, "nodes": nodes, "coordinate_space": "original_screen_pixels"}
	for {
		result["nodes"], result["omitted_nodes"] = nodes, omitted
		b, _ := json.Marshal(result)
		if len(b) <= 12000 || len(nodes) == 0 {
			return b
		}
		nodes = nodes[:len(nodes)-1]
		omitted++
	}
}

func shortText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit] + "…"
}
