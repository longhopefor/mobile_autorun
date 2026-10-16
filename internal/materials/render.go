package materials

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"mobile_autorun/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
)

type Output struct {
	Name string
	MIME string
	Data []byte
}

func loadFont() (*opentype.Font, error) {
	paths := []string{os.Getenv("MATERIAL_FONT_PATH"), "/System/Library/Fonts/Supplemental/Arial Unicode.ttf", "/System/Library/Fonts/STHeiti Medium.ttc", "/System/Library/Fonts/Supplemental/Songti.ttc", "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"}
	for _, p := range paths {
		if p == "" {
			continue
		}
		b, e := os.ReadFile(p)
		if e != nil {
			continue
		}
		if f, e := opentype.Parse(b); e == nil {
			return f, nil
		}
		if c, e := opentype.ParseCollection(b); e == nil {
			if f, e := c.Font(0); e == nil {
				return f, nil
			}
		}
	}
	return nil, errors.New("material_cjk_font_missing: configure MATERIAL_FONT_PATH")
}
func paragraph(img *image.RGBA, f *opentype.Font, text string, x, y, width int, size float64, lines int) error {
	face, e := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if e != nil {
		return e
	}
	defer face.Close()
	d := font.Drawer{Dst: img, Src: image.NewUniform(color.RGBA{30, 42, 48, 255}), Face: face}
	var line []rune
	count := 0
	flush := func() { d.Dot = fixed.P(x, y); d.DrawString(string(line)); line = nil; y += int(size * 1.5); count++ }
	for _, r := range text {
		if r == '\n' {
			flush()
			if count >= lines {
				return errors.New("material_text_overflows_card")
			}
			continue
		}
		next := append(append([]rune{}, line...), r)
		if font.MeasureString(face, string(next)).Ceil() > width && len(line) > 0 {
			flush()
			if count >= lines {
				return errors.New("material_text_overflows_card")
			}
		}
		line = append(line, r)
	}
	if len(line) > 0 {
		flush()
	}
	return nil
}

// Render uses original product photos and real text. Scene cards are editorial
// compositions, not a claim that a diffusion model generated a new environment.
func Render(r domain.BusinessRecord, photos [][]byte) ([]Output, error) {
	f, e := loadFont()
	if e != nil {
		return nil, e
	}
	imgs := []image.Image{}
	for _, b := range photos {
		cfg, _, e := image.DecodeConfig(bytes.NewReader(b))
		if e != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
			return nil, errors.New("invalid_product_photo")
		}
		im, _, e := image.Decode(bytes.NewReader(b))
		if e != nil {
			return nil, e
		}
		imgs = append(imgs, im)
	}
	out := []Output{}
	for i, frame := range r.Frames {
		for _, kind := range []string{"xhs", "detail", "scene"} {
			h := 1280
			if kind == "detail" {
				h = 1600
			}
			dst := image.NewRGBA(image.Rect(0, 0, 960, h))
			bg := color.RGBA{245, 242, 232, 255}
			if kind == "scene" {
				bg = color.RGBA{228, 239, 232, 255}
			}
			draw.Draw(dst, dst.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
			if e = paragraph(dst, f, frame.Title, 58, 105, 844, 48, 2); e != nil {
				return nil, e
			}
			if len(imgs) > 0 {
				im := imgs[i%len(imgs)]
				rect := image.Rect(58, 230, 902, 850)
				ratio := float64(im.Bounds().Dx()) / float64(im.Bounds().Dy())
				w, h := rect.Dx(), rect.Dy()
				if float64(w)/float64(h) > ratio {
					w = int(float64(h) * ratio)
				} else {
					h = int(float64(w) / ratio)
				}
				rect = image.Rect(480-w/2, 540-h/2, 480+(w+1)/2, 540+(h+1)/2)
				xdraw.CatmullRom.Scale(dst, rect, im, im.Bounds(), draw.Over, nil)
			}
			if e = paragraph(dst, f, frame.Text, 58, 950, 844, 32, 6); e != nil {
				return nil, e
			}
			if kind == "detail" {
				if e = paragraph(dst, f, "商品参数请以商家提供的真实规格为准。", 58, 1430, 844, 24, 2); e != nil {
					return nil, e
				}
			}
			var b bytes.Buffer
			if e = png.Encode(&b, dst); e != nil {
				return nil, e
			}
			out = append(out, Output{Name: fmt.Sprintf("%s-%02d.png", kind, i+1), MIME: "image/png", Data: b.Bytes()})
		}
	}
	return out, nil
}

// Video creates an actual silent MP4 slideshow from scene cards. Narration,
// captions and AI motion prompts are also included in the workflow manifest.
func Video(ctx context.Context, outputs []Output) (Output, error) {
	bin := os.Getenv("MATERIAL_FFMPEG_BINARY")
	if bin == "" {
		bin = "ffmpeg"
	}
	path, e := exec.LookPath(bin)
	if e != nil {
		return Output{}, errors.New("ffmpeg_missing")
	}
	dir, e := os.MkdirTemp("", "material-video-")
	if e != nil {
		return Output{}, e
	}
	defer os.RemoveAll(dir)
	i := 0
	for _, o := range outputs {
		if len(o.Name) >= 6 && o.Name[:6] == "scene-" {
			i++
			if e = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d.png", i)), o.Data, 0600); e != nil {
				return Output{}, e
			}
		}
	}
	if i == 0 {
		return Output{}, errors.New("video_frames_required")
	}
	out := filepath.Join(dir, "product.mp4")
	cmd := exec.CommandContext(ctx, path, "-nostdin", "-v", "error", "-framerate", "1/3", "-i", filepath.Join(dir, "%02d.png"), "-vf", "scale=720:960,format=yuv420p", "-r", "24", "-c:v", "libx264", "-threads", "2", "-movflags", "+faststart", "-y", out)
	cmd.WaitDelay = 3 * 1e9
	if e = cmd.Run(); e != nil {
		return Output{}, errors.New("material_video_render_failed")
	}
	b, e := os.ReadFile(out)
	if e != nil {
		return Output{}, e
	}
	if len(b) > 16<<20 {
		return Output{}, errors.New("material_video_too_large")
	}
	return Output{Name: "product.mp4", MIME: "video/mp4", Data: b}, nil
}
