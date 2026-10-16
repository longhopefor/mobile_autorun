package app

import (
	"context"
	"errors"
)

func (a *Service) ArchiveTask(ctx context.Context, owner, id string, archived bool) error {
	// Pause can mark waiting_human while an in-flight operation unwinds. Do not
	// hide that task before the worker has actually stopped.
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, active := a.active[id]; active {
		return errors.New("task_must_be_stopped_before_archive")
	}
	return a.Store.SetTaskArchived(ctx, owner, id, archived)
}
