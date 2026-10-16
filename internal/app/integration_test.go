package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"mobile_autorun/internal/device"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/model"
	"mobile_autorun/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRiverEinoPortalTaskLoop(t *testing.T)                   { runLoop(t, false) }
func TestCancelUnknownActionNeverReplayed(t *testing.T)          { runLoop(t, true) }
func TestButtonCaretBlinkTaskContinues(t *testing.T)             { runLoop(t, false, "button") }
func TestButtonPageChangeReobservesBeforeDispatch(t *testing.T)  { runLoop(t, false, "button_page") }
func TestAnonymousHistoryCaretTaskContinues(t *testing.T)        { runLoop(t, false, "button_history") }
func TestAnonymousHistoryPageChangeReobserves(t *testing.T)      { runLoop(t, false, "button_history_page") }
func TestCaretBlinkTaskContinues(t *testing.T)                   { runLoop(t, false, "caret") }
func TestPageChangeReobservesBeforeDispatch(t *testing.T)        { runLoop(t, false, "page") }
func TestContinuousPageChangesPauseWithoutDispatch(t *testing.T) { runLoop(t, false, "unstable") }
func TestNoProgressPausesBeforeSixthCommand(t *testing.T)        { runLoop(t, false, "no_progress") }
func TestLauncherTapNotBlockedByTargetPackages(t *testing.T)     { runLoop(t, false, "launcher") }
func TestTaskWithoutTargetPackagesCanTapLauncher(t *testing.T) {
	runLoop(t, false, "launcher_no_packages")
}
func TestLaunchOutsideTargetPackages(t *testing.T)               { runLoop(t, false, "unlisted_launch") }
func TestCarouselBadgeSettlesBeforeNextDecision(t *testing.T)    { runLoop(t, false, "swipe_badge") }
func TestIterationBudgetPausesWithoutExtraCommands(t *testing.T) { runLoop(t, false, "budget") }
func TestModelRetryRecoversWithFreshObservation(t *testing.T)    { runLoop(t, false, "model_retry") }
func TestModelRetryExhaustionPausesWithDiagnostic(t *testing.T)  { runLoop(t, false, "model_exhausted") }
func TestModelAuthenticationFailureNeverRetried(t *testing.T)    { runLoop(t, false, "model_auth") }
func runLoop(t *testing.T, cancelOnAction bool, screenChange ...string) {
	a, owner, spec := testApp(t)
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	spec.ScheduledAt = nil
	var mu sync.Mutex
	calls, actions := 0, 0
	var tappedX float64
	issued := make(chan struct{}, 1)
	mode := ""
	if len(screenChange) > 0 {
		mode = screenChange[0]
	}
	scopeMode := strings.HasPrefix(mode, "launcher") || mode == "unlisted_launch" || mode == "swipe_badge" || mode == "budget" || strings.HasPrefix(mode, "model_")
	if mode == "budget" {
		spec.MaxIterations = 1
	}
	var badgeUntil time.Time
	if mode == "launcher_no_packages" {
		spec.Packages = nil
	}
	var buffer, changed bytes.Buffer
	frame := image.NewRGBA(image.Rect(0, 0, 100, 200))
	draw.Draw(frame, frame.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	_ = png.Encode(&buffer, frame)
	for y := 70; y < 80; y++ {
		frame.Set(4, y, color.Black)
	}
	if mode == "page" || strings.HasSuffix(mode, "_page") || mode == "unstable" {
		frame.Set(90, 150, color.Black)
	}
	_ = png.Encode(&changed, frame)
	screenshots := 0
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := websocket.Accept(w, r, nil)
		if e != nil {
			return
		}
		a.Devices.Serve(r.Context(), spec.DeviceID, conn)
	}))
	defer rpc.Close()
	conn, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(rpc.URL, "http"), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.CloseNow()
	go func() {
		for {
			var f device.Frame
			if wsjson.Read(ctx, conn, &f) != nil {
				return
			}
			var result any
			switch f.Method {
			case "screenshot":
				screenshots++
				data := buffer.Bytes()
				if (mode != "" && !scopeMode && mode != "unstable" && screenshots > 1) || (mode == "unstable" && screenshots%2 == 0) {
					data = changed.Bytes()
				}
				if mode == "swipe_badge" {
					mu.Lock()
					visible := time.Now().Before(badgeUntil)
					mu.Unlock()
					if visible {
						data = changed.Bytes()
					}
				}
				result = base64.StdEncoding.EncodeToString(data)
			case "state":
				result = `{"a11y_tree":[],"phone_state":{"packageName":"com.android.settings"}}`
				if mode != "" && !scopeMode {
					result = `{"a11y_tree":{"children":[{"resourceId":"title","isEditable":true,"isVisibleToUser":true,"isEnabled":true,"boundsInScreen":{"left":10,"top":30,"right":90,"bottom":60}},{"resourceId":"body","isEditable":true,"isFocused":true,"isVisibleToUser":true,"isEnabled":true,"boundsInScreen":{"left":0,"top":70,"right":100,"bottom":100}}]},"phone_state":{"isEditable":true,"focusedElement":{"resourceId":"body"},"packageName":"com.android.settings","activityName":"Editor"}}`
					if strings.HasPrefix(mode, "button") {
						state := result.(string)
						state = strings.Replace(state, `"right":90,"bottom":60`, `"right":70,"bottom":60`, 1)
						result = strings.Replace(state, `"bottom":100}}]`, `"bottom":100}},{"resourceId":"search","text":"搜索","isClickable":true,"isVisibleToUser":true,"isEnabled":true,"boundsInScreen":{"left":75,"top":30,"right":95,"bottom":60}}]`, 1)
					}
					if strings.HasPrefix(mode, "button_history") {
						result = strings.Replace(result.(string), `"resourceId":"search","text":"搜索"`, `"resourceId":"","text":"","children":[{"resourceId":"history_label","text":"ai 智能体 工作流","isVisibleToUser":true,"isEnabled":true,"boundsInScreen":{"left":78,"top":35,"right":92,"bottom":55}}]`, 1)
					}
				}
				if strings.HasPrefix(mode, "launcher") || mode == "unlisted_launch" {
					result = `{"a11y_tree":[],"phone_state":{"packageName":"com.huawei.android.launcher"}}`
				}
			case "app", "tap", "swipe":
				mu.Lock()
				actions++
				if mode == "swipe_badge" {
					badgeUntil = time.Now().Add(2 * time.Second)
				}
				var params map[string]any
				_ = json.Unmarshal(f.Params, &params)
				tappedX, _ = params["x"].(float64)
				mu.Unlock()
				select {
				case issued <- struct{}{}:
				default:
				}
				if cancelOnAction {
					continue
				} // The phone may have acted; its reply is lost.
				result = "ok"
			default:
				result = "ok"
			}
			b, _ := json.Marshal(result)
			_ = wsjson.Write(ctx, conn, device.Frame{ID: f.ID, Status: "success", Result: b})
		}
	}()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		msgs := request["messages"].([]any)
		parts := msgs[1].(map[string]any)["content"].([]any)
		prompt := parts[0].(map[string]any)["text"].(string)
		marker := "当前 observation_id："
		pos := strings.Index(prompt, marker)
		obs := strings.Split(prompt[pos+len(marker):], "\n")[0]
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		d := domain.Decision{Tool: "finish_task", ObservationID: obs, Reason: "completion candidate"}
		if n == 1 || (mode == "swipe_badge" && n == 2) || mode == "unstable" || mode == "no_progress" || ((mode == "page" || strings.HasSuffix(mode, "_page")) && n == 2) {
			d.Tool = "phone_action"
			d.Action = &domain.Action{Kind: "tap", X: 500, Y: 500}
			if mode == "swipe_badge" {
				d.Action = &domain.Action{Kind: "swipe", X: 800, Y: 400, EndX: 200, EndY: 400}
			}
			if mode == "unlisted_launch" {
				d.Action = &domain.Action{Kind: "launch", Package: "com.xingin.xhs"}
			}
			if mode != "" && !scopeMode {
				d.Action.Y = 200
				if strings.HasPrefix(mode, "button") {
					d.Action.X = 850
				}
			}
			if n == 2 && mode != "unstable" && mode != "no_progress" {
				d.Action.X = 600
			}
		}
		b, _ := json.Marshal(d)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"function": map[string]any{"name": d.Tool, "arguments": string(b)}}}}}}})
	}))
	defer api.Close()
	t.Setenv("MODEL_LOOP_KEY", "test-only")
	_, e = a.Store.SaveProfile(ctx, owner, domain.Profile{ID: spec.ModelProfileID, Name: "fixture API", Provider: "api", Model: "fixture", BaseURL: api.URL, SecretEnv: "MODEL_LOOP_KEY"})
	if e != nil {
		t.Fatal(e)
	}
	if strings.HasPrefix(mode, "model_") {
		peer, _, err := a.Store.Pair(ctx, owner, "model", "fixture connector")
		if err != nil {
			t.Fatal(err)
		}
		modelRPC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			a.Models.Serve(r.Context(), peer.ID, c)
		}))
		defer modelRPC.Close()
		mc, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(modelRPC.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer mc.CloseNow()
		go func() {
			var previous string
			for {
				var f device.Frame
				if wsjson.Read(ctx, mc, &f) != nil {
					return
				}
				var q model.Request
				if json.Unmarshal(f.Params, &q) != nil {
					return
				}
				mu.Lock()
				calls++
				n := calls
				mu.Unlock()
				if q.ObservationID == previous {
					return
				}
				previous = q.ObservationID
				response := device.Frame{ID: f.ID, Status: "success"}
				if n == 1 || mode != "model_retry" {
					code := "model_network_error"
					if mode == "model_auth" {
						code = "model_auth_required"
					}
					response.Status = "error"
					response.Error, _ = json.Marshal(map[string]string{"code": code, "message": "secret-fixture-not-forwarded"})
				} else {
					d := domain.Decision{Tool: "finish_task", ObservationID: q.ObservationID, Reason: "completion candidate"}
					if n == 2 {
						d.Tool = "phone_action"
						d.Action = &domain.Action{Kind: "tap", X: 500, Y: 500}
					}
					response.Result, _ = json.Marshal(model.Result{Decision: d})
				}
				if wsjson.Write(ctx, mc, response) != nil {
					return
				}
			}
		}()
		_, e = a.Store.SaveProfile(ctx, owner, domain.Profile{ID: spec.ModelProfileID, Name: "fixture CLI", Provider: "codex_cli", Model: "fixture", ConnectorID: peer.ID})
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = a.Queue.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer a.Queue.Stop(context.Background())
	task, e := a.Create(ctx, owner, "loop", spec)
	if e != nil {
		t.Fatal(e)
	}
	if cancelOnAction {
		select {
		case <-issued:
		case <-ctx.Done():
			t.Fatal("action not issued")
		}
		if e = a.Pause(ctx, task.ID, "cancel"); e != nil {
			t.Fatal(e)
		}
	}
	for {
		task, e = a.Store.Task(ctx, task.ID)
		if e != nil {
			t.Fatal(e)
		}
		if (!cancelOnAction && task.Status == "waiting_human") || (cancelOnAction && task.Status == "cancelled") {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("task did not finish: %+v", task)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if cancelOnAction {
		unknown, e := a.Store.Uncertain(ctx, spec.DeviceID)
		if e != nil || !unknown {
			t.Fatalf("unknown action missing: %v %v", unknown, e)
		}
		second, e := a.Create(ctx, owner, "next-task", spec)
		if e != nil {
			t.Fatal(e)
		}
		for {
			next, e := a.Store.Task(ctx, second.ID)
			if e != nil {
				t.Fatal(e)
			}
			if next.Status == "reconciling" {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("next task did not require reconciliation")
			case <-time.After(30 * time.Millisecond):
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if actions != 1 || calls != 1 {
			t.Fatalf("replayed uncertain action: calls=%d actions=%d", calls, actions)
		}
		return
	}
	mu.Lock()
	defer mu.Unlock()
	page := mode == "page" || strings.HasSuffix(mode, "_page")
	wantCalls, wantCommands := 2, 1
	if mode == "model_retry" {
		wantCalls, wantCommands = 3, 1
	}
	if mode == "model_exhausted" {
		wantCalls, wantCommands = 3, 0
	}
	if mode == "model_auth" {
		wantCalls, wantCommands = 1, 0
	}
	if mode == "swipe_badge" {
		wantCalls, wantCommands = 3, 2
	}
	if mode == "budget" {
		wantCalls, wantCommands = 1, 1
	}
	if page {
		wantCalls = 3
	}
	if mode == "unstable" {
		wantCalls, wantCommands = 4, 0
	}
	if mode == "no_progress" {
		wantCalls, wantCommands = 6, 5
	}
	if calls != wantCalls || actions != wantCommands || task.Status == "succeeded" {
		t.Fatalf("calls=%d actions=%d status=%s error=%s", calls, actions, task.Status, task.Error)
	}
	if page && tappedX != float64(domain.Pixel(600, 100)) {
		t.Fatalf("stale coordinate dispatched: x=%v", tappedX)
	}
	if mode == "unstable" && !strings.Contains(task.Error, "screen_changed_during_model_call") {
		t.Fatalf("missing exhaustion: %+v", task)
	}
	var commands int
	if err := a.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM device_commands WHERE task_id=$1", task.ID).Scan(&commands); err != nil {
		t.Fatal(err)
	}
	if commands != wantCommands {
		t.Fatalf("commands prepared=%d want=%d", commands, wantCommands)
	}
	events, e := a.Store.Events(ctx, task.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	candidate := false
	budgetPaused := false
	reobserved, exhausted := 0, 0
	for _, v := range events {
		if v.Type == "screen_reobserve" || v.Type == "screen_reobserve_exhausted" {
			var data struct {
				Executed  *bool  `json:"action_executed"`
				Discarded string `json:"discarded_observation_id"`
				Latest    string `json:"latest_observation_id"`
			}
			if json.Unmarshal(v.Data, &data) != nil || data.Executed == nil || *data.Executed || data.Discarded == "" || data.Latest == "" || data.Discarded == data.Latest {
				t.Fatalf("bad rejection event: %s", v.Data)
			}
			if v.Type == "screen_reobserve" {
				reobserved++
			} else {
				exhausted++
			}
		}
		if v.Type == "iteration_limit_reached" {
			budgetPaused = true
		}
		if v.Type == "completion_candidate" {
			candidate = true
		}
	}
	if mode == "unstable" {
		if reobserved != 3 || exhausted != 1 || candidate {
			t.Fatalf("reobserved=%d exhausted=%d candidate=%v", reobserved, exhausted, candidate)
		}
		return
	}
	if strings.HasPrefix(mode, "model_") {
		retries, failed := 0, 0
		for _, v := range events {
			if v.Type == "model_retry" {
				retries++
			}
			if v.Type == "model_decision" {
				var data struct {
					Error string `json:"error"`
				}
				_ = json.Unmarshal(v.Data, &data)
				if data.Error != "" {
					failed++
				}
			}
		}
		wantRetries, wantFailed := 1, 1
		if mode == "model_exhausted" {
			wantRetries, wantFailed = 2, 3
		}
		if mode == "model_auth" {
			wantRetries, wantFailed = 0, 1
		}
		if retries != wantRetries || failed != wantFailed || task.Iterations != calls || strings.Contains(task.Error, "secret-fixture") {
			t.Fatalf("retry audit mismatch retries=%d failed=%d task=%+v", retries, failed, task)
		}
		if mode != "model_retry" {
			code := "model_network_error"
			if mode == "model_auth" {
				code = "model_auth_required"
			}
			if candidate || !strings.HasPrefix(task.Error, code) || strings.Contains(task.Error, "NodeRunError") {
				t.Fatalf("unhelpful error: %s", task.Error)
			}
			return
		}
	}
	if mode == "no_progress" {
		warnings, paused, results := 0, 0, 0
		for _, v := range events {
			switch v.Type {
			case "no_progress_warning":
				warnings++
			case "no_progress_paused":
				paused++
				var data struct {
					Executed bool `json:"action_executed"`
				}
				if json.Unmarshal(v.Data, &data) != nil || data.Executed {
					t.Fatal("paused action marked executed")
				}
			case "action_result":
				results++
				var data struct {
					Before string `json:"observation_id"`
					After  string `json:"after_observation_id"`
				}
				if json.Unmarshal(v.Data, &data) != nil || data.Before == "" || data.After == "" || data.Before == data.After {
					t.Fatal("replay evidence missing")
				}
			}
		}
		if warnings != 3 || paused != 1 || results != 5 || candidate || !strings.Contains(task.Error, "phone_no_progress") {
			t.Fatalf("warnings=%d paused=%d results=%d candidate=%v error=%s", warnings, paused, results, candidate, task.Error)
		}
		return
	}
	if page && (reobserved != 1 || exhausted != 0) {
		t.Fatalf("reobserved=%d exhausted=%d", reobserved, exhausted)
	}
	if mode == "budget" {
		if !budgetPaused || candidate || !strings.Contains(task.Error, "已达到 1 轮") {
			t.Fatalf("bad budget pause: %v %+v", budgetPaused, task)
		}
		return
	}
	if mode == "swipe_badge" && reobserved != 0 {
		t.Fatal("transient carousel badge caused a new model decision")
	}
	if !candidate {
		t.Fatal("missing completion evidence")
	}
}

func testApp(t *testing.T) (*Service, string, domain.TaskSpec) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set: PostgreSQL integration not verified")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	name := "mobile_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	u.Path = "/" + name
	s, e := store.New(ctx, u.String(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		s.Pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		admin.Close()
	})
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.Bootstrap(ctx, "test", "test-password-long"); e != nil {
		t.Fatal(e)
	}
	user, _, e := s.Login(ctx, "test", "test-password-long")
	if e != nil {
		t.Fatal(e)
	}
	device, _, e := s.Pair(ctx, user.ID, "device", "test phone")
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.SaveProfile(ctx, user.ID, domain.Profile{Name: "test", Provider: "api", Model: "test", BaseURL: "http://127.0.0.1:12345/v1", SecretEnv: "MODEL_TEST_KEY"})
	if e != nil {
		t.Fatal(e)
	}
	a, e := New(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	future := time.Now().Add(time.Hour)
	return a, user.ID, domain.TaskSpec{DeviceID: device.ID, ModelProfileID: p.ID, Template: "general", Goal: "open settings", Packages: []string{"com.android.settings"}, ScheduledAt: &future}
}
func TestPostgresRiverAtomicAndIdempotent(t *testing.T) {
	a, owner, spec := testApp(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); task, e := a.Create(ctx, owner, "same-key", spec); ids <- task.ID; errs <- e }()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	for v := range ids {
		if id != "" && v != id {
			t.Fatal("duplicate task")
		}
		id = v
	}
	var count int
	if e := a.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE args->>'task_id'=$1", id).Scan(&count); e != nil || count != 1 {
		t.Fatalf("queue count=%d err=%v", count, e)
	}
	spec.Goal = "different"
	if _, e := a.Create(ctx, owner, "same-key", spec); e == nil {
		t.Fatal("accepted conflicting idempotency key")
	}
	tx, e := a.Store.Pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	rollbackID := uuid.NewString()
	b, _ := json.Marshal(spec)
	_, e = tx.Exec(ctx, "INSERT INTO tasks(id,owner,idempotency_key,request_hash,spec,status) VALUES($1,$2,'rollback','hash',$3,'queued')", rollbackID, owner, b)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Queue.InsertTx(ctx, tx, TaskArgs{rollbackID}, nil); e != nil {
		t.Fatal(e)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM tasks WHERE id=$1)+(SELECT count(*) FROM river_job WHERE args->>'task_id'=$1)", rollbackID).Scan(&count); e != nil || count != 0 {
		t.Fatalf("rollback count=%d err=%v", count, e)
	}
}
func TestPostgresLeaseFencingAndRecovery(t *testing.T) {
	a, owner, spec := testApp(t)
	ctx := context.Background()
	g, e := a.Store.Acquire(ctx, spec.DeviceID, "first")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.Acquire(ctx, spec.DeviceID, "second"); e != domain.ErrBusy {
		t.Fatalf("expected busy, got %v", e)
	}
	_, e = a.Store.Pool.Exec(ctx, "UPDATE device_leases SET expires_at=now()-interval '1 second' WHERE device_id=$1", spec.DeviceID)
	if e != nil {
		t.Fatal(e)
	}
	g2, e := a.Store.Acquire(ctx, spec.DeviceID, "second")
	if e != nil || g2 <= g {
		t.Fatalf("new lease %d %v", g2, e)
	}
	if e = a.Store.Renew(ctx, spec.DeviceID, "first", g); e == nil {
		t.Fatal("stale holder renewed")
	}
	if e = a.Store.Release(ctx, spec.DeviceID, "first", g); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Renew(ctx, spec.DeviceID, "second", g2); e != nil {
		t.Fatal("old release invalidated current lease", e)
	}
	task, e := a.Create(ctx, owner, "recovery", spec)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.State(ctx, task.ID, "running", ""); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	task, e = a.Store.Task(ctx, task.ID)
	if e != nil || task.Status != "reconciling" {
		t.Fatal("unsafe restart recovery", e)
	}
}

func TestResumeExtendsBudgetAtomicallyWithoutResettingProgress(t *testing.T) {
	a, owner, spec := testApp(t)
	ctx := context.Background()
	spec.MaxIterations = 2
	task, err := a.Create(ctx, owner, "budget-resume", spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.Pool.Exec(ctx, "UPDATE tasks SET status='waiting_human',iterations=2,error='old limit' WHERE id=$1", task.ID); err != nil {
		t.Fatal(err)
	}
	before, err := a.Store.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []int{0, -1, domain.MaxTaskIterations} {
		err = a.ResumeWithBudget(ctx, task.ID, false, extra)
		if err == nil {
			t.Fatalf("accepted invalid extension %d", extra)
		}
		if extra == 0 && !errors.Is(err, domain.ErrIterationLimit) {
			t.Fatal(err)
		}
	}
	if err = a.Store.Event(ctx, task.ID, "model_decision", map[string]any{"decision": domain.Decision{Tool: "phone_action", ObservationID: "discarded", Reason: "continue from picture 12", Action: &domain.Action{Kind: "swipe"}}}); err != nil {
		t.Fatal(err)
	}
	history, err := a.Store.RecentDecisionHistory(ctx, task.ID)
	if err != nil || len(history) != 1 || !strings.Contains(history[0], "continue from picture 12") || !strings.Contains(history[0], `"action_executed": false`) {
		t.Fatal("resume history did not retain the undispatched decision", history, err)
	}
	paused, _ := a.Store.Task(ctx, task.ID)
	if paused.Status != "waiting_human" || paused.Spec.MaxIterations != 2 || paused.Iterations != 2 {
		t.Fatal("invalid resume modified task")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- a.ResumeWithBudget(ctx, task.ID, false, 3) }()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	after, err := a.Store.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if success != 1 || after.Status != "queued" || after.Iterations != 2 || after.Spec.MaxIterations != 5 || after.Spec.Goal != before.Spec.Goal || after.Spec.ModelSnapshot.ID != before.Spec.ModelSnapshot.ID {
		t.Fatalf("success=%d after=%+v", success, after)
	}
	var n int
	if err = a.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM task_events WHERE task_id=$1 AND type='iteration_budget_extended'", task.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal("budget audit missing", n, err)
	}
	if _, err = a.Store.Pool.Exec(ctx, "UPDATE tasks SET status='running' WHERE id=$1", task.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err = a.Store.Iterations(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.Store.Iterations(ctx, task.ID); !errors.Is(err, domain.ErrIterationLimit) {
		t.Fatal("database budget overrun", err)
	}
	final, _ := a.Store.Task(ctx, task.ID)
	if final.Iterations != 5 {
		t.Fatal("iteration history reset or overrun")
	}
}
