package taskfused

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Build a real v2 database through the frozen legacy migration, without calling
// Open (which advances to the current schema). The old worker schema has strict state checks.
func v2Store(t *testing.T, path string) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
 CREATE TABLE taskfused_meta(name TEXT PRIMARY KEY,value TEXT NOT NULL);
 INSERT INTO taskfused_meta VALUES('application','taskfused');
 CREATE TABLE operations(id TEXT PRIMARY KEY,namespace TEXT NOT NULL,handler TEXT NOT NULL,handler_version TEXT NOT NULL,operation_key TEXT NOT NULL,input BLOB NOT NULL,input_sha256 TEXT NOT NULL,state TEXT NOT NULL CHECK(state='queued'),created_at TEXT NOT NULL,UNIQUE(namespace,handler,handler_version,operation_key));
 CREATE TABLE events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,operation_id TEXT NOT NULL REFERENCES operations(id),kind TEXT NOT NULL,occurred_at TEXT NOT NULL);
 PRAGMA user_version=1;`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateV2(context.Background(), tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return &Store{db: db, now: time.Now}
}

func TestV3MigrationPreservesActiveAttemptsResultsAndEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	old := v2Store(t, path)
	active := legacySubmit(t, old, request())
	claim := legacyClaim(t, old, active)
	r := request()
	r.Key = "completed"
	complete := legacySubmit(t, old, r)
	completedClaim := legacyClaim(t, old, complete)
	legacyComplete(t, old, completedClaim, []byte("old result"))
	r.Key = "queued"
	queued := legacySubmit(t, old, r)
	var previousEvents int
	if err := old.db.QueryRow("SELECT count(*) FROM events").Scan(&previousEvents); err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec("UPDATE operations SET state='canceled' WHERE id=?", queued.ID); err == nil {
		t.Fatal("fixture did not have v2 state constraints")
	}
	old.Close()
	upgraded := openTest(t, path)
	got := getTest(t, upgraded, complete)
	if got.State != StateSucceeded || string(got.Result) != "old result" || got.AttemptCount != 1 {
		t.Fatalf("stored result migration: %+v", got)
	}
	history, err := upgraded.Attempts(context.Background(), active.Namespace, active.ID)
	if err != nil || len(history) != 1 || history[0].ID != claim.Attempt.ID || history[0].Token != claim.Attempt.Token || history[0].State != StateRunning {
		t.Fatalf("active attempt migration: %+v %v", history, err)
	}
	var events int
	if err := upgraded.db.QueryRow("SELECT count(*) FROM events").Scan(&events); err != nil || events != previousEvents {
		t.Fatalf("event preservation: %d %v", events, err)
	}
	if _, err := upgraded.Renew(context.Background(), claim, time.Minute); err != nil {
		t.Fatalf("preserved ownership was not usable: %v", err)
	}
	if _, err := upgraded.Cancel(context.Background(), active.Namespace, active.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := upgraded.Cancel(context.Background(), queued.Namespace, queued.ID); err != nil {
		t.Fatal(err)
	}
	history, err = upgraded.Attempts(context.Background(), active.Namespace, active.ID)
	if err != nil || history[0].State != StateCanceled {
		t.Fatalf("new attempt state rejected: %+v %v", history, err)
	}
	var violations int
	if err := upgraded.db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("migration foreign keys: %d %v", violations, err)
	}
	upgraded.Close()
	reopened := openTest(t, path)
	if getTest(t, reopened, active).State != StateCanceled || string(getTest(t, reopened, complete).Result) != "old result" {
		t.Fatal("v3 data did not survive reopen")
	}
}

func TestV3MigrationFailureLeavesV2Intact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	old := v2Store(t, path)
	op := legacySubmit(t, old, request())
	if _, err := old.db.Exec("CREATE TABLE attempts_v3(blocker TEXT)"); err != nil {
		t.Fatal(err)
	}
	old.Close()
	if s, err := Open(context.Background(), path); err == nil {
		s.Close()
		t.Fatal("expected migration name conflict")
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, operations, events, partial int
	for _, query := range []struct {
		sql  string
		dest *int
	}{
		{"PRAGMA user_version", &version},
		{"SELECT count(*) FROM operations WHERE id='" + op.ID + "'", &operations},
		{"SELECT count(*) FROM events", &events},
		{"SELECT count(*) FROM sqlite_master WHERE name='operations_v3'", &partial},
	} {
		if err := db.QueryRow(query.sql).Scan(query.dest); err != nil {
			t.Fatal(err)
		}
	}
	if version != 2 || operations != 1 || events != 1 || partial != 0 {
		t.Fatalf("partial migration: version=%d operations=%d events=%d partial=%d", version, operations, events, partial)
	}
}

// These fixed-schema fixture helpers intentionally do not call the current Store
// methods: current projections include columns absent from genuine old databases.
func legacySubmit(t *testing.T, s *Store, r Request) Operation {
	t.Helper()
	id, err := randomID("op_")
	if err != nil {
		t.Fatal(err)
	}
	attempts, delay, err := retryPolicy(r.MaxAttempts, r.RetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(r.Input)
	op := Operation{ID: id, Namespace: r.Namespace, Handler: r.Handler, Version: r.Version, Key: r.Key, Input: r.Input, InputSHA256: hex.EncodeToString(sum[:]), State: StateQueued, CreatedAt: time.Now().UTC(), MaxAttempts: attempts, RetryDelay: delay}
	_, err = s.db.Exec(`INSERT INTO operations (`+identityColumns+`,max_attempts,retry_base_ns,next_run_ns) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, op.ID, op.Namespace, op.Handler, op.Version, op.Key, op.Input, op.InputSHA256, op.State, op.CreatedAt.Format(time.RFC3339Nano), attempts, int64(delay), op.CreatedAt.UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO events(operation_id,kind,occurred_at) VALUES(?,'submitted',?)", id, op.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return op
}

