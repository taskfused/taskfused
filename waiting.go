// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package taskfused

import (
	"context"
	"errors"
	"time"
)

func terminal(state string) bool {
	return state == StateSucceeded || state == StateFailed || state == StateCanceled
}

// ErrClosed is returned by Wait when its Store is closed.
var ErrClosed = errors.New("store closed")

type waitKey struct{ namespace, id string }
type waitPoll struct {
	done        chan struct{}
	cancel      context.CancelFunc
	subscribers int
	err         error // Published before done closes.
}

// Wait reads immediately, then shares a 100ms SQLite poller with other local
// callers for this Store, namespace and operation. Other processes remain visible.
// Failure/cancellation are terminal Operation states with a nil API error.
// Canceling ctx stops only this caller. Each result is independently read, so
// payloads never alias and a reopened retry cannot reuse a cached terminal result.
func (s *Store) Wait(ctx context.Context, namespace, id string) (Operation, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Operation{}, err
		}
		s.waitMu.Lock()
		closed := s.closed
		s.waitMu.Unlock()
		if closed {
			return Operation{}, ErrClosed
		}
		op, err := s.Get(ctx, namespace, id)
		if err != nil {
			// Close can finish between the preflight check and this read.
			s.waitMu.Lock()
			closed = s.closed
			s.waitMu.Unlock()
			if closed && ctx.Err() == nil {
				return Operation{}, ErrClosed
			}
			return Operation{}, err
		}
		if terminal(op.State) {
			return op, nil
		}
		key := waitKey{namespace, id}
		p, err := s.subscribe(key)
		if err != nil {
			return Operation{}, err
		}
		select {
		case <-ctx.Done():
			s.unsubscribe(key, p)
			return Operation{}, ctx.Err()
		case <-p.done:
			s.unsubscribe(key, p)
			if p.err != nil {
				return Operation{}, p.err
			}
			// Re-read rather than caching a terminal snapshot across retry cycles.
		}
	}
}

func (s *Store) subscribe(key waitKey) (*waitPoll, error) {
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if s.waits == nil {
		s.waits = make(map[waitKey]*waitPoll)
	}
	p := s.waits[key]
	if p == nil {
		ctx, cancel := context.WithCancel(context.Background())
		p = &waitPoll{done: make(chan struct{}), cancel: cancel}
		s.waits[key] = p
		s.waitWG.Add(1)
		go s.pollWait(ctx, key, p)
	}
	p.subscribers++
	return p, nil
}

func (s *Store) unsubscribe(key waitKey, p *waitPoll) {
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	p.subscribers--
	if p.subscribers == 0 {
		if s.waits[key] == p {
			delete(s.waits, key)
		}
		p.cancel()
	}
}

func (s *Store) pollWait(ctx context.Context, key waitKey, p *waitPoll) {
	defer s.waitWG.Done()
	defer p.cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var err error
	for {
		var op Operation
		op, err = s.Get(ctx, key.namespace, key.id)
		if err != nil || terminal(op.State) {
			break
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-ticker.C:
			continue
		}
		break
	}
	s.waitMu.Lock()
	if s.waits[key] == p {
		delete(s.waits, key)
	}
	if s.closed {
		err = ErrClosed
	}
	p.err = err
	close(p.done)
	s.waitMu.Unlock()
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
