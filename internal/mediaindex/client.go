package mediaindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mobile_autorun/internal/device"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Caller is implemented by both authenticated WebSocket gateways.
type Caller interface {
	Call(context.Context, string, string, any) (json.RawMessage, error)
	Online(string) bool
}
type Client struct{ Portal, Legacy Caller }
type Capabilities struct {
	Version                int    `json:"version"`
	SupportsIndex          *bool  `json:"supports_index"`
	PermissionGranted      *bool  `json:"permission_granted"`
	RequiresAllFilesAccess *bool  `json:"requires_all_files_access"`
	MaxFiles               int    `json:"max_files"`
	MaxFileBytes           int64  `json:"max_file_bytes"`
	PathFormat             string `json:"path_format"`
}
type Availability struct {
	Native       *Capabilities `json:"native,omitempty"`
	LegacyOnline bool          `json:"legacy_online"`
	Transport    string        `json:"transport"`
	Reason       string        `json:"reason,omitempty"`
}

const pathFormat = "Pictures/mobile_autorun_<uuid>/01.png"

// Detect only probes capabilities. Network, permission and protocol errors must
// never trigger a second indexing attempt through the legacy gateway.
func (c Client) Detect(ctx context.Context, id string) (Availability, error) {
	out := Availability{Transport: "unavailable", LegacyOnline: c.Legacy != nil && c.Legacy.Online(id)}
	if c.Portal == nil || !c.Portal.Online(id) {
		out.Reason = "portal_offline"
		return out, errors.New(out.Reason)
	}
	probe, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	raw, err := c.Portal.Call(probe, id, "media/capabilities", map[string]any{})
	if err != nil {
		var remote *device.RemoteError
		if errors.As(err, &remote) && remote.Method == "media/capabilities" && remote.Message == "Unknown method: media/capabilities" {
			return legacyAvailability(out), nil
		}
		out.Reason = "portal_capabilities_failed"
		return out, fmt.Errorf("%s: %w", out.Reason, err)
	}
	var caps Capabilities
	if json.Unmarshal(raw, &caps) != nil || caps.Version != 1 || caps.SupportsIndex == nil || caps.PermissionGranted == nil || caps.RequiresAllFilesAccess == nil || !*caps.RequiresAllFilesAccess || caps.MaxFiles != 9 || caps.MaxFileBytes != 16<<20 || caps.PathFormat != pathFormat {
		out.Reason = "invalid_media_capabilities"
		return out, errors.New(out.Reason)
	}
	out.Native = &caps
	if !*caps.SupportsIndex {
		return legacyAvailability(out), nil
	}
	if !*caps.PermissionGranted {
		out.Reason = "portal_all_files_access_required"
		return out, nil
	}
	out.Transport = "portal_native"
	return out, nil
}
func legacyAvailability(out Availability) Availability {
	out.Reason = "portal_media_index_unsupported"
	if out.LegacyOnline {
		out.Transport = "usb_legacy"
	} else {
		out.Reason = "portal_update_or_usb_media_connector_required"
	}
	return out
}
func (c Client) Index(ctx context.Context, id string, paths []string) (Result, error) {
	var out Result
	if err := ValidatePaths(paths); err != nil {
		return out, err
	}
	availability, err := c.Detect(ctx, id)
	if err != nil {
		return out, err
	}
	requestPaths := append([]string(nil), paths...)
	caller := c.Portal
	switch availability.Transport {
	case "portal_native":
		for i, p := range requestPaths {
			requestPaths[i] = strings.TrimPrefix(p, "/sdcard/")
		}
	case "usb_legacy":
		caller = c.Legacy
	default:
		return out, errors.New(availability.Reason)
	}
	raw, err := caller.Call(ctx, id, "media/index", Request{Paths: requestPaths})
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return Result{}, errors.New("invalid_media_index_result")
	}
	if len(out.Files) != len(paths) {
		return Result{}, errors.New("media_index_count_mismatch")
	}
	seen := map[string]bool{}
	for i, f := range out.Files {
		if f.Path != requestPaths[i] || !validMediaURI(f.URI) || seen[f.URI] {
			return Result{}, errors.New("media_index_path_or_uri_mismatch")
		}
		seen[f.URI] = true
		out.Files[i].Path = paths[i]
	}
	out.Transport = availability.Transport
	return out, nil
}

var imageRow = regexp.MustCompile(`^/(external|external_primary)/images/media/[1-9][0-9]*$`)

func validMediaURI(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && !strings.Contains(raw, "#") && u.Scheme == "content" && u.Host == "media" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == "" && u.RawPath == "" && imageRow.MatchString(u.Path)
}
