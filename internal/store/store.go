package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/store/db"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	Pool    *pgxpool.Pool
	DataDir string
}
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}
type Peer struct {
	ID                  string          `json:"id"`
	Owner               string          `json:"owner"`
	Kind                string          `json:"kind"`
	Name                string          `json:"name"`
	Metadata            json.RawMessage `json:"metadata"`
	Online              bool            `json:"online"`
	MediaOnline         bool            `json:"media_online,omitempty"`
	ConnectionTransport string          `json:"connection_transport,omitempty"`
}

func New(ctx context.Context, url, dir string) (*Store, error) {
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		p.Close()
		return nil, e
	}
	return &Store{p, dir}, nil
}
func (s *Store) Migrate(ctx context.Context) error {
	files, e := migrations.ReadDir("migrations")
	if e != nil {
		return e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(673187021)"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"); e != nil {
		return e
	}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		var done bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)", file.Name()).Scan(&done); e != nil {
			return e
		}
		if done {
			continue
		}
		b, err := migrations.ReadFile("migrations/" + file.Name())
		if err != nil {
			return err
		}
		if _, e = tx.Exec(ctx, string(b)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO schema_migrations(name) VALUES($1)", file.Name()); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func Hash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func Token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func (s *Store) Bootstrap(ctx context.Context, user, password string) error {
	if len(password) < 12 {
		return errors.New("admin_password_min_12_chars")
	}
	h, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if e != nil {
		return e
	}
	_, e = s.Pool.Exec(ctx, "INSERT INTO users VALUES($1,$2,$3,'admin') ON CONFLICT(username) DO NOTHING", uuid.NewString(), user, string(h))
	return e
}
func (s *Store) Login(ctx context.Context, name, password string) (User, string, error) {
	var u User
	var h string
	e := s.Pool.QueryRow(ctx, "SELECT id,username,role,password_hash FROM users WHERE username=$1", name).Scan(&u.ID, &u.Username, &u.Role, &h)
	if e != nil || bcrypt.CompareHashAndPassword([]byte(h), []byte(password)) != nil {
		return u, "", errors.New("invalid_credentials")
	}
	t := Token()
	_, e = s.Pool.Exec(ctx, "INSERT INTO sessions VALUES($1,$2,now()+interval '12 hours')", Hash(t), u.ID)
	return u, t, e
}
func (s *Store) Auth(ctx context.Context, token string) (User, error) {
	var u User
	e := s.Pool.QueryRow(ctx, "SELECT u.id,u.username,u.role FROM users u JOIN sessions s ON s.user_id=u.id WHERE s.token_hash=$1 AND s.expires_at>now()", Hash(token)).Scan(&u.ID, &u.Username, &u.Role)
	return u, e
}
func (s *Store) Peers(ctx context.Context, owner, kind string) ([]Peer, error) {
	rows, e := s.Pool.Query(ctx, "SELECT id,owner,kind,name,metadata FROM peers WHERE owner=$1 AND kind=$2 ORDER BY name", owner, kind)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Peer{}
	for rows.Next() {
		var p Peer
		if e = rows.Scan(&p.ID, &p.Owner, &p.Kind, &p.Name, &p.Metadata); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) OwnPeer(ctx context.Context, owner, id, kind string) error {
	var ok bool
	return s.Pool.QueryRow(ctx, "SELECT true FROM peers WHERE id=$1 AND owner=$2 AND kind=$3", id, owner, kind).Scan(&ok)
}
func (s *Store) Pair(ctx context.Context, owner, kind, name string) (Peer, string, error) {
	p := Peer{ID: uuid.NewString(), Owner: owner, Kind: kind, Name: name}
	t := Token()
	_, e := s.Pool.Exec(ctx, "INSERT INTO peers(id,owner,kind,name,token_hash,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '15 minutes')", p.ID, owner, kind, name, Hash(t))
	return p, t, e
}
func (s *Store) PeerAuth(ctx context.Context, token, kind string) (Peer, error) {
	var p Peer
	e := s.Pool.QueryRow(ctx, "UPDATE peers SET expires_at=now()+interval '30 days' WHERE token_hash=$1 AND kind=$2 AND expires_at>now() RETURNING id,owner,kind,name,metadata", Hash(token), kind).Scan(&p.ID, &p.Owner, &p.Kind, &p.Name, &p.Metadata)
	return p, e
}
func (s *Store) PeerMeta(ctx context.Context, id string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = s.Pool.Exec(ctx, "UPDATE peers SET metadata=$2 WHERE id=$1", id, b)
	return e
}
func (s *Store) Profiles(ctx context.Context, owner string) ([]domain.Profile, error) {
	rows, e := s.Pool.Query(ctx, "SELECT config FROM model_profiles WHERE owner=$1 ORDER BY id", owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Profile{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var p domain.Profile
		if e = json.Unmarshal(b, &p); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) Profile(ctx context.Context, owner, id string) (domain.Profile, error) {
	var p domain.Profile
	var b []byte
	e := s.Pool.QueryRow(ctx, "SELECT config FROM model_profiles WHERE id=$1 AND owner=$2", id, owner).Scan(&b)
	if e == nil {
		e = json.Unmarshal(b, &p)
	}
	return p, e
}
func (s *Store) SaveProfile(ctx context.Context, owner string, p domain.Profile) (domain.Profile, error) {
	if p.ImageMaxWidth != 0 && p.ImageMaxWidth != -1 && (p.ImageMaxWidth < 320 || p.ImageMaxWidth > 2560) {
		return p, errors.New("invalid_model_image_width")
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	switch p.Provider {
	case "api":
		if !regexp.MustCompile(`^MODEL_[A-Z0-9_]+$`).MatchString(p.SecretEnv) {
			return p, errors.New("model_secret_env_required")
		}
		if p.Model == "" || p.BaseURL == "" || p.SecretEnv == "" {
			return p, errors.New("api_config_required")
		}
	case "codex_cli", "workbuddy_cli":
		if e := s.OwnPeer(ctx, owner, p.ConnectorID, "model"); e != nil {
			return p, errors.New("connector_not_owned")
		}
	default:
		return p, errors.New("invalid_provider")
	}
	b, e := json.Marshal(p)
	if e != nil {
		return p, e
	}
	tag, e := s.Pool.Exec(ctx, "INSERT INTO model_profiles(id,owner,config) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET config=excluded.config WHERE model_profiles.owner=excluded.owner", p.ID, owner, b)
	if e == nil && tag.RowsAffected() != 1 {
		e = errors.New("profile_not_owned")
	}
	return p, e
}

type Asset struct {
	Name  string `json:"name"`
	ID    string `json:"id"`
	MIME  string `json:"mime"`
	Bytes int64  `json:"bytes"`
	Path  string `json:"-"`
	SHA   string `json:"sha256"`
}

func (s *Store) SaveAsset(ctx context.Context, owner, mime string, data []byte) (Asset, error) {
	return s.SaveNamedAsset(ctx, owner, "", mime, data)
}
func (s *Store) SaveNamedAsset(ctx context.Context, owner, name, mime string, data []byte) (Asset, error) {
	a := Asset{ID: uuid.NewString(), Name: name, MIME: mime, Bytes: int64(len(data)), SHA: Hash(string(data))}
	a.Path = filepath.Join(s.DataDir, a.ID)
	if e := os.WriteFile(a.Path, data, 0600); e != nil {
		return a, e
	}
	_, e := s.Pool.Exec(ctx, "INSERT INTO assets(id,owner,mime,bytes,sha256,path,name) VALUES($1,$2,$3,$4,$5,$6,$7)", a.ID, owner, a.MIME, a.Bytes, a.SHA, a.Path, a.Name)
	if e != nil {
		_ = os.Remove(a.Path)
	}
	return a, e
}
func (s *Store) Asset(ctx context.Context, owner, id string) (Asset, []byte, error) {
	var a Asset
	e := s.Pool.QueryRow(ctx, "SELECT id,mime,bytes,path,sha256,name FROM assets WHERE id=$1 AND owner=$2", id, owner).Scan(&a.ID, &a.MIME, &a.Bytes, &a.Path, &a.SHA, &a.Name)
	if e != nil {
		return a, nil, e
	}
	b, e := os.ReadFile(a.Path)
	return a, b, e
}
func (s *Store) Assets(ctx context.Context, owner string) ([]Asset, error) {
	rows, e := s.Pool.Query(ctx, "SELECT id,mime,bytes,sha256,name FROM assets WHERE owner=$1 ORDER BY created_at DESC LIMIT 100", owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Asset{}
	for rows.Next() {
		var a Asset
		if e = rows.Scan(&a.ID, &a.MIME, &a.Bytes, &a.SHA, &a.Name); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) SaveObservation(ctx context.Context, owner, task string, o *domain.Observation) error {
	a, e := s.SaveAsset(ctx, owner, "image/png", o.Image)
	if e != nil {
		return e
	}
	o.AssetID = a.ID
	if len(o.State) == 0 {
		o.State = json.RawMessage("{}")
	}
	var tid any
	if task != "" {
		tid = task
	}
	_, e = s.Pool.Exec(ctx, "INSERT INTO observations VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)", o.ID, o.DeviceID, tid, owner, o.AssetID, o.Width, o.Height, o.State, o.CapturedAt)
	return e
}
func (s *Store) Task(ctx context.Context, id string) (domain.Task, error) {
	var t domain.Task
	var b []byte
	e := s.Pool.QueryRow(ctx, "SELECT id,owner,status,spec,error,iterations,created_at,archived_at FROM tasks WHERE id=$1", id).Scan(&t.ID, &t.Owner, &t.Status, &b, &t.Error, &t.Iterations, &t.CreatedAt, &t.ArchivedAt)
	if e == nil {
		e = json.Unmarshal(b, &t.Spec)
	}
	return t, e
}
func (s *Store) Tasks(ctx context.Context, owner string) ([]domain.Task, error) {
	return s.TasksPage(ctx, owner, false, "")
}
func (s *Store) TasksPage(ctx context.Context, owner string, archived bool, before string) ([]domain.Task, error) {
	rows, e := s.Pool.Query(ctx, `SELECT id,owner,status,spec,error,iterations,created_at,archived_at FROM tasks
	 WHERE owner=$1 AND (archived_at IS NOT NULL)=$2
	 AND ($3::text='' OR (created_at,id)<(SELECT created_at,id FROM tasks WHERE id=$3 AND owner=$1))
	 ORDER BY created_at DESC,id DESC LIMIT 100`, owner, archived, before)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Task{}
	for rows.Next() {
		var t domain.Task
		var b []byte
		if e = rows.Scan(&t.ID, &t.Owner, &t.Status, &b, &t.Error, &t.Iterations, &t.CreatedAt, &t.ArchivedAt); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &t.Spec); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) Event(ctx context.Context, id, kind string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = s.Pool.Exec(ctx, "INSERT INTO task_events(task_id,type,data) VALUES($1,$2,$3)", id, kind, b)
	return e
}
func (s *Store) Events(ctx context.Context, id string, after int64) ([]domain.Event, error) {
	rows, e := s.Pool.Query(ctx, "SELECT id,type,data,created_at FROM task_events WHERE task_id=$1 AND id>$2 ORDER BY id LIMIT 100", id, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Event{}
	for rows.Next() {
		var v domain.Event
		if e = rows.Scan(&v.ID, &v.Type, &v.Data, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) State(ctx context.Context, id, status, reason string) error {
	allowed := []string{"queued", "waiting_device", "waiting_model", "running", "waiting_human", "reconciling", "cancel_requested"}
	switch status {
	case "running", "waiting_device", "waiting_model":
		allowed = []string{"queued", "waiting_device", "waiting_model"}
	case "waiting_human":
		allowed = []string{"queued", "waiting_device", "waiting_model", "running", "waiting_human", "reconciling"}
	case "succeeded":
		allowed = []string{"waiting_human"}
	case "cancelled":
		allowed = []string{"cancel_requested"}
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, "UPDATE tasks SET status=$2,error=$3,updated_at=now() WHERE id=$1 AND status=ANY($4)", id, status, reason, allowed)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return errors.New("state_conflict")
	}
	b, _ := json.Marshal(map[string]string{"status": status, "reason": reason})
	if _, e = tx.Exec(ctx, "INSERT INTO task_events(task_id,type,data) VALUES($1,'status',$2)", id, b); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) Iterations(ctx context.Context, id string) error {
	r, e := s.Pool.Exec(ctx, "UPDATE tasks SET iterations=iterations+1 WHERE id=$1 AND iterations<(spec->>'max_iterations')::integer", id)
	if e == nil && r.RowsAffected() != 1 {
		return domain.ErrIterationLimit
	}
	return e
}

// Keep resume context tied to the command ledger. A discarded decision is not
// evidence that the phone acted; an uncertain reply remains explicitly unknown.
func (s *Store) RecentDecisionHistory(ctx context.Context, id string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT jsonb_build_object('decision',e.data->'decision','command_status',coalesce(c.status,'not_dispatched'),'action_executed',CASE WHEN c.status='reported' THEN true WHEN c.status IS NULL THEN false ELSE NULL END)::text
FROM task_events e LEFT JOIN LATERAL (SELECT status FROM device_commands WHERE task_id=e.task_id AND observation_id=e.data->'decision'->>'observation_id' ORDER BY created_at DESC LIMIT 1) c ON true
WHERE e.task_id=$1 AND e.type IN ('model_decision','rule_decision') AND e.data->'decision' IS NOT NULL AND coalesce(e.data->>'error','')='' ORDER BY e.id DESC LIMIT 12`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var history []string
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			return nil, err
		}
		history = append(history, line)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}
	return history, nil
}
func (s *Store) Acquire(ctx context.Context, device, holder string) (int64, error) {
	g, e := db.New(s.Pool).AcquireDeviceLease(ctx, db.AcquireDeviceLeaseParams{DeviceID: device, Holder: holder})
	if errors.Is(e, pgx.ErrNoRows) {
		return 0, domain.ErrBusy
	}
	return g, e
}
func (s *Store) Renew(ctx context.Context, device, holder string, g int64) error {
	n, e := db.New(s.Pool).RenewDeviceLease(ctx, db.RenewDeviceLeaseParams{DeviceID: device, Holder: holder, Generation: g})
	if e == nil && n != 1 {
		e = errors.New("lease_lost")
	}
	return e
}
func (s *Store) Release(ctx context.Context, device, holder string, g int64) error {
	return db.New(s.Pool).ReleaseDeviceLease(ctx, db.ReleaseDeviceLeaseParams{DeviceID: device, Holder: holder, Generation: g})
}
func (s *Store) Uncertain(ctx context.Context, device string) (bool, error) {
	var yes bool
	e := s.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM device_commands WHERE device_id=$1 AND status IN ('dispatched','uncertain'))", device).Scan(&yes)
	return yes, e
}
func (s *Store) Prepare(ctx context.Context, task, device, holder, obs string, g int64, a domain.Action) (string, error) {
	id := uuid.NewString()
	b, e := json.Marshal(a)
	if e != nil {
		return "", e
	}
	var tid any
	if task != "" {
		tid = task
	}
	r, e := s.Pool.Exec(ctx, "INSERT INTO device_commands(id,task_id,device_id,holder,generation,observation_id,action,status) SELECT $1,$2,$3,$4,$5,$6,$7,'dispatched' WHERE EXISTS(SELECT 1 FROM device_leases WHERE device_id=$3 AND holder=$4 AND generation=$5 AND expires_at>now()) AND ($2::text IS NULL OR EXISTS(SELECT 1 FROM tasks WHERE id=$2 AND status='running'))", id, tid, device, holder, g, obs, b)
	if e == nil && r.RowsAffected() != 1 {
		e = errors.New("lease_lost")
	}
	return id, e
}
func (s *Store) CommandResult(ctx context.Context, id, status, reason string) error {
	_, e := s.Pool.Exec(ctx, "UPDATE device_commands SET status=$2,error=$3 WHERE id=$1", id, status, reason)
	return e
}
func (s *Store) Reconcile(ctx context.Context, device string) error {
	_, e := s.Pool.Exec(ctx, "UPDATE device_commands SET status='reconciled' WHERE device_id=$1 AND status IN ('dispatched','uncertain')", device)
	return e
}
func (s *Store) Get(ctx context.Context, id string) ([]byte, bool, error) {
	var b []byte
	e := s.Pool.QueryRow(ctx, "SELECT data FROM agent_checkpoints WHERE id=$1", id).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return b, e == nil, e
}
func (s *Store) Set(ctx context.Context, id string, b []byte) error {
	_, e := s.Pool.Exec(ctx, "INSERT INTO agent_checkpoints VALUES($1,$2,now()) ON CONFLICT(id) DO UPDATE SET data=excluded.data,updated_at=now()", id, b)
	return e
}
func (s *Store) Recover(ctx context.Context) error {
	if _, e := s.Pool.Exec(ctx, "UPDATE material_workflows SET status='failed',error='server_restarted_during_generation' WHERE status='generating'"); e != nil {
		return e
	}
	_, e := s.Pool.Exec(ctx, "UPDATE tasks SET status='reconciling',error='服务重启：先核对在途操作，再继续' WHERE status IN ('running','cancel_requested')")
	return e
}
func (s *Store) RecordModel(ctx context.Context, id, task, profile, obs string, d time.Duration, usage json.RawMessage, reason string) error {
	status := "completed"
	if reason != "" {
		status = "failed"
	}
	if _, e := s.Pool.Exec(ctx, "UPDATE model_requests SET status=$2 WHERE id=$1", id, status); e != nil {
		return e
	}
	var u any
	if len(usage) > 0 {
		u = usage
	}
	_, e := s.Pool.Exec(ctx, "INSERT INTO model_calls(id,task_id,profile_id,observation_id,duration_ms,usage,error) VALUES($1,$2,$3,$4,$5,$6,$7)", id, task, profile, obs, d.Milliseconds(), u, reason)
	return e
}
func (s *Store) CheckPeerOwner(ctx context.Context, owner, id string) error {
	var v string
	e := s.Pool.QueryRow(ctx, "SELECT owner FROM peers WHERE id=$1", id).Scan(&v)
	if e == nil && v != owner {
		return fmt.Errorf("not_owned")
	}
	return e
}

func (s *Store) Verify(ctx context.Context, t domain.Task, observationID, note string, reconciled bool) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var status string
	if e = tx.QueryRow(ctx, "SELECT status FROM tasks WHERE id=$1 FOR UPDATE", t.ID).Scan(&status); e != nil {
		return e
	}
	if status != "waiting_human" {
		return errors.New("state_conflict")
	}
	var evidence bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM observations WHERE id=$1 AND task_id=$2 AND owner=$3 AND captured_at=(SELECT max(captured_at) FROM observations WHERE task_id=$2))", observationID, t.ID, t.Owner).Scan(&evidence); e != nil {
		return e
	}
	if !evidence {
		return errors.New("task_evidence_required")
	}
	var unknown bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM device_commands WHERE device_id=$1 AND status IN ('dispatched','uncertain'))", t.Spec.DeviceID).Scan(&unknown); e != nil {
		return e
	}
	if unknown && !reconciled {
		return errors.New("reconciliation_confirmation_required")
	}
	if unknown {
		if _, e = tx.Exec(ctx, "UPDATE device_commands SET status='reconciled' WHERE device_id=$1 AND status IN ('dispatched','uncertain')", t.Spec.DeviceID); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, "UPDATE tasks SET status='succeeded',error='',updated_at=now() WHERE id=$1", t.ID); e != nil {
		return e
	}
	b, _ := json.Marshal(map[string]any{"observation_id": observationID, "note": note, "reconciled": reconciled})
	if _, e = tx.Exec(ctx, "INSERT INTO task_events(task_id,type,data) VALUES($1,'human_verified',$2)", t.ID, b); e != nil {
		return e
	}
	b, _ = json.Marshal(map[string]string{"status": "succeeded", "reason": note})
	if _, e = tx.Exec(ctx, "INSERT INTO task_events(task_id,type,data) VALUES($1,'status',$2)", t.ID, b); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
