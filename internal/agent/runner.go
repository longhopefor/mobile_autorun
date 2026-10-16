package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cloudwego/eino/adk"
	em "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"mobile_autorun/internal/domain"
	pm "mobile_autorun/internal/model"
	"time"
)

// MaxScreenReobservations bounds consecutive decisions discarded before dispatch.
const MaxScreenReobservations = 3

// Carousel page indicators can remain visible briefly after a successful swipe.
// Keep the immediate after-image as evidence, but wait before the next decision.
const PostSwipeSettle = 3 * time.Second

const MaxModelRetries = 2

type Hooks struct {
	History       []string
	Business      func(context.Context, domain.Decision, domain.Observation) (string, error)
	StartModel    func(context.Context, pm.Request) error
	ModelRetry    func(context.Context, string, int, time.Duration) error
	Observe       func(context.Context) (domain.Observation, error)
	Execute       func(context.Context, domain.Decision) (domain.Observation, error)
	ScreenChanged func(context.Context, domain.Decision, domain.Observation, int, bool) error
	NoProgress    func(context.Context, domain.Decision, int, bool) error
	Decision      func(context.Context, pm.Request, pm.Result, time.Duration, error) error
	Finish        func(context.Context, domain.Decision) error
	Takeover      func(context.Context, domain.Decision) error
	BeforeModel   func(context.Context) error
	Checkpoints   adk.CheckPointStore
}
type runModel struct {
	provider      pm.Provider
	task          domain.Task
	profile       domain.Profile
	hooks         *Hooks
	obs           domain.Observation
	history       []string
	screenChanges int
	progress      progressWatch
	remaining     int
	observeAfter  time.Time
}

func (m *runModel) WithTools(_ []*schema.ToolInfo) (em.ToolCallingChatModel, error) { return m, nil }
func (m *runModel) Generate(ctx context.Context, _ []*schema.Message, _ ...em.Option) (*schema.Message, error) {
	for retry := 0; ; retry++ {
		message, err := m.generateOnce(ctx)
		if err == nil || !domain.RetryableModelError(err) || retry >= MaxModelRetries || ctx.Err() != nil {
			return message, err
		}
		if m.remaining <= 0 {
			return nil, domain.ErrIterationLimit
		}
		delay := time.Duration(retry+1) * time.Second
		if m.hooks.ModelRetry != nil {
			if e := m.hooks.ModelRetry(ctx, domain.ModelErrorCode(err), retry+1, delay); e != nil {
				return nil, e
			}
		}
		b, _ := json.Marshal(map[string]any{"status": "model_retry", "code": domain.ModelErrorCode(err), "action_executed": false, "message": "上一轮模型失败，未执行手机动作。依据新截图重新决策。"})
		m.history = append(m.history, string(b))
		if len(m.history) > 12 {
			m.history = m.history[len(m.history)-12:]
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (m *runModel) generateOnce(ctx context.Context) (*schema.Message, error) {
	if m.remaining <= 0 {
		return nil, domain.ErrIterationLimit
	}
	if delay := time.Until(m.observeAfter); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if e := m.hooks.BeforeModel(ctx); e != nil {
		return nil, e
	}
	m.remaining--
	o, e := m.hooks.Observe(ctx)
	if e != nil {
		return nil, e
	}
	m.obs = o
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	q := pm.Request{ID: uuid.NewString(), TaskID: m.task.ID, Profile: m.profile, Goal: m.task.Spec.Goal, ObservationID: o.ID, Image: base64.StdEncoding.EncodeToString(o.Image), State: o.State, History: m.history, Packages: m.task.Spec.Packages, Deadline: deadline.UTC().Format(time.RFC3339Nano)}
	start := time.Now()
	var r pm.Result
	handled := false
	if planner, ok := m.provider.(interface {
		PlanObservation(context.Context, domain.Observation) (pm.Result, bool, error)
	}); ok {
		r, handled, e = planner.PlanObservation(ctx, o)
		if e != nil {
			return nil, e
		}
	}
	if !handled && m.hooks.StartModel != nil {
		if e = m.hooks.StartModel(ctx, q); e != nil {
			return nil, e
		}
	}
	if !handled {
		r, e = m.provider.Next(ctx, q)
	}
	if e == nil {
		e = r.Decision.Validate(o.ID, m.task.Spec.Packages)
	}
	recordCtx, recordDone := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer recordDone()
	if re := m.hooks.Decision(recordCtx, q, r, time.Since(start), e); re != nil {
		return nil, re
	}
	if e != nil {
		return nil, e
	}
	b, _ := json.Marshal(r.Decision)
	m.history = append(m.history, string(b))
	if len(m.history) > 12 {
		m.history = m.history[len(m.history)-12:]
	}
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: uuid.NewString(), Type: "function", Function: schema.FunctionCall{Name: r.Decision.Tool, Arguments: string(b)}}}}, nil
}
func (m *runModel) Stream(ctx context.Context, in []*schema.Message, opts ...em.Option) (*schema.StreamReader[*schema.Message], error) {
	v, e := m.Generate(ctx, in, opts...)
	if e != nil {
		return nil, e
	}
	return schema.StreamReaderFromArray([]*schema.Message{v}), nil
}

