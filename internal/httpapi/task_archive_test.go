package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"mobile_autorun/internal/app"
	"mobile_autorun/internal/domain"
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

func TestTaskArchiveInputsAndOwnerIsolation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set: archive integration not verified")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "mobile_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	s, err := store.New(ctx, u.String(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		admin.Close()
	})
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Bootstrap(ctx, "first", "test-password-long"); err != nil {
		t.Fatal(err)
	}
	if err = s.Bootstrap(ctx, "second", "test-password-long"); err != nil {
		t.Fatal(err)
	}
	owner, _, err := s.Login(ctx, "first", "test-password-long")
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := s.Pair(ctx, owner.ID, "device", "test phone")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.SaveProfile(ctx, owner.ID, domain.Profile{Name: "snapshot", Provider: "api", Model: "fixture", BaseURL: "http://localhost/v1", SecretEnv: "MODEL_TEST_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := s.SaveNamedAsset(ctx, owner.ID, "original.txt", "text/plain", []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	// Push the attachment beyond the material library's newest-100 window.
	if _, err = s.Pool.Exec(ctx, `INSERT INTO assets(id,owner,mime,bytes,sha256,path,name,created_at) SELECT 'recent-'||n,$1,'text/plain',1,'sha','unused','recent',now()+interval '1 minute' FROM generate_series(1,101) n`, owner.ID); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	spec := domain.TaskSpec{DeviceID: d.ID, ModelProfileID: p.ID, Goal: strings.Repeat("完整任务输入\n", 300), Template: "general", Packages: []string{"com.android.settings"}, AssetIDs: []string{asset.ID}, ScheduledAt: &future}
	task, err := a.Create(ctx, owner.ID, "archive-fixture", spec)
	if err != nil {
		t.Fatal(err)
	}
	routes := (&Server{Service: a}).Routes()
	call := func(method, path string, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		r := httptest.NewRecorder()
		routes.ServeHTTP(r, req)
		return r
	}
	login := func(user string) []*http.Cookie {
		r := call("POST", "/api/v1/auth/login", map[string]string{"username": user, "password": "test-password-long"}, nil)
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		return r.Result().Cookies()
	}
	first, second := login("first"), login("second")
	path := "/api/v1/tasks/" + task.ID
	for _, endpoint := range []string{"/inputs", "/archive", "/restore", "/resume"} {
		method := "POST"
		if endpoint == "/inputs" {
			method = "GET"
		}
		r := call(method, path+endpoint, map[string]any{}, second)
		if r.Code != 404 {
			t.Fatalf("owner isolation %s: %d", endpoint, r.Code)
		}
	}
	if r := call("POST", path+"/archive", map[string]any{}, first); r.Code != 409 {
		t.Fatal("queued task archived", r.Code)
	}
	for _, status := range []string{"waiting_device", "waiting_model", "running", "cancel_requested"} {
		if _, err = s.Pool.Exec(ctx, "UPDATE tasks SET status=$2 WHERE id=$1", task.ID, status); err != nil {
			t.Fatal(err)
		}
		if r := call("POST", path+"/archive", map[string]any{}, first); r.Code != 409 {
			t.Fatal(status, r.Code)
		}
	}
	if _, err = s.Pool.Exec(ctx, "UPDATE tasks SET status='waiting_human',iterations=4,error='original error' WHERE id=$1", task.ID); err != nil {
		t.Fatal(err)
	}
	before, err := s.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.ArchiveTask(ctx, owner.ID, task.ID, true); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	archived, err := s.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(before.Spec)
	b2, _ := json.Marshal(archived.Spec)
	if archived.ArchivedAt == nil || archived.Status != before.Status || archived.Iterations != 4 || archived.Error != before.Error || !bytes.Equal(b1, b2) {
		t.Fatal("archive changed execution/input state")
	}
	if r := call("POST", path+"/resume", map[string]bool{"reconciled": false}, first); r.Code != 409 || !strings.Contains(r.Body.String(), "task_archived_restore_first") {
		t.Fatal("archived resume accepted", r.Code)
	}
	if err = a.Run(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM task_events WHERE task_id=$1 AND type='task_archived'", task.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("archive not idempotent", count, err)
	}
	active, err := s.Tasks(ctx, owner.ID)
	if err != nil || len(active) != 0 {
		t.Fatal("archived still in default list")
	}
	list := call("GET", "/api/v1/tasks?archived=true", nil, first)
	var tasks []domain.Task
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &tasks) != nil || len(tasks) != 1 {
		t.Fatal("archive list", list.Code)
	}
	r := call("GET", path+"/inputs", nil, first)
	var inputs struct {
		Spec   domain.TaskSpec `json:"spec"`
		Assets []store.Asset   `json:"assets"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &inputs) != nil || inputs.Spec.Goal != spec.Goal || inputs.Spec.ModelSnapshot.Name != "snapshot" || len(inputs.Assets) != 1 || inputs.Assets[0].Name != "original.txt" {
		t.Fatal("complete inputs or old attachment missing", r.Code)
	}
	if r := call("POST", path+"/restore", map[string]any{}, first); r.Code != 200 {
		t.Fatal(r.Code)
	}
	restored, err := s.Task(ctx, task.ID)
	if err != nil || restored.ArchivedAt != nil || restored.Status != before.Status || restored.Error != before.Error {
		t.Fatal("restore altered execution")
	}
	if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE args->>'task_id'=$1", task.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("restore enqueued new work", count, err)
	}
	// Same timestamps must paginate stably by ID, without dropping older archives.
	for i := 0; i < 101; i++ {
		id := fmt.Sprintf("page-%03d", i)
		if _, err = s.Pool.Exec(ctx, "INSERT INTO tasks(id,owner,idempotency_key,request_hash,spec,status,archived_at,created_at) VALUES($1,$2,$1,'hash',$3,'cancelled',now(),$4)", id, owner.ID, b1, future); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.TasksPage(ctx, owner.ID, true, "")
	if err != nil || len(page) != 100 {
		t.Fatal("archive page", err, len(page))
	}
	next, err := s.TasksPage(ctx, owner.ID, true, page[99].ID)
	if err != nil || len(next) != 1 || next[0].ID == page[99].ID {
		t.Fatal("archive cursor", err, len(next))
	}
}
