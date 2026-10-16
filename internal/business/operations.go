package business

import (
	"context"
	"encoding/json"
	"errors"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/model"
	"strings"
)

type OperationsState struct {
	FriendRecipient *domain.BusinessRecord `json:"friend_recipient,omitempty"`
	AccountVerified bool                   `json:"account_verified"`
	Records         int                    `json:"records"`
	Posts           int                    `json:"posts"`
	DraftTaskID     string                 `json:"draft_task_id"`
}
type Operations struct {
	Spec     domain.TaskSpec
	State    OperationsState
	Delegate model.Provider
	Save     func(context.Context, OperationsState) error
	History  string
}

func UIQuote(o domain.Observation, quote string) bool {
	if strings.TrimSpace(quote) == "" {
		return false
	}
	u, e := domain.ParseUI(o)
	if e != nil {
		return false
	}
	for _, n := range u.Nodes {
		if n.Visible && (strings.Contains(n.Text, quote) || strings.Contains(n.Description, quote) || strings.Contains(n.Hint, quote)) {
			return true
		}
	}
	return false
}
func (b *Operations) Next(ctx context.Context, q model.Request) (model.Result, error) {
	u, e := domain.ParseUI(domain.Observation{State: q.State})
	if e != nil {
		return model.Result{}, e
	}
	for _, n := range u.Nodes {
		if n.Visible {
			for _, v := range []string{"验证码", "安全验证", "账号异常", "登录后"} {
				if strings.Contains(n.Text, v) {
					return model.Result{}, errors.New("business_login_or_verification_takeover")
				}
			}
		}
	}
	q.BusinessTools = true
	state, _ := json.Marshal(b.State)
	q.Goal += "\n已执行状态：" + string(state) + "\n已记录/已发送对象（避免重复）：" + b.History + "\n只能对有明确需求的原评论或已有客户消息回复。账号核对后才能输入业务消息。dispatch_business 的 body 必须与编辑框全文完全一致，name 和 evidence 必须在当前页可见。热度未知时写未知。"
	return b.Delegate.Next(ctx, q)
}
func (b *Operations) CheckRecord(o domain.Observation, r domain.BusinessRecord) error {
	if e := r.Validate(); e != nil {
		return e
	}
	if r.Kind == "account" {
		u, _ := domain.ParseUI(o)
		if r.Name != b.Spec.Account || !u.HasText(r.Name) || (!UIQuote(o, "微信号") && !UIQuote(o, "小红书号")) {
			return errors.New("account_profile_evidence_required")
		}
		return nil
	}
	if !b.State.AccountVerified {
		return errors.New("account_not_verified")
	}
	if r.Kind != "lead" && r.Kind != "post" && r.Kind != "note" {
		return errors.New("record_kind_not_allowed_for_phone_task")
	}
	if r.Kind == "note" {
		if b.Spec.Template != "xhs_growth" || b.Spec.Mode != "draft" || b.State.Posts < 1 {
			return errors.New("draft_requires_researched_post")
		}
		return nil
	}
	if b.State.Records >= b.Spec.Business.MaxRecords {
		return errors.New("record_limit_reached")
	}
	if !UIQuote(o, r.Evidence) || (r.Kind == "lead" && !UIQuote(o, r.Name)) {
		return errors.New("record_quote_not_visible")
	}
	if r.Contact != "" && !UIQuote(o, r.Contact) {
		return errors.New("contact_not_in_observation")
	}
	return nil
}
func (b *Operations) Recorded(ctx context.Context, r domain.BusinessRecord, isNew bool) error {
	if r.Kind == "account" {
		b.State.AccountVerified = true
	}
	if isNew && (r.Kind == "lead" || r.Kind == "post") {
		b.State.Records++
		if r.Kind == "post" {
			b.State.Posts++
		}
	}
	return b.Save(ctx, b.State)
}
func outbound(text string) bool {
	for _, v := range []string{"发送", "回复", "发布", "添加到通讯录", "Send", "Post", "确认添加"} {
		if text == v {
			return true
		}
	}
	return false
}

