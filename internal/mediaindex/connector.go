// Package mediaindex supports native Portal indexing and a local USB compatibility connector.
package mediaindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"mobile_autorun/internal/device"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type Config struct {
	ServerURL string `json:"server_url"`
	Token     string `json:"token"`
	ADBBinary string `json:"adb_binary"`
	Serial    string `json:"serial"`
}
type Request struct {
	Paths []string `json:"paths"`
}
type File struct {
	Path string `json:"path"`
	URI  string `json:"uri"`
}
type Result struct {
	Files     []File `json:"files"`
	Transport string `json:"transport,omitempty"`
}

var ownedPath = regexp.MustCompile(`^/sdcard/Pictures/mobile_autorun_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/0[1-9]\.(png|jpg)$`)

func ValidatePaths(paths []string) error {
	if len(paths) < 1 || len(paths) > 9 {
		return errors.New("one_to_nine_owned_images_required")
	}
	seen := map[string]bool{}
	folder := ""
	for _, p := range paths {
		if !ownedPath.MatchString(p) || seen[p] {
			return errors.New("invalid_owned_media_path")
		}
		seen[p] = true
		dir := p[:strings.LastIndex(p, "/")]
		if folder != "" && folder != dir {
			return errors.New("mixed_task_folders")
		}
		folder = dir
	}
	return nil
}
func (c Config) adb(ctx context.Context, args ...string) (string, error) {
	if c.ADBBinary == "" || c.Serial == "" {
		return "", errors.New("adb_binary_and_serial_required")
	}
	cmd := exec.CommandContext(ctx, c.ADBBinary, append([]string{"-s", c.Serial}, args...)...)
	output, e := cmd.Output()
	if e != nil {
		return "", errors.New("adb_media_command_failed")
	}
	if len(output) > 64<<10 {
		return "", errors.New("adb_output_too_large")
	}
	return string(output), nil
}
func (c Config) Index(ctx context.Context, q Request) (Result, error) {
	var out Result
	if e := ValidatePaths(q.Paths); e != nil {
		return out, e
	}
	state, e := c.adb(ctx, "get-state")
	if e != nil || strings.TrimSpace(state) != "device" {
		return out, errors.New("usb_device_not_authorized")
	}
	// Scan in reverse order so descending MediaStore insertion order starts
	// with 01. Gallery order is still checked visually, never assumed.
	for i := len(q.Paths) - 1; i >= 0; i-- {
		p := q.Paths[i]
		if _, e = c.adb(ctx, "shell", "am", "broadcast", "-a", "android.intent.action.MEDIA_SCANNER_SCAN_FILE", "-d", "file://"+p); e != nil {
			return out, e
		}
	}
	for _, p := range q.Paths {
		var uri string
		for attempt := 0; attempt < 8; attempt++ {
			// Shell input consists solely of the validated fixed prefix, UUID and
			// numeric filename. Never accept arbitrary paths or shell fragments.
			where := `"_data='/storage/emulated/0/` + strings.TrimPrefix(p, "/sdcard/") + `'"`
			text, e := c.adb(ctx, "shell", "content", "query", "--uri", "content://media/external/images/media", "--projection", "_id", "--where", where)
			if e != nil {
				return out, e
			}
			match := regexp.MustCompile(`_id=(\d+)`).FindStringSubmatch(text)
			if len(match) == 2 {
				uri = "content://media/external/images/media/" + match[1]
				break
			}
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return out, ctx.Err()
			case <-timer.C:
			}
		}
		if uri == "" {
			return out, errors.New("media_store_entry_not_found")
		}
		out.Files = append(out.Files, File{Path: p, URI: uri})
	}
	return out, nil
}
func Serve(ctx context.Context, c Config) error {
	u, e := url.Parse(c.ServerURL)
	if e != nil || u.Host == "" || (u.Scheme != "wss" && !(u.Scheme == "ws" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) {
		return errors.New("wss_or_loopback_ws_required")
	}
	if c.Token == "" || c.Serial == "" || c.ADBBinary == "" {
		return errors.New("media_connector_config_incomplete")
	}
	for ctx.Err() == nil {
		e = serveOnce(ctx, c)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fmt.Println("MediaConnector disconnected; retrying")
		t := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
		case <-t.C:
		}
	}
	return ctx.Err()
}
func serveOnce(ctx context.Context, c Config) error {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+c.Token)
	conn, _, e := websocket.Dial(ctx, c.ServerURL, &websocket.DialOptions{HTTPHeader: h})
	if e != nil {
		return e
	}
	defer conn.CloseNow()
	conn.SetReadLimit(64 << 10)
	for {
		var frame device.Frame
		if e = wsjson.Read(ctx, conn, &frame); e != nil {
			return e
		}
		if frame.ID == "" {
			continue
		}
		var q Request
		reply := device.Frame{ID: frame.ID, Status: "success"}
		if frame.Method != "media/index" {
			e = errors.New("unsupported_media_method")
		} else {
			e = json.Unmarshal(frame.Params, &q)
		}
		if e == nil {
			job, stop := context.WithTimeout(ctx, 25*time.Second)
			var result Result
			result, e = c.Index(job, q)
			stop()
			if e == nil {
				reply.Result, _ = json.Marshal(result)
			}
		}
		if e != nil {
			reply.Status = "error"
			reply.Error, _ = json.Marshal(e.Error())
		}
		if e = wsjson.Write(ctx, conn, reply); e != nil {
			return e
		}
	}
}
