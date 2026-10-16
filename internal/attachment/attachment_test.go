package attachment

import (
	"bytes"
	"image"
	"image/png"
	"mime"
	"strings"
	"testing"
)

func TestFileInspectionAndNames(t *testing.T) {
	name, kind, err := Inspect(`C:\private\notes.txt`, []byte("hello"))
	if err != nil || name != "notes.txt" || !strings.HasPrefix(kind, "text/plain") {
		t.Fatalf("%s %s %v", name, kind, err)
	}
	name, _, err = Inspect("../图文\n说明.pdf", []byte("%PDF-1.4\nexample"))
	if err != nil || name != "图文说明.pdf" {
		t.Fatalf("%s %v", name, err)
	}
	for _, data := range [][]byte{nil, bytes.Repeat([]byte{'x'}, MaxBytes+1), []byte("\x89PNG\r\n\x1a\ninvalid")} {
		if _, _, err = Inspect("x.png", data); err == nil {
			t.Fatal("invalid attachment accepted")
		}
	}
}
func TestActualImageAndSafeExtension(t *testing.T) {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 12)))
	_, kind, err := Inspect("misleading.txt", b.Bytes())
	if err != nil || kind != "image/png" || Extension("misleading.txt", kind) != ".png" {
		t.Fatal(kind, err)
	}
	for _, name := range []string{"file.;sh", "file.longextensionlong", "file", "file.中文"} {
		if Extension(name, "application/octet-stream") != ".bin" {
			t.Fatal(name)
		}
	}
	if Extension("说明.PDF", "application/pdf") != ".pdf" {
		t.Fatal("PDF extension lost")
	}
	disposition := Disposition("图文说明.html")
	typ, params, err := mime.ParseMediaType(disposition)
	if err != nil || typ != "attachment" || params["filename"] != "图文说明.html" {
		t.Fatal(disposition, err)
	}
}
