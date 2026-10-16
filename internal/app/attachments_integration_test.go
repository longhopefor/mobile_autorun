package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"image"
	"image/png"
	"mobile_autorun/internal/device"
	"mobile_autorun/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGeneralAttachmentsTransferIndexAndModelReference(t *testing.T) {
	a, owner, spec := testApp(t)
	ctx, stop := context.WithTimeout(context.Background(), 12*time.Second)
	defer stop()
	var img bytes.Buffer
	_ = png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 32, 32)))
	file, err := a.Store.SaveNamedAsset(ctx, owner, "说明.pdf", "application/pdf", []byte("%PDF-1.4\nfixture"))
	if err != nil {
		t.Fatal(err)
	}
	photo, err := a.Store.SaveNamedAsset(ctx, owner, "示意图.png", "image/png", img.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	spec.ScheduledAt = nil
	spec.AssetIDs = []string{file.ID, photo.ID}
	uploaded := make(chan string, 2)
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err == nil {
			a.Devices.Serve(ctx, spec.DeviceID, c)
		}
	}))
	defer rpc.Close()
	phone, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(rpc.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	go func() {
		data := map[string]string{}
		for {
			var f device.Frame
			if wsjson.Read(ctx, phone, &f) != nil {
				return
			}
			var result any
			switch f.Method {
			case "files/upload":
				var q map[string]string
				_ = json.Unmarshal(f.Params, &q)
				data[q["path"]] = q["data"]
				uploaded <- q["path"]
				result = "ok"
			case "files/download":
				var q map[string]string
				_ = json.Unmarshal(f.Params, &q)
				result = data[q["path"]]
			case "media/capabilities":
				result = map[string]any{"version": 1, "supports_index": true, "permission_granted": true, "requires_all_files_access": true, "max_files": 9, "max_file_bytes": 16777216, "path_format": "Pictures/mobile_autorun_<uuid>/01.png"}
			case "media/index":
				var q struct {
					Paths []string `json:"paths"`
				}
				_ = json.Unmarshal(f.Params, &q)
				if len(q.Paths) != 1 {
					return
				}
				result = map[string]any{"files": []any{map[string]string{"path": q.Paths[0], "uri": "content://media/external_primary/images/media/42"}}}
			case "screenshot":
				result = base64.StdEncoding.EncodeToString(img.Bytes())
			case "state":
				result = `{"a11y_tree":[],"phone_state":{"packageName":"com.android.settings"}}`
			default:
				return
			}
			raw, _ := json.Marshal(result)
			if wsjson.Write(ctx, phone, device.Frame{ID: f.ID, Status: "success", Result: raw}) != nil {
				return
			}
		}
	}()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q map[string]any
		_ = json.NewDecoder(r.Body).Decode(&q)
		parts := q["messages"].([]any)[1].(map[string]any)["content"].([]any)
		prompt := parts[0].(map[string]any)["text"].(string)
		if !strings.Contains(prompt, "说明.pdf") || !strings.Contains(prompt, "/01.pdf") || !strings.Contains(prompt, "/02.png") || len(parts) != 4 {
			t.Errorf("attachment context missing: %s parts=%d", prompt, len(parts))
		}
		marker := "当前 observation_id："
		pos := strings.Index(prompt, marker)
		obs := strings.Split(prompt[pos+len(marker):], "\n")[0]
		d := domain.Decision{Tool: "request_takeover", ObservationID: obs, Reason: "test complete"}
		raw, _ := json.Marshal(d)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"function": map[string]any{"name": d.Tool, "arguments": string(raw)}}}}}}})
	}))
	defer api.Close()
	profile, err := a.Store.Profile(ctx, owner, spec.ModelProfileID)
	if err != nil {
		t.Fatal(err)
	}
	profile.BaseURL = api.URL
	_, err = a.Store.SaveProfile(ctx, owner, profile)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MODEL_TEST_KEY", "fixture")
	for !a.Devices.Online(spec.DeviceID) {
		select {
		case <-ctx.Done():
			t.Fatal("phone not registered")
		case <-time.After(time.Millisecond):
		}
	}
	task, err := a.Create(ctx, owner, "general-attachments", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Run(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Download/mobile_autorun_" + task.ID + "/01.pdf", "Pictures/mobile_autorun_" + task.ID + "/02.png"} {
		select {
		case actual := <-uploaded:
			if actual != expected {
				t.Fatalf("upload path %s expected %s", actual, expected)
			}
		default:
			t.Fatal("missing upload")
		}
	}
	events, err := a.Store.Events(ctx, task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	indexed := 0
	transferred := 0
	for _, event := range events {
		if event.Type == "material_transferred" {
			transferred++
		}
		if event.Type == "media_indexed" {
			indexed++
			if !strings.Contains(string(event.Data), "portal_native") {
				t.Fatal(string(event.Data))
			}
		}
	}
	if transferred != 2 || indexed != 1 {
		t.Fatalf("transfers=%d indexed=%d", transferred, indexed)
	}
	task, err = a.Store.Task(ctx, task.ID)
	if err != nil || task.Status != "waiting_human" || task.Error != "test complete" {
		t.Fatalf("%+v %v", task, err)
	}
	list, err := a.Store.Assets(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, asset := range list {
		if asset.ID == file.ID {
			found = asset.Name == "说明.pdf"
		}
	}
	if !found {
		t.Fatal("asset name not persisted")
	}
}
func TestAttachmentOwnershipAndXHSImageRestriction(t *testing.T) {
	a, owner, spec := testApp(t)
	ctx := context.Background()
	file, err := a.Store.SaveNamedAsset(ctx, owner, "notes.txt", "text/plain", []byte("example"))
	if err != nil {
		t.Fatal(err)
	}
	spec.AssetIDs = []string{file.ID}
	spec.Template = "xhs_image"
	spec.Mode = "draft"
	spec.Account = "test"
	spec.Title = "test"
	spec.Body = "test"
	if _, err = a.Create(ctx, owner, "file-not-xhs", spec); err == nil || err.Error() != "xhs_image_asset_required" {
		t.Fatal("XHS accepted file", err)
	}
	if err = a.Store.Bootstrap(ctx, "other", "long-test-password"); err != nil {
		t.Fatal(err)
	}
	other, _, err := a.Store.Login(ctx, "other", "long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := a.Store.SaveNamedAsset(ctx, other.ID, "secret.txt", "text/plain", []byte("other owner"))
	if err != nil {
		t.Fatal(err)
	}
	spec.Template = "general"
	spec.AssetIDs = []string{foreign.ID}
	if _, err = a.Create(ctx, owner, "foreign-asset", spec); err == nil || err.Error() != "asset_not_owned" {
		t.Fatal("foreign file accepted", err)
	}
}
