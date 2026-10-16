package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"image"
	"image/png"
	"mobile_autorun/internal/device"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/mediaindex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Stop before UI automation, so this test checks the task's real PostgreSQL
// event chain and native RPC integration without implying a real phone test.
type mediaStageDriver struct {
	uploadPath   string
	observations int
}

func (d *mediaStageDriver) Upload(_ context.Context, _ string, path string, _ []byte) error {
	d.uploadPath = path
	return nil
}
func (d *mediaStageDriver) Observe(context.Context, string) (domain.Observation, error) {
	d.observations++
	return domain.Observation{}, errors.New("test_stop_before_ui")
}
func (d *mediaStageDriver) Execute(context.Context, string, domain.Action, domain.Observation) error {
	return errors.New("unexpected_ui_action")
}

func TestXHSTaskNativeMediaStage(t *testing.T) {
	for _, permission := range []bool{true, false} {
		name := "permission_denied"
		if permission {
			name = "native_indexed"
		}
		t.Run(name, func(t *testing.T) {
			a, owner, spec := testApp(t)
			ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			var b bytes.Buffer
			_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 32, 32)))
			asset, err := a.Store.SaveAsset(ctx, owner, "image/png", b.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			spec.ScheduledAt = nil
			spec.Template = "xhs_image"
			spec.Mode = "draft"
			spec.Title = "test"
			spec.Body = "test body"
			spec.Account = "test account"
			spec.AssetIDs = []string{asset.ID}
			driver := &mediaStageDriver{}
			a.Driver = driver
			rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				a.Devices.Serve(ctx, spec.DeviceID, c)
			}))
			defer rpc.Close()
			phone, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(rpc.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer phone.CloseNow()
			go func() {
				for {
					var f device.Frame
					if wsjson.Read(ctx, phone, &f) != nil {
						return
					}
					var result any
					switch f.Method {
					case "media/capabilities":
						result = map[string]any{"version": 1, "supports_index": true, "permission_granted": permission, "requires_all_files_access": true, "max_files": 9, "max_file_bytes": 16777216, "path_format": "Pictures/mobile_autorun_<uuid>/01.png"}
					case "media/index":
						var q mediaindex.Request
						if json.Unmarshal(f.Params, &q) != nil {
							return
						}
						if len(q.Paths) != 1 {
							return
						}
						result = mediaindex.Result{Files: []mediaindex.File{{Path: q.Paths[0], URI: "content://media/external_primary/images/media/42"}}}
					default:
						return
					}
					raw, _ := json.Marshal(result)
					if wsjson.Write(ctx, phone, device.Frame{ID: f.ID, Status: "success", Result: raw}) != nil {
						return
					}
				}
			}()
			for !a.Devices.Online(spec.DeviceID) {
				select {
				case <-ctx.Done():
					t.Fatal("portal connection failed")
				case <-time.After(time.Millisecond):
				}
			}
			task, err := a.Create(ctx, owner, "native-media-test", spec)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.Run(ctx, task.ID); err != nil {
				t.Fatal(err)
			}
			events, err := a.Store.Events(ctx, task.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			indexed := 0
			for _, event := range events {
				if event.Type == "media_indexed" {
					indexed++
					var result mediaindex.Result
					if json.Unmarshal(event.Data, &result) != nil || result.Transport != "portal_native" || len(result.Files) != 1 || result.Files[0].Path != driver.uploadPath {
						t.Fatalf("event: %s", event.Data)
					}
				}
			}
			if permission && (indexed != 1 || driver.observations != 1) {
				t.Fatalf("index=%d observations=%d", indexed, driver.observations)
			}
			if !permission && (indexed != 0 || driver.observations != 0) {
				t.Fatal("UI entered despite denied media permission")
			}
			saved, err := a.Store.Task(ctx, task.ID)
			if err != nil || saved.Status != "waiting_human" {
				t.Fatalf("%+v %v", saved, err)
			}
			if !permission && !strings.Contains(saved.Error, "portal_all_files_access_required") {
				t.Fatal(saved.Error)
			}
		})
	}
}
