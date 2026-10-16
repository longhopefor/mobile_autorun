package domain

import (
	"encoding/json"
	"testing"
)

func TestSemanticControlRequiresSameNodeAndGeometry(t *testing.T) {
	n := UINode{ResourceID: "control", Text: "我", Visible: true, Enabled: true, Bounds: Bounds{Left: 20, Top: 100, Right: 90, Bottom: 140}}
	makeObs := func(n UINode, image string) Observation {
		s, _ := json.Marshal(map[string]any{"phone_state": map[string]string{"packageName": "app", "activityName": "page"}, "a11y_tree": []UINode{n}})
		return Observation{Width: 100, Height: 200, State: s, Image: []byte(image)}
	}
	before := makeObs(n, "animated-feed-frame-one")
	after := makeObs(n, "animated-feed-frame-two")
	a, e := TargetTap(before, UITarget{ResourceID: "control", Text: "我"})
	if e != nil {
		t.Fatal(e)
	}
	if ok, e := SameObservationForAction(before, after, a); e != nil || !ok {
		t.Fatal("same control rejected")
	}
	n.Bounds.Top++
	after = makeObs(n, "two")
	if SameUITarget(before, after, a) {
		t.Fatal("moved control accepted")
	}
	n.Bounds.Top--
	n.Text = "发布"
	after = makeObs(n, "two")
	if SameUITarget(before, after, a) {
		t.Fatal("changed control accepted")
	}
}
