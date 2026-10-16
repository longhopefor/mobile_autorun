package device

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"image"
	"image/png"
	"mobile_autorun/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPortalReverseScreenshotAndCoordinates(t *testing.T) {
	g := NewGateway()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := websocket.Accept(w, r, nil)
		if e != nil {
			return
		}
		g.Serve(r.Context(), "phone", c)
	}))
	defer srv.Close()
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	c, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.CloseNow()
	var pngBytes bytes.Buffer
	_ = png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 1080, 1920)))
	tap := make(chan map[string]int, 1)
	go func() {
		for {
			var f Frame
			if wsjson.Read(ctx, c, &f) != nil {
				return
			}
			var value any
			switch f.Method {
			case "screenshot":
				value = base64.StdEncoding.EncodeToString(pngBytes.Bytes())
			case "state":
				value = `{"a11y_tree":[],"phone_state":{"packageName":"com.android.settings"}}`
			case "tap":
				var p map[string]int
				_ = json.Unmarshal(f.Params, &p)
				tap <- p
				value = "ok"
			default:
				value = "ok"
			}
			b, _ := json.Marshal(value)
			_ = wsjson.Write(ctx, c, Frame{ID: f.ID, Status: "success", Result: b})
		}
	}()
	p := &Portal{Gateway: g}
	o, e := p.Observe(ctx, "phone")
	if e != nil {
		t.Fatal(e)
	}
	if o.Width != 1080 || o.Height != 1920 {
		t.Fatal("image dimensions")
	}
	if e = p.Execute(ctx, "phone", domain.Action{Kind: "tap", X: 1000, Y: 500}, o); e != nil {
		t.Fatal(e)
	}
	v := <-tap
	if v["x"] != 1079 || v["y"] != 960 {
		t.Fatalf("wrong physical coordinates: %v", v)
	}
}

func TestPortalUploadUsesRelativePathAndVerifiesBytes(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "matched", true: "corrupt"}[corrupt], func(t *testing.T) {
			g := NewGateway()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, e := websocket.Accept(w, r, nil)
				if e == nil {
					g.Serve(r.Context(), "phone", c)
				}
			}))
			defer srv.Close()
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			c, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if e != nil {
				t.Fatal(e)
			}
			defer c.CloseNow()
			paths := make(chan string, 2)
			go func() {
				var stored string
				for {
					var f Frame
					if wsjson.Read(ctx, c, &f) != nil {
						return
					}
					var args map[string]string
					_ = json.Unmarshal(f.Params, &args)
					paths <- args["path"]
					value := "File written successfully"
					if f.Method == "files/upload" {
						stored = args["data"]
					} else if f.Method == "files/download" {
						value = stored
						if corrupt {
							value = base64.StdEncoding.EncodeToString([]byte("different"))
						}
					}
					b, _ := json.Marshal(value)
					_ = wsjson.Write(ctx, c, Frame{ID: f.ID, Status: "success", Result: b})
				}
			}()
			e = (&Portal{Gateway: g}).Upload(ctx, "phone", "/sdcard/Pictures/test.png", []byte("original"))
			if (e != nil) != corrupt {
				t.Fatalf("corrupt=%v err=%v", corrupt, e)
			}
			for i := 0; i < 2; i++ {
				if got := <-paths; got != "Pictures/test.png" {
					t.Fatalf("wrong Portal relative path: %s", got)
				}
			}
		})
	}
}