type phoneTool struct {
	name string
	m    *runModel
}

func (t *phoneTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "One phone decision tied to latest observation", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"observation_id": {Type: schema.String, Required: true}, "reason": {Type: schema.String, Required: true}, "action": {Type: schema.Object}})}, nil
}
func (t *phoneTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var d domain.Decision
	if e := domain.DecodeStrict([]byte(args), &d); e != nil {
		return "", e
	}
	if d.Tool != t.name {
		return "", errors.New("tool_mismatch")
	}
	if e := d.Validate(t.m.obs.ID, t.m.task.Spec.Packages); e != nil {
		return "", e
	}
	switch t.name {
	case "phone_action":
		frame, streak := t.m.progress.check(t.m.obs, *d.Action)
		if streak >= ProgressStopAt {
			if t.m.hooks.NoProgress != nil {
				if err := t.m.hooks.NoProgress(ctx, d, streak, true); err != nil {
					return "", err
				}
			}
			return "", errors.New("phone_no_progress: 连续重复同一动作且页面没有进展，已暂停；本轮动作未下发")
		}
		o, e := t.m.hooks.Execute(ctx, d)
		if e != nil {
			t.m.progress.reset()
		}
		if errors.Is(e, domain.ErrScreenChanged) {
			t.m.screenChanges++
			retry := t.m.screenChanges <= MaxScreenReobservations
			if t.m.hooks.ScreenChanged != nil {
				if err := t.m.hooks.ScreenChanged(ctx, d, o, t.m.screenChanges, retry); err != nil {
					return "", err
				}
			}
			if !retry {
				return "", e
			}
			// Execute returns this typed error only before preparing/dispatching
			// the command. Never repeat it: Generate obtains a new observation
			// and requests a new decision under the existing task budget.
			t.m.obs = o
			b, _ := json.Marshal(map[string]any{
				"status": "screen_changed_reobserve", "action_executed": false,
				"discarded_observation_id": d.ObservationID, "latest_observation_id": o.ID,
				"attempt": t.m.screenChanges,
				"message": "上一条动作尚未执行，因页面变化已丢弃。请根据下一张新截图重新决策，不要假定动作已成功。",
			})
			t.m.history = append(t.m.history, string(b))
			if len(t.m.history) > 12 {
				t.m.history = t.m.history[len(t.m.history)-12:]
			}
			timer := time.NewTimer(500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-timer.C:
			}
			return string(b), nil
		}
		if e != nil {
			return "", e
		}
		t.m.screenChanges = 0
		if d.Action.Kind == "swipe" {
			t.m.observeAfter = time.Now().Add(PostSwipeSettle)
		}
		t.m.progress.commit(frame, *d.Action, streak)
		t.m.obs = o
		if streak >= ProgressWarnAt {
			if t.m.hooks.NoProgress != nil {
				if err := t.m.hooks.NoProgress(ctx, d, streak, false); err != nil {
					return "", err
				}
			}
			note := fmt.Sprintf("no_progress_warning: 已连续%d次在不变页面选择相同动作；请依据新截图换目标、返回或接管，禁止继续重复。动作有回报不代表业务成功。", streak)
			t.m.history = append(t.m.history, note)
			return note, nil
		}
		b, _ := json.Marshal(o)
		return string(b), nil
	case "record_business", "dispatch_business":
		t.m.progress.reset()
		if t.m.hooks.Business == nil {
			return "", errors.New("business_tools_disabled")
		}
		return t.m.hooks.Business(ctx, d, t.m.obs)
	case "finish_task":
		return "completion_candidate_requires_verification", t.m.hooks.Finish(ctx, d)
	default:
		return "waiting_human", t.m.hooks.Takeover(ctx, d)
	}
}
func Run(ctx context.Context, t domain.Task, p domain.Profile, provider pm.Provider, h Hooks) error {
	remaining := t.Spec.MaxIterations - t.Iterations
	if remaining <= 0 {
		return domain.ErrIterationLimit
	}
	m := &runModel{provider: provider, task: t, profile: p, hooks: &h, remaining: remaining, history: append([]string(nil), h.History...)}
	tools := []tool.BaseTool{}
	for _, n := range []string{"phone_action", "finish_task", "request_takeover", "record_business", "dispatch_business"} {
		tools = append(tools, &phoneTool{name: n, m: m})
	}
	// One extra model node lets our budget check return a typed pause before
	// Eino's generic iteration error. It cannot invoke the provider or a tool.
	a, e := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "phone_operator", Description: "Operate phone apps for the task goal", Instruction: "Choose one tool per fresh observation", Model: m, MaxIterations: remaining + 1, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}, ReturnDirectly: map[string]bool{"finish_task": true, "request_takeover": true}}})
	if e != nil {
		return e
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: a, CheckPointStore: h.Checkpoints})
	iter := runner.Run(ctx, []adk.Message{schema.UserMessage(t.Spec.Goal)}, adk.WithCheckPointID(t.ID))
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return event.Err
		}
	}
	return nil
}
