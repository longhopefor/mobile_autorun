package business

import (
	"context"
	"encoding/json"
	"mobile_autorun/internal/domain"
	"strings"
	"testing"
)

func node(id, text, hint string, editable bool) domain.UINode {
	return domain.UINode{ResourceID: id, Text: text, Hint: hint, Visible: true, Enabled: true, Editable: editable, Bounds: domain.Bounds{Left: 20, Top: 100, Right: 900, Bottom: 200}}
}
func obs(id string, nodes ...domain.UINode) domain.Observation {
	state, _ := json.Marshal(map[string]any{"phone_state": map[string]string{"packageName": XHSPackage, "activityName": "fixture"}, "a11y_tree": nodes})
	return domain.Observation{ID: id, Width: 1080, Height: 2400, State: state}
}
func adapter() *XHS {
	return &XHS{Spec: domain.TaskSpec{Account: "123", Title: "标题", Body: "正文内容", Mode: "draft", AssetIDs: []string{"image"}}, Album: "task-album", Save: func(context.Context, XHSState) error { return nil }}
}
func TestXHSAccountAndPublicationGuards(t *testing.T) {
	ctx := context.Background()
	x := adapter()
	if e := x.Observe(ctx, obs("account", node(XHSPackage+":id/h0m", "小红书号：999", "", false))); e == nil {
		t.Fatal("wrong account accepted")
	}
	o := obs("editor", node("title", "标题", "标题", true), node("body", "正文内容", "正文", true), node("publish", "发布笔记", "", false))
	x.State = XHSState{Stage: "save", AccountVerified: true, SelectedCount: 1, ContentVerified: true}
	if e := x.Guard(ctx, o, domain.Action{Kind: "tap", X: 426, Y: 62}); e == nil {
		t.Fatal("draft mode publication permitted")
	}
	if e := x.Completion(o); e == nil {
		t.Fatal("unreopened draft accepted")
	}
	x.State.Stage = "compose"
	if e := x.Guard(ctx, o, domain.Action{Kind: "type", Text: "unrequested content"}); e == nil {
		t.Fatal("unexpected content permitted")
	}
}
func TestXHSDraftRequiresListAndReopen(t *testing.T) {
	ctx := context.Background()
	x := adapter()
	if e := x.Observe(ctx, obs("account", node(XHSPackage+":id/h0m", "小红书号：123", "", false))); e != nil {
		t.Fatal(e)
	}
	if !x.State.AccountVerified {
		t.Fatal("profile not verified")
	}
	if e := x.Observe(ctx, obs("album", node(XHSPackage+":id/oa", x.Album, "", false), node("next", "下一步(1)", "", false))); e != nil {
		t.Fatal(e)
	}
	editor := obs("editor", node("title", "标题", "标题", true), node("body", "正文内容", "正文", true))
	if e := x.Observe(ctx, editor); e != nil {
		t.Fatal(e)
	}
	if e := x.Completion(editor); e == nil {
		t.Fatal("editing alone accepted as draft")
	}
	x.State.SaveRequested = true
	x.State.Stage = "verify_draft"
	if e := x.Observe(ctx, obs("not-drafts", node("post", "标题", "", false))); e != nil {
		t.Fatal(e)
	}
	if x.State.DraftListed {
		t.Fatal("ordinary note mistaken for draft")
	}
	if e := x.Observe(ctx, obs("drafts", node("label", "草稿箱", "", false), node("draft", "标题", "", false))); e != nil {
		t.Fatal(e)
	}
	editor.ID = "reopened"
	if e := x.Observe(ctx, editor); e != nil {
		t.Fatal(e)
	}
	if e := x.Completion(editor); e != nil {
		t.Fatal(e)
	}
	editor.ID = "stale"
	if e := x.Completion(editor); e == nil {
		t.Fatal("different observation accepted")
	}
}
func TestXHSContentConstraints(t *testing.T) {
	for _, s := range []domain.TaskSpec{{Title: strings.Repeat("字", 21), Body: "正文", AssetIDs: []string{"one"}}, {Title: "标题", Body: strings.Repeat("字", 1001), AssetIDs: []string{"one"}}, {Title: "标题", Body: "正文", AssetIDs: []string{"one", "one"}}} {
		s.DeviceID = "device"
		s.ModelProfileID = "model"
		s.Template = "xhs_image"
		s.Mode = "draft"
		s.Account = "123"
		if s.Validate() == nil {
			t.Fatal("invalid XHS content accepted")
		}
	}
}

