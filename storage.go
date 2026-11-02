// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package taskfused

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrCapacity = errors.New("namespace admission limit reached")

// Limits are durable namespace admission limits; zero means unlimited. Lowering
// them never cancels existing work. All retained identities count as operations.
// These are logical counts, not a database file-size or filesystem quota.
//
// IdentityRetention is how long a finished operation (succeeded, failed or
// canceled) keeps its identity after completion; zero keeps it forever.
// ExpireIdentities deletes expired ones, after which the same key is new work.
type Limits struct {
	MaxOperations     int64         `json:"max_operations"`
	MaxActive         int64         `json:"max_active"`
	IdentityRetention time.Duration `json:"identity_retention"`
}

func validateNamespace(namespace string) error {
	return validate(Request{Namespace: namespace, Handler: "storage", Version: "v1", Key: "storage"})
}

// SetLimits replaces a namespace policy atomically, including across processes.
// Authentication and authorization for this administrative action belong to the host.
func (s *Store) SetLimits(ctx context.Context, namespace string, limits Limits) error {
	if err := validateNamespace(namespace); err != nil {
		return err
	}
	if limits.MaxOperations < 0 || limits.MaxActive < 0 || limits.IdentityRetention < 0 {
		return fmt.Errorf("%w: limits must be nonnegative", ErrInvalid)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO namespace_limits(namespace,max_operations,max_active,identity_retention_ns) VALUES(?,?,?,?) ON CONFLICT(namespace) DO UPDATE SET max_operations=excluded.max_operations,max_active=excluded.max_active,identity_retention_ns=excluded.identity_retention_ns`, namespace, limits.MaxOperations, limits.MaxActive, int64(limits.IdentityRetention))
	return err
}

// checkAdmission runs under the same immediate write transaction as admission.
// Joins and already-applied retry actions bypass it; they create no active work.
func checkAdmission(ctx context.Context, tx *sql.Tx, namespace string, newIdentity bool) error {
	var limits Limits
	err := tx.QueryRowContext(ctx, `SELECT max_operations,max_active FROM namespace_limits WHERE namespace=?`, namespace).Scan(&limits.MaxOperations, &limits.MaxActive)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if newIdentity && limits.MaxOperations > 0 {
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM operations WHERE namespace=?`, namespace).Scan(&count); err != nil {
			return err
		}
		if count >= limits.MaxOperations {
			return fmt.Errorf("%w: max_operations", ErrCapacity)
		}
	}
	if limits.MaxActive > 0 {
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM operations WHERE namespace=? AND state IN ('queued','running','retry_wait')`, namespace).Scan(&count); err != nil {
			return err
		}
		if count >= limits.MaxActive {
			return fmt.Errorf("%w: max_active", ErrCapacity)
		}
	}
	return nil
}

// Usage is a namespace snapshot of logical records and retained payload bytes.
// It excludes SQLite pages/indexes/WAL, error strings and native memory.
type Usage struct {
	Limits        Limits `json:"limits"`
	Operations    int64  `json:"operations"`
	Active        int64  `json:"active"`
	InputBytes    int64  `json:"input_bytes"`
	ResultBytes   int64  `json:"result_bytes"`
	PrunedResults int64  `json:"pruned_results"`
	Attempts      int64  `json:"attempts"`
	Events        int64  `json:"events"`
	RetryActions  int64  `json:"retry_actions"`
}

func (s *Store) Usage(ctx context.Context, namespace string) (Usage, error) {
	if err := validateNamespace(namespace); err != nil {
		return Usage{}, err
	}
	var u Usage
	// One SELECT gives a consistent SQLite read snapshot without reserving a writer.
	var retention int64
	err := s.db.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT max_operations FROM namespace_limits WHERE namespace=?1),0),
 COALESCE((SELECT max_active FROM namespace_limits WHERE namespace=?1),0),
 COALESCE((SELECT identity_retention_ns FROM namespace_limits WHERE namespace=?1),0),
 count(*),COALESCE(sum(state IN ('queued','running','retry_wait')),0),
 COALESCE(sum(length(input)),0),COALESCE(sum(length(result)),0),COALESCE(sum(result_pruned),0),
 (SELECT count(*) FROM attempts a JOIN operations o ON o.id=a.operation_id WHERE o.namespace=?1),
 (SELECT count(*) FROM events e JOIN operations o ON o.id=e.operation_id WHERE o.namespace=?1),
 (SELECT count(*) FROM retry_actions r JOIN operations o ON o.id=r.operation_id WHERE o.namespace=?1)
 FROM operations WHERE namespace=?1`, namespace).Scan(&u.Limits.MaxOperations, &u.Limits.MaxActive, &retention, &u.Operations, &u.Active, &u.InputBytes, &u.ResultBytes, &u.PrunedResults, &u.Attempts, &u.Events, &u.RetryActions)
	u.Limits.IdentityRetention = time.Duration(retention)
	return u, err
}

