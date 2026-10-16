package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"mobile_autorun/internal/domain"
	"time"
)

type BusinessEntry struct {
	ID            string                `json:"id"`
	TaskID        string                `json:"task_id"`
	Platform      string                `json:"platform"`
	Account       string                `json:"account"`
	Record        domain.BusinessRecord `json:"record"`
	ObservationID string                `json:"observation_id"`
	AssetID       string                `json:"asset_id"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

func (s *Store) PutBusinessRecord(ctx context.Context, t domain.Task, o domain.Observation, r domain.BusinessRecord) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	// The observation must belong to this task and owner; clients cannot attach other users' evidence.
	tag, e := s.Pool.Exec(ctx, `INSERT INTO business_records(id,owner,task_id,platform,account,kind,record_key,data,observation_id)
 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9 FROM observations WHERE id=$9 AND owner=$2 AND task_id=$3
 ON CONFLICT(owner,platform,account,kind,record_key) DO UPDATE SET data=$8,task_id=$3,observation_id=$9,updated_at=now()`, uuid.NewString(), t.Owner, t.ID, t.Spec.Template, t.Spec.Account, r.Kind, r.Key, b, o.ID)
	if e == nil && tag.RowsAffected() != 1 {
		return errors.New("business_evidence_not_owned")
	}
	return e
}
func (s *Store) BusinessEntries(ctx context.Context, owner, kind, task string) ([]BusinessEntry, error) {
	return s.businessEntries(ctx, owner, kind, task, 2000)
}

// ExportBusinessLeads includes older contacts beyond the workbench's display limit.
func (s *Store) ExportBusinessLeads(ctx context.Context, owner, task string) ([]BusinessEntry, error) {
	return s.businessEntries(ctx, owner, "lead", task, 0)
}

func (s *Store) businessEntries(ctx context.Context, owner, kind, task string, limit int) ([]BusinessEntry, error) {
	rows, e := s.Pool.Query(ctx, `SELECT b.id,b.task_id,b.platform,b.account,b.data,b.observation_id,o.asset_id,b.updated_at FROM business_records b JOIN observations o ON o.id=b.observation_id WHERE b.owner=$1 AND ($2='' OR b.kind=$2) AND ($3='' OR b.task_id=$3) ORDER BY b.updated_at DESC LIMIT NULLIF($4,0)`, owner, kind, task, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []BusinessEntry{}
	for rows.Next() {
		var v BusinessEntry
		var b []byte
		if e = rows.Scan(&v.ID, &v.TaskID, &v.Platform, &v.Account, &b, &v.ObservationID, &v.AssetID, &v.UpdatedAt); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &v.Record); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) ReserveOutreach(ctx context.Context, t domain.Task, o domain.Observation, r domain.BusinessRecord) (string, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", t.Owner+":"+t.Spec.Template+":"+t.Spec.Account); e != nil {
		return "", e
	}
	key := Hash(r.Kind + "\x00" + r.Name + "\x00" + r.Evidence)
	if r.Kind == "friend" {
		key = Hash("friend\x00" + r.Name)
	}
	var duplicate bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM business_outreach WHERE owner=$1 AND platform=$2 AND account=$3 AND message_key=$4)", t.Owner, t.Spec.Template, t.Spec.Account, key).Scan(&duplicate); e != nil {
		return "", e
	}
	if duplicate {
		return "", errors.New("outreach_already_reserved_or_sent")
	}
	var count int
	var recent bool
	e = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE task_id=$1),COALESCE(bool_or(created_at>now()-make_interval(secs=>$5)),false) FROM business_outreach WHERE owner=$2 AND platform=$3 AND account=$4`, t.ID, t.Owner, t.Spec.Template, t.Spec.Account, t.Spec.Business.IntervalSeconds).Scan(&count, &recent)
	if e != nil {
		return "", e
	}
	if count >= t.Spec.Business.MaxReplies {
		return "", errors.New("reply_limit_reached")
	}
	if recent {
		return "", errors.New("reply_interval_not_elapsed")
	}
	// Ignore model-chosen keys. One outgoing action per recipient and incoming quote, even if source spelling changes.
	b, _ := json.Marshal(r)
	id := uuid.NewString()
	tag, e := tx.Exec(ctx, `INSERT INTO business_outreach(id,owner,task_id,platform,account,recipient,message_key,data,status,observation_id) SELECT $1,$2,$3,$4,$5,$6,$7,$8,'reserved',$9 FROM observations WHERE id=$9 AND owner=$2 AND task_id=$3 ON CONFLICT(owner,platform,account,message_key) DO NOTHING`, id, t.Owner, t.ID, t.Spec.Template, t.Spec.Account, r.Name, key, b, o.ID)
	if e != nil {
		return "", e
	}
	if tag.RowsAffected() != 1 {
		return "", errors.New("outreach_already_reserved_or_sent")
	}
	return id, tx.Commit(ctx)
}
func (s *Store) OutreachResult(ctx context.Context, owner, id, status string) error {
	if status != "reported" && status != "uncertain" {
		return errors.New("invalid_outreach_status")
	}
	_, e := s.Pool.Exec(ctx, "UPDATE business_outreach SET status=$3 WHERE id=$1 AND owner=$2 AND status='reserved'", id, owner, status)
	return e
}
