package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/model"
	"strings"
	"testing"
	"time"
)

type fakeProvider struct{ n int }

func (p *fakeProvider) Next(_ context.Context, r model.Request) (model.Result, error) {
	p.n++
	d := domain.Decision{ObservationID: r.ObservationID, Tool: "finish_task", Reason: "candidate"}
	if p.n == 1 {
		d.Tool = "phone_action"
		d.Action = &domain.Action{Kind: "back"}
	}
	return model.Result{Decision: d}, nil
}
func TestEinoActionObservationLoop(t *testing.T) {
	p := &fakeProvider{}
	observed, executed := 0, 0
	finished := false
	task := domain.Task{ID: "test", Spec: domain.TaskSpec{Goal: "test", MaxIterations: 3}}
	h := Hooks{BeforeModel: func(context.Context) error { return nil }, Observe: func(context.Context) (domain.Observation, error) {
		observed++
		return domain.Observation{ID: "fresh", Image: []byte("fixture")}, nil
	}, Decision: func(context.Context, model.Request, model.Result, time.Duration, error) error { return nil }, Execute: func(context.Context, domain.Decision) (domain.Observation, error) {
		executed++
		return domain.Observation{ID: "after"}, nil
	}, Finish: func(context.Context, domain.Decision) error { finished = true; return nil }, Takeover: func(context.Context, domain.Decision) error { return nil }}
	if e := Run(context.Background(), task, domain.Profile{}, p, h); e != nil {
		t.Fatal(e)
	}
	if observed != 2 || executed != 1 || !finished {
		t.Fatalf("observed=%d executed=%d finished=%v", observed, executed, finished)
	}
}

type providerFunc func(context.Context, model.Request) (model.Result, error)

func (f providerFunc) Next(ctx context.Context, q model.Request) (model.Result, error) {
	return f(ctx, q)
}

func TestScreenChangeDiscardsDecisionAndReplans(t *testing.T) {
	observed, calls, checked, dispatched := 0, 0, 0, 0
	finished := false
	h := Hooks{
		BeforeModel: func(context.Context) error { return nil },
		Observe: func(context.Context) (domain.Observation, error) {
			observed++
			return domain.Observation{ID: fmt.Sprintf("fresh-%d", observed), Image: []byte("fixture")}, nil
		},
		Decision: func(context.Context, model.Request, model.Result, time.Duration, error) error { return nil },
		Execute: func(_ context.Context, d domain.Decision) (domain.Observation, error) {
			checked++
			if checked == 1 {
				return domain.Observation{ID: "rejected-fresh"}, domain.ErrScreenChanged
			}
			if d.ObservationID != "fresh-2" || d.Action.X != 700 {
				t.Fatalf("old decision replayed: %+v", d)
			}
			dispatched++
			return domain.Observation{ID: "after"}, nil
		},
		ScreenChanged: func(_ context.Context, d domain.Decision, o domain.Observation, attempt int, retry bool) error {
			if d.ObservationID != "fresh-1" || o.ID != "rejected-fresh" || attempt != 1 || !retry {
				t.Fatalf("bad reobserve event: %s %s %d %v", d.ObservationID, o.ID, attempt, retry)
			}
			return nil
		},
		Finish: func(context.Context, domain.Decision) error { finished = true; return nil },
	}
	p := providerFunc(func(_ context.Context, q model.Request) (model.Result, error) {
		calls++
		d := domain.Decision{ObservationID: q.ObservationID, Tool: "phone_action", Reason: "fixture", Action: &domain.Action{Kind: "tap", X: 200, Y: 300}}
		if calls == 2 {
			if q.ObservationID != "fresh-2" {
				t.Fatal("did not observe again")
			}
			var outcome struct {
				Status    string `json:"status"`
				Executed  *bool  `json:"action_executed"`
				Discarded string `json:"discarded_observation_id"`
			}
			if len(q.History) < 2 || json.Unmarshal([]byte(q.History[len(q.History)-1]), &outcome) != nil || outcome.Status != "screen_changed_reobserve" || outcome.Executed == nil || *outcome.Executed || outcome.Discarded != "fresh-1" {
				t.Fatalf("missing rejection history: %v", q.History)
			}
			d.Action.X = 700
		} else if calls == 3 {
			d.Tool, d.Action = "finish_task", nil
		}
		return model.Result{Decision: d}, nil
	})
	err := Run(context.Background(), domain.Task{ID: "replan", Spec: domain.TaskSpec{Goal: "test", MaxIterations: 6}}, domain.Profile{}, p, h)
	if err != nil || observed != 3 || calls != 3 || checked != 2 || dispatched != 1 || !finished {
		t.Fatalf("err=%v observed=%d calls=%d checked=%d dispatched=%d finished=%v", err, observed, calls, checked, dispatched, finished)
	}
}

func TestScreenReobservationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name           string
		executeErr     error
		cancelOnReject bool
		budget         int
		wantCalls      int
		wantRetries    int
		wantExhausted  int
	}{
		{"continuous_changes", domain.ErrScreenChanged, false, 10, 4, 3, 1},
		{"unknown_action", domain.ErrUncertain, false, 10, 1, 0, 0},
		{"offline", domain.ErrOffline, false, 10, 1, 0, 0},
		{"cancel_during_wait", domain.ErrScreenChanged, true, 10, 1, 1, 0},
		{"task_iteration_budget", domain.ErrScreenChanged, false, 2, 2, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, observed, checked, retries, exhausted := 0, 0, 0, 0, 0
			h := Hooks{
				BeforeModel: func(context.Context) error { return nil },
				Observe: func(context.Context) (domain.Observation, error) {
					observed++
					return domain.Observation{ID: fmt.Sprint(observed)}, nil
				},
				Decision: func(context.Context, model.Request, model.Result, time.Duration, error) error { return nil },
				Execute: func(context.Context, domain.Decision) (domain.Observation, error) {
					checked++
					return domain.Observation{ID: "fresh"}, tc.executeErr
				},
				ScreenChanged: func(_ context.Context, _ domain.Decision, _ domain.Observation, attempt int, retry bool) error {
					if attempt != retries+exhausted+1 {
						t.Fatal("incorrect consecutive count")
					}
					if retry {
						retries++
					} else {
						exhausted++
					}
					if tc.cancelOnReject {
						cancel()
					}
					return nil
				},
			}
			p := providerFunc(func(_ context.Context, q model.Request) (model.Result, error) {
				calls++
				return model.Result{Decision: domain.Decision{Tool: "phone_action", ObservationID: q.ObservationID, Reason: "fixture", Action: &domain.Action{Kind: "back"}}}, nil
			})
			err := Run(ctx, domain.Task{ID: tc.name, Spec: domain.TaskSpec{Goal: "test", MaxIterations: tc.budget}}, domain.Profile{}, p, h)
			if err == nil || calls != tc.wantCalls || observed != calls || checked != calls || retries != tc.wantRetries || exhausted != tc.wantExhausted {
				t.Fatalf("err=%v calls=%d observations=%d checks=%d retries=%d exhausted=%d", err, calls, observed, checked, retries, exhausted)
			}
			if tc.name == "continuous_changes" && !strings.Contains(err.Error(), domain.ErrScreenChanged.Error()) {
				t.Fatal(err)
			}
			if tc.name == "unknown_action" && !strings.Contains(err.Error(), domain.ErrUncertain.Error()) {
				t.Fatal(err)
			}
		})
	}
}

func TestSuccessfulActionResetsScreenChangeCount(t *testing.T) {
	m := &runModel{obs: domain.Observation{ID: "old"}, screenChanges: MaxScreenReobservations, hooks: &Hooks{}}
	m.hooks.Execute = func(context.Context, domain.Decision) (domain.Observation, error) {
		return domain.Observation{ID: "after"}, nil
	}
	tool := &phoneTool{name: "phone_action", m: m}
	args := func(id string) string {
		b, _ := json.Marshal(domain.Decision{Tool: "phone_action", ObservationID: id, Reason: "fixture", Action: &domain.Action{Kind: "back"}})
		return string(b)
	}
	if _, err := tool.InvokableRun(context.Background(), args("old")); err != nil || m.screenChanges != 0 {
		t.Fatalf("err=%v count=%d", err, m.screenChanges)
	}
	m.hooks.Execute = func(context.Context, domain.Decision) (domain.Observation, error) {
		return domain.Observation{ID: "fresh"}, fmt.Errorf("preflight: %w", domain.ErrScreenChanged)
	}
	if _, err := tool.InvokableRun(context.Background(), args("after")); err != nil || m.screenChanges != 1 {
		t.Fatalf("err=%v count=%d", err, m.screenChanges)
	}
}

