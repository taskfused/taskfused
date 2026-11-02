// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package taskfused

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// OperationStates and AttemptStates list every state Metrics reports, so each
// namespace always has the same series, zeros included.
var (
	OperationStates = []string{StateQueued, StateRunning, StateRetryWait, StateSucceeded, StateFailed, StateCanceled}
	AttemptStates   = []string{"running", "succeeded", "failed", "expired", "canceled"}
)

// NamespaceMetrics is one namespace's counts at the moment Metrics read them.
// Attempts in state expired are lease expiries. Counts can go down: pruning
// doesn't change them, but identity expiry deletes finished operations and
// their attempts.
type NamespaceMetrics struct {
	Namespace  string           `json:"namespace"`
	Operations map[string]int64 `json:"operations"`
	Attempts   map[string]int64 `json:"attempts"`
	// OldestQueued is the submission time of the oldest operation in state
	// queued; nil when none is queued.
	OldestQueued *time.Time `json:"oldest_queued,omitempty"`
	Limits       Limits     `json:"limits"`
}

// Metrics is a snapshot of every namespace that has operations or limits,
// sorted by namespace, read with one SQL statement (one consistent snapshot
// that takes no write lock). It scans every operation and attempt row, so a
// scrape costs time in proportion to what the file keeps.
type Metrics struct {
	Namespaces []NamespaceMetrics `json:"namespaces"`
	ReadAt     time.Time          `json:"read_at"`
}

func (s *Store) Metrics(ctx context.Context) (Metrics, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT 'operation',namespace,state,count(*),0,0,NULL FROM operations GROUP BY namespace,state
UNION ALL
SELECT 'attempt',o.namespace,a.state,count(*),0,0,NULL FROM attempts a JOIN operations o ON o.id=a.operation_id GROUP BY o.namespace,a.state
UNION ALL
SELECT 'oldest',namespace,'',0,0,0,min(created_at) FROM operations WHERE state='queued' GROUP BY namespace
UNION ALL
SELECT 'limits',namespace,'',max_operations,max_active,identity_retention_ns,NULL FROM namespace_limits`)
	if err != nil {
		return Metrics{}, err
	}
	defer rows.Close()
	byName := map[string]*NamespaceMetrics{}
	get := func(ns string) *NamespaceMetrics {
		m, ok := byName[ns]
		if !ok {
			m = &NamespaceMetrics{Namespace: ns, Operations: map[string]int64{}, Attempts: map[string]int64{}}
			for _, state := range OperationStates {
				m.Operations[state] = 0
			}
			for _, state := range AttemptStates {
				m.Attempts[state] = 0
			}
			byName[ns] = m
		}
		return m
	}
	for rows.Next() {
		var kind, ns, state string
		var a, b, c int64
		var text sql.NullString
		if err := rows.Scan(&kind, &ns, &state, &a, &b, &c, &text); err != nil {
			return Metrics{}, err
		}
		m := get(ns)
		switch kind {
		case "operation":
			m.Operations[state] = a
		case "attempt":
			m.Attempts[state] = a
		case "oldest":
			if text.Valid {
				if t, err := time.Parse(time.RFC3339Nano, text.String); err == nil {
					m.OldestQueued = &t
				}
			}
		case "limits":
			m.Limits = Limits{MaxOperations: a, MaxActive: b, IdentityRetention: time.Duration(c)}
		}
	}
	if err := rows.Err(); err != nil {
		return Metrics{}, err
	}
	result := Metrics{Namespaces: []NamespaceMetrics{}, ReadAt: s.now().UTC()}
	for _, m := range byName {
		result.Namespaces = append(result.Namespaces, *m)
	}
	sort.Slice(result.Namespaces, func(i, j int) bool { return result.Namespaces[i].Namespace < result.Namespaces[j].Namespace })
	return result, nil
}
