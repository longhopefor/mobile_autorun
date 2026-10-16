package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrScreenChanged means a decision was rejected before command preparation or dispatch.
var ErrScreenChanged = errors.New("screen_changed_during_model_call")
var ErrWorkBuddyLoginRequired = errors.New("workbuddy_login_required: 请在本机 WorkBuddy/CodeBuddy CLI 中完成登录后重试")
var ErrOffline = errors.New("offline")
var ErrBusy = errors.New("device_busy")
var ErrUncertain = errors.New("action_uncertain")
var ErrPause = errors.New("pause_requested")
var ErrIterationLimit = errors.New("iteration_limit_reached")

const MaxTaskIterations = 500

type Action struct {
	Kind       string    `json:"kind"`
	X          int       `json:"x,omitempty"`
	Y          int       `json:"y,omitempty"`
	EndX       int       `json:"end_x,omitempty"`
	EndY       int       `json:"end_y,omitempty"`
	DurationMS int       `json:"duration_ms,omitempty"`
	Text       string    `json:"text,omitempty"`
	Package    string    `json:"package,omitempty"`
	Target     *UITarget `json:"target,omitempty"`
}

func (a Action) Validate(_ []string) error {
	if a.Target != nil && (a.Kind != "tap" || (a.Target.ResourceID == "" && a.Target.Text == "" && a.Target.Description == "")) {
		return errors.New("invalid_ui_target")
	}
	if len(a.Text) > 16000 {
		return errors.New("text_too_long")
	}
	switch a.Kind {
	case "tap", "swipe":
		for _, v := range []int{a.X, a.Y, a.EndX, a.EndY} {
			if v < 0 || v > 1000 {
				return errors.New("coordinates_out_of_range")
			}
		}
		if a.DurationMS < 0 || a.DurationMS > 5000 {
			return errors.New("invalid_duration")
		}
	case "type":
		if a.Text == "" {
			return errors.New("empty_text")
		}
	case "launch":
		if a.Package == "" {
			return errors.New("package_required")
		}
	case "back", "home":
	case "wait":
		if a.DurationMS < 100 || a.DurationMS > 5000 {
			return errors.New("wait_out_of_range")
		}
	default:
		return fmt.Errorf("unsupported_action: %s", a.Kind)
	}
	return nil
}
func Pixel(v, size int) int {
	if size <= 1 {
		return 0
	}
	return (v*(size-1) + 500) / 1000
}

type Observation struct {
	ID         string          `json:"id"`
	DeviceID   string          `json:"device_id"`
	AssetID    string          `json:"asset_id"`
	Width      int             `json:"width"`
	Height     int             `json:"height"`
	State      json.RawMessage `json:"state"`
	CapturedAt time.Time       `json:"captured_at"`
	Image      []byte          `json:"-"`
	Revision   uint64          `json:"-"`
}
type Decision struct {
	Tool          string          `json:"tool"`
	ObservationID string          `json:"observation_id"`
	Action        *Action         `json:"action"`
	Reason        string          `json:"reason"`
	Record        *BusinessRecord `json:"record,omitempty"`
}

func (d Decision) Validate(o string, packages []string) error {
	if d.ObservationID != o || o == "" {
		return errors.New("stale_observation")
	}
	if len(d.Reason) > 4000 {
		return errors.New("reason_too_long")
	}
	if d.Tool != "record_business" && d.Tool != "dispatch_business" && d.Record != nil {
		return errors.New("unexpected_business_record")
	}
	switch d.Tool {
	case "phone_action":
		if d.Action == nil {
			return errors.New("action_required")
		}
		return d.Action.Validate(packages)
	case "record_business", "dispatch_business":
		if d.Action != nil || d.Record == nil {
			return errors.New("business_record_required")
		}
		return d.Record.Validate()
	case "finish_task", "request_takeover":
		if d.Action != nil {
			return errors.New("unexpected_action")
		}
	default:
		return errors.New("unsupported_tool")
	}
	return nil
}
func DecodeStrict(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("trailing_json")
	}
	return nil
}

type TaskSpec struct {
	Business       *BusinessSpec `json:"business,omitempty"`
	Goal           string        `json:"goal"`
	DeviceID       string        `json:"device_id"`
	ModelProfileID string        `json:"model_profile_id"`
	ModelSnapshot  *Profile      `json:"model_snapshot,omitempty"`
	Template       string        `json:"template"`
	Mode           string        `json:"mode"`
	Title          string        `json:"title"`
	Body           string        `json:"body"`
	Account        string        `json:"account"`
	AssetIDs       []string      `json:"asset_ids"`
	Packages       []string      `json:"packages"`
	ScheduledAt    *time.Time    `json:"scheduled_at,omitempty"`
	MaxIterations  int           `json:"max_iterations"`
}

func (s *TaskSpec) Validate() error {
	if s.DeviceID == "" || s.ModelProfileID == "" {
		return errors.New("device_and_model_required")
	}
	if s.MaxIterations == 0 {
		s.MaxIterations = 30
	}
	if s.MaxIterations < 1 || s.MaxIterations > MaxTaskIterations {
		return errors.New("invalid_iteration_limit")
	}
	if s.Template == "" {
		s.Template = "general"
	}
	if len(s.AssetIDs) > 9 {
		return errors.New("attachment_limit_9")
	}
	seenAssets := map[string]bool{}
	for _, id := range s.AssetIDs {
		if id == "" || seenAssets[id] {
			return errors.New("invalid_or_duplicate_asset")
		}
		seenAssets[id] = true
	}
	switch s.Template {
	case "wechat_crm", "xhs_growth":
		return s.ValidateBusiness()
	case "general":
		if s.Goal == "" || len(s.Goal) > 12000 {
			return errors.New("invalid_goal")
		}
	case "xhs_image":
		if s.Mode != "edit" && s.Mode != "draft" && s.Mode != "publish" {
			return errors.New("explicit_mode_required")
		}
		if s.Title == "" || s.Body == "" || s.Account == "" || len(s.AssetIDs) == 0 || len(s.AssetIDs) > 9 {
			return errors.New("xhs_content_account_images_required")
		}
		if len([]rune(s.Title)) > 20 || len([]rune(s.Body)) > 1000 {
			return errors.New("xhs_title_20_body_1000_limit")
		}
		seen := map[string]bool{}
		for _, id := range s.AssetIDs {
			if seen[id] {
				return errors.New("duplicate_image_asset")
			}
			seen[id] = true
		}
		s.Packages = []string{"com.xingin.xhs"}
		s.Goal = fmt.Sprintf("小红书图文操作。指定账号：%s。标题：%s。正文：%s。模式：%s。图片按预先传入顺序选择。核对账号和全部内容；edit 只编辑停在编辑页，draft 只保存草稿，publish 明确发布一次。遇到验证码、登录或账号不匹配请求接管。完成后提交可核验的页面证据。", s.Account, s.Title, s.Body, s.Mode)
	default:
		return errors.New("unsupported_template")
	}
	return nil
}

type Task struct {
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	ID         string     `json:"id"`
	Owner      string     `json:"owner"`
	Status     string     `json:"status"`
	Spec       TaskSpec   `json:"spec"`
	Error      string     `json:"error,omitempty"`
	Iterations int        `json:"iterations"`
	CreatedAt  time.Time  `json:"created_at"`
}
type Profile struct {
	ImageMaxWidth int    `json:"image_max_width,omitempty"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	BaseURL       string `json:"base_url,omitempty"`
	SecretEnv     string `json:"secret_env,omitempty"`
	ConnectorID   string `json:"connector_id,omitempty"`
}
type Event struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}
