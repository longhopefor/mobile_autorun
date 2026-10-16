package model

import (
	"context"
	"encoding/json"
	"errors"
	"mobile_autorun/internal/device"
	"mobile_autorun/internal/domain"
)

type Remote struct{ Gateway *device.Gateway }

func (r *Remote) Next(ctx context.Context, q Request) (Result, error) {
	var v Result
	if q.Profile.ConnectorID == "" {
		return v, domain.ErrOffline
	}
	var prepErr error
	q, prepErr = PrepareVision(q)
	if prepErr != nil {
		return v, prepErr
	}
	v.Input = q.Input
	// The connector selects an allowed local provider. Never send host paths or secrets.
	q.Profile.BaseURL = ""
	q.Profile.SecretEnv = ""
	b, err := r.Gateway.Call(ctx, q.Profile.ConnectorID, "model/next", q)
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return v, errors.New("invalid_connector_result")
	}
	// Older connectors do not echo the optional metrics.
	v.Input = q.Input
	if err = ValidateResult(q, v); err != nil {
		return v, err
	}
	return v, nil
}
