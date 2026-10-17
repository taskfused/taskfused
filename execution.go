// Copyright 2026 TaskFused. All rights reserved.
package taskfused

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StateRetryWait = "retry_wait"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCanceled  = "canceled"
	MaxResultBytes = 1024 * 1024
	MaxErrorBytes  = 8192
	MinLease       = 100 * time.Millisecond
	MaxLease       = time.Hour
)

func retryPolicy(attempts int, delay time.Duration) (int, time.Duration, error) {
	if attempts == 0 {
		attempts = 3
	}
	if delay == 0 {
		delay = time.Second
	}
	if attempts < 1 || attempts > 100 || delay < time.Millisecond || delay > time.Hour {
		return 0, 0, fmt.Errorf("%w: max attempts must be 1–100 and retry delay 1ms–1h", ErrInvalid)
	}
	return attempts, delay, nil
}

// Retry delays double after each failed attempt and saturate at one hour.
func backoff(base time.Duration, attempt int) time.Duration {
	for i := 1; i < attempt && base < time.Hour; i++ {
		base *= 2
	}
	if base > time.Hour {
		return time.Hour
	}
	return base
}

func randomID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]), nil
}

// ClaimRequest selects one handler/version in one namespace. LeaseDuration must
// be between MinLease and MaxLease. Claims never execute handler code themselves.
type ClaimRequest struct {
	Namespace     string
	Handler       string
	Version       string
	LeaseDuration time.Duration
}

// Attempt records a single execution opportunity. Token increases per operation.
// A running record can have an expired lease; it becomes expired on reclamation.
type Attempt struct {
	ID          string     `json:"id"`
	OperationID string     `json:"operation_id"`
	Number      int        `json:"number"`
	Token       int64      `json:"token"`
	State       string     `json:"state"`
	StartedAt   time.Time  `json:"started_at"`
	LeaseUntil  time.Time  `json:"lease_until"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type Claim struct {
	Operation Operation `json:"operation"`
	Attempt   Attempt   `json:"attempt"`
}

func validLease(d time.Duration) bool { return d >= MinLease && d <= MaxLease }

func event(ctx context.Context, tx *sql.Tx, opID, attemptID, kind string, now time.Time) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO events(operation_id,attempt_id,kind,occurred_at) VALUES (?,?,?,?)", opID, attemptID, kind, now.Format(time.RFC3339Nano))
	return err
}

// Claim atomically takes queued/due work or reclaims an expired attempt. Crashed
// attempts count toward the retry budget. Recovery does not need a separate sweep.
func (s *Store) Claim(ctx context.Context, r ClaimRequest) (Claim, error) {
	if err := validate(Request{Namespace: r.Namespace, Handler: r.Handler, Version: r.Version, Key: "claim"}); err != nil {
		return Claim{}, err
	}
	if !validLease(r.LeaseDuration) {
		return Claim{}, fmt.Errorf("%w: lease must be 100ms–1h", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Claim{}, err
	}
	defer tx.Rollback()
	// Read the clock only after acquiring the write transaction, so lock waits do
	// not make a lease stale before it is issued or checked.
	for {
		now := s.now().UTC()
		op, err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+columns+` FROM operations WHERE namespace=? AND handler=? AND handler_version=? AND
   ((state IN ('queued','retry_wait') AND next_run_ns<=?) OR (state='running' AND lease_until_ns<=?))
   ORDER BY created_at, id LIMIT 1`, r.Namespace, r.Handler, r.Version, now.UnixNano(), now.UnixNano()))
		if errors.Is(err, ErrNotFound) {
			if err := tx.Commit(); err != nil {
				return Claim{}, err
			}
			return Claim{}, ErrNoWork
		}
		if err != nil {
			return Claim{}, err
		}
		var token int64
		var oldID string
		if err := tx.QueryRowContext(ctx, "SELECT ownership_token,active_attempt_id FROM operations WHERE id=?", op.ID).Scan(&token, &oldID); err != nil {
			return Claim{}, err
		}
		if op.State == StateRunning {
			if _, err := tx.ExecContext(ctx, "UPDATE attempts SET state='expired',finished_at=?,error='lease expired' WHERE id=? AND state='running'", now.Format(time.RFC3339Nano), oldID); err != nil {
				return Claim{}, err
			}
			if err := event(ctx, tx, op.ID, oldID, "lease_expired", now); err != nil {
				return Claim{}, err
			}
			if op.AttemptCount >= op.AttemptLimit {
				if _, err := tx.ExecContext(ctx, `UPDATE operations SET state='failed',last_error='lease expired; attempt budget exhausted',completed_at=?,active_attempt_id='',lease_until_ns=0 WHERE id=?`, now.Format(time.RFC3339Nano), op.ID); err != nil {
					return Claim{}, err
				}
				if err := event(ctx, tx, op.ID, oldID, "failed", now); err != nil {
					return Claim{}, err
				}
				continue
			}
		}
		id, err := randomID("at_")
		if err != nil {
			return Claim{}, err
		}
		attempt := Attempt{ID: id, OperationID: op.ID, Number: op.AttemptCount + 1, Token: token + 1, State: StateRunning, StartedAt: now, LeaseUntil: now.Add(r.LeaseDuration)}
		if _, err := tx.ExecContext(ctx, `INSERT INTO attempts(id,operation_id,number,token,state,started_at,lease_until_ns) VALUES (?,?,?,?,?,?,?)`, id, op.ID, attempt.Number, attempt.Token, StateRunning, now.Format(time.RFC3339Nano), attempt.LeaseUntil.UnixNano()); err != nil {
			return Claim{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET state='running',attempt_count=?,ownership_token=?,active_attempt_id=?,lease_until_ns=? WHERE id=?`, attempt.Number, attempt.Token, id, attempt.LeaseUntil.UnixNano(), op.ID); err != nil {
			return Claim{}, err
		}
		if err := event(ctx, tx, op.ID, id, "claimed", now); err != nil {
			return Claim{}, err
		}
		if err := tx.Commit(); err != nil {
			return Claim{}, err
		}
		op.State = StateRunning
		op.AttemptCount = attempt.Number
		return Claim{Operation: op, Attempt: attempt}, nil
	}
}