func withPhone(o domain.Observation, keyboard bool, focus string) domain.Observation {
	var state map[string]any
	_ = json.Unmarshal(o.State, &state)
	phone := state["phone_state"].(map[string]any)
	phone["keyboardVisible"] = keyboard
	phone["focusedElement"] = map[string]string{"resourceId": focus}
	o.State, _ = json.Marshal(state)
	return o
}
func TestXHSSaveMustHideKeyboard(t *testing.T) {
	ctx := context.Background()
	x := adapter()
	x.State = XHSState{Stage: "save", AccountVerified: true, SelectedCount: 1, ContentVerified: true}
	done := node(XHSPackage+":id/dxf", "完成", "", false)
	done.Bounds = domain.Bounds{Left: 800, Top: 1600, Right: 1000, Bottom: 1700}
	save := node(XHSPackage+":id/aos", "存草稿", "", false)
	save.Bounds = domain.Bounds{Left: 0, Top: 2200, Right: 200, Bottom: 2300}
	o := withPhone(obs("keyboard", node("title", "标题", "标题", true), node("body", "正文内容", "正文", true), save, done), true, "")
	r, handled, e := x.PlanObservation(ctx, o)
	if e != nil || !handled || r.Decision.Action.Target.ResourceID != done.ResourceID {
		t.Fatalf("keyboard not hidden first: %+v %v", r, e)
	}
	a, e := domain.TargetTap(o, domain.UITarget{ResourceID: save.ResourceID, Text: save.Text})
	if e != nil {
		t.Fatal(e)
	}
	if e = x.Guard(ctx, o, a); e == nil || x.State.SaveRequested {
		t.Fatal("obscured save accepted")
	}
	o = withPhone(o, false, "")
	if e = x.Guard(ctx, o, a); e != nil {
		t.Fatal(e)
	}
	if !x.State.SaveRequested || x.State.Stage != "verify_draft" {
		t.Fatal("save intent not recorded")
	}
}
func TestXHSSaveDialogRequiresNamedConfirmation(t *testing.T) {
	ctx := context.Background()
	x := adapter()
	x.State = XHSState{Stage: "verify_draft", AccountVerified: true, SelectedCount: 1, ContentVerified: true, SaveRequested: true}
	o := obs("dialog", node("message", "确认保存笔记至草稿箱吗?", "", false), node(XHSPackage+":id/idy", "确定", "", false))
	if e := x.Guard(ctx, o, domain.Action{Kind: "tap", X: 500, Y: 500}); e == nil || x.State.SaveConfirmed {
		t.Fatal("unlabelled dialog tap accepted")
	}
	r, handled, e := x.PlanObservation(ctx, o)
	if e != nil || !handled || r.Decision.Action.Target.Text != "确定" {
		t.Fatalf("missing named confirmation: %+v %v", r, e)
	}
	if e = x.Guard(ctx, o, *r.Decision.Action); e != nil {
		t.Fatal(e)
	}
	if !x.State.SaveConfirmed {
		t.Fatal("confirmation intent missing")
	}
	if e = x.Guard(ctx, o, *r.Decision.Action); e == nil {
		t.Fatal("confirmation repeated")
	}
}
func TestXHSInputRequiresCorrectFocusedField(t *testing.T) {
	ctx := context.Background()
	x := adapter()
	x.State = XHSState{Stage: "compose", AccountVerified: true, SelectedCount: 1}
	n := node(XHSPackage+":id/c4f", "", "标题", true)
	n.Focused = true
	o := withPhone(obs("focus", n), true, n.ResourceID)
	if e := x.Guard(ctx, o, domain.Action{Kind: "type", Text: x.Spec.Body}); e == nil {
		t.Fatal("body allowed in title field")
	}
	if e := x.Guard(ctx, o, domain.Action{Kind: "type", Text: x.Spec.Title}); e != nil {
		t.Fatal(e)
	}
}
