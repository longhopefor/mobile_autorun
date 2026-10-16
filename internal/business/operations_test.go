package business

import (
	"encoding/json"
	"mobile_autorun/internal/domain"
	"testing"
)

func operationObservation(texts ...string) domain.Observation {
	nodes := []domain.UINode{}
	for i, v := range texts {
		nodes = append(nodes, domain.UINode{Text: v, Visible: true, Enabled: true, Bounds: domain.Bounds{Left: 10, Top: i*50 + 10, Right: 390, Bottom: i*50 + 45}})
	}
	nodes = append(nodes, domain.UINode{Text: "这是回复", Editable: true, Visible: true, Enabled: true, Bounds: domain.Bounds{Left: 10, Top: 500, Right: 300, Bottom: 550}})
	raw, _ := json.Marshal(map[string]any{"phone_state": map[string]any{"packageName": "com.tencent.mm"}, "a11y_tree": nodes})
	return domain.Observation{ID: "o", Width: 400, Height: 800, State: raw}
}
func testOperations() *Operations {
	return &Operations{Spec: domain.TaskSpec{Template: "wechat_crm", Mode: "followup", Account: "我的账号", Business: &domain.BusinessSpec{Targets: []string{"客户甲"}, MaxRecords: 10, MaxReplies: 3, AddFriends: true}}, State: OperationsState{AccountVerified: true}}
}
func TestBusinessDispatchUsesExactRecipientQuoteAndEditor(t *testing.T) {
	b := testOperations()
	o := operationObservation("客户甲", "想了解价格", "发送")
	r := domain.BusinessRecord{Kind: "reply", Key: "r", Name: "客户甲", Source: "会话客户甲", Evidence: "想了解价格", Body: "这是回复"}
	action, e := b.DispatchAction(o, r)
	if e != nil {
		t.Fatal(e)
	}
	if action.Target == nil || action.Target.Text != "发送" {
		t.Fatal("not a named send control")
	}
	if e = b.Guard(o, action, true, &r); e != nil {
		t.Fatal(e)
	}
	if e = b.Guard(operationObservation("客户乙", "想了解价格", "发送"), action, true, &r); e == nil {
		t.Fatal("recipient changed while send control stayed stable")
	}
	if e = b.Guard(o, action, false); e == nil {
		t.Fatal("generic coordinate action bypassed dispatch")
	}
	for _, change := range []func(*domain.BusinessRecord){func(r *domain.BusinessRecord) { r.Name = "另一个人" }, func(r *domain.BusinessRecord) { r.Evidence = "不存在的需求" }, func(r *domain.BusinessRecord) { r.Body = "另一段内容" }} {
		bad := r
		change(&bad)
		if _, e = b.DispatchAction(o, bad); e == nil {
			t.Fatal("unchecked outgoing context")
		}
	}
	b.Spec.Mode = "research"
	b.Spec.Template = "xhs_growth"
	if _, e = b.DispatchAction(o, r); e == nil {
		t.Fatal("research must not reply")
	}
}
func TestRecordRejectsInventedContactAndSubstringAccount(t *testing.T) {
	b := testOperations()
	o := operationObservation("客户甲", "想了解价格", "我的账号", "微信号 wxid_example")
	r := domain.BusinessRecord{Kind: "lead", Key: "k", Name: "客户甲", Contact: "123456", Intent: "high", Reason: "问价格", Source: "会话客户甲", Evidence: "想了解价格"}
	if e := b.CheckRecord(o, r); e == nil {
		t.Fatal("invented contact accepted")
	}
	r.Contact = ""
	if e := b.CheckRecord(o, r); e != nil {
		t.Fatal(e)
	}
	b.Spec.Account = "我的"
	r = domain.BusinessRecord{Kind: "account", Key: "account", Name: "我的"}
	if e := b.CheckRecord(o, r); e == nil {
		t.Fatal("substring account accepted")
	}
}
func TestFriendRequestStaysBoundToVerifiedProfile(t *testing.T) {
	b := testOperations()
	r := domain.BusinessRecord{Kind: "friend", Key: "friend", Name: "客户甲", Contact: "wxid_example", Source: "wxid_example", Evidence: "wxid_example", Body: "这是回复"}
	profile := operationObservation("客户甲", "微信号 wxid_example", "添加到通讯录")
	action, e := b.DispatchAction(profile, r)
	if e != nil || action.Target.Text != "添加到通讯录" {
		t.Fatalf("prepare: %+v %v", action, e)
	}
	form := operationObservation("发送添加朋友申请", "发送")
	action, e = b.DispatchAction(form, r)
	if e != nil || action.Target.Text != "发送" {
		t.Fatalf("verify: %+v %v", action, e)
	}
	r.Name = "陌生人"
	if _, e = b.DispatchAction(form, r); e == nil {
		t.Fatal("friend recipient changed")
	}
}
