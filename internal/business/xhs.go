package business

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/model"
	"regexp"
	"strings"
)

const XHSPackage = "com.xingin.xhs"

type XHSState struct {
	Stage              string `json:"stage"`
	AccountVerified    bool   `json:"account_verified"`
	AccountObservation string `json:"account_observation_id,omitempty"`
	AlbumVerified      bool   `json:"album_verified"`
	SelectedCount      int    `json:"selected_count"`
	ContentVerified    bool   `json:"content_verified"`
	ContentObservation string `json:"content_observation_id,omitempty"`
	SaveRequested      bool   `json:"save_requested"`
	SaveConfirmed      bool   `json:"save_confirmed"`
	DraftListed        bool   `json:"draft_listed"`
	ReopenedVerified   bool   `json:"reopened_verified"`
	PublishRequested   bool   `json:"publish_requested"`
	ResultObservation  string `json:"result_observation_id,omitempty"`
}
type XHS struct {
	Spec       domain.TaskSpec
	Album      string
	State      XHSState
	Delegate   model.Provider
	References []model.Reference
	Save       func(context.Context, XHSState) error
}

func (x *XHS) persist(ctx context.Context, prior XHSState) error {
	if prior == x.State {
		return nil
	}
	return x.Save(ctx, x.State)
}
func editable(u domain.UI, role string) (domain.UINode, bool) {
	for _, n := range u.Nodes {
		if !n.Visible || !n.Enabled || !n.Editable {
			continue
		}
		label := n.Hint + " " + n.Text
		if role == "title" && (strings.Contains(label, "标题") || strings.HasSuffix(n.ResourceID, "/title")) {
			return n, true
		}
		if role == "body" && (strings.Contains(label, "正文") || strings.Contains(label, "说点什么") || strings.Contains(label, "分享") || strings.HasSuffix(n.ResourceID, "/content")) {
			return n, true
		}
	}
	return domain.UINode{}, false
}
func (x *XHS) fields(u domain.UI) (domain.UINode, domain.UINode, bool) {
	var title, body domain.UINode
	ht, hb := false, false
	for _, n := range u.Nodes {
		if n.Visible && n.Editable && n.Text == x.Spec.Title {
			title = n
			ht = true
		}
		if n.Visible && n.Editable && n.Text == x.Spec.Body {
			body = n
			hb = true
		}
	}
	return title, body, ht && hb
}
func isEditor(u domain.UI) bool {
	for _, n := range u.Nodes {
		if n.Visible && n.Editable && (strings.Contains(n.Hint, "标题") || strings.Contains(n.Hint, "正文") || strings.Contains(n.Hint, "分享") || strings.Contains(n.Hint, "说点什么")) {
			return true
		}
	}
	return u.HasText("发布笔记") || u.HasText("存草稿")
}
func saveDialog(u domain.UI) bool {
	for _, n := range u.Nodes {
		if n.Visible && strings.Contains(n.Text, "确认保存笔记至草稿箱") {
			return true
		}
	}
	return false
}
func (x *XHS) Observe(ctx context.Context, o domain.Observation) error {
	u, e := domain.ParseUI(o)
	if e != nil {
		return e
	}
	prior := x.State
	if x.State.Stage == "" {
		x.State.Stage = "account"
	}
	if u.Package != XHSPackage {
		return nil
	}
	for _, n := range u.Nodes {
		if !n.Visible {
			continue
		}
		if strings.Contains(n.Text, "验证码") || n.Text == "登录" || n.Text == "账号异常" || strings.Contains(n.Text, "安全验证") {
			return errors.New("xhs_login_or_verification_requires_takeover")
		}
	}
	// Only the actual profile name/number controls establish account identity.
	profile := false
	matched := false
	for _, n := range u.Nodes {
		if n.Visible && (n.ResourceID == XHSPackage+":id/h0k" || n.ResourceID == XHSPackage+":id/h0m") {
			profile = true
			v := strings.TrimSpace(strings.TrimPrefix(n.Text, "小红书号："))
			if v == strings.TrimSpace(x.Spec.Account) {
				matched = true
			}
		}
	}
	if profile && !matched {
		return errors.New("xhs_account_mismatch")
	}
	if profile && matched && !x.State.AccountVerified {
		x.State.AccountVerified = true
		x.State.AccountObservation = o.ID
		x.State.Stage = "media"
	}
	if x.State.AccountVerified {
		if x.State.Stage == "media" {
			for _, n := range u.Nodes {
				if n.Visible && n.ResourceID == XHSPackage+":id/oa" {
					x.State.AlbumVerified = n.Text == x.Album
				}
			}
		}
		for _, n := range u.Nodes {
			if n.Visible && n.ResourceID == XHSPackage+":id/oa" && n.Text == x.Album {
				x.State.AlbumVerified = true
			}
		}
		if x.State.AlbumVerified && x.State.SelectedCount == 0 {
			for _, n := range u.Nodes {
				m := regexp.MustCompile(`^下一步[（(](\d+)[）)]$`).FindStringSubmatch(n.Text)
				if n.Visible && len(m) == 2 && m[1] == fmt.Sprint(len(x.Spec.AssetIDs)) {
					x.State.SelectedCount = len(x.Spec.AssetIDs)
					x.State.Stage = "compose"
				}
			}
		}
		_, _, correct := x.fields(u)
		if correct && x.State.SelectedCount == len(x.Spec.AssetIDs) {
			if !x.State.ContentVerified {
				x.State.ContentObservation = o.ID
			}
			x.State.ContentVerified = true
			if x.State.Stage == "compose" {
				x.State.Stage = "save"
			}
		}
		if x.State.SaveRequested && !isEditor(u) && (u.HasText("草稿") || u.HasText("草稿箱") || u.HasText("本地草稿")) && u.HasText(x.Spec.Title) {
			x.State.DraftListed = true
			x.State.Stage = "reopen"
		}
		if x.State.DraftListed && correct && isEditor(u) {
			x.State.ReopenedVerified = true
			x.State.ResultObservation = o.ID
			x.State.Stage = "complete"
		}
		if x.Spec.Mode == "edit" && correct && x.State.ContentVerified {
			x.State.ResultObservation = o.ID
			x.State.Stage = "complete"
		}
		if x.Spec.Mode == "publish" && x.State.PublishRequested && u.HasText("发布成功") {
			x.State.ResultObservation = o.ID
			x.State.Stage = "complete"
		}
	}
	return x.persist(ctx, prior)
}
func (x *XHS) PlanObservation(ctx context.Context, o domain.Observation) (model.Result, bool, error) {
	if e := x.Observe(ctx, o); e != nil {
		return model.Result{}, false, e
	}
	u, e := domain.ParseUI(o)
	if e != nil {
		return model.Result{}, false, e
	}
	decision := domain.Decision{Tool: "phone_action", ObservationID: o.ID, Reason: "小红书阶段：" + x.State.Stage}
	result := func(a domain.Action) (model.Result, bool, error) {
		decision.Action = &a
		return model.Result{Decision: decision, Source: "xhs_rules"}, true, nil
	}
	tap := func(t domain.UITarget) (model.Result, bool, error) {
		a, e := domain.TargetTap(o, t)
		if e != nil {
			return model.Result{}, false, nil
		}
		return result(a)
	}
	if u.Package != XHSPackage {
		return result(domain.Action{Kind: "launch", Package: XHSPackage})
	}
	if x.State.Stage == "complete" {
		decision.Tool = "finish_task"
		decision.Reason = "业务阶段检查通过，仍需人工核验最新截图"
		return model.Result{Decision: decision, Source: "xhs_rules"}, true, nil
	}
	if !x.State.AccountVerified {
		if isEditor(u) {
			return model.Result{}, false, errors.New("xhs_existing_editor_requires_takeover")
		}
		return tap(domain.UITarget{ResourceID: XHSPackage + ":id/dvj", Description: "我"})
	}
	if x.State.Stage == "media" {
		if x.State.SelectedCount == 0 && (u.HasText("裁切") || u.HasText("滤镜")) {
			return result(domain.Action{Kind: "back", Package: XHSPackage})
		}
		if _, e = u.Find(domain.UITarget{ResourceID: XHSPackage + ":id/dvl", Description: "发布"}); e == nil {
			return tap(domain.UITarget{ResourceID: XHSPackage + ":id/dvl", Description: "发布"})
		}
		if !x.State.AlbumVerified {
			if u.HasText(x.Album) {
				return tap(domain.UITarget{Text: x.Album})
			}
			if _, e = u.Find(domain.UITarget{ResourceID: XHSPackage + ":id/oa"}); e == nil {
				return tap(domain.UITarget{ResourceID: XHSPackage + ":id/oa"})
			}
		}
	}
	if x.State.Stage == "compose" {
		for _, n := range u.Nodes {
			if n.Visible && strings.HasPrefix(n.Text, "下一步") {
				return tap(domain.UITarget{ResourceID: n.ResourceID, Text: n.Text})
			}
		}
		for _, role := range []string{"title", "body"} {
			want := x.Spec.Title
			if role == "body" {
				want = x.Spec.Body
			}
			for _, n := range u.Nodes {
				if n.Visible && n.Editable && n.Text == want {
					goto nextField
				}
			}
			if n, ok := editable(u, role); ok {
				if n.Focused && u.Focus == n.ResourceID {
					return result(domain.Action{Kind: "type", Text: want, Package: XHSPackage})
				}
				return tap(domain.UITarget{ResourceID: n.ResourceID})
			}
		nextField:
		}
	}
	if x.State.Stage == "save" {
		if u.Keyboard {
			if _, e := u.Find(domain.UITarget{ResourceID: XHSPackage + ":id/dxf", Text: "完成"}); e == nil {
				return tap(domain.UITarget{ResourceID: XHSPackage + ":id/dxf", Text: "完成"})
			}
			return result(domain.Action{Kind: "back", Package: XHSPackage})
		}
		if x.Spec.Mode == "publish" {
			return tap(domain.UITarget{Text: "发布笔记"})
		}
		for _, label := range []string{"存草稿", "保存草稿"} {
			if u.HasText(label) {
				return tap(domain.UITarget{Text: label})
			}
		}
		if isEditor(u) {
			return result(domain.Action{Kind: "back", Package: XHSPackage})
		}
	}
	if x.State.Stage == "reopen" {
		return tap(domain.UITarget{Text: x.Spec.Title})
	}
	if x.State.SaveRequested {
		if saveDialog(u) {
			if x.State.SaveConfirmed {
				return model.Result{}, false, errors.New("xhs_draft_confirmation_pending")
			}
			return tap(domain.UITarget{ResourceID: XHSPackage + ":id/idy", Text: "确定"})
		}
		if isEditor(u) {
			return result(domain.Action{Kind: "back", Package: XHSPackage})
		}
		for _, label := range []string{"草稿", "草稿箱", "本地草稿"} {
			if u.HasText(label) {
				return tap(domain.UITarget{Text: label})
			}
		}
		if _, e = u.Find(domain.UITarget{ResourceID: XHSPackage + ":id/dvj", Description: "我"}); e == nil {
			return tap(domain.UITarget{ResourceID: XHSPackage + ":id/dvj", Description: "我"})
		}
	}
	return model.Result{}, false, nil
}
func (x *XHS) Next(ctx context.Context, q model.Request) (model.Result, error) {
	q.Goal = fmt.Sprintf("当前业务阶段=%s。账号已经核对=%v。只能操作当前小红书任务；不要改账号、不要选择已有用户草稿、不要使用其他图片。目标相册=%s；只从这个独立相册选择本任务 %d 张图片，按01到09顺序。相册找不到或图片与参考图不符就 request_takeover，不要选择全部相册第一张。标题=%s。正文=%s。模式=%s。禁止公开发布，除非模式明确为publish且服务端已经核对全部内容。保存草稿后必须进入草稿箱，重新打开当前标题并核对内容，不能仅凭退出编辑器就完成。当前stage不允许的动作会被服务端拒绝。", x.State.Stage, x.State.AccountVerified, x.Album, len(x.Spec.AssetIDs), x.Spec.Title, x.Spec.Body, x.Spec.Mode)
	if x.State.Stage == "media" && x.State.AlbumVerified {
		q.References = x.References
		q.Goal += " 第一张附图是实时手机截图，之后附图是依次对应01、02等目标素材参考图。在网格点击图片右上角选择圆圈，不要点击缩略图打开预览。确认选中后先等下一轮观察，必须看到下一步(所需图片数)，不要直接提交finish_task。如果误入预览页先返回网格。"
	}
	u, e := domain.ParseUI(domain.Observation{State: q.State})
	if e != nil {
		return model.Result{}, e
	}
	originalState := q.State
	compact, _ := json.Marshal(u)
	q.State = compact
	r, e := x.Delegate.Next(ctx, q)
	if e != nil {
		return r, e
	}
	r.Source = "visual_model"
	// Preserve a visual decision's named-control intent while requiring the
	// exact control to remain unchanged. Pixel-only image taps stay strict.
	if r.Decision.Action != nil && r.Decision.Action.Kind == "tap" {
		a := r.Decision.Action
		for _, n := range u.Nodes {
			if n.Visible && n.Enabled && !n.Editable && n.Text != "" && n.ResourceID != "" && (strings.HasPrefix(n.Text, "下一步") || n.Text == "完成") {
				// Dimensions are obtained from the screenshot, never from UI text.
				if conf, e := png.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(q.Image))); e == nil {
					px, py := domain.Pixel(a.X, conf.Width), domain.Pixel(a.Y, conf.Height)
					if px >= n.Bounds.Left && px < n.Bounds.Right && py >= n.Bounds.Top && py < n.Bounds.Bottom {
						o := domain.Observation{State: originalState, Width: conf.Width, Height: conf.Height}
						named, e := domain.TargetTap(o, domain.UITarget{ResourceID: n.ResourceID, Text: n.Text})
						if e == nil {
							r.Decision.Action = &named
						}
					}
				}
			}
		}
	}
	return r, nil
}
func (x *XHS) Guard(ctx context.Context, o domain.Observation, a domain.Action) error {
	if e := x.Observe(ctx, o); e != nil {
		return e
	}
	u, e := domain.ParseUI(o)
	if e != nil {
		return e
	}
	if a.Kind == "launch" && a.Package != XHSPackage {
		return errors.New("xhs_foreign_app_forbidden")
	}
	if a.Kind == "type" {
		if !x.State.AccountVerified || x.State.SelectedCount != len(x.Spec.AssetIDs) || x.State.Stage != "compose" {
			return errors.New("xhs_content_stage_required")
		}
		if a.Text != x.Spec.Title && a.Text != x.Spec.Body {
			return errors.New("xhs_unexpected_content")
		}
		focusOK := false
		for _, n := range u.Nodes {
			if n.Visible && n.Editable && n.Focused && n.ResourceID == u.Focus {
				title := strings.Contains(n.Hint, "标题") || n.ResourceID == XHSPackage+":id/c4f"
				body := strings.Contains(n.Hint, "正文") || strings.Contains(n.Hint, "说点什么") || n.ResourceID == XHSPackage+":id/gtq"
				focusOK = (title && a.Text == x.Spec.Title) || (body && a.Text == x.Spec.Body)
			}
		}
		if !focusOK {
			return errors.New("xhs_wrong_focused_input")
		}
	}
	if a.Kind == "tap" {
		if saveDialog(u) {
			if x.Spec.Mode != "draft" || !x.State.ContentVerified || !x.State.SaveRequested || x.State.SaveConfirmed || a.Target == nil || a.Target.ResourceID != XHSPackage+":id/idy" || a.Target.Text != "确定" {
				return errors.New("xhs_draft_dialog_control_required")
			}
			prior := x.State
			x.State.SaveConfirmed = true
			return x.persist(ctx, prior)
		}
		if (x.State.Stage == "save" || x.State.Stage == "verify_draft") && a.Target == nil {
			return errors.New("xhs_named_control_required")
		}
		px, py := domain.Pixel(a.X, o.Width), domain.Pixel(a.Y, o.Height)
		for _, n := range u.Nodes {
			if !n.Visible || px < n.Bounds.Left || px >= n.Bounds.Right || py < n.Bounds.Top || py >= n.Bounds.Bottom {
				continue
			}
			label := n.Text
			if label == "" {
				label = n.Description
			}
			if label == "发布笔记" || (label == "发布" && isEditor(u)) {
				if x.Spec.Mode != "publish" || !x.State.ContentVerified || x.State.PublishRequested {
					return errors.New("xhs_publication_not_allowed")
				}
				prior := x.State
				x.State.PublishRequested = true
				return x.persist(ctx, prior)
			}
			if label == "存草稿" || label == "保存草稿" {
				if u.Keyboard {
					return errors.New("xhs_hide_keyboard_before_save")
				}
				if x.Spec.Mode != "draft" || !x.State.ContentVerified || x.State.SaveRequested {
					return errors.New("xhs_draft_save_not_allowed")
				}
				prior := x.State
				x.State.SaveRequested = true
				x.State.Stage = "verify_draft"
				return x.persist(ctx, prior)
			}
		}
	}
	if !x.State.AccountVerified && a.Kind != "launch" {
		if a.Kind != "tap" || a.Target == nil || a.Target.Description != "我" {
			return errors.New("xhs_account_check_first")
		}
	}
	return nil
}
func (x *XHS) Completion(o domain.Observation) error {
	if x.State.Stage != "complete" || !x.State.AccountVerified || x.State.SelectedCount != len(x.Spec.AssetIDs) || x.State.ResultObservation != o.ID {
		return errors.New("xhs_business_evidence_incomplete")
	}
	if x.Spec.Mode == "draft" && !x.State.ReopenedVerified {
		return errors.New("xhs_draft_reopen_required")
	}
	return nil
}
