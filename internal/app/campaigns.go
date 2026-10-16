package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"mobile_autorun/internal/domain"
	"time"
)

type Campaign struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Spec            domain.TaskSpec `json:"spec"`
	IntervalMinutes int             `json:"interval_minutes"`
	Enabled         bool            `json:"enabled"`
	NextRun         time.Time       `json:"next_run"`
	LastTaskID      string          `json:"last_task_id"`
	Error           string          `json:"error"`
}

func (a *Service) Campaigns(ctx context.Context, owner string) ([]Campaign, error) {
	rows, e := a.Store.Pool.Query(ctx, "SELECT id,name,spec,interval_minutes,enabled,next_run,COALESCE(last_task_id,''),error FROM business_campaigns WHERE owner=$1 ORDER BY created_at DESC LIMIT 100", owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Campaign{}
	for rows.Next() {
		var c Campaign
		var raw []byte
		if e = rows.Scan(&c.ID, &c.Name, &raw, &c.IntervalMinutes, &c.Enabled, &c.NextRun, &c.LastTaskID, &c.Error); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &c.Spec); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (a *Service) CreateCampaign(ctx context.Context, owner string, c Campaign) (Campaign, error) {
	if c.Name == "" || len(c.Name) > 200 || c.IntervalMinutes < 15 || c.IntervalMinutes > 10080 {
		return c, errors.New("invalid_campaign")
	}
	if c.Spec.Template != "wechat_crm" && c.Spec.Template != "xhs_growth" {
		return c, errors.New("business_template_required")
	}
	c.Spec.ModelSnapshot = nil
	c.Spec.ScheduledAt = nil
	if e := c.Spec.Validate(); e != nil {
		return c, e
	}
	if e := a.Store.OwnPeer(ctx, owner, c.Spec.DeviceID, "device"); e != nil {
		return c, errors.New("device_not_owned")
	}
	if _, e := a.Store.Profile(ctx, owner, c.Spec.ModelProfileID); e != nil {
		return c, errors.New("model_not_owned")
	}
	for _, id := range c.Spec.AssetIDs {
		if _, _, e := a.Store.Asset(ctx, owner, id); e != nil {
			return c, errors.New("asset_not_owned")
		}
	}
	c.ID = uuid.NewString()
	c.Enabled = true
	c.NextRun = time.Now()
	raw, _ := json.Marshal(c.Spec)
	_, e := a.Store.Pool.Exec(ctx, "INSERT INTO business_campaigns(id,owner,name,spec,interval_minutes,next_run) VALUES($1,$2,$3,$4,$5,$6)", c.ID, owner, c.Name, raw, c.IntervalMinutes, c.NextRun)
	return c, e
}
func (a *Service) SetCampaign(ctx context.Context, owner, id string, enabled bool) error {
	tag, e := a.Store.Pool.Exec(ctx, "UPDATE business_campaigns SET enabled=$3 WHERE id=$1 AND owner=$2", id, owner, enabled)
	if e == nil && tag.RowsAffected() != 1 {
		return errors.New("campaign_not_found")
	}
	return e
}

// Single-server scheduler. Transactional row locks and task idempotency also
// prevent a crash between enqueueing and advancing the schedule from duplicating a run.
func (a *Service) TickCampaigns(ctx context.Context) error {
	rows, e := a.Store.Pool.Query(ctx, "SELECT id FROM business_campaigns WHERE enabled AND next_run<=now() ORDER BY next_run LIMIT 50")
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = a.tickCampaign(ctx, id); e != nil {
			slog.Warn("campaign scheduling failed", "campaign", id, "error", e)
		}
	}
	return nil
}
func (a *Service) tickCampaign(ctx context.Context, id string) error {
	tx, e := a.Store.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var owner, last string
	var raw []byte
	var next time.Time
	var minutes int
	e = tx.QueryRow(ctx, `SELECT owner,spec,next_run,interval_minutes,COALESCE(last_task_id,'') FROM business_campaigns WHERE id=$1 AND enabled AND next_run<=now() FOR UPDATE SKIP LOCKED`, id).Scan(&owner, &raw, &next, &minutes, &last)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if last != "" {
		var status string
		if e = tx.QueryRow(ctx, "SELECT status FROM tasks WHERE id=$1", last).Scan(&status); e != nil {
			return e
		}
		if status != "succeeded" && status != "cancelled" && status != "failed" {
			return nil
		}
	}
	var childStatus string
	e = tx.QueryRow(ctx, "SELECT t.status FROM business_progress b JOIN tasks t ON t.id=b.state->>'draft_task_id' WHERE b.task_id=$1", last).Scan(&childStatus)
	if e == nil && childStatus != "succeeded" && childStatus != "cancelled" && childStatus != "failed" {
		return nil
	}
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	var spec domain.TaskSpec
	if e = json.Unmarshal(raw, &spec); e != nil {
		return e
	}
	task, e := a.Create(ctx, owner, "campaign:"+id+":"+next.UTC().Format(time.RFC3339Nano), spec)
	if e != nil {
		if _, err := tx.Exec(ctx, "UPDATE business_campaigns SET error=$2 WHERE id=$1", id, e.Error()); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return e
	}
	_, e = tx.Exec(ctx, "UPDATE business_campaigns SET last_task_id=$2,next_run=$3,error='' WHERE id=$1", id, task.ID, time.Now().Add(time.Duration(minutes)*time.Minute))
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (a *Service) StartCampaigns(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			run, done := context.WithTimeout(ctx, 20*time.Second)
			if e := a.TickCampaigns(run); e != nil {
				slog.Warn("campaign scheduler", "error", e)
			}
			done()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