func TestBusinessToolRecordsWithoutDispatchingPhoneAction(t *testing.T) {
	calls, records, actions := 0, 0, 0
	p := providerFunc(func(_ context.Context, q model.Request) (model.Result, error) {
		calls++
		d := domain.Decision{Tool: "finish_task", ObservationID: q.ObservationID}
		if calls == 1 {
			d.Tool = "record_business"
			d.Record = &domain.BusinessRecord{Kind: "account", Key: "account", Name: "verified"}
		}
		return model.Result{Decision: d}, nil
	})
	h := Hooks{BeforeModel: func(context.Context) error { return nil }, Observe: func(context.Context) (domain.Observation, error) {
		return domain.Observation{ID: "fresh", Image: []byte("fixture")}, nil
	}, Decision: func(context.Context, model.Request, model.Result, time.Duration, error) error { return nil }, Execute: func(context.Context, domain.Decision) (domain.Observation, error) {
		actions++
		return domain.Observation{}, nil
	}, Business: func(_ context.Context, d domain.Decision, o domain.Observation) (string, error) {
		if d.ObservationID != o.ID || d.Record.Name != "verified" {
			t.Fatal("business evidence lost")
		}
		records++
		return "saved", nil
	}, Finish: func(context.Context, domain.Decision) error { return nil }, Takeover: func(context.Context, domain.Decision) error { return nil }}
	if e := Run(context.Background(), domain.Task{ID: "task", Spec: domain.TaskSpec{MaxIterations: 3}}, domain.Profile{}, p, h); e != nil {
		t.Fatal(e)
	}
	if calls != 2 || records != 1 || actions != 0 {
		t.Fatalf("calls=%d records=%d actions=%d", calls, records, actions)
	}
}

func TestIterationBudgetStopsBeforeExtraModelCall(t *testing.T) {
	for _, spent := range []int{0, 1} {
		p := &fakeProvider{}
		observations, actions := 0, 0
		h := Hooks{
			BeforeModel: func(context.Context) error { return nil },
			Observe: func(context.Context) (domain.Observation, error) {
				observations++
				return domain.Observation{ID: "fresh"}, nil
			},
			Decision: func(context.Context, model.Request, model.Result, time.Duration, error) error { return nil },
			Execute: func(context.Context, domain.Decision) (domain.Observation, error) {
				actions++
				return domain.Observation{ID: "after"}, nil
			},
			Finish: func(context.Context, domain.Decision) error { t.Fatal("budget exhausted before finish"); return nil },
		}
		err := Run(context.Background(), domain.Task{ID: "budget", Iterations: spent, Spec: domain.TaskSpec{MaxIterations: 1}}, domain.Profile{}, p, h)
		if !errors.Is(err, domain.ErrIterationLimit) || p.n != 1-spent || observations != 1-spent || actions != 1-spent {
			t.Fatalf("spent=%d error=%v calls=%d observations=%d actions=%d", spent, err, p.n, observations, actions)
		}
	}
}
func TestSwipeSettleCancellationDoesNotRequestAnotherDecision(t *testing.T) {
	m := &runModel{remaining: 1, obs: domain.Observation{ID: "before"}, hooks: &Hooks{Execute: func(context.Context, domain.Decision) (domain.Observation, error) {
		return domain.Observation{ID: "after"}, nil
	}}}
	tool := &phoneTool{name: "phone_action", m: m}
	b, _ := json.Marshal(domain.Decision{Tool: "phone_action", ObservationID: "before", Action: &domain.Action{Kind: "swipe", X: 800, Y: 400, EndX: 200, EndY: 400}})
	if _, err := tool.InvokableRun(context.Background(), string(b)); err != nil {
		t.Fatal(err)
	}
	if time.Until(m.observeAfter) < 2*time.Second {
		t.Fatal("next observation was not deferred")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Generate(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("settle ignored cancellation: %v", err)
	}
}
