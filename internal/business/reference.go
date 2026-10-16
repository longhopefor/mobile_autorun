package business

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"mobile_autorun/internal/model"
)

func ReferenceImage(name string, data []byte) (model.Reference, error) {
	cfg, _, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width*cfg.Height > 16_000_000 {
		return model.Reference{}, errors.New("invalid_reference_dimensions")
	}
	src, _, e := image.Decode(bytes.NewReader(data))
	if e != nil {
		return model.Reference{}, e
	}
	w, h := cfg.Width, cfg.Height
	if w > 512 || h > 512 {
		if w > h {
			h = h * 512 / w
			w = 512
		} else {
			w = w * 512 / h
			h = 512
		}
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(x, y, src.At(src.Bounds().Min.X+x*cfg.Width/w, src.Bounds().Min.Y+y*cfg.Height/h))
		}
	}
	var out bytes.Buffer
	if e = png.Encode(&out, dst); e != nil {
		return model.Reference{}, e
	}
	return model.Reference{Name: name, Image: base64.StdEncoding.EncodeToString(out.Bytes())}, nil
}
