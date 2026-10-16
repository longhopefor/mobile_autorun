package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Archival changes list visibility, never the execution status or task inputs.
// The row lock serialises archival against resume and concurrent requests.
func (s *Store) SetTaskArchived(ctx context.Context, owner, id string, archive bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	var archived *time.Time
	if err = tx.QueryRow(ctx, "SELECT status,archived_at FROM tasks WHERE id=$1 AND owner=$2 FOR UPDATE", id, owner).Scan(&status, &archived); err != nil {
		return err
	}
	if archive {
		switch status {
		case "waiting_human", "reconciling", "succeeded", "failed", "cancelled":
		default:
			return errors.New("task_must_be_stopped_before_archive")
		}
	}
	if (archived != nil) == archive {
		return tx.Commit(ctx)
	}
	kind := "task_restored"
	if archive {
		kind = "task_archived"
		_, err = tx.Exec(ctx, "UPDATE tasks SET archived_at=now(),updated_at=now() WHERE id=$1 AND owner=$2", id, owner)
	} else {
		_, err = tx.Exec(ctx, "UPDATE tasks SET archived_at=NULL,updated_at=now() WHERE id=$1 AND owner=$2", id, owner)
	}
	if err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]any{"archived": archive})
	if _, err = tx.Exec(ctx, "INSERT INTO task_events(task_id,type,data) VALUES($1,$2,$3)", id, kind, b); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Metadata is fetched by the task's immutable attachment IDs, not the newest
// 100 items in the material library. No file content is read for this endpoint.
func (s *Store) TaskAssets(ctx context.Context, owner string, ids []string) ([]Asset, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id,mime,bytes,sha256,name FROM assets WHERE owner=$1 AND id=ANY($2)", owner, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]Asset{}
	for rows.Next() {
		var a Asset
		if err = rows.Scan(&a.ID, &a.MIME, &a.Bytes, &a.SHA, &a.Name); err != nil {
			return nil, err
		}
		byID[a.ID] = a
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	assets := []Asset{}
	for _, id := range ids {
		if a, ok := byID[id]; ok {
			assets = append(assets, a)
		}
	}
	return assets, nil
}
