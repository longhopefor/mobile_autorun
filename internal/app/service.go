package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"mobile_autorun/internal/agent"
	"mobile_autorun/internal/attachment"
	"mobile_autorun/internal/business"
	"mobile_autorun/internal/device"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/mediaindex"
	"mobile_autorun/internal/model"
	"mobile_autorun/internal/store"
	"sync"
	"time"
)

type TaskArgs struct {
	TaskID string `json:"task_id"`
}

func (TaskArgs) Kind() string { return "phone_task" }

type Worker struct {
	river.WorkerDefaults[TaskArgs]
	Service *Service
}

func (w *Worker) Work(ctx context.Context, j *river.Job[TaskArgs]) error {
	return w.Service.Run(ctx, j.Args.TaskID)
}

type Service struct {
	Store   *store.Store
	Devices *device.Gateway
	Models  *device.Gateway
	Media   *device.Gateway
	Driver  device.Driver
	Queue   *river.Client[pgx.Tx]
	mu      sync.Mutex
	active  map[string]context.CancelFunc
}

func New(ctx context.Context, s *store.Store) (*Service, error) {
	a := &Service{Store: s, Devices: device.NewGateway(), Models: device.NewGateway(), Media: device.NewGateway(), active: map[string]context.CancelFunc{}}
	a.Driver = &device.Portal{Gateway: a.Devices}
	workers := river.NewWorkers()
	river.AddWorker(workers, &Worker{Service: a})
	driver := riverpgxv5.New(s.Pool)
	m, e := rivermigrate.New(driver, nil)
	if e != nil {
		return nil, e
	}
	if _, e = m.Migrate(ctx, rivermigrate.DirectionUp, nil); e != nil {
		return nil, e
	}
	a.Queue, e = river.NewClient(driver, &river.Config{Workers: workers, Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 5}}, JobTimeout: 12 * time.Minute})
	return a, e
}
func (a *Service) Create(ctx context.Context, owner, key string, spec domain.TaskSpec) (domain.Task, error) {
	if key == "" || len(key) > 200 {
		return domain.Task{}, errors.New("idempotency_key_required")
	}
	if e := spec.Validate(); e != nil {
		return domain.Task{}, e
	}
	if e := a.Store.OwnPeer(ctx, owner, spec.DeviceID, "device"); e != nil {
		return domain.Task{}, errors.New("device_not_owned")
	}
	profile, e := a.Store.Profile(ctx, owner, spec.ModelProfileID)
	if e != nil {
		return domain.Task{}, errors.New("model_not_owned")
	}
	for _, id := range spec.AssetIDs {
		asset, _, e := a.Store.Asset(ctx, owner, id)
		if e != nil {
			return domain.Task{}, errors.New("asset_not_owned")
		}
		if asset.Bytes < 1 || asset.Bytes > attachment.MaxBytes {
			return domain.Task{}, errors.New("invalid_attachment_size")
		}
		if spec.Template == "xhs_image" && !attachment.IsImage(asset.MIME) {
			return domain.Task{}, errors.New("xhs_image_asset_required")
		}
	}
	spec.ModelSnapshot = nil // Never accept a client-supplied provider configuration.
	request, _ := json.Marshal(spec)
	hash := store.Hash(string(request))
	spec.ModelSnapshot = &profile
	b, _ := json.Marshal(spec)
	tx, e := a.Store.Pool.Begin(ctx)
	if e != nil {
		return domain.Task{}, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", owner+":"+key); e != nil {
		return domain.Task{}, e
	}
	var id, oldHash string
	e = tx.QueryRow(ctx, "SELECT id,request_hash FROM tasks WHERE owner=$1 AND idempotency_key=$2", owner, key).Scan(&id, &oldHash)
	if e == nil {
		if hash != oldHash {
			return domain.Task{}, errors.New("idempotency_conflict")
		}
		if e = tx.Commit(ctx); e != nil {
			return domain.Task{}, e
		}
		return a.Store.Task(ctx, id)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return domain.Task{}, e
	}
	id = uuid.NewString()
	if _, e = tx.Exec(ctx, "INSERT INTO tasks(id,owner,idempotency_key,request_hash,spec,status) VALUES($1,$2,$3,$4,$5,'queued')", id, owner, key, hash, b); e != nil {
		return domain.Task{}, e
	}
	opts := &river.InsertOpts{MaxAttempts: 3}
	if spec.ScheduledAt != nil {
		opts.ScheduledAt = *spec.ScheduledAt
	}
	if _, e = a.Queue.InsertTx(ctx, tx, TaskArgs{id}, opts); e != nil {
		return domain.Task{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return domain.Task{}, e
	}
	return a.Store.Task(ctx, id)
}
func (a *Service) observe(ctx context.Context, t domain.Task) (domain.Observation, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	o, e := a.Driver.Observe(ctx, t.Spec.DeviceID)
	if e != nil {
		return o, e
	}
	e = a.Store.SaveObservation(ctx, t.Owner, t.ID, &o)
	if e == nil {
		e = a.Store.Event(ctx, t.ID, "observation", o)
	}
	return o, e
}
func (a *Service) Run(parent context.Context, id string) error {
	t, e := a.Store.Task(parent, id)
	if e != nil {
		return e
	}
	if t.ArchivedAt != nil {
		return nil
	}
	switch t.Status {
	case "queued", "waiting_device", "waiting_model":
	default:
		return nil
	}
	if !a.Devices.Online(t.Spec.DeviceID) {
		if e = a.Store.State(parent, id, "waiting_device", "手机离线"); e != nil {
			return e
		}
		return river.JobSnooze(15 * time.Second)
	}
	var p domain.Profile
	if t.Spec.ModelSnapshot != nil {
		p = *t.Spec.ModelSnapshot
	} else {
		p, e = a.Store.Profile(parent, t.Owner, t.Spec.ModelProfileID)
		if e != nil {
			return e
		}
	}
	if p.Provider != "api" && !a.Models.Online(p.ConnectorID) {
		if e = a.Store.State(parent, id, "waiting_model", "模型连接器离线"); e != nil {
			return e
		}
		return river.JobSnooze(15 * time.Second)
	}
	holder := uuid.NewString()
	g, e := a.Store.Acquire(parent, t.Spec.DeviceID, holder)
	if errors.Is(e, domain.ErrBusy) {
		return river.JobSnooze(5 * time.Second)
	}
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	defer func() {
		clean, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = a.Store.Release(clean, t.Spec.DeviceID, holder, g)
	}()
	a.mu.Lock()
	a.active[id] = cancel
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.active, id); a.mu.Unlock() }()
	// Re-read after lease acquisition: cancellation may have arrived while waiting.
	t, e = a.Store.Task(ctx, id)
	if e != nil {
		return e
	}
	if t.ArchivedAt != nil || (t.Status != "queued" && t.Status != "waiting_device" && t.Status != "waiting_model") {
		return nil
	}
	uncertain, e := a.Store.Uncertain(ctx, t.Spec.DeviceID)
	if e != nil {
		return e
	}
	if uncertain {
		return a.Store.State(ctx, id, "reconciling", "发现未核对的在途动作，需人工确认")
	}
	if t.Iterations >= t.Spec.MaxIterations {
		return a.Store.State(ctx, id, "failed", "模型迭代预算耗尽")
	}
	if e = a.Store.State(ctx, id, "running", ""); e != nil {
		return e
	}
	go func() {
		timer := time.NewTicker(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if e := a.Store.Renew(ctx, t.Spec.DeviceID, holder, g); e != nil {
					cancel()
					return
				}
			}
		}
	}()
	// Material transfer can partially succeed. A failure stops automation; it never triggers publish.
	paths := []string{}
	refs := []model.Reference{}
	attachments := []model.Attachment{}
	for i, assetID := range t.Spec.AssetIDs {
		asset, data, err := a.Store.Asset(ctx, t.Owner, assetID)
		if err != nil {
			e = err
			break
		}
		imageAsset := attachment.IsImage(asset.MIME)
		ext := attachment.Extension(asset.Name, asset.MIME)
		folder := "Download"
		if imageAsset {
			folder = "Pictures"
		}
		path := fmt.Sprintf("/sdcard/%s/mobile_autorun_%s/%02d%s", folder, t.ID, i+1, ext)

		if err = a.Driver.Upload(ctx, t.Spec.DeviceID, path, data); err != nil {
			e = err
			break
		}
		if err = a.Store.Event(ctx, id, "material_transferred", map[string]string{"asset_id": assetID, "name": asset.Name, "mime": asset.MIME, "phone_path": path, "verification": "selection_pending", "integrity": "readback_matched"}); err != nil {
			e = err
			break
		}
		attachments = append(attachments, model.Attachment{AssetID: assetID, Name: asset.Name, MIME: asset.MIME, PhonePath: path})
		if imageAsset {
			paths = append(paths, path)
			refName := fmt.Sprintf("%02d", i+1)
			if t.Spec.Template == "general" && asset.Name != "" {
				refName += ": " + asset.Name
			}
			ref, err := business.ReferenceImage(refName, data)
			if err != nil {
				e = err
				break
			}
			refs = append(refs, ref)
		}
	}
	if e == nil && len(paths) > 0 {
		indexCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		indexed, indexErr := (mediaindex.Client{Portal: a.Devices, Legacy: a.Media}).Index(indexCtx, t.Spec.DeviceID, paths)
		stop()
		if indexErr != nil {
			e = fmt.Errorf("图片已传输，图库索引未完成: %w", indexErr)
		} else {
			e = a.Store.Event(ctx, id, "media_indexed", indexed)
		}
	}

	var provider model.Provider = &model.API{}
	if p.Provider != "api" {
		provider = &model.Remote{Gateway: a.Models}
	}
	if t.Spec.Template == "general" && len(attachments) > 0 {
		provider = &model.WithAttachments{Delegate: provider, Files: attachments, References: refs}
	}
	var xhs *business.XHS
	if e == nil && t.Spec.Template == "xhs_image" {
		xhs = &business.XHS{Spec: t.Spec, Album: "mobile_autorun_" + t.ID, Delegate: provider, References: refs, Save: func(c context.Context, state business.XHSState) error {
			return a.Store.SaveBusinessProgress(c, id, state)
		}}
		var saved json.RawMessage
		saved, e = a.Store.BusinessProgress(ctx, id)
		if e == nil && len(saved) > 0 {
			e = json.Unmarshal(saved, &xhs.State)
		}
		provider = xhs
	}
	var ops *business.Operations
	if e == nil && (t.Spec.Template == "wechat_crm" || t.Spec.Template == "xhs_growth") {
		ops, e = a.operations(ctx, t, provider)
		if e == nil {
			provider = ops
		}
	}
	var current domain.Observation
	execute := func(c context.Context, d domain.Decision) (domain.Observation, error) {
		cur, err := a.Store.Task(c, id)
		if err != nil {
			return current, err
		}
		if cur.Status != "running" {
			return current, domain.ErrPause
		}
		if current.Revision != a.Devices.Revision(t.Spec.DeviceID) {
			return current, domain.ErrScreenChanged
		}
		fresh, err := a.observe(c, t)
		if err != nil {
			return current, err
		}
		if d.Action.NeedsStableScreen() {
			stable, e := domain.SameObservationForAction(fresh, current, *d.Action)
			if e != nil {
				return current, e
			}
			if !stable {
				return fresh, domain.ErrScreenChanged
			}
		}
		if xhs != nil {
			if err = xhs.Guard(c, fresh, *d.Action); err != nil {
				return current, err
			}
		}
		if ops != nil {
			if err = ops.Guard(fresh, *d.Action, d.Tool == "dispatch_business", d.Record); err != nil {
				return current, err
			}
		}
		// Keep the decision's observation ID; the fresh image confirms it is still current.
		cmd, err := a.Store.Prepare(c, id, t.Spec.DeviceID, holder, current.ID, g, *d.Action)
		if err != nil {
			return current, err
		}
		actCtx, done := context.WithTimeout(c, 30*time.Second)
		err = a.Driver.Execute(actCtx, t.Spec.DeviceID, *d.Action, current)
		done()
		clean, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err != nil {
			if x := a.Store.CommandResult(clean, cmd, "uncertain", "执行回报未明确"); x != nil {
				return current, x
			}
			if x := a.Store.Event(clean, id, "action_uncertain", map[string]any{"action_id": cmd, "action": d.Action, "observation_id": current.ID}); x != nil {
				return current, x
			}
			return current, domain.ErrUncertain
		}
		if err = a.Store.CommandResult(clean, cmd, "reported", ""); err != nil {
			return current, err
		}
		if err = a.Store.Event(clean, id, "action_reported", map[string]any{"action_id": cmd, "action": d.Action, "observation_id": current.ID}); err != nil {
			return current, err
		}
		after, err := a.observe(c, t)
		if err == nil {
			err = a.Store.Event(c, id, "action_result", map[string]any{"action_id": cmd, "observation_id": current.ID, "after_observation_id": after.ID})
		}
		return after, err
	}
	var history []string
	if e == nil {
		history, e = a.Store.RecentDecisionHistory(ctx, id)
	}
	if e == nil {
		e = agent.Run(ctx, t, p, provider, agent.Hooks{Checkpoints: a.Store, History: history,
			ModelRetry: func(c context.Context, code string, attempt int, delay time.Duration) error {
				return a.Store.Event(c, id, "model_retry", map[string]any{"code": code, "retry": attempt, "max_retries": agent.MaxModelRetries, "delay_ms": delay.Milliseconds(), "action_executed": false, "reason": "模型调用暂时失败，将获取新截图后重新决策；未下发手机动作"})
			},
			StartModel: func(c context.Context, q model.Request) error {
				var connector any
				if p.ConnectorID != "" {
					connector = p.ConnectorID
				}
				deadline, e := time.Parse(time.RFC3339Nano, q.Deadline)
				if e != nil {
					return e
				}
				_, e = a.Store.Pool.Exec(c, "INSERT INTO model_requests(id,task_id,observation_id,connector_id,status,deadline) VALUES($1,$2,$3,$4,'dispatched',$5)", q.ID, id, q.ObservationID, connector, deadline)
				return e
			},
			BeforeModel: func(c context.Context) error {
				cur, err := a.Store.Task(c, id)
				if err != nil {
					return err
				}
				if cur.Status != "running" {
					return domain.ErrPause
				}
				if err = a.Store.Renew(c, t.Spec.DeviceID, holder, g); err != nil {
					return err
				}
				return a.Store.Iterations(c, id)
			},
			Observe: func(c context.Context) (domain.Observation, error) {
				var err error
				current, err = a.observe(c, t)
				return current, err
			},
			Decision: func(c context.Context, q model.Request, r model.Result, d time.Duration, err error) error {
				if r.Source == "xhs_rules" {
					return a.Store.Event(c, id, "rule_decision", map[string]any{"decision": r.Decision, "source": r.Source, "duration_ms": d.Milliseconds()})
				}
				reason := ""
				if err != nil {
					reason = err.Error()
				}
				if x := a.Store.RecordModel(c, q.ID, id, p.ID, q.ObservationID, d, r.Usage, reason); x != nil {
					return x
				}
				return a.Store.Event(c, id, "model_decision", map[string]any{"decision": r.Decision, "duration_ms": d.Milliseconds(), "usage": r.Usage, "error": reason, "input": r.Input, "provider": p.Provider, "model": p.Model})
			},
			ScreenChanged: func(c context.Context, d domain.Decision, o domain.Observation, attempt int, retry bool) error {
				kind := "screen_reobserve"
				if !retry {
					kind = "screen_reobserve_exhausted"
				}
				return a.Store.Event(c, id, kind, map[string]any{"attempt": attempt, "max_reobservations": agent.MaxScreenReobservations, "action_executed": false, "discarded_observation_id": d.ObservationID, "latest_observation_id": o.ID, "action": d.Action})
			},
			Execute: execute,
			NoProgress: func(c context.Context, d domain.Decision, streak int, stopped bool) error {
				kind := "no_progress_warning"
				if stopped {
					kind = "no_progress_paused"
				}
				return a.Store.Event(c, id, kind, map[string]any{"streak": streak, "warn_at": agent.ProgressWarnAt, "stop_at": agent.ProgressStopAt, "action_executed": !stopped, "observation_id": d.ObservationID, "action": d.Action})
			},
			Business: func(c context.Context, d domain.Decision, o domain.Observation) (string, error) {
				return a.handleBusiness(c, t, ops, d, o, execute)
			},
			Finish: func(c context.Context, d domain.Decision) error {
				if ops != nil && (!ops.State.AccountVerified || (t.Spec.Mode == "draft" && ops.State.DraftTaskID == "")) {
					return errors.New("business_result_incomplete")
				}
				if xhs != nil {
					if err := xhs.Completion(current); err != nil {
						return err
					}
				}
				if err := a.Store.Event(c, id, "completion_candidate", map[string]any{"decision": d, "evidence": current, "mode": t.Spec.Mode}); err != nil {
					return err
				}
				return a.Store.State(c, id, "waiting_human", "请核验截图中的业务结果，确认后标记成功")
			},
			Takeover: func(c context.Context, d domain.Decision) error {
				return a.Store.State(c, id, "waiting_human", d.Reason)
			},
		})
	}
	clean, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	cur, ce := a.Store.Task(clean, id)
	if ce != nil {
		return ce
	}
	if cur.Status == "cancel_requested" {
		return a.Store.State(clean, id, "cancelled", "已停止新动作，在途结果需核对")
	}
	if cur.Status != "running" {
		return nil
	}
	if e != nil {
		var modelErr *domain.ModelError
		if errors.As(e, &modelErr) {
			return a.Store.State(clean, id, "waiting_human", modelErr.Error())
		}
		if errors.Is(e, domain.ErrIterationLimit) {
			if err := a.Store.Event(clean, id, "iteration_limit_reached", map[string]any{"iterations": cur.Iterations, "max_iterations": cur.Spec.MaxIterations, "action_executed": false}); err != nil {
				return err
			}
			return a.Store.State(clean, id, "waiting_human", fmt.Sprintf("已达到 %d 轮决策上限，任务已暂停；可追加轮数后继续，已完成的操作不会自动重放", cur.Spec.MaxIterations))
		}
		return a.Store.State(clean, id, "waiting_human", e.Error())
	}
	return a.Store.State(clean, id, "waiting_human", "Agent 已停止，需核验结果")
}
func (a *Service) Pause(ctx context.Context, id, mode string) error {
	status := "waiting_human"
	if mode == "cancel" {
		status = "cancel_requested"
	}
	if e := a.Store.State(ctx, id, status, "用户请求"); e != nil {
		return e
	}
	a.mu.Lock()
	cancel := a.active[id]
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	} else if mode == "cancel" {
		return a.Store.State(ctx, id, "cancelled", "已取消排队任务")
	}
	return nil
}
func (a *Service) Resume(ctx context.Context, id string, reconciled bool) error {
	return a.ResumeWithBudget(ctx, id, reconciled, 0)
}

