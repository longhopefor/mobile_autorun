package domain

import (
	"testing"
)

func TestActionBoundary(t *testing.T) {
	for _, a := range []Action{{Kind: "tap", X: 1001}, {Kind: "shell", Text: "rm"}, {Kind: "launch"}, {Kind: "wait", DurationMS: 6000}} {
		if a.Validate([]string{"allowed.app"}) == nil {
			t.Fatalf("accepted invalid action %+v", a)
		}
	}
	if Pixel(1000, 1080) != 1079 || Pixel(500, 1080) != 540 {
		t.Fatal("coordinate transformation")
	}
	if (&Decision{Tool: "phone_action", ObservationID: "old", Action: &Action{Kind: "back"}}).Validate("new", nil) == nil {
		t.Fatal("stale decision accepted")
	}
}
func TestStrictOutput(t *testing.T) {
	for _, b := range []string{`{"kind":"tap","unexpected":true}`, `{"kind":"back"} {"kind":"home"}`} {
		var a Action
		if DecodeStrict([]byte(b), &a) == nil {
			t.Fatalf("accepted %s", b)
		}
	}
}

func TestTargetPackagesDoNotRestrictLaunch(t *testing.T) {
	for _, packages := range [][]string{nil, {"com.android.settings"}} {
		if err := (Action{Kind: "launch", Package: "com.xingin.xhs"}).Validate(packages); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGeneralAttachmentBoundaries(t *testing.T) {
	for _, ids := range [][]string{{"same", "same"}, {""}, {"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}} {
		s := TaskSpec{DeviceID: "phone", ModelProfileID: "model", Template: "general", Goal: "test", Packages: []string{"allowed"}, AssetIDs: ids}
		if s.Validate() == nil {
			t.Fatalf("invalid attachments accepted: %v", ids)
		}
	}
	s := TaskSpec{DeviceID: "phone", ModelProfileID: "model", Template: "general", Goal: "test", Packages: []string{"allowed"}, AssetIDs: []string{"file", "image"}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}
