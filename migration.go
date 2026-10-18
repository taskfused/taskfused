// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package taskfused

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Rebuild the constrained v1 tables inside the caller's write transaction. Copy
// child events before dropping their old parent, preserving IDs and event order.
func migrateV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE operations_v2 (
 id TEXT PRIMARY KEY,
 namespace TEXT NOT NULL,
 handler TEXT NOT NULL,
 handler_version TEXT NOT NULL,
 operation_key TEXT NOT NULL,
 input BLOB NOT NULL,
 input_sha256 TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('queued','running','retry_wait','succeeded','failed')),
 created_at TEXT NOT NULL,
 attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count >= 0),
 max_attempts INTEGER NOT NULL DEFAULT 3 CHECK(max_attempts BETWEEN 1 AND 100),
 retry_base_ns INTEGER NOT NULL DEFAULT 1000000000,
 next_run_ns INTEGER NOT NULL DEFAULT 0,
 ownership_token INTEGER NOT NULL DEFAULT 0,
 active_attempt_id TEXT NOT NULL DEFAULT '',
 lease_until_ns INTEGER NOT NULL DEFAULT 0,
 result BLOB,
 last_error TEXT NOT NULL DEFAULT '',
 completed_at TEXT,
 UNIQUE(namespace, handler, handler_version, operation_key)
);
INSERT INTO operations_v2 (id, namespace, handler, handler_version, operation_key,
 input, input_sha256, state, created_at)
 SELECT id, namespace, handler, handler_version, operation_key, input, input_sha256,
 state, created_at FROM operations;
CREATE TABLE events_v2 (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 operation_id TEXT NOT NULL REFERENCES operations_v2(id),
 kind TEXT NOT NULL,
 occurred_at TEXT NOT NULL,
 attempt_id TEXT NOT NULL DEFAULT ''
);
INSERT INTO events_v2(sequence, operation_id, kind, occurred_at)
 SELECT sequence, operation_id, kind, occurred_at FROM events;
DROP TABLE events;
DROP TABLE operations;
ALTER TABLE operations_v2 RENAME TO operations;
ALTER TABLE events_v2 RENAME TO events;
CREATE TABLE attempts (
 id TEXT PRIMARY KEY,
 operation_id TEXT NOT NULL REFERENCES operations(id),
 number INTEGER NOT NULL,
 token INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('running','succeeded','failed','expired')),
 started_at TEXT NOT NULL,
 lease_until_ns INTEGER NOT NULL,
 finished_at TEXT,
 error TEXT NOT NULL DEFAULT '',
 UNIQUE(operation_id, number)
);
CREATE INDEX operations_claim ON operations(namespace, handler, handler_version, state, next_run_ns, lease_until_ns);
PRAGMA user_version = 2;`)
	return err
}

// Version 3 adds canceled to the operation/attempt state domains. Cancellation
// revokes ownership transactionally; it does not need a pending cancellation flag.
func migrateV3(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE operations_v3 (
 id TEXT PRIMARY KEY,
 namespace TEXT NOT NULL,
 handler TEXT NOT NULL,
 handler_version TEXT NOT NULL,
 operation_key TEXT NOT NULL,
 input BLOB NOT NULL,
 input_sha256 TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('queued','running','retry_wait','succeeded','failed','canceled')),
 created_at TEXT NOT NULL,
 attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count >= 0),
 max_attempts INTEGER NOT NULL DEFAULT 3 CHECK(max_attempts BETWEEN 1 AND 100),
 retry_base_ns INTEGER NOT NULL DEFAULT 1000000000,
 next_run_ns INTEGER NOT NULL DEFAULT 0,
 ownership_token INTEGER NOT NULL DEFAULT 0,
 active_attempt_id TEXT NOT NULL DEFAULT '',
 lease_until_ns INTEGER NOT NULL DEFAULT 0,
 result BLOB,
 last_error TEXT NOT NULL DEFAULT '',
 completed_at TEXT,
 UNIQUE(namespace, handler, handler_version, operation_key)
);
INSERT INTO operations_v3 SELECT * FROM operations;
CREATE TABLE attempts_v3 (
 id TEXT PRIMARY KEY,
 operation_id TEXT NOT NULL REFERENCES operations_v3(id),
 number INTEGER NOT NULL,
 token INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('running','succeeded','failed','expired','canceled')),
 started_at TEXT NOT NULL,
 lease_until_ns INTEGER NOT NULL,
 finished_at TEXT,
 error TEXT NOT NULL DEFAULT '',
 UNIQUE(operation_id, number)
);
INSERT INTO attempts_v3 SELECT * FROM attempts;
CREATE TABLE events_v3 (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 operation_id TEXT NOT NULL REFERENCES operations_v3(id),
 kind TEXT NOT NULL,
 occurred_at TEXT NOT NULL,
 attempt_id TEXT NOT NULL DEFAULT ''
);
INSERT INTO events_v3 SELECT * FROM events;
DROP TABLE events;
DROP TABLE attempts;
DROP TABLE operations;
ALTER TABLE operations_v3 RENAME TO operations;
ALTER TABLE attempts_v3 RENAME TO attempts;
ALTER TABLE events_v3 RENAME TO events;
CREATE INDEX operations_claim ON operations(namespace, handler, handler_version, state, next_run_ns, lease_until_ns);
PRAGMA user_version = 3;`)
	return err
}

