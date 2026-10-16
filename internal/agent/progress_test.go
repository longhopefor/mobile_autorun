package agent

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/model"
	"strings"
	"testing"
	"time"
)

func progressFixture(t *testing.T) domain.Observation {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, 200, 400))
	draw.Draw(m, m.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return domain.Observation{ID: "screen", Width: 200, Height: 400, Image: b.Bytes(), State: []byte(`{"a11y_tree":[],"phone_state":{"packageName":"app"}}`)}
}
func TestRepeatedNoProgressStopsBeforeSixthDispatch(t *testing.T) {
	o := progressFixture(t)
	calls, executed, warnings, paused := 0, 0, 0, 0
	h := Hooks{BeforeModel: func(context.Context) error { return nil }, Observe: func(context.Context) (domain.Observation, error) { o.ID = fmt.Sprint(calls + 1); return o, nil }, Decision: func(context.Context, model.Request, model.Result, time.Duration, error) error { return nil }, Execute: func(context.Context, domain.Decision) (domain.Observation, error) { executed++; return o, nil }, NoProgress: func(_ context.Context, _ domain.Decision, n int, stop bool) error {
		if stop {
			paused++
			if n != 6 {
				t.Fatal(n)
			}
		} else {
			warnings++
		}
		return nil
	}}
	p := providerFunc(func(_ context.Context, q model.Request) (model.Result, error) {
		calls++
		if calls == 4 && !strings.Contains(strings.Join(q.History, "\n"), "no_progress_warning") {
			t.Fatal("model did not get no-progress feedback")
		}
		return model.Result{Decision: domain.Decision{Tool: "phone_action", ObservationID: q.ObservationID, Action: &domain.Action{Kind: "tap", X: 500, Y: 500}}}, nil
	})
	err := Run(context.Background(), domain.Task{ID: "progress", Spec: domain.TaskSpec{Goal: "test", MaxIterations: 10}}, domain.Profile{}, p, h)
	if err == nil || !strings.Contains(err.Error(), "phone_no_progress") || calls != 6 || executed != 5 || warnings != 3 || paused != 1 {
		t.Fatalf("err=%v calls=%d executed=%d warnings=%d paused=%d", err, calls, executed, warnings, paused)
	}
}
func TestProgressResetsOnContentTargetAndForegroundChanges(t *testing.T) {
	o := progressFixture(t)
	a := domain.Action{Kind: "tap", X: 500, Y: 500}
	w := progressWatch{}
	f, n := w.check(o, a)
	w.commit(f, a, n)
	if _, n = w.check(o, a); n != 2 {
		t.Fatal("unchanged screen not detected")
	}
	for _, change := range []string{`{"a11y_tree":[{"text":"新结果","isVisibleToUser":true}],"phone_state":{"packageName":"app"}}`, `{"a11y_tree":[],"phone_state":{"packageName":"other"}}`} {
		next := o
		next.State = []byte(change)
		if _, n = w.check(next, a); n != 1 {
			t.Fatal("semantic progress ignored")
		}
	}
	other := a
	other.X = 600
	if _, n = w.check(o, other); n != 1 {
		t.Fatal("different target counted")
	}
	other = a
	other.X += 5
	other.DurationMS = 150
	other.Text = "unused"
	other.Package = "app"
	if _, n = w.check(o, other); n != 2 {
		t.Fatal("minor pointing jitter or unused fields bypassed repeat detection")
	}
	if _, n = w.check(o, domain.Action{Kind: "wait", DurationMS: 100}); n != 0 {
		t.Fatal("waiting counted as failed action")
	}
	next := o
	m := image.NewRGBA(image.Rect(0, 0, 200, 400))
	draw.Draw(m, m.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)
	var b bytes.Buffer
	_ = png.Encode(&b, m)
	next.Image = b.Bytes()
	if _, n = w.check(next, a); n != 1 {
		t.Fatal("visual progress ignored")
	}
	bad := o
	bad.Image = []byte("invalid")
	if _, n = w.check(bad, a); n != 0 {
		t.Fatal("invalid evidence counted")
	}
}
func TestProgressDoesNotRetryUncertainActions(t *testing.T) {
	o := progressFixture(t)
	calls, executed := 0, 0
	h := Hooks{BeforeModel: func(context.Context) error { return nil }, Observe: func(context.Context) (domain.Observation, error) { return o, nil }, Decision: func(context.Context, model.Request, model.Result, time.Duration, error) error { return nil }, Execute: func(context.Context, domain.Decision) (domain.Observation, error) {
		executed++
		return o, domain.ErrUncertain
	}}
	p := providerFunc(func(_ context.Context, q model.Request) (model.Result, error) {
		calls++
		return model.Result{Decision: domain.Decision{Tool: "phone_action", ObservationID: q.ObservationID, Action: &domain.Action{Kind: "back"}}}, nil
	})
	err := Run(context.Background(), domain.Task{ID: "uncertain", Spec: domain.TaskSpec{Goal: "test", MaxIterations: 10}}, domain.Profile{}, p, h)
	if err == nil || calls != 1 || executed != 1 {
		t.Fatalf("err=%v calls=%d executed=%d", err, calls, executed)
	}
}
