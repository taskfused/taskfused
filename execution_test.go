package taskfused

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct{ ns atomic.Int64 }

func newClock() *testClock {
	c := &testClock{}
	c.ns.Store(time.Date(2026, 10, 17, 18, 0, 0, 0, time.UTC).UnixNano())
	return c
}
func (c *testClock) now() time.Time          { return time.Unix(0, c.ns.Load()).UTC() }
func (c *testClock) advance(d time.Duration) { c.ns.Add(int64(d)) }
func claimRequest() ClaimRequest {
	r := request()
	return ClaimRequest{Namespace: r.Namespace, Handler: r.Handler, Version: r.Version, LeaseDuration: time.Second}
}
func submitTest(t *testing.T, s *Store, r Request) Operation {
	t.Helper()
	result, err := s.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return result.Operation
}
func claimTest(t *testing.T, s *Store) Claim {
	t.Helper()
	c, err := s.Claim(context.Background(), claimRequest())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func getTest(t *testing.T, s *Store, op Operation) Operation {
	t.Helper()
	got, err := s.Get(context.Background(), op.Namespace, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLeaseExpiryInvalidatesAllMutationsBeforeAndAfterReclaim(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	op := submitTest(t, s, request())
	first := claimTest(t, s)
	if first.Operation.ID != op.ID || first.Attempt.Token != 1 || first.Attempt.Number != 1 {
		t.Fatalf("first claim: %+v", first)
	}
	clock.advance(time.Second) // Exact boundary: lease is no longer valid.
	for name, action := range map[string]func() error{
		"renew":    func() error { _, err := s.Renew(context.Background(), first, time.Second); return err },
		"complete": func() error { return s.Complete(context.Background(), first, []byte("stale")) },
		"fail":     func() error { return s.Fail(context.Background(), first, "stale failure") },
	} {
		if err := action(); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("%s before reclaim: %v", name, err)
		}
	}
	second := claimTest(t, s)
	if second.Attempt.ID == first.Attempt.ID || second.Attempt.Token != 2 || second.Operation.ID != op.ID {
		t.Fatalf("reclaim: %+v", second)
	}
	if err := s.Complete(context.Background(), first, []byte("stale")); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("late complete: %v", err)
	}
	if err := s.Fail(context.Background(), first, "stale failure"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("late failure: %v", err)
	}
	if _, err := s.Renew(context.Background(), first, time.Second); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("late renew: %v", err)
	}
	if err := s.Complete(context.Background(), second, []byte("current")); err != nil {
		t.Fatal(err)
	}
	got := getTest(t, s, op)
	if got.State != StateSucceeded || string(got.Result) != "current" || got.CompletedAt == nil {
		t.Fatalf("result: %+v", got)
	}
	history, err := s.Attempts(context.Background(), op.Namespace, op.ID)
	if err != nil || len(history) != 2 || history[0].State != "expired" || history[1].State != StateSucceeded {
		t.Fatalf("history: %+v %v", history, err)
	}
	joined, err := s.Submit(context.Background(), request())
	if err != nil || !joined.Joined || joined.Operation.State != StateSucceeded {
		t.Fatalf("terminal join: %+v %v", joined, err)
	}
	if _, err := s.Claim(context.Background(), claimRequest()); !errors.Is(err, ErrNoWork) {
		t.Fatalf("reran terminal operation: %v", err)
	}
}

func TestRenewNeverShortensLease(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	submitTest(t, s, request())
	c := claimTest(t, s)
	short, err := s.Renew(context.Background(), c, MinLease)
	if err != nil || !short.Equal(c.Attempt.LeaseUntil) {
		t.Fatalf("shortened lease: %v %v", short, err)
	}
	clock.advance(500 * time.Millisecond)
	extended, err := s.Renew(context.Background(), c, time.Second)
	if err != nil || !extended.Equal(c.Attempt.LeaseUntil.Add(500*time.Millisecond)) {
		t.Fatalf("renew: %v %v", extended, err)
	}
	clock.advance(600 * time.Millisecond)
	if err := s.Complete(context.Background(), c, nil); err != nil {
		t.Fatalf("renewal was not persisted: %v", err)
	}
}

