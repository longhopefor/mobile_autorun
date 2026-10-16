package domain

import "testing"

func TestBusinessSpecModesAndLimits(t *testing.T) {
	s := TaskSpec{Template: "wechat_crm", Mode: "followup", DeviceID: "device", ModelProfileID: "model", Account: "account", Business: &BusinessSpec{Targets: []string{"客户"}, Prompt: "回复", MaxReplies: 2}}
	if e := s.Validate(); e != nil {
		t.Fatal(e)
	}
	if s.Packages[0] != "com.tencent.mm" || s.Business.IntervalSeconds != 30 {
		t.Fatal("wrong package or limits")
	}
	s.Business.IntervalSeconds = 1
	if e := s.Validate(); e == nil {
		t.Fatal("unbounded sends")
	}
	s.Business.IntervalSeconds = 30
	s.Template = "xhs_growth"
	s.Mode = "draft"
	if e := s.Validate(); e == nil {
		t.Fatal("missing topic")
	}
	s.Business.Topic = "健康"
	if e := s.Validate(); e != nil {
		t.Fatal(e)
	}
}
func TestBusinessDecisionPayloadCannotBecomePhoneAction(t *testing.T) {
	r := &BusinessRecord{Kind: "account", Key: "account"}
	d := Decision{Tool: "record_business", ObservationID: "o", Record: r}
	if e := d.Validate("o", nil); e != nil {
		t.Fatal(e)
	}
	d.Tool = "phone_action"
	d.Action = &Action{Kind: "back"}
	if e := d.Validate("o", nil); e == nil {
		t.Fatal("unexpected report accepted")
	}
}