// All taps on outgoing controls must use the dispatch tool. Coordinate-only taps
// on known outgoing controls are checked too; keyboard actions cannot bypass it.
func (b *Operations) Guard(o domain.Observation, a domain.Action, dispatch bool, records ...*domain.BusinessRecord) error {
	if dispatch {
		if len(records) != 1 || records[0] == nil {
			return errors.New("dispatch_record_required")
		}
		checked, e := b.DispatchAction(o, *records[0])
		if e != nil {
			return e
		}
		if a.Kind != checked.Kind || a.X != checked.X || a.Y != checked.Y || a.Target == nil || checked.Target == nil || *a.Target != *checked.Target {
			return errors.New("dispatch_target_changed")
		}
		return nil
	}
	if a.Kind == "type" && !b.State.AccountVerified {
		return errors.New("account_not_verified")
	}
	if a.Kind == "back" || a.Kind == "home" || a.Kind == "launch" {
		b.State.FriendRecipient = nil
	}
	if a.Kind != "tap" {
		return nil
	}
	u, e := domain.ParseUI(o)
	if e != nil {
		return e
	}
	x, y := domain.Pixel(a.X, o.Width), domain.Pixel(a.Y, o.Height)
	for _, n := range u.Nodes {
		if n.Visible && x >= n.Bounds.Left && x <= n.Bounds.Right && y >= n.Bounds.Top && y <= n.Bounds.Bottom && (outbound(n.Text) || outbound(n.Description)) {
			return errors.New("use_dispatch_business_for_outgoing_action")
		}
	}
	return nil
}
func (b *Operations) DispatchAction(o domain.Observation, r domain.BusinessRecord) (domain.Action, error) {
	if !b.State.AccountVerified {
		return domain.Action{}, errors.New("account_not_verified")
	}
	if r.Kind != "reply" && r.Kind != "friend" {
		return domain.Action{}, errors.New("invalid_dispatch_kind")
	}
	if b.Spec.Template == "xhs_growth" && b.Spec.Mode != "reply" && b.Spec.Mode != "operate" {
		return domain.Action{}, errors.New("research_and_draft_do_not_reply")
	}
	if b.Spec.Business.MaxReplies == 0 {
		return domain.Action{}, errors.New("outgoing_actions_disabled")
	}
	if len(b.Spec.Business.Targets) > 0 {
		found := false
		for _, n := range b.Spec.Business.Targets {
			if n == r.Name || n == r.Contact {
				found = true
			}
		}
		if !found {
			return domain.Action{}, errors.New("recipient_not_in_targets")
		}
	}
	if r.Kind == "friend" && (b.Spec.Template != "wechat_crm" || !b.Spec.Business.AddFriends) {
		return domain.Action{}, errors.New("friend_request_not_enabled")
	}
	friendForm := r.Kind == "friend" && b.State.FriendRecipient != nil && b.State.FriendRecipient.Name == r.Name && b.State.FriendRecipient.Source == r.Source && b.State.FriendRecipient.Evidence == r.Evidence && (UIQuote(o, "朋友验证") || UIQuote(o, "发送添加朋友申请"))
	if !friendForm && (!UIQuote(o, r.Name) || !UIQuote(o, r.Evidence)) {
		return domain.Action{}, errors.New("recipient_and_incoming_message_must_be_visible")
	}
	if r.Kind == "friend" && !friendForm && UIQuote(o, "微信号") {
		a, e := domain.TargetTap(o, domain.UITarget{Text: "添加到通讯录"})
		if e == nil {
			b.State.FriendRecipient = &r
			return a, nil
		}
	}
	u, e := domain.ParseUI(o)
	if e != nil {
		return domain.Action{}, e
	}
	if r.Kind == "reply" {
		if b.Spec.Template == "wechat_crm" && !u.HasText(r.Name) {
			return domain.Action{}, errors.New("chat_header_recipient_not_verified")
		}
		if b.Spec.Template == "xhs_growth" {
			target := false
			for _, n := range u.Nodes {
				if n.Visible && ((n.Editable && strings.Contains(n.Hint, r.Name)) || strings.Contains(n.Description, "回复"+r.Name) || strings.Contains(n.Text, "回复 @"+r.Name) || strings.Contains(n.Text, "回复 "+r.Name)) {
					target = true
				}
			}
			if !target {
				return domain.Action{}, errors.New("comment_reply_target_not_verified")
			}
		}
	}
	typed := false
	for _, n := range u.Nodes {
		if n.Visible && n.Editable && n.Text == r.Body {
			typed = true
		}
	}
	if !typed {
		return domain.Action{}, errors.New("outgoing_editor_text_mismatch")
	}
	// Only an unambiguous named button can dispatch; no guessed coordinates.
	for _, label := range []string{"发送", "Send"} {
		a, e := domain.TargetTap(o, domain.UITarget{Text: label})
		if e == nil {
			return a, nil
		}
	}
	return domain.Action{}, errors.New("outgoing_button_not_unique")
}
