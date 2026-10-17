// Copyright 2026 TaskFused. All rights reserved.
package taskfused

import (
	"context"
	"database/sql"
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
