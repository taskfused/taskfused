package taskfused

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// Build a real v2 database through the frozen legacy migration, without calling
// Open (which now advances to v3). The old worker schema has strict state checks.
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
	active := submitTest(t, old, request())
	lease := claimRequest()
	lease.LeaseDuration = time.Minute
	claim, err := old.Claim(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	r := request()
	r.Key = "completed"
	complete := submitTest(t, old, r)
	completedClaim, err := old.Claim(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Complete(context.Background(), completedClaim, []byte("old result")); err != nil {
		t.Fatal(err)
	}
	r.Key = "queued"
	queued := submitTest(t, old, r)
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
	op := submitTest(t, old, request())
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
