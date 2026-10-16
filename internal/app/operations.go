package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"mobile_autorun/internal/business"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/materials"
	"mobile_autorun/internal/model"
	"mobile_autorun/internal/store"
	"strings"
	"time"
)

func (a *Service) operations(ctx context.Context, t domain.Task, p model.Provider) (*business.Operations, error) {
	b := &business.Operations{Spec: t.Spec, Delegate: p, Save: func(c context.Context, s business.OperationsState) error {
		return a.Store.SaveBusinessProgress(c, t.ID, s)
	}}
	saved, e := a.Store.BusinessProgress(ctx, t.ID)
	if e != nil {
		return nil, e
	}
	if len(saved) > 0 {
		if e = json.Unmarshal(saved, &b.State); e != nil {
			return nil, e
		}
	}
	records, e := a.Store.BusinessEntries(ctx, t.Owner, "", "")
	if e != nil {
		return nil, e
	}
	b.State.AccountVerified = false
	b.State.Records = 0
	b.State.Posts = 0
	for _, r := range records {
		if r.TaskID == t.ID {
			if r.Record.Kind == "lead" || r.Record.Kind == "post" {
				b.State.Records++
			}
			if r.Record.Kind == "post" {
				b.State.Posts++
			}
		}
	}
	hints := []string{}
	for _, r := range records {
		if r.Platform == t.Spec.Template && r.Account == t.Spec.Account {
			hints = append(hints, r.Record.Kind+":"+r.Record.Name+":"+r.Record.Source+":"+r.Record.Evidence)
			if len(hints) >= 30 {
				break
			}
		}
	}
	b.History = strings.Join(hints, "\n")
	return b, nil
}
func (a *Service) handleBusiness(ctx context.Context, t domain.Task, b *business.Operations, d domain.Decision, o domain.Observation, execute func(context.Context, domain.Decision) (domain.Observation, error)) (string, error) {
	if b == nil || d.Record == nil {
		return "", errors.New("business_tools_disabled")
	}
	r := *d.Record
	if r.Kind == "account" {
		r.Key = t.Spec.Account
	}
	if r.Kind == "lead" {
		r.Key = store.Hash(r.Name + "\x00" + r.Source)
	}
	if r.Kind == "post" {
		r.Key = store.Hash(r.Source)
	}
	if r.Kind == "note" {
		r.Key = t.ID
	}
	if d.Tool == "record_business" {
		if e := b.CheckRecord(o, r); e != nil {
			return "", e
		}
		existing, e := a.Store.BusinessEntries(ctx, t.Owner, r.Kind, "")
		if e != nil {
			return "", e
		}
		isNew := true
		for _, v := range existing {
			if v.Platform == t.Spec.Template && v.Account == t.Spec.Account && v.Record.Key == r.Key && v.TaskID == t.ID {
				isNew = false
			}
		}
		if e = a.Store.PutBusinessRecord(ctx, t, o, r); e != nil {
			return "", e
		}
		if r.Kind == "note" {
			if b.State.DraftTaskID == "" {
				var existingID string
				err := a.Store.Pool.QueryRow(ctx, "SELECT id FROM tasks WHERE owner=$1 AND idempotency_key=$2", t.Owner, "growth-draft:"+t.ID).Scan(&existingID)
				if err == nil {
					b.State.DraftTaskID = existingID
				} else if !errors.Is(err, pgx.ErrNoRows) {
					return "", err
				}
			}
			if b.State.DraftTaskID == "" {
				outputs, e := materials.Render(r, nil)
				if e != nil {
					return "", e
				}
				ids := []string{}
				for _, out := range outputs {
					if strings.HasPrefix(out.Name, "xhs-") {
						asset, e := a.Store.SaveNamedAsset(ctx, t.Owner, out.Name, out.MIME, out.Data)
						if e != nil {
							return "", e
						}
						ids = append(ids, asset.ID)
					}
				}
				child, e := a.Create(ctx, t.Owner, "growth-draft:"+t.ID, domain.TaskSpec{Template: "xhs_image", Mode: "draft", DeviceID: t.Spec.DeviceID, ModelProfileID: t.Spec.ModelProfileID, Title: r.Title, Body: r.Body, Account: t.Spec.Account, AssetIDs: ids, MaxIterations: 60})
				if e != nil {
					return "", e
				}
				b.State.DraftTaskID = child.ID
				if e = a.Store.Event(ctx, t.ID, "draft_task_created", map[string]string{"task_id": child.ID}); e != nil {
					return "", e
				}
			}
		}
		if e = b.Recorded(ctx, r, isNew); e != nil {
			return "", e
		}
		return "business_record_saved; draft_task=" + b.State.DraftTaskID, nil
	}
	if d.Tool != "dispatch_business" {
		return "", errors.New("invalid_business_tool")
	}
	// An incoming lead must already be persisted with exactly the same recipient,
	// source and quote; arbitrary model-selected targets cannot be dispatched.
	leads, e := a.Store.BusinessEntries(ctx, t.Owner, "lead", "")
	if e != nil {
		return "", e
	}
	found := false
	for _, l := range leads {
		if l.Platform == t.Spec.Template && l.Account == t.Spec.Account && l.Record.Name == r.Name && l.Record.Source == r.Source && l.Record.Evidence == r.Evidence {
			found = true
			if r.Contact == "" {
				r.Contact = l.Record.Contact
			}
		}
	}
	if !found {
		return "", errors.New("record_incoming_lead_before_dispatch")
	}
	action, e := b.DispatchAction(o, r)
	if e != nil {
		return "", e
	}
	if action.Target != nil && action.Target.Text == "添加到通讯录" {
		after, err := execute(ctx, domain.Decision{Tool: "dispatch_business", ObservationID: o.ID, Action: &action, Record: &r, Reason: "打开已核对客户的好友验证页"})
		if err != nil {
			return "", err
		}
		if e = b.Save(ctx, b.State); e != nil {
			return "", e
		}
		return "friend_verification_opened: fill verification message, dispatch_business kind=friend again; evidence from verified profile; observation=" + after.ID, nil
	}
	id, e := a.Store.ReserveOutreach(ctx, t, o, r)
	if e != nil {
		if e.Error() == "reply_interval_not_elapsed" {
			return "interval_not_elapsed: wait or handle other records before dispatch", nil
		}
		if e.Error() == "outreach_already_reserved_or_sent" {
			return "already_sent_or_uncertain: skip this incoming message; never repeat", nil
		}
		return "", e
	}
	decision := domain.Decision{Tool: "dispatch_business", ObservationID: o.ID, Action: &action, Record: &r, Reason: d.Reason}
	after, err := execute(ctx, decision)
	clean, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	if r.Kind == "friend" {
		b.State.FriendRecipient = nil
		if e = b.Save(clean, b.State); e != nil {
			return "", e
		}
	}
	status := "reported"
	if err != nil {
		status = "uncertain"
	}
	if e = a.Store.OutreachResult(clean, t.Owner, id, status); e != nil {
		return "", e
	}
	if e = a.Store.Event(clean, t.ID, "business_outreach", map[string]any{"id": id, "status": status, "recipient": r.Name, "record": r, "evidence": after.ID}); e != nil {
		return "", e
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("outgoing_action_reported id=%s; verify next screen; do not resend", id), nil
}