// owned is evaluated in the same immediate transaction as the mutation. Expiry
// itself revokes authority, even when no replacement attempt has been issued.
func owned(ctx context.Context, tx *sql.Tx, c Claim, now time.Time) (Operation, error) {
	op, err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+columns+` FROM operations WHERE id=? AND namespace=? AND state='running' AND active_attempt_id=? AND ownership_token=? AND lease_until_ns>?`, c.Operation.ID, c.Operation.Namespace, c.Attempt.ID, c.Attempt.Token, now.UnixNano()))
	if errors.Is(err, ErrNotFound) {
		var state string
		lookupErr := tx.QueryRowContext(ctx, "SELECT state FROM operations WHERE id=? AND namespace=?", c.Operation.ID, c.Operation.Namespace).Scan(&state)
		if lookupErr == nil && state == StateCanceled {
			return Operation{}, ErrOperationCanceled
		}
		if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
			return Operation{}, lookupErr
		}
		return Operation{}, ErrLeaseLost
	}
	return op, err
}

// Renew extends an unexpired lease. A stale claim cannot resurrect ownership.
func (s *Store) Renew(ctx context.Context, c Claim, duration time.Duration) (time.Time, error) {
	if !validLease(duration) {
		return time.Time{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if _, err := owned(ctx, tx, c, now); err != nil {
		return time.Time{}, err
	}
	until := now.Add(duration)
	if _, err := tx.ExecContext(ctx, "UPDATE operations SET lease_until_ns=MAX(lease_until_ns,?) WHERE id=?", until.UnixNano(), c.Operation.ID); err != nil {
		return time.Time{}, err
	}
	var actual int64
	if err := tx.QueryRowContext(ctx, "SELECT lease_until_ns FROM operations WHERE id=?", c.Operation.ID).Scan(&actual); err != nil {
		return time.Time{}, err
	}
	until = time.Unix(0, actual).UTC()
	if _, err := tx.ExecContext(ctx, "UPDATE attempts SET lease_until_ns=? WHERE id=?", until.UnixNano(), c.Attempt.ID); err != nil {
		return time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, err
	}
	return until, nil
}

// Complete records a result only while the attempt still owns a live lease.
func (s *Store) Complete(ctx context.Context, c Claim, result []byte) error {
	if len(result) > MaxResultBytes {
		return fmt.Errorf("%w: result exceeds %d bytes", ErrInvalid, MaxResultBytes)
	}
	return s.finish(ctx, c, append([]byte{}, result...), "")
}

// Fail records an execution failure and either schedules a retry or marks the
// operation failed. Handler errors are persisted; they do not change input/identity.
func (s *Store) Fail(ctx context.Context, c Claim, message string) error {
	if strings.TrimSpace(message) == "" || len(message) > MaxErrorBytes {
		return fmt.Errorf("%w: failure message must contain 1–8192 bytes", ErrInvalid)
	}
	return s.finish(ctx, c, nil, message)
}

func (s *Store) finish(ctx context.Context, c Claim, result []byte, message string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	op, err := owned(ctx, tx, c, now)
	if err != nil {
		return err
	}
	state, attemptState, kind := StateSucceeded, StateSucceeded, "succeeded"
	next := int64(0)
	var completed any = now.Format(time.RFC3339Nano)
	if message != "" {
		state, attemptState, kind = StateFailed, StateFailed, "failed"
		if op.AttemptCount < op.AttemptLimit {
			state = StateRetryWait
			kind = "retry_scheduled"
			next = now.Add(backoff(op.RetryDelay, op.AttemptCount-op.RetryCycleStart)).UnixNano()
			completed = nil
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET state=?,result=?,last_error=?,completed_at=?,next_run_ns=?,active_attempt_id='',lease_until_ns=0 WHERE id=?`, state, result, message, completed, next, op.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE attempts SET state=?,finished_at=?,error=? WHERE id=?", attemptState, now.Format(time.RFC3339Nano), message, c.Attempt.ID); err != nil {
		return err
	}
	if err := event(ctx, tx, op.ID, c.Attempt.ID, kind, now); err != nil {
		return err
	}
	return tx.Commit()
}

// Attempts returns durable attempt history in execution order. It requires the
// same namespace-qualified identity as Get, including for a missing operation.
func (s *Store) Attempts(ctx context.Context, namespace, id string) ([]Attempt, error) {
	if _, err := s.Get(ctx, namespace, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,operation_id,number,token,state,started_at,lease_until_ns,finished_at,error FROM attempts WHERE operation_id=? ORDER BY number`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attempts := []Attempt{}
	for rows.Next() {
		var a Attempt
		var started string
		var finished sql.NullString
		var lease int64
		if err := rows.Scan(&a.ID, &a.OperationID, &a.Number, &a.Token, &a.State, &started, &lease, &finished, &a.Error); err != nil {
			return nil, err
		}
		a.StartedAt, err = time.Parse(time.RFC3339Nano, started)
		if err != nil {
			return nil, err
		}
		a.LeaseUntil = time.Unix(0, lease).UTC()
		if finished.Valid {
			t, err := time.Parse(time.RFC3339Nano, finished.String)
			if err != nil {
				return nil, err
			}
			a.FinishedAt = &t
		}
		attempts = append(attempts, a)
	}
	return attempts, rows.Err()
}
