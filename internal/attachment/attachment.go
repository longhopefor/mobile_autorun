// Package attachment defines bounded user-file handling and generated phone paths.
package attachment

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxBytes = 16 << 20

func IsImage(mime string) bool { return mime == "image/png" || mime == "image/jpeg" }
func Inspect(filename string, data []byte) (string, string, error) {
	if len(data) == 0 || len(data) > MaxBytes {
		return "", "", errors.New("attachment_must_be_1_byte_to_16_mib")
	}
	name := path.Base(strings.ReplaceAll(filename, "\\", "/"))
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	if !utf8.ValidString(name) || len(name) > 255 {
		return "", "", errors.New("invalid_attachment_name")
	}
	if name == "" || name == "." || name == ".." {
		name = "attachment.bin"
	}
	kind := http.DetectContentType(data)
	if IsImage(kind) {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
			return "", "", errors.New("invalid_image_dimensions")
		}
	}
	return name, kind, nil
}

var safeExtension = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)

func Extension(name, kind string) string {
	if kind == "image/png" {
		return ".png"
	}
	if kind == "image/jpeg" {
		return ".jpg"
	}
	ext := strings.ToLower(path.Ext(name))
	if safeExtension.MatchString(ext) {
		return ext
	}
	return ".bin"
}
func Disposition(name string) string {
	if name == "" {
		name = "attachment.bin"
	}
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}
