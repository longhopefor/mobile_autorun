package mediaindex

import (
	"context"
	"encoding/json"
	"errors"
	"mobile_autorun/internal/device"
	"strconv"
	"strings"
	"testing"
)

const testPath = "/sdcard/Pictures/mobile_autorun_33d06875-9ebf-4d2a-b5c5-3cffac42903f/01.png"
const nativeCaps = `{"version":1,"supports_index":true,"permission_granted":true,"requires_all_files_access":true,"max_files":9,"max_file_bytes":16777216,"path_format":"Pictures/mobile_autorun_<uuid>/01.png"}`

type fakeCaller struct {
	online  bool
	methods []string
	call    func(string, any) (json.RawMessage, error)
}

func (f *fakeCaller) Online(string) bool { return f.online }
func (f *fakeCaller) Call(_ context.Context, _ string, m string, p any) (json.RawMessage, error) {
	f.methods = append(f.methods, m)
	return f.call(m, p)
}
func reply(paths []string, volume string) json.RawMessage {
	r := Result{}
	for i, p := range paths {
		r.Files = append(r.Files, File{Path: p, URI: "content://media/" + volume + "/images/media/" + strconv.Itoa(i+1)})
	}
	b, _ := json.Marshal(r)
	return b
}
func TestNativeIndexWithoutUSB(t *testing.T) {
	portal := &fakeCaller{online: true, call: func(m string, p any) (json.RawMessage, error) {
		if m == "media/capabilities" {
			return []byte(nativeCaps), nil
		}
		q := p.(Request)
		if q.Paths[0] != strings.TrimPrefix(testPath, "/sdcard/") {
			t.Fatalf("native paths: %v", q.Paths)
		}
		return reply(q.Paths, "external_primary"), nil
	}}
	out, err := (Client{Portal: portal}).Index(context.Background(), "phone", []string{testPath})
	if err != nil || out.Transport != "portal_native" || out.Files[0].Path != testPath {
		t.Fatalf("%+v %v", out, err)
	}
}
func TestOnlyExplicitUnsupportedUsesLegacy(t *testing.T) {
	for _, caps := range []string{"", strings.Replace(nativeCaps, `"supports_index":true`, `"supports_index":false`, 1)} {
		t.Run(caps, func(t *testing.T) {
			portal := &fakeCaller{online: true, call: func(m string, _ any) (json.RawMessage, error) {
				if caps == "" {
					return nil, &device.RemoteError{Method: m, Message: "Unknown method: media/capabilities"}
				}
				return []byte(caps), nil
			}}
			legacy := &fakeCaller{online: true, call: func(m string, p any) (json.RawMessage, error) {
				if m != "media/index" || p.(Request).Paths[0] != testPath {
					t.Fatal("legacy protocol changed")
				}
				return reply(p.(Request).Paths, "external"), nil
			}}
			out, err := (Client{portal, legacy}).Index(context.Background(), "phone", []string{testPath})
			if err != nil || out.Transport != "usb_legacy" || len(legacy.methods) != 1 {
				t.Fatalf("%+v %v", out, err)
			}
		})
	}
}
func TestNoFallbackOnPermissionProtocolOrConnectionFailure(t *testing.T) {
	cases := []struct {
		name, caps string
		err        error
	}{
		{name: "permission", caps: strings.Replace(nativeCaps, `"permission_granted":true`, `"permission_granted":false`, 1)},
		{name: "missing fields", caps: `{"version":1,"supports_index":false}`},
		{name: "bad version", caps: strings.Replace(nativeCaps, `"version":1`, `"version":2`, 1)},
		{name: "null", caps: `null`},
		{name: "offline", err: errors.New("device_offline")},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "other remote error", err: &device.RemoteError{Method: "media/capabilities", Message: "Permission denied"}},
		{name: "untyped unknown text", err: errors.New("Unknown method: media/capabilities")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			portal := &fakeCaller{online: true, call: func(string, any) (json.RawMessage, error) { return []byte(tc.caps), tc.err }}
			legacy := &fakeCaller{online: true, call: func(string, any) (json.RawMessage, error) { t.Fatal("unexpected fallback"); return nil, nil }}
			_, err := (Client{portal, legacy}).Index(context.Background(), "phone", []string{testPath})
			if err == nil {
				t.Fatal("failure accepted")
			}
		})
	}
}
func TestNoFallbackOnNativeIndexFailure(t *testing.T) {
	portal := &fakeCaller{online: true, call: func(m string, _ any) (json.RawMessage, error) {
		if m == "media/capabilities" {
			return []byte(nativeCaps), nil
		}
		return nil, context.DeadlineExceeded
	}}
	legacy := &fakeCaller{online: true, call: func(string, any) (json.RawMessage, error) { t.Fatal("duplicate indexing attempt"); return nil, nil }}
	_, err := (Client{portal, legacy}).Index(context.Background(), "phone", []string{testPath})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestIndexRejectsBadPathsAndResponses(t *testing.T) {
	relative := strings.TrimPrefix(testPath, "/sdcard/")
	good := string(reply([]string{relative}, "external_primary"))
	for _, raw := range []string{`null`, `{"files":[]}`, strings.Replace(good, relative, "Pictures/other.png", 1), strings.Replace(good, "media/1", "media/0", 1), strings.Replace(good, "external_primary", "internal", 1), strings.Replace(good, "media/1", "media/1?x=1", 1), strings.Replace(good, "media/1", "media/%31", 1), strings.Replace(good, "media/1", "media/1#x", 1), strings.Replace(good, "media/1", "media/1#", 1)} {
		t.Run(raw, func(t *testing.T) {
			portal := &fakeCaller{online: true, call: func(m string, _ any) (json.RawMessage, error) {
				if m == "media/capabilities" {
					return []byte(nativeCaps), nil
				}
				return []byte(raw), nil
			}}
			if _, err := (Client{Portal: portal}).Index(context.Background(), "phone", []string{testPath}); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
	portal := &fakeCaller{online: true, call: func(string, any) (json.RawMessage, error) { t.Fatal("unsafe path sent to phone"); return nil, nil }}
	if _, err := (Client{Portal: portal}).Index(context.Background(), "phone", []string{testPath + "/../01.png"}); err == nil {
		t.Fatal("unsafe path accepted")
	}
}
func TestDuplicateURIsAndReorderedFilesRejected(t *testing.T) {
	paths := []string{testPath, strings.Replace(testPath, "01.png", "02.png", 1)}
	for _, reorder := range []bool{false, true} {
		portal := &fakeCaller{online: true, call: func(m string, p any) (json.RawMessage, error) {
			if m == "media/capabilities" {
				return []byte(nativeCaps), nil
			}
			raw := reply(p.(Request).Paths, "external")
			var r Result
			_ = json.Unmarshal(raw, &r)
			if reorder {
				r.Files[0], r.Files[1] = r.Files[1], r.Files[0]
			} else {
				r.Files[1].URI = r.Files[0].URI
			}
			b, _ := json.Marshal(r)
			return b, nil
		}}
		if _, err := (Client{Portal: portal}).Index(context.Background(), "phone", paths); err == nil {
			t.Fatal("bad ordering/duplicate URI accepted")
		}
	}
}
