// Copyright 2026 TaskFused. All rights reserved.
package taskfused

import (
	"context"
	"time"
)

func terminal(state string) bool {
	return state == StateSucceeded || state == StateFailed || state == StateCanceled
}

// Wait reads until the operation reaches a terminal state. Execution failure and
// operation cancellation are returned as Operation states with a nil API error.
// Context cancellation stops only this wait and returns the context error. It does
// not mutate the operation, its lease, its attempts, or other callers' waits.
//
// Wait performs an immediate read, then polls local SQLite every 100ms. It works
// across Store instances/processes without an in-memory subscription registry.
func (s *Store) Wait(ctx context.Context, namespace, id string) (Operation, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return Operation{}, err
		}
		op, err := s.Get(ctx, namespace, id)
		if err != nil {
			return Operation{}, err
		}
		if terminal(op.State) {
			return op, nil
		}
		select {
		case <-ctx.Done():
			return Operation{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Cancellation reports whether this call changed a nonterminal operation.
// Terminal operations, including an already canceled operation, are unchanged.
type Cancellation struct {
	Operation Operation `json:"operation"`
	Changed   bool      `json:"changed"`
}

// Cancel atomically makes pending/running work terminal and revokes its current
// attempt. A running handler is notified cooperatively on its next renewal. The
// canceled state means authority was revoked, not that external code has stopped.
// Success/failure already committed wins over a later Cancel. Repeated Cancel
// calls are idempotent and do not append duplicate cancellation events.
func (s *Store) Cancel(ctx context.Context, namespace, id string) (Cancellation, error) {
	// Match Get validation before opening a write transaction.
	if err := validateLookup(namespace, id); err != nil {
		return Cancellation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Cancellation{}, err
	}
	defer tx.Rollback()
	op, err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM operations WHERE namespace=? AND id=?", namespace, id))
	if err != nil {
		return Cancellation{}, err
	}
	if terminal(op.State) {
		if err := tx.Commit(); err != nil {
			return Cancellation{}, err
		}
		return Cancellation{Operation: op}, nil
	}
	now := s.now().UTC()
	var active string
	if err := tx.QueryRowContext(ctx, "SELECT active_attempt_id FROM operations WHERE id=?", id).Scan(&active); err != nil {
		return Cancellation{}, err
	}
	if op.State == StateRunning {
		if _, err := tx.ExecContext(ctx, `UPDATE attempts SET state='canceled',finished_at=?,error='operation canceled' WHERE id=? AND state='running'`, now.Format(time.RFC3339Nano), active); err != nil {
			return Cancellation{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET state='canceled',completed_at=?,active_attempt_id='',lease_until_ns=0,next_run_ns=0,result=NULL,last_error='operation canceled' WHERE id=?`, now.Format(time.RFC3339Nano), id); err != nil {
		return Cancellation{}, err
	}
	if err := event(ctx, tx, id, active, "canceled", now); err != nil {
		return Cancellation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Cancellation{}, err
	}
	op.State = StateCanceled
	op.CompletedAt = &now
	op.Result = nil
	op.LastError = "operation canceled"
	return Cancellation{Operation: op, Changed: true}, nil
}