const retentionTimeFormat = "2006-01-02T15:04:05.000000000Z"
const MaxPruneBatch = 200

type PruneRequest struct {
	Namespace string
	Before    time.Time // Exclusive completion-time cutoff, not age since submission.
	Limit     int       // 1–200; zero defaults to 50.
}

type PruneResult struct {
	OperationIDs []string `json:"operation_ids"`
	RemovedBytes int64    `json:"removed_bytes"`
}

// PruneResults irreversibly removes succeeded result payloads in a bounded atomic
// batch. It keeps exact input, identity, state, attempts and retry actions forever.
// Repeated calls safely drain eligible results; a pruned result never re-executes.
// SQLite can reuse freed pages; this does not shrink files or securely erase bytes.
func (s *Store) PruneResults(ctx context.Context, r PruneRequest) (PruneResult, error) {
	if err := validateNamespace(r.Namespace); err != nil {
		return PruneResult{}, err
	}
	if r.Before.IsZero() || r.Before.UTC().Year() < 1 || r.Before.UTC().Year() > 9999 || r.Limit < 0 || r.Limit > MaxPruneBatch {
		return PruneResult{}, fmt.Errorf("%w: cutoff required and limit 1–200 (zero defaults to 50)", ErrInvalid)
	}
	if r.Limit == 0 {
		r.Limit = 50
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PruneResult{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT o.id,COALESCE(length(o.result),0) FROM result_retention r JOIN operations o ON o.id=r.operation_id WHERE r.namespace=? AND r.completed_at<? AND o.state='succeeded' AND o.result_pruned=0 ORDER BY r.completed_at,r.operation_id LIMIT ?`, r.Namespace, r.Before.UTC().Format(retentionTimeFormat), r.Limit)
	if err != nil {
		return PruneResult{}, err
	}
	result := PruneResult{OperationIDs: []string{}}
	for rows.Next() {
		var id string
		var bytes int64
		if err := rows.Scan(&id, &bytes); err != nil {
			rows.Close()
			return PruneResult{}, err
		}
		result.OperationIDs = append(result.OperationIDs, id)
		result.RemovedBytes += bytes
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return PruneResult{}, err
	}
	now := s.now().UTC()
	for _, id := range result.OperationIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET result=NULL,result_pruned=1 WHERE id=?`, id); err != nil {
			return PruneResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM result_retention WHERE operation_id=?`, id); err != nil {
			return PruneResult{}, err
		}
		if err := event(ctx, tx, id, "", "result_pruned", now); err != nil {
			return PruneResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return PruneResult{}, err
	}
	return result, nil
}

// MaxExpireBatch is the most identities one ExpireIdentities call removes.
const MaxExpireBatch = 200

type ExpireRequest struct {
	Namespace string
	Limit     int // 1–200; zero defaults to 50.
}

type ExpireResult struct {
	// OperationIDs are the operations deleted, oldest completion first.
	OperationIDs []string `json:"operation_ids"`
	// Cutoff is the exclusive completion-time cutoff used (now minus the
	// namespace's IdentityRetention); nil when the namespace keeps identities
	// forever and nothing was looked at.
	Cutoff *time.Time `json:"cutoff,omitempty"`
}

// ExpireIdentities deletes, in one atomic batch, finished operations
// (succeeded, failed or canceled) whose completion is older than the
// namespace's IdentityRetention, with their attempts, events, retry actions and
// result-retention rows. Their identities are free again: submitting the same
// key afterwards creates new work, and joining or replay protection for the old
// operation is gone. Queued, running and retry_wait work is never touched. With
// no retention set it does nothing. Repeated calls drain what is due. SQLite can
// reuse the freed pages; this does not shrink the file.
func (s *Store) ExpireIdentities(ctx context.Context, r ExpireRequest) (ExpireResult, error) {
	if err := validateNamespace(r.Namespace); err != nil {
		return ExpireResult{}, err
	}
	if r.Limit < 0 || r.Limit > MaxExpireBatch {
		return ExpireResult{}, fmt.Errorf("%w: limit 1–200 (zero defaults to 50)", ErrInvalid)
	}
	if r.Limit == 0 {
		r.Limit = 50
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExpireResult{}, err
	}
	defer tx.Rollback()
	var retention int64
	err = tx.QueryRowContext(ctx, `SELECT identity_retention_ns FROM namespace_limits WHERE namespace=?`, r.Namespace).Scan(&retention)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && retention == 0) {
		return ExpireResult{OperationIDs: []string{}}, nil
	}
	if err != nil {
		return ExpireResult{}, err
	}
	cutoff := s.now().UTC().Add(-time.Duration(retention))
	result := ExpireResult{OperationIDs: []string{}, Cutoff: &cutoff}
	if cutoff.Year() < 1 {
		return result, nil
	}
	// An operation canceled while running may still have a worker holding an
	// unexpired lease (its attempt is canceled, the lease time unchanged); it
	// keeps the record until that lease ends, so the worker's Renew, Complete
	// or Fail still reports ErrOperationCanceled. Attempts that finished
	// themselves have no worker left to tell.
	rows, err := tx.QueryContext(ctx, `SELECT e.operation_id FROM identity_expiry e JOIN operations o ON o.id=e.operation_id WHERE e.namespace=? AND e.terminal_at<? AND o.state IN ('succeeded','failed','canceled') AND NOT EXISTS (SELECT 1 FROM attempts a WHERE a.operation_id=o.id AND a.state IN ('running','canceled') AND a.lease_until_ns>?) ORDER BY e.terminal_at,e.operation_id LIMIT ?`, r.Namespace, cutoff.Format(expiryTimeFormat), s.now().UTC().UnixNano(), r.Limit)
	if err != nil {
		return ExpireResult{}, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return ExpireResult{}, err
		}
		result.OperationIDs = append(result.OperationIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ExpireResult{}, err
	}
	for _, id := range result.OperationIDs {
		for _, statement := range []string{
			`DELETE FROM events WHERE operation_id=?`,
			`DELETE FROM attempts WHERE operation_id=?`,
			`DELETE FROM retry_actions WHERE operation_id=?`,
			`DELETE FROM result_retention WHERE operation_id=?`,
			`DELETE FROM identity_expiry WHERE operation_id=?`,
			`DELETE FROM operations WHERE id=?`,
		} {
			if _, err := tx.ExecContext(ctx, statement, id); err != nil {
				return ExpireResult{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return ExpireResult{}, err
	}
	return result, nil
}

// ExpireDue runs ExpireIdentities in batches of MaxExpireBatch for every
// namespace with an IdentityRetention until nothing more is due, each batch
// in its own transaction. It returns how many identities were removed.
func (s *Store) ExpireDue(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT namespace FROM namespace_limits WHERE identity_retention_ns>0 ORDER BY namespace`)
	if err != nil {
		return 0, err
	}
	var namespaces []string
	for rows.Next() {
		var ns string
		if err := rows.Scan(&ns); err != nil {
			rows.Close()
			return 0, err
		}
		namespaces = append(namespaces, ns)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	// A namespace that fails doesn't stop the others; its error is returned
	// with the rest.
	removed := 0
	var errs []error
	for _, ns := range namespaces {
		for {
			if err := ctx.Err(); err != nil {
				return removed, errors.Join(append(errs, err)...)
			}
			r, err := s.ExpireIdentities(ctx, ExpireRequest{Namespace: ns, Limit: MaxExpireBatch})
			if err != nil {
				errs = append(errs, fmt.Errorf("namespace %q: %w", ns, err))
				break
			}
			removed += len(r.OperationIDs)
			if len(r.OperationIDs) < MaxExpireBatch {
				break
			}
		}
	}
	return removed, errors.Join(errs...)
}
