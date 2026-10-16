package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"math"
)

// SameScreen compares decoded content, excluding only the top status-bar band.
// The app content remains exact: changed labels, buttons or layout invalidate it.
func SameScreen(a, b []byte) (bool, error) {
	if bytes.Equal(a, b) {
		return true, nil
	}
	x, e := decodeScreen(a)
	if e != nil {
		return false, e
	}
	y, e := decodeScreen(b)
	if e != nil {
		return false, e
	}
	if x.Bounds() != y.Bounds() {
		return false, nil
	}
	r := x.Bounds()
	for py := r.Min.Y + r.Dy()*4/100; py < r.Max.Y; py++ {
		for px := r.Min.X; px < r.Max.X; px++ {
			ar, ag, ab, aa := x.At(px, py).RGBA()
			br, bg, bb, ba := y.At(px, py).RGBA()
			if ar != br || ag != bg || ab != bb || aa != ba {
				return false, nil
			}
		}
	}
	return true, nil
}
func (a Action) NeedsStableScreen() bool {
	return a.Kind == "tap" || a.Kind == "swipe" || a.Kind == "type"
}

func SameScreenForAction(a, b []byte, action Action) (bool, error) {
	// Coordinates in the excluded band must still match the entire screenshot.
	if (action.Kind == "tap" && action.Y < 40) || (action.Kind == "swipe" && (action.Y < 40 || action.EndY < 40)) {
		return bytes.Equal(a, b), nil
	}
	return SameScreen(a, b)
}

// A blinking caret can change pixels without changing the editable controls.
// Text input and the narrowly scoped caret-aware tap fallback require matching
// complete accessibility trees and phone states.
func SameObservationForAction(a, b Observation, action Action) (bool, error) {
	if action.Target != nil {
		return SameUITarget(a, b, action), nil
	}
	stable, e := SameScreenForAction(a.Image, b.Image, action)
	if e != nil || stable || (action.Kind != "type" && action.Kind != "tap") {
		return stable, e
	}
	signature := func(o Observation) ([]byte, error) {
		var state map[string]json.RawMessage
		if e := json.Unmarshal(o.State, &state); e != nil {
			return nil, e
		}
		var phone struct {
			Editable bool `json:"isEditable"`
			Focus    struct {
				ID string `json:"resourceId"`
			} `json:"focusedElement"`
		}
		if e := json.Unmarshal(state["phone_state"], &phone); e != nil {
			return nil, e
		}
		if !phone.Editable || phone.Focus.ID == "" || len(state["a11y_tree"]) == 0 {
			return nil, errors.New("editable_target_unknown")
		}
		var tree, ps any
		if e := json.Unmarshal(state["a11y_tree"], &tree); e != nil {
			return nil, e
		}
		if e := json.Unmarshal(state["phone_state"], &ps); e != nil {
			return nil, e
		}
		return json.Marshal(map[string]any{"a11y_tree": tree, "phone_state": ps})
	}
	x, e := signature(a)
	if e != nil {
		return false, nil
	}
	y, e := signature(b)
	if e != nil {
		return false, nil
	}
	if !bytes.Equal(x, y) {
		return false, nil
	}
	if action.Kind == "type" {
		return true, nil
	}
	return sameTapWithCaret(a, b, action)
}

