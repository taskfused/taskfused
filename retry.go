// Copyright 2026 TaskFused. All rights reserved.
package taskfused

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const MaxLifetimeAttempts = 1000

// RetryRequest is an explicit operator action on failed work. Reuse ActionKey and
// exactly the same parameters after uncertainty. ExpectedAttempts must be the
// lifetime count observed at failure, so a delayed new action cannot target a later
// failure. AdditionalAttempts grants 1–100 claims, up to MaxLifetimeAttempts total.
type RetryRequest struct {
	Namespace          string
	ID                 string
	ActionKey          string
	ExpectedAttempts   int
	AdditionalAttempts int
}

type RetryAction struct {
	Key                string    `json:"key"`
	ExpectedAttempts   int       `json:"expected_attempts"`
	AdditionalAttempts int       `json:"additional_attempts"`
	AttemptLimit       int       `json:"attempt_limit"`
	RequestedAt        time.Time `json:"requested_at"`
}

type RetryResult struct {
	Operation Operation   `json:"operation"`
	Action    RetryAction `json:"action"`
	Applied   bool        `json:"applied"`
}

// Retry reopens failed work with the same identity/input and a new attempt budget.
// It never resets attempt numbers or ownership tokens. A previously applied action
// returns its original grant and the operation's current state without reapplying,
// even if work has since failed again, succeeded, or been canceled.
func (s *Store) Retry(ctx context.Context, r RetryRequest) (RetryResult, error) {
	if err := validateLookup(r.Namespace, r.ID); err != nil {
		return RetryResult{}, err
	}
	if err := validate(Request{Namespace: r.Namespace, Handler: "retry", Version: "v1", Key: r.ActionKey}); err != nil {
		return RetryResult{}, err
	}
	if r.ExpectedAttempts < 1 || r.ExpectedAttempts > MaxLifetimeAttempts || r.AdditionalAttempts < 1 || r.AdditionalAttempts > 100 {
		return RetryResult{}, fmt.Errorf("%w: expected attempts 1–1000 and additional attempts 1–100 are required", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RetryResult{}, err
	}
	defer tx.Rollback()
	op, err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM operations WHERE namespace=? AND id=?", r.Namespace, r.ID))
	if err != nil {
		return RetryResult{}, err
	}
	action := RetryAction{Key: r.ActionKey}
	var requested string
	err = tx.QueryRowContext(ctx, `SELECT expected_attempts,additional_attempts,attempt_limit,requested_at FROM retry_actions WHERE operation_id=? AND action_key=?`, r.ID, r.ActionKey).Scan(&action.ExpectedAttempts, &action.AdditionalAttempts, &action.AttemptLimit, &requested)
	if err == nil {
		if action.ExpectedAttempts != r.ExpectedAttempts || action.AdditionalAttempts != r.AdditionalAttempts {
			return RetryResult{}, ErrRetryConflict
		}
		action.RequestedAt, err = time.Parse(time.RFC3339Nano, requested)
		if err != nil {
			return RetryResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return RetryResult{}, err
		}
		return RetryResult{Operation: op, Action: action}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RetryResult{}, err
	}
	if op.State != StateFailed {
		return RetryResult{}, fmt.Errorf("%w: current state %s", ErrRetryState, op.State)
	}
	if op.AttemptCount != r.ExpectedAttempts {
		return RetryResult{}, ErrRetryStale
	}
	limit := op.AttemptCount + r.AdditionalAttempts
	if limit > MaxLifetimeAttempts {
		return RetryResult{}, ErrRetryBudget
	}
	now := s.now().UTC()
	action.ExpectedAttempts = r.ExpectedAttempts
	action.AdditionalAttempts = r.AdditionalAttempts
	action.AttemptLimit = limit
	action.RequestedAt = now
	if _, err := tx.ExecContext(ctx, `INSERT INTO retry_actions(operation_id,action_key,expected_attempts,additional_attempts,attempt_limit,requested_at) VALUES (?,?,?,?,?,?)`, op.ID, r.ActionKey, r.ExpectedAttempts, r.AdditionalAttempts, limit, now.Format(time.RFC3339Nano)); err != nil {
		return RetryResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET state='queued',attempt_limit=?,retry_cycle_start=attempt_count,next_run_ns=?,completed_at=NULL,last_error='',result=NULL,active_attempt_id='',lease_until_ns=0 WHERE id=?`, limit, now.UnixNano(), op.ID); err != nil {
		return RetryResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO events(operation_id,attempt_id,kind,occurred_at,action_key) VALUES (?,'','retry_requested',?,?)`, op.ID, now.Format(time.RFC3339Nano), r.ActionKey); err != nil {
		return RetryResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RetryResult{}, err
	}
	op.State = StateQueued
	op.AttemptLimit = limit
	op.RetryCycleStart = op.AttemptCount
	op.RetryEligible = false
	op.CompletedAt = nil
	op.LastError = ""
	op.Result = nil
	return RetryResult{Operation: op, Action: action, Applied: true}, nil
}
