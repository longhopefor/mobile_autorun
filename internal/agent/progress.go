package agent

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mobile_autorun/internal/domain"

	"golang.org/x/image/draw"
)

const ProgressWarnAt = 3
const ProgressStopAt = 6

type progressFrame struct {
	state         string
	width, height int
	pixels        []uint8
}
type progressWatch struct {
	frame  *progressFrame
	action domain.Action
	streak int
}

// This approximate comparison detects lack of progress only. It never authorises
// an action or replaces the original full-resolution stability checks.
func makeProgressFrame(o domain.Observation) *progressFrame {
	u, err := domain.ParseUI(o)
	if err != nil || u.Package == "" {
		return nil
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(o.Image))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
		return nil
	}
	src, err := png.Decode(bytes.NewReader(o.Image))
	if err != nil {
		return nil
	}
	signature, err := json.Marshal(u)
	if err != nil {
		return nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, 32, 64))
	r := src.Bounds()
	r.Min.Y += r.Dy() * 4 / 100
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, r, draw.Src, nil)
	pixels := make([]uint8, 0, 32*64*3)
	for y := 0; y < 64; y++ {
		for x := 0; x < 32; x++ {
			c := dst.RGBAAt(x, y)
			pixels = append(pixels, c.R, c.G, c.B)
		}
	}
	return &progressFrame{state: string(signature), width: cfg.Width, height: cfg.Height, pixels: pixels}
}
func sameProgressFrame(a, b *progressFrame) bool {
	if a == nil || b == nil || a.state != b.state || a.width != b.width || a.height != b.height {
		return false
	}
	changed := 0
	for i := 0; i < len(a.pixels); i += 3 {
		for c := 0; c < 3; c++ {
			d := int(a.pixels[i+c]) - int(b.pixels[i+c])
			if d < 0 {
				d = -d
			}
			if d > 8 {
				changed++
				break
			}
		}
	}
	return changed <= 4 // at most 0.2% of the sampled cells (a clock/caret)
}
func progressAction(a domain.Action) bool {
	return a.Kind == "tap" || a.Kind == "swipe" || a.Kind == "type" || a.Kind == "back" || a.Kind == "home"
}
func sameProgressAction(a, b domain.Action) bool {
	if a.Kind != b.Kind {
		return false
	}
	near := func(x, y int) bool { d := x - y; return d >= -10 && d <= 10 }
	switch a.Kind {
	case "tap":
		if a.Target != nil || b.Target != nil {
			x, _ := json.Marshal(a.Target)
			y, _ := json.Marshal(b.Target)
			return bytes.Equal(x, y)
		}
		return near(a.X, b.X) && near(a.Y, b.Y)
	case "swipe":
		return near(a.X, b.X) && near(a.Y, b.Y) && near(a.EndX, b.EndX) && near(a.EndY, b.EndY)
	case "type":
		return a.Text == b.Text
	case "back", "home":
		return true
	default:
		return false
	}
}
func (w *progressWatch) check(o domain.Observation, a domain.Action) (*progressFrame, int) {
	if !progressAction(a) {
		return nil, 0
	}
	f := makeProgressFrame(o)
	if f == nil {
		return nil, 0
	}
	if sameProgressFrame(w.frame, f) && sameProgressAction(w.action, a) {
		return f, w.streak + 1
	}
	return f, 1
}
func (w *progressWatch) commit(f *progressFrame, a domain.Action, streak int) {
	w.frame, w.action, w.streak = f, a, streak
}
func (w *progressWatch) reset() { *w = progressWatch{} }
