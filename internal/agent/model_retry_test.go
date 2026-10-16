package agent

import (
	"context"
	"errors"
	"fmt"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/model"
	"testing"
	"time"
)

func TestModelRetryGetsFreshObservationAndNeverDispatchesFailedDecision(t *testing.T) {
	for _, tc := range []struct {
		name, code                       string
		failures, budget, calls, retries int
		cancel                           bool
	}{
		{"recover_network", "model_network_error", 1, 5, 2, 1, false},
		{"recover_invalid_decision", "model_invalid_decision", 1, 5, 2, 1, false},
		{"bounded", "model_network_error", 10, 5, 3, 2, false},
		{"budget", "model_network_error", 10, 1, 1, 0, false},
		{"auth_no_retry", "model_auth_required", 10, 5, 1, 0, false},
		{"quota_no_retry", "model_quota_exhausted", 10, 5, 1, 0, false},
		{"host_tool_no_retry", "model_host_tool_rejected", 10, 5, 1, 0, false},
		{"cancel_wait", "model_network_error", 10, 5, 1, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed, calls, retries, recorded, executed, charged := 0, 0, 0, 0, 0, 0
			finished := false
			h := Hooks{
				BeforeModel: func(context.Context) error { charged++; return nil },
				Observe: func(context.Context) (domain.Observation, error) {
					observed++
					return domain.Observation{ID: fmt.Sprint(observed)}, nil
				},
				Decision: func(context.Context, model.Request, model.Result, time.Duration, error) error { recorded++; return nil },
				ModelRetry: func(_ context.Context, code string, n int, _ time.Duration) error {
					retries++
					if code != tc.code || n != retries {
						t.Fatal("wrong retry event")
					}
					if tc.cancel {
						cancel()
					}
					return nil
				},
				Execute: func(context.Context, domain.Decision) (domain.Observation, error) {
					executed++
					return domain.Observation{}, nil
				},
				Finish: func(context.Context, domain.Decision) error { finished = true; return nil },
			}
			p := providerFunc(func(_ context.Context, q model.Request) (model.Result, error) {
				calls++
				if q.ObservationID != fmt.Sprint(calls) {
					t.Fatal("stale screenshot on retry")
				}
				if calls <= tc.failures {
					return model.Result{Decision: domain.Decision{Tool: "phone_action", ObservationID: q.ObservationID, Action: &domain.Action{Kind: "back"}}}, domain.ModelErrorForCode(tc.code)
				}
				return model.Result{Decision: domain.Decision{Tool: "finish_task", ObservationID: q.ObservationID, Reason: "candidate"}}, nil
			})
			err := Run(ctx, domain.Task{ID: tc.name, Spec: domain.TaskSpec{Goal: "test", MaxIterations: tc.budget}}, domain.Profile{}, p, h)
			if calls != tc.calls || charged != calls || observed != calls || recorded != calls || executed != 0 || retries != tc.retries {
				t.Fatalf("err=%v calls=%d charged=%d observed=%d recorded=%d actions=%d retries=%d", err, calls, charged, observed, recorded, executed, retries)
			}
			if calls > tc.failures {
				if err != nil || !finished {
					t.Fatal(err)
				}
			} else if err == nil || finished {
				t.Fatal("failure did not pause")
			}
			if tc.name == "budget" && !errors.Is(err, domain.ErrIterationLimit) {
				t.Fatal(err)
			}
		})
	}
}