func legacyClaim(t *testing.T, s *Store, op Operation) Claim {
	t.Helper()
	id, err := randomID("at_")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	a := Attempt{ID: id, OperationID: op.ID, Number: 1, Token: 1, State: StateRunning, StartedAt: now, LeaseUntil: now.Add(time.Hour)}
	if _, err := s.db.Exec("INSERT INTO attempts(id,operation_id,number,token,state,started_at,lease_until_ns) VALUES(?,?,1,1,'running',?,?)", id, op.ID, now.Format(time.RFC3339Nano), a.LeaseUntil.UnixNano()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE operations SET state='running',attempt_count=1,ownership_token=1,active_attempt_id=?,lease_until_ns=? WHERE id=?", id, a.LeaseUntil.UnixNano(), op.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO events(operation_id,attempt_id,kind,occurred_at) VALUES(?,?,'claimed',?)", op.ID, id, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	op.State = StateRunning
	op.AttemptCount = 1
	return Claim{Operation: op, Attempt: a}
}

func legacyComplete(t *testing.T, s *Store, c Claim, result []byte) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec("UPDATE operations SET state='succeeded',result=?,completed_at=?,active_attempt_id='',lease_until_ns=0 WHERE id=?", result, now, c.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE attempts SET state='succeeded',finished_at=? WHERE id=?", now, c.Attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO events(operation_id,attempt_id,kind,occurred_at) VALUES(?,?,'succeeded',?)", c.Operation.ID, c.Attempt.ID, now); err != nil {
		t.Fatal(err)
	}
}

func v3Store(t *testing.T, path string) *Store {
	t.Helper()
	old := v2Store(t, path)
	tx, err := old.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateV3(context.Background(), tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return old
}

func legacyFailed(t *testing.T, s *Store) Operation {
	t.Helper()
	r := request()
	r.MaxAttempts = 1
	op := legacySubmit(t, s, r)
	c := legacyClaim(t, s, op)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec("UPDATE operations SET state='failed',last_error='legacy failure',completed_at=?,active_attempt_id='',lease_until_ns=0 WHERE id=?", now, op.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE attempts SET state='failed',finished_at=?,error='legacy failure' WHERE id=?", now, c.Attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO events(operation_id,attempt_id,kind,occurred_at) VALUES(?,?,'failed',?)", op.ID, c.Attempt.ID, now); err != nil {
		t.Fatal(err)
	}
	op.State = StateFailed
	op.AttemptCount = 1
	return op
}

func TestV4MigrationPreservesOriginalPolicyAndAllowsAuditedRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.db")
	old := v3Store(t, path)
	failed := legacyFailed(t, old)
	r := request()
	r.Key = "old-canceled"
	canceled := legacySubmit(t, old, r)
	if _, err := old.db.Exec("UPDATE operations SET state='canceled',completed_at=?,last_error='operation canceled' WHERE id=?", time.Now().UTC().Format(time.RFC3339Nano), canceled.ID); err != nil {
		t.Fatal(err)
	}
	old.Close()
	current := openTest(t, path)
	got := getTest(t, current, failed)
	if got.MaxAttempts != 1 || got.AttemptLimit != 1 || got.RetryCycleStart != 0 || !got.RetryEligible || got.LastError != "legacy failure" {
		t.Fatalf("policy migration: %+v", got)
	}
	page, err := current.Events(context.Background(), got.Namespace, got.ID, EventQuery{})
	if err != nil || len(page.Events) != 3 {
		t.Fatalf("legacy events: %+v %v", page, err)
	}
	for _, e := range page.Events {
		if e.ActionKey != "" || e.ExpectedAttempts != 0 {
			t.Fatalf("fabricated old action: %+v", e)
		}
	}
	if _, err := current.Retry(context.Background(), retryRequest(got)); err != nil {
		t.Fatal(err)
	}
	claim := claimTest(t, current)
	if claim.Attempt.Number != 2 || claim.Attempt.Token != 2 {
		t.Fatalf("migration reset counters: %+v", claim)
	}
	if err := current.Complete(context.Background(), claim, []byte("recovered legacy work")); err != nil {
		t.Fatal(err)
	}
	if got := getTest(t, current, canceled); got.State != StateCanceled || got.AttemptLimit != got.MaxAttempts || got.RetryEligible {
		t.Fatalf("canceled migration: %+v", got)
	}
	var violations, version int
	if err := current.db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("foreign keys: %d %v", violations, err)
	}
	if err := current.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version: %d %v", version, err)
	}
}