func TestRetriesBackoffAndBudget(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	r := request()
	r.MaxAttempts = 3
	r.RetryDelay = 100 * time.Millisecond
	op := submitTest(t, s, r)
	for attempt := 1; attempt <= 3; attempt++ {
		c := claimTest(t, s)
		if c.Attempt.Number != attempt {
			t.Fatalf("attempt number %d", c.Attempt.Number)
		}
		if err := s.Fail(context.Background(), c, "handler failed"); err != nil {
			t.Fatal(err)
		}
		got := getTest(t, s, op)
		if attempt == 3 {
			if got.State != StateFailed || got.CompletedAt == nil || got.LastError != "handler failed" {
				t.Fatalf("terminal: %+v", got)
			}
			break
		}
		if got.State != StateRetryWait || got.CompletedAt != nil {
			t.Fatalf("retry: %+v", got)
		}
		delay := backoff(r.RetryDelay, attempt)
		clock.advance(delay - time.Nanosecond)
		if _, err := s.Claim(context.Background(), claimRequest()); !errors.Is(err, ErrNoWork) {
			t.Fatalf("early retry: %v", err)
		}
		clock.advance(time.Nanosecond)
	}
	if _, err := s.Claim(context.Background(), claimRequest()); !errors.Is(err, ErrNoWork) {
		t.Fatalf("budget: %v", err)
	}
	if backoff(time.Hour, 100) != time.Hour || backoff(time.Millisecond, 100) != time.Hour {
		t.Fatal("backoff did not saturate")
	}
	same := r
	same.MaxAttempts = 4
	if _, err := s.Submit(context.Background(), same); !errors.Is(err, ErrPolicyConflict) {
		t.Fatalf("policy mutation: %v", err)
	}
	joined, err := s.Submit(context.Background(), r)
	if err != nil || !joined.Joined || joined.Operation.State != StateFailed {
		t.Fatalf("failed join reran work: %+v %v", joined, err)
	}
}

func TestCrashedAttemptExhaustsBudget(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	r := request()
	r.MaxAttempts = 1
	op := submitTest(t, s, r)
	c := claimTest(t, s)
	clock.advance(time.Second)
	if _, err := s.Claim(context.Background(), claimRequest()); !errors.Is(err, ErrNoWork) {
		t.Fatalf("budget reclaim: %v", err)
	}
	got := getTest(t, s, op)
	if got.State != StateFailed || got.AttemptCount != 1 || got.CompletedAt == nil {
		t.Fatalf("budget state: %+v", got)
	}
	if err := s.Complete(context.Background(), c, nil); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired final result: %v", err)
	}
}

func TestConcurrentClaimsHaveOneOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	stores := []*Store{openTest(t, path), openTest(t, path), openTest(t, path), openTest(t, path)}
	submitTest(t, stores[0], request())
	var owners atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := stores[i%4].Claim(context.Background(), claimRequest())
			if err == nil {
				owners.Add(1)
			} else if !errors.Is(err, ErrNoWork) {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if owners.Load() != 1 {
		t.Fatalf("owners: %d", owners.Load())
	}
}