func (a *Service) ResumeWithBudget(ctx context.Context, id string, reconciled bool, additional int) error {
	t, e := a.Store.Task(ctx, id)
	if e != nil {
		return e
	}
	if t.ArchivedAt != nil {
		return errors.New("task_archived_restore_first")
	}
	if t.Status != "waiting_human" && t.Status != "reconciling" {
		return errors.New("task_not_paused")
	}
	if additional < 0 || additional > domain.MaxTaskIterations-t.Spec.MaxIterations {
		return errors.New("invalid_additional_iterations")
	}
	if t.Iterations >= t.Spec.MaxIterations+additional {
		return domain.ErrIterationLimit
	}
	holder := uuid.NewString()
	g, e := a.Store.Acquire(ctx, t.Spec.DeviceID, holder)
	if e != nil {
		return e
	}
	defer a.Store.Release(ctx, t.Spec.DeviceID, holder, g)
	unknown, e := a.Store.Uncertain(ctx, t.Spec.DeviceID)
	if e != nil {
		return e
	}
	if unknown && !reconciled {
		return errors.New("reconciliation_confirmation_required")
	}
	if unknown {
		if e = a.Store.Reconcile(ctx, t.Spec.DeviceID); e != nil {
			return e
		}
	}
	// Explicit reconciliation also repairs the old keyboard-obscured draft
	// command. It never resets a publish attempt or a reopened/saved result.
	if reconciled && t.Spec.Template == "xhs_image" && t.Spec.Mode == "draft" {
		raw, err := a.Store.BusinessProgress(ctx, id)
		if err != nil {
			return err
		}
		var state business.XHSState
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &state); err != nil {
				return err
			}
		}
		if state.SaveRequested && !state.SaveConfirmed && !state.DraftListed && !state.PublishRequested {
			var old json.RawMessage
			err = a.Store.Pool.QueryRow(ctx, `SELECT o.state FROM device_commands c JOIN observations o ON o.id=c.observation_id WHERE c.task_id=$1 AND c.status='reported' AND c.action->'target'->>'text' IN ('存草稿','保存草稿') ORDER BY c.created_at DESC LIMIT 1`, id).Scan(&old)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil {
				ui, err := domain.ParseUI(domain.Observation{State: old})
				if err != nil {
					return err
				}
				if ui.Keyboard {
					state.SaveRequested = false
					state.Stage = "save"
					if err = a.Store.SaveBusinessProgress(ctx, id, state); err != nil {
						return err
					}
					if err = a.Store.Event(ctx, id, "business_reconciled", map[string]string{"reason": "操作者核对键盘遮挡的旧草稿指令，允许收起键盘后重试保存"}); err != nil {
						return err
					}
				}
			}
		}
	}
	tx, e := a.Store.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	// Lock and recheck so concurrent resume requests cannot both extend the
	// budget, erase an archive, or enqueue duplicate work.
	var spec domain.TaskSpec
	var iterations int
	var raw json.RawMessage
	if e = tx.QueryRow(ctx, "SELECT spec,iterations FROM tasks WHERE id=$1 AND archived_at IS NULL AND status IN ('waiting_human','reconciling') FOR UPDATE", id).Scan(&raw, &iterations); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return errors.New("state_conflict")
		}
		return e
	}
	if e = json.Unmarshal(raw, &spec); e != nil {
		return e
	}
	if additional < 0 || additional > domain.MaxTaskIterations-spec.MaxIterations {
		return errors.New("invalid_additional_iterations")
	}
	if iterations >= spec.MaxIterations+additional {
		return domain.ErrIterationLimit
	}
	previous := spec.MaxIterations
	tag, e := tx.Exec(ctx, "UPDATE tasks SET status='queued',error='',spec=jsonb_set(spec,'{max_iterations}',to_jsonb($2::integer)) WHERE id=$1", id, previous+additional)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return errors.New("state_conflict")
	}
	if additional > 0 {
		data, _ := json.Marshal(map[string]int{"previous_limit": previous, "new_limit": previous + additional, "additional_iterations": additional, "iterations": iterations})
		if _, e = tx.Exec(ctx, "INSERT INTO task_events(task_id,type,data) VALUES($1,'iteration_budget_extended',$2)", id, data); e != nil {
			return e
		}
	}
	if _, e = a.Queue.InsertTx(ctx, tx, TaskArgs{id}, &river.InsertOpts{MaxAttempts: 3}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