func TestV4MigrationFailureRollsBackAddedColumnsAndData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.db")
	old := v3Store(t, path)
	failed := legacyFailed(t, old)
	if _, err := old.db.Exec("CREATE TABLE retry_actions(blocker TEXT)"); err != nil {
		t.Fatal(err)
	}
	old.Close()
	if s, err := Open(context.Background(), path); err == nil {
		s.Close()
		t.Fatal("expected migration conflict")
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, columns, events int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('operations') WHERE name IN ('attempt_limit','retry_cycle_start')").Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('events') WHERE name='action_key'").Scan(&events); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.QueryRow("SELECT state FROM operations WHERE id=?", failed.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if version != 3 || columns != 0 || events != 0 || state != StateFailed {
		t.Fatalf("partial v4 migration: version=%d columns=%d event columns=%d state=%s", version, columns, events, state)
	}
}

func TestV5MigrationBackfillsResultsInBatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v4.db")
	old := v3Store(t, path)
	tx, err := old.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateV4(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	stamp := "2026-10-17T18:00:00.000000001Z"
	tx, err = old.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 201; i++ {
		if _, err := tx.Exec(`INSERT INTO operations(id,namespace,handler,handler_version,operation_key,input,input_sha256,state,created_at,completed_at,result) VALUES(?, 'ns','echo','v1',?,X'00','legacy','succeeded',?,?,X'01')`, fmt.Sprint(i), fmt.Sprint(i), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	old.Close()
	s := openTest(t, path)
	if u := usageTest(t, s, "ns"); u.Operations != 201 || u.ResultBytes != 201 || u.PrunedResults != 0 {
		t.Fatalf("migration lost payloads: %+v", u)
	}
	exact, _ := time.Parse(time.RFC3339Nano, stamp)
	r := PruneRequest{Namespace: "ns", Before: exact, Limit: 200}
	if p, err := s.PruneResults(context.Background(), r); err != nil || len(p.OperationIDs) != 0 {
		t.Fatalf("migration lost precision: %+v %v", p, err)
	}
	r.Before = exact.Add(time.Nanosecond)
	for _, want := range []int{200, 1, 0} {
		p, err := s.PruneResults(context.Background(), r)
		if err != nil || len(p.OperationIDs) != want {
			t.Fatalf("backfill batch: got %d want %d: %v", len(p.OperationIDs), want, err)
		}
	}
	var violations int
	if err := s.db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("foreign keys: %d %v", violations, err)
	}
}

func TestV5MigrationMalformedTimestampRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollback-v5.db")
	old := v3Store(t, path)
	tx, err := old.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateV4(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	op := legacySubmit(t, old, request())
	c := legacyClaim(t, old, op)
	legacyComplete(t, old, c, []byte("keep"))
	if _, err := old.db.Exec(`UPDATE operations SET completed_at='invalid' WHERE id=?`, op.ID); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(context.Background(), path); err == nil {
		s.Close()
		t.Fatal("invalid timestamp migrated")
	}
	var version, columns, tables int
	for _, q := range []struct {
		sql string
		out *int
	}{
		{"PRAGMA user_version", &version},
		{"SELECT count(*) FROM pragma_table_info('operations') WHERE name='result_pruned'", &columns},
		{"SELECT count(*) FROM sqlite_master WHERE name IN ('result_retention','namespace_limits')", &tables},
	} {
		if err := old.db.QueryRow(q.sql).Scan(q.out); err != nil {
			t.Fatal(err)
		}
	}
	if version != 4 || columns != 0 || tables != 0 {
		t.Fatalf("partial migration version=%d columns=%d tables=%d", version, columns, tables)
	}
}