// Version 4 separates the immutable submission policy from the effective lifetime
// attempt limit. Manual grants get durable action IDs and event linkage.
func migrateV4(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE operations ADD COLUMN attempt_limit INTEGER NOT NULL DEFAULT 3 CHECK(attempt_limit BETWEEN 1 AND 1000);
ALTER TABLE operations ADD COLUMN retry_cycle_start INTEGER NOT NULL DEFAULT 0 CHECK(retry_cycle_start BETWEEN 0 AND 1000);
UPDATE operations SET attempt_limit=max_attempts;
ALTER TABLE events ADD COLUMN action_key TEXT NOT NULL DEFAULT '';
CREATE TABLE retry_actions (
 operation_id TEXT NOT NULL REFERENCES operations(id),
 action_key TEXT NOT NULL,
 expected_attempts INTEGER NOT NULL CHECK(expected_attempts BETWEEN 1 AND 1000),
 additional_attempts INTEGER NOT NULL CHECK(additional_attempts BETWEEN 1 AND 100),
 attempt_limit INTEGER NOT NULL CHECK(attempt_limit=expected_attempts+additional_attempts AND attempt_limit<=1000),
 requested_at TEXT NOT NULL,
 PRIMARY KEY(operation_id,action_key)
);
CREATE INDEX events_operation_sequence ON events(operation_id,sequence);
PRAGMA user_version = 4;`)
	return err
}

// Version 5 adds durable admission policy and explicit result pruning. Retention
// timestamps use fixed-width UTC nanoseconds for exact indexed cutoff comparison.
func migrateV5(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
 ALTER TABLE operations ADD COLUMN result_pruned INTEGER NOT NULL DEFAULT 0 CHECK(result_pruned IN (0,1));
 CREATE TABLE namespace_limits(namespace TEXT PRIMARY KEY,max_operations INTEGER NOT NULL CHECK(max_operations>=0),max_active INTEGER NOT NULL CHECK(max_active>=0));
 CREATE TABLE result_retention(operation_id TEXT PRIMARY KEY REFERENCES operations(id),namespace TEXT NOT NULL,completed_at TEXT NOT NULL);
 CREATE INDEX result_retention_cutoff ON result_retention(namespace,completed_at,operation_id);
 `); err != nil {
		return err
	}
	// Read only small metadata batches; never materialize stored payloads in migration.
	after := ""
	for {
		rows, err := tx.QueryContext(ctx, `SELECT id,namespace,completed_at FROM operations WHERE state='succeeded' AND id>? ORDER BY id LIMIT 200`, after)
		if err != nil {
			return err
		}
		type record struct{ id, namespace, completed string }
		batch := []record{}
		for rows.Next() {
			var r record
			if err := rows.Scan(&r.id, &r.namespace, &r.completed); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			completed, err := time.Parse(time.RFC3339Nano, r.completed)
			if err != nil {
				return fmt.Errorf("migrate result completion: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO result_retention(operation_id,namespace,completed_at) VALUES(?,?,?)`, r.id, r.namespace, completed.UTC().Format(retentionTimeFormat)); err != nil {
				return err
			}
		}
		after = batch[len(batch)-1].id
	}
	_, err := tx.ExecContext(ctx, `PRAGMA user_version=5;`)
	return err
}