func decodeScreen(data []byte) (image.Image, error) {
	cfg, e := png.DecodeConfig(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width*cfg.Height > 16_000_000 {
		return nil, errors.New("screen_size_limit")
	}
	return png.Decode(bytes.NewReader(data))
}

// A coordinate tap into a uniquely identified editable or clickable control
// may tolerate only a narrow caret blinking inside the focused editor. All other
// pixels and the complete accessibility/phone state must remain unchanged.
func sameTapWithCaret(a, b Observation, action Action) (bool, error) {
	if action.Y < 40 || a.Width < 2 || a.Height < 2 || a.Width != b.Width || a.Height != b.Height {
		return false, nil
	}
	u, err := ParseUI(a)
	if err != nil || u.Package == "" || u.Activity == "" || (action.Package != "" && action.Package != u.Package) {
		return false, nil
	}
	point := image.Pt(Pixel(action.X, a.Width), Pixel(action.Y, a.Height))
	targets := 0
	for _, n := range u.Nodes {
		if (n.Editable || n.Clickable) && n.Visible && n.Enabled && point.In(controlRect(n.Bounds)) {
			if !validControlBounds(n.Bounds, a) {
				return false, nil
			}
			// The complete raw trees already match, including this control's
			// position, properties and descendants. Coordinate taps identify the
			// unique hit control; recycled IDs or an anonymous clickable parent
			// containing a text child do not make that hit ambiguous.
			targets++
		}
	}
	focus, err := u.Find(UITarget{ResourceID: u.Focus})
	if targets != 1 || u.Focus == "" || err != nil || !focus.Editable || !focus.Focused || !validControlBounds(focus.Bounds, a) {
		return false, nil
	}
	x, err := decodeScreen(a.Image)
	if err != nil {
		return false, err
	}
	y, err := decodeScreen(b.Image)
	if err != nil {
		return false, err
	}
	if x.Bounds() != image.Rect(0, 0, a.Width, a.Height) || y.Bounds() != x.Bounds() {
		return false, nil
	}
	var changed image.Rectangle
	count := 0
	for py := a.Height * 4 / 100; py < a.Height; py++ {
		for px := 0; px < a.Width; px++ {
			if samePixel(x, y, px, py) {
				continue
			}
			if count == 0 {
				changed = image.Rect(px, py, px+1, py+1)
			} else {
				changed = changed.Union(image.Rect(px, py, px+1, py+1))
			}
			count++
			if !changed.In(controlRect(focus.Bounds)) || changed.Dx() > max(2, a.Width/100) || changed.Dy() > a.Height/20 {
				return false, nil
			}
		}
	}
	if count < 6 || count != changed.Dx()*changed.Dy() || changed.Dy() < changed.Dx()*3 {
		return false, nil
	}
	// Android draws the caret over hint text as well as the background. Require
	// one constant-color overlay, including the same opacity at every pixel.
	return caretOverlay(x, y, changed) || caretOverlay(y, x, changed) || caretTint(x, y, changed) || caretTint(y, x, changed), nil
}

func controlRect(b Bounds) image.Rectangle { return image.Rect(b.Left, b.Top, b.Right, b.Bottom) }
func validControlBounds(b Bounds, o Observation) bool {
	return b.Left >= 0 && b.Top >= 0 && b.Right > b.Left && b.Bottom > b.Top && b.Right <= o.Width && b.Bottom <= o.Height
}
func samePixel(a, b image.Image, x, y int) bool {
	ar, ag, ab, aa := a.At(x, y).RGBA()
	br, bg, bb, ba := b.At(x, y).RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func rgbaAt(m image.Image, x, y int) [4]float64 {
	r, g, b, a := m.At(x, y).RGBA()
	return [4]float64{float64(r), float64(g), float64(b), float64(a)}
}
func caretOverlay(background, painted image.Image, r image.Rectangle) bool {
	base := rgbaAt(background, r.Min.X, r.Min.Y)
	color := rgbaAt(painted, r.Min.X, r.Min.Y)
	scale, spread := 0.0, 0.0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			b, p := rgbaAt(background, x, y), rgbaAt(painted, x, y)
			if b[3] != 65535 || p[3] != 65535 {
				return false
			}
			for c := 0; c < 3; c++ {
				if delta := b[c] - base[c]; math.Abs(delta) > spread {
					spread = math.Abs(delta)
					scale = (p[c] - color[c]) / delta
				}
			}
		}
	}
	// Uniform backgrounds require a uniform caret. Otherwise use sufficient
	// contrast to estimate opacity, allowing two 8-bit levels of rounding.
	if spread == 0 {
		scale = 0
	} else if spread < 4096 {
		return false
	}
	if scale < 0 || scale > 0.95 {
		return false
	}
	const rounding = 2 * 257
	// The ink must contrast with the unchanged pixels beside the caret. This
	// rejects explaining arbitrary colored content as an opaque background erase.
	neighborX := r.Max.X
	if neighborX >= background.Bounds().Max.X {
		neighborX = r.Min.X - 1
	}
	if neighborX < 0 {
		return false
	}
	neighbor := rgbaAt(background, neighborX, (r.Min.Y+r.Max.Y)/2)
	contrast := false
	for c := 0; c < 3; c++ {
		ink := color[c] - scale*base[c]
		if ink < -rounding || ink > 65535*(1-scale)+rounding {
			return false
		}
		if math.Abs(ink/(1-scale)-neighbor[c]) >= 4096 {
			contrast = true
		}
	}
	if !contrast {
		return false
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			b, p := rgbaAt(background, x, y), rgbaAt(painted, x, y)
			for c := 0; c < 3; c++ {
				if math.Abs(p[c]-(color[c]+scale*(b[c]-base[c]))) > rounding {
					return false
				}
			}
		}
	}
	return true
}

// Some Android editors tint the glyph beneath the caret rather than replacing
// it with a uniform alpha overlay. Require the same per-channel tint everywhere.
func caretTint(background, painted image.Image, r image.Rectangle) bool {
	var brightest, sample [3]float64
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			b, p := rgbaAt(background, x, y), rgbaAt(painted, x, y)
			if b[3] != 65535 || p[3] != 65535 {
				return false
			}
			for c := 0; c < 3; c++ {
				if b[c] > brightest[c] {
					brightest[c], sample[c] = b[c], p[c]
				}
			}
		}
	}
	var scale [3]float64
	contrast := false
	for c := 0; c < 3; c++ {
		if brightest[c] < 4096 {
			return false
		}
		scale[c] = sample[c] / brightest[c]
		// On a gray search-field background, a red caret slightly brightens
		// the red channel while darkening the others. Require one bounded gain
		// per channel, rather than assuming every tint only darkens pixels.
		if scale[c] > 2 {
			return false
		}
		if brightest[c]-sample[c] >= 4096 {
			contrast = true
		}
	}
	if !contrast {
		return false
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			b, p := rgbaAt(background, x, y), rgbaAt(painted, x, y)
			for c := 0; c < 3; c++ {
				if math.Abs(p[c]-b[c]*scale[c]) > 2*257 {
					return false
				}
			}
		}
	}
	return true
}
