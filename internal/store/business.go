package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
)

func (s *Store) BusinessProgress(ctx context.Context, id string) (json.RawMessage, error) {
	var b json.RawMessage
	e := s.Pool.QueryRow(ctx, "SELECT state FROM business_progress WHERE task_id=$1", id).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	return b, e
}
func (s *Store) SaveBusinessProgress(ctx context.Context, id string, state any) error {
	b, e := json.Marshal(state)
	if e != nil {
		return e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "INSERT INTO business_progress(task_id,state) VALUES($1,$2) ON CONFLICT(task_id) DO UPDATE SET state=$2,updated_at=now()", id, b); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO task_events(task_id,type,data) VALUES($1,'business_progress',$2)", id, b); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
