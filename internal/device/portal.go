package device

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"image/png"
	"mobile_autorun/internal/domain"
	"strings"
	"sync"
	"time"
)

type Driver interface {
	Observe(context.Context, string) (domain.Observation, error)
	Execute(context.Context, string, domain.Action, domain.Observation) error
	Upload(context.Context, string, string, []byte) error
}
type Portal struct {
	Gateway         *Gateway
	screenshotMu    sync.Mutex
	screenshotSlots map[string]time.Time
}

func (p *Portal) Observe(ctx context.Context, id string) (domain.Observation, error) {
	var o domain.Observation
	generation := p.Gateway.Generation(id)
	// Android Accessibility screenshots are rate-limited. Reserve a capture
	// slot per phone so refresh/manual/agent calls cannot race the platform.
	p.screenshotMu.Lock()
	if p.screenshotSlots == nil {
		p.screenshotSlots = map[string]time.Time{}
	}
	next := time.Now()
	if prior := p.screenshotSlots[id].Add(750 * time.Millisecond); prior.After(next) {
		next = prior
	}
	p.screenshotSlots[id] = next
	p.screenshotMu.Unlock()
	if delay := time.Until(next); delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return o, ctx.Err()
		case <-timer.C:
		}
	}
	b, err := p.Gateway.Call(ctx, id, "screenshot", map[string]any{"hideOverlay": true})
	if err != nil {
		return o, err
	}
	var encoded string
	if err = json.Unmarshal(b, &encoded); err != nil {
		var x struct {
			Data   string `json:"data"`
			Base64 string `json:"base64"`
			Image  string `json:"image"`
		}
		if err = json.Unmarshal(b, &x); err != nil {
			return o, errors.New("invalid_screenshot_result")
		}
		encoded = x.Data
		if encoded == "" {
			encoded = x.Base64
		}
		if encoded == "" {
			encoded = x.Image
		}
	}
	encoded = strings.TrimPrefix(encoded, "data:image/png;base64,")
	o.Image, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(o.Image) > 20<<20 {
		return o, errors.New("invalid_screenshot_image")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(o.Image))
	if err != nil {
		return o, errors.New("screenshot_not_png")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 12000 || cfg.Height > 12000 {
		return o, errors.New("invalid_screenshot_size")
	}
	o.State, err = p.Gateway.Call(ctx, id, "state", map[string]any{"filter": true})
	if err != nil {
		return o, err
	}
	// Portal returns state as a JSON-encoded string, unlike screenshot's raw base64.
	var nested string
	if json.Unmarshal(o.State, &nested) == nil {
		o.State = json.RawMessage(nested)
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(o.State, &state) != nil || state == nil {
		return o, errors.New("invalid_phone_state")
	}
	o.ID = uuid.NewString()
	o.DeviceID = id
	o.Width = cfg.Width
	o.Height = cfg.Height
	o.CapturedAt = time.Now().UTC()
	o.Revision = p.Gateway.Revision(id)
	if generation == 0 || generation != p.Gateway.Generation(id) {
		return domain.Observation{}, domain.ErrScreenChanged
	}
	return o, nil
}
func (p *Portal) Execute(ctx context.Context, id string, a domain.Action, o domain.Observation) error {
	var method string
	var args any
	switch a.Kind {
	case "tap":
		method = "tap"
		args = map[string]int{"x": domain.Pixel(a.X, o.Width), "y": domain.Pixel(a.Y, o.Height)}
	case "swipe":
		method = "swipe"
		args = map[string]int{"startX": domain.Pixel(a.X, o.Width), "startY": domain.Pixel(a.Y, o.Height), "endX": domain.Pixel(a.EndX, o.Width), "endY": domain.Pixel(a.EndY, o.Height), "duration": a.DurationMS}
	case "type":
		method = "keyboard/input"
		args = map[string]any{"base64_text": base64.StdEncoding.EncodeToString([]byte(a.Text)), "clear": true}
	case "launch":
		method = "app"
		args = map[string]any{"package": a.Package, "stopBeforeLaunch": false}
	case "back":
		method = "global"
		args = map[string]int{"action": 1}
	case "home":
		method = "global"
		args = map[string]int{"action": 2}
	case "wait":
		t := time.NewTimer(time.Duration(a.DurationMS) * time.Millisecond)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	default:
		return errors.New("unsupported_action")
	}
	_, err := p.Gateway.CallAtRevision(ctx, id, method, args, o.Revision)
	return err
}
func (p *Portal) Upload(ctx context.Context, id, path string, data []byte) error {
	// Portal resolves every file path relative to external storage, including
	// strings beginning with '/'. Passing '/sdcard/...' creates sdcard/sdcard/...
	if !strings.HasPrefix(path, "/sdcard/") || strings.Contains(path, "..") {
		return errors.New("invalid_phone_upload_path")
	}
	relative := strings.TrimPrefix(path, "/sdcard/")
	if _, err := p.Gateway.Call(ctx, id, "files/upload", map[string]string{"path": relative, "data": base64.StdEncoding.EncodeToString(data)}); err != nil {
		return err
	}
	// A successful write response alone cannot establish the file's location or
	// integrity. Read back through the same executor before recording transfer.
	result, err := p.Gateway.Call(ctx, id, "files/download", map[string]string{"path": relative})
	if err != nil {
		return err
	}
	var encoded string
	if err = json.Unmarshal(result, &encoded); err != nil {
		return errors.New("invalid_phone_upload_verification")
	}
	actual, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !bytes.Equal(actual, data) {
		return errors.New("phone_upload_verification_failed")
	}
	return nil
}