func TestClaimAndCompletionRollbackOnEventFailure(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	op := submitTest(t, s, request())
	if _, err := s.db.Exec(`CREATE TRIGGER reject_claim BEFORE INSERT ON events WHEN NEW.kind='claimed' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(context.Background(), claimRequest()); err == nil {
		t.Fatal("claim committed without event")
	}
	got := getTest(t, s, op)
	history, _ := s.Attempts(context.Background(), op.Namespace, op.ID)
	if got.State != StateQueued || got.AttemptCount != 0 || len(history) != 0 {
		t.Fatalf("partial claim: %+v %+v", got, history)
	}
	s.db.Exec("DROP TRIGGER reject_claim")
	c := claimTest(t, s)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_result BEFORE INSERT ON events WHEN NEW.kind='succeeded' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(context.Background(), c, []byte("answer")); err == nil {
		t.Fatal("result committed without event")
	}
	got = getTest(t, s, op)
	history, _ = s.Attempts(context.Background(), op.Namespace, op.ID)
	if got.State != StateRunning || got.Result != nil || history[0].State != StateRunning {
		t.Fatalf("partial result: %+v %+v", got, history)
	}
	s.db.Exec("DROP TRIGGER reject_result")
	if err := s.Complete(context.Background(), c, []byte("answer")); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationPreservesV1OperationsAndEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
 CREATE TABLE taskfused_meta(name TEXT PRIMARY KEY,value TEXT NOT NULL);
 INSERT INTO taskfused_meta VALUES('application','taskfused');
 CREATE TABLE operations(id TEXT PRIMARY KEY,namespace TEXT NOT NULL,handler TEXT NOT NULL,handler_version TEXT NOT NULL,operation_key TEXT NOT NULL,input BLOB NOT NULL,input_sha256 TEXT NOT NULL,state TEXT NOT NULL CHECK(state='queued'),created_at TEXT NOT NULL,UNIQUE(namespace,handler,handler_version,operation_key));
 CREATE TABLE events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,operation_id TEXT NOT NULL REFERENCES operations(id),kind TEXT NOT NULL,occurred_at TEXT NOT NULL);
 INSERT INTO operations VALUES('op_legacy','legacy','echo','v1','key',x'6869','8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4','queued','2026-10-17T00:00:00Z');
 INSERT INTO events VALUES(17,'op_legacy','submitted','2026-10-17T00:00:00Z');
 PRAGMA user_version=1;`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	s := openTest(t, path)
	got, err := s.Get(context.Background(), "legacy", "op_legacy")
	if err != nil || string(got.Input) != "hi" || got.MaxAttempts != 3 || got.RetryDelay != time.Second {
		t.Fatalf("migration: %+v %v", got, err)
	}
	joined, err := s.Submit(context.Background(), Request{Namespace: "legacy", Handler: "echo", Version: "v1", Key: "key", Input: []byte("hi"), JoinExisting: true})
	if err != nil || !joined.Joined || joined.Operation.ID != "op_legacy" {
		t.Fatalf("legacy identity: %+v %v", joined, err)
	}
	var sequence int
	if err := s.db.QueryRow("SELECT sequence FROM events WHERE operation_id='op_legacy'").Scan(&sequence); err != nil || sequence != 17 {
		t.Fatalf("event identity: %d %v", sequence, err)
	}
	c, err := s.Claim(context.Background(), ClaimRequest{Namespace: "legacy", Handler: "echo", Version: "v1", LeaseDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(context.Background(), c, []byte("migrated")); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&count); err != nil || count != 0 {
		t.Fatalf("foreign keys: %d %v", count, err)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("schema: %d %v", version, err)
	}
	s.Close()
	reopened := openTest(t, path)
	got = getTest(t, reopened, got)
	if got.State != StateSucceeded || string(got.Result) != "migrated" {
		t.Fatalf("reopen: %+v", got)
	}
}

func TestWorkerCrashRecoveryAcrossProcessExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.db")
	cmd := helper(path)
	cmd.Env = append(cmd.Env, "TASKFUSED_TEST_ACTION=claim")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v %s", err, output)
	}
	var old Claim
	if err := json.Unmarshal(output, &old); err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(old.Attempt.LeaseUntil); remaining > 0 {
		timer := time.NewTimer(remaining)
		<-timer.C
	}
	s := openTest(t, path)
	worker, err := NewWorker(s, WorkerOptions{Namespace: request().Namespace, Handlers: []Registration{{Name: request().Handler, Version: request().Version, Handle: func(ctx context.Context, c Claim) ([]byte, error) {
		if c.Operation.ID != old.Operation.ID || c.Attempt.ID == old.Attempt.ID || c.Attempt.Number != 2 {
			t.Errorf("recovered claim: %+v", c)
		}
		return []byte("recovered"), nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := worker.RunOnce(context.Background()); err != nil || !worked {
		t.Fatalf("recovery: %v %v", worked, err)
	}
	got := getTest(t, s, old.Operation)
	if got.State != StateSucceeded || string(got.Result) != "recovered" {
		t.Fatalf("result: %+v", got)
	}
	if err := s.Complete(context.Background(), old, []byte("stale")); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old process token: %v", err)
	}
}

func TestExecutionValidationAndNamespaceIsolation(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	r := request()
	r.MaxAttempts = 101
	if _, err := s.Submit(context.Background(), r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("attempt limit: %v", err)
	}
	r = request()
	r.RetryDelay = -1
	if _, err := s.Submit(context.Background(), r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("retry delay: %v", err)
	}
	op := submitTest(t, s, request())
	c := claimTest(t, s)
	wrong := c
	wrong.Operation.Namespace = "other"
	if err := s.Complete(context.Background(), wrong, nil); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("cross namespace commit: %v", err)
	}
	if _, err := s.Attempts(context.Background(), "other", op.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross namespace history: %v", err)
	}
	if err := s.Complete(context.Background(), c, make([]byte, MaxResultBytes+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("result bound: %v", err)
	}
	if err := s.Fail(context.Background(), c, strings.Repeat("x", MaxErrorBytes+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error bound: %v", err)
	}
	if err := s.Fail(context.Background(), c, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank failure: %v", err)
	}
	invalid := claimRequest()
	invalid.LeaseDuration = MinLease - 1
	if _, err := s.Claim(context.Background(), invalid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("lease duration: %v", err)
	}
}

func TestConcurrentSuccessAndFailureHaveOneCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	first := openTest(t, path)
	second := openTest(t, path)
	r := request()
	r.MaxAttempts = 1
	op := submitTest(t, first, r)
	c := claimTest(t, first)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- first.Complete(context.Background(), c, []byte("answer")) }()
	go func() { <-start; results <- second.Fail(context.Background(), c, "failure") }()
	close(start)
	wins, losses := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			wins++
		} else if errors.Is(err, ErrLeaseLost) {
			losses++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("commits=%d rejected=%d", wins, losses)
	}
	got := getTest(t, first, op)
	history, err := first.Attempts(context.Background(), op.Namespace, op.ID)
	if err != nil || len(history) != 1 || history[0].State != got.State {
		t.Fatalf("inconsistent winner: %+v %+v %v", got, history, err)
	}
	if got.State == StateSucceeded && string(got.Result) != "answer" {
		t.Fatal("success result missing")
	}
	if got.State == StateFailed && got.Result != nil {
		t.Fatal("failure retained losing result")
	}
}
