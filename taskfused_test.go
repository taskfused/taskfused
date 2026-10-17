package taskfused

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func request() Request {
	return Request{Namespace: "account-a", Handler: "report", Version: "v1", Key: "2026-10", Input: []byte(`{"month":"2026-10"}`), JoinExisting: true}
}
func openTest(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSubmissionIdentityAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	s := openTest(t, path)
	r := request()
	first, err := s.Submit(ctx, r)
	if err != nil || first.Joined || first.Operation.State != "queued" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	joined, err := s.Submit(ctx, r)
	if err != nil || !joined.Joined || joined.Operation.ID != first.Operation.ID {
		t.Fatalf("join = %+v, %v", joined, err)
	}
	r.Input = []byte("changed")
	if _, err := s.Submit(ctx, r); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict: %v", err)
	}
	r = request()
	r.JoinExisting = false
	if _, err := s.Submit(ctx, r); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTest(t, path)
	got, err := reopened.Get(ctx, r.Namespace, first.Operation.ID)
	if err != nil || string(got.Input) != string(r.Input) || !got.CreatedAt.Equal(first.Operation.CreatedAt) {
		t.Fatalf("get = %+v, %v", got, err)
	}
	again, err := reopened.Submit(ctx, request())
	if err != nil || !again.Joined || again.Operation.ID != got.ID {
		t.Fatalf("restart join = %+v, %v", again, err)
	}
	if _, err := reopened.Get(ctx, "account-b", got.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross namespace get: %v", err)
	}
	var count int
	if err := reopened.db.QueryRow("SELECT count(*) FROM events").Scan(&count); err != nil || count != 1 {
		t.Fatalf("events = %d, %v", count, err)
	}
}

func TestIdentitySeparatesNamespaceHandlerVersionAndKey(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	requests := []Request{request(), request(), request(), request(), request()}
	requests[1].Namespace = "account-b"
	requests[2].Handler = "thumbnail"
	requests[3].Version = "v2"
	requests[4].Key = "2026-11"
	ids := map[string]bool{}
	for _, r := range requests {
		result, err := s.Submit(context.Background(), r)
		if err != nil || result.Joined || ids[result.Operation.ID] {
			t.Fatalf("identity = %+v, %v", result, err)
		}
		ids[result.Operation.ID] = true
	}
}

func TestConcurrentSubmissionsAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	stores := []*Store{openTest(t, path), openTest(t, path), openTest(t, path), openTest(t, path)}
	var wg sync.WaitGroup
	results := make(chan Submission, 100)
	errs := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, e := stores[i%len(stores)].Submit(context.Background(), request())
			if e != nil {
				errs <- e
				return
			}
			results <- r
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var id string
	created, total := 0, 0
	for r := range results {
		total++
		if id == "" {
			id = r.Operation.ID
		}
		if r.Operation.ID != id {
			t.Fatal("duplicate operations were created")
		}
		if !r.Joined {
			created++
		}
	}
	if total != 100 || created != 1 {
		t.Fatalf("total %d, created %d", total, created)
	}
}

func TestSubmissionAndEventAreAtomic(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	if _, err := s.db.Exec(`CREATE TRIGGER reject_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT, 'injected event failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(context.Background(), request()); err == nil {
		t.Fatal("expected injected failure")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM operations").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial operation committed: %d, %v", count, err)
	}
}

func TestValidationAndCanceledContext(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	cases := []Request{request(), request(), request(), request(), request(), request()}
	cases[0].Namespace = " "
	cases[1].Handler = ""
	cases[2].Version = "v\x00bad"
	cases[3].Key = ""
	cases[4].Input = make([]byte, MaxInputBytes+1)
	cases[5].Key = string(make([]byte, 257))
	for i, r := range cases {
		if _, err := s.Submit(context.Background(), r); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Submit(ctx, request()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	r := request()
	r.Input = nil
	a, err := s.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	r.Input = []byte{}
	b, err := s.Submit(context.Background(), r)
	if err != nil || !b.Joined || b.Operation.ID != a.Operation.ID {
		t.Fatalf("empty input: %+v, %v", b, err)
	}
}

func TestRefuseUnrelatedOrFutureSchema(t *testing.T) {
	for _, setup := range []string{"CREATE TABLE unrelated (id INTEGER)", "PRAGMA user_version=99", "PRAGMA user_version=1"} {
		path := filepath.Join(t.TempDir(), "foreign.db")
		db, err := sql.Open("sqlite3", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(setup); err != nil {
			t.Fatal(err)
		}
		db.Close()
		if s, err := Open(context.Background(), path); err == nil {
			s.Close()
			t.Fatalf("accepted foreign schema %q", setup)
		}
	}
}

// A separate process commits work and exits without closing SQLite. This checks
// persistence beyond connection reuse and exercises recovery with WAL sidecars.
func TestProcessHelper(t *testing.T) {
	if os.Getenv("TASKFUSED_TEST_HELPER") != "1" {
		return
	}
	s, err := Open(context.Background(), os.Getenv("TASKFUSED_TEST_DB"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	result, err := s.Submit(context.Background(), request())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	if os.Getenv("TASKFUSED_TEST_ACTION") == "wait" {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "waiting"}); err != nil {
			os.Exit(7)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		op, err := s.Wait(ctx, result.Operation.Namespace, result.Operation.ID)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(8)
		}
		if err := json.NewEncoder(os.Stdout).Encode(op); err != nil {
			os.Exit(9)
		}
		os.Exit(0)
	}
	if os.Getenv("TASKFUSED_TEST_ACTION") == "claim" {
		c, err := s.Claim(context.Background(), ClaimRequest{Namespace: request().Namespace, Handler: request().Handler, Version: request().Version, LeaseDuration: MinLease})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(5)
		}
		if err := json.NewEncoder(os.Stdout).Encode(c); err != nil {
			os.Exit(6)
		}
		os.Exit(0)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(4)
	}
	os.Exit(0) // Deliberately bypass Store.Close and test cleanup.
}

func helper(path string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
	cmd.Env = append(os.Environ(), "TASKFUSED_TEST_HELPER=1", "TASKFUSED_TEST_DB="+path)
	return cmd
}

func TestAcceptanceSurvivesAbruptProcessExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	output, err := helper(path).CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v: %s", err, output)
	}
	var accepted Submission
	if err := json.Unmarshal(output, &accepted); err != nil {
		t.Fatal(err)
	}
	s := openTest(t, path)
	got, err := s.Get(context.Background(), request().Namespace, accepted.Operation.ID)
	if err != nil || got.ID != accepted.Operation.ID {
		t.Fatalf("recovery: %+v, %v", got, err)
	}
}

func TestConcurrentSubmissionsAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	openTest(t, path)
	var wg sync.WaitGroup
	results := make(chan Submission, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			output, err := helper(path).CombinedOutput()
			if err != nil {
				errs <- fmt.Errorf("helper: %w: %s", err, output)
				return
			}
			var r Submission
			if err := json.Unmarshal(output, &r); err != nil {
				errs <- err
				return
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var id string
	created := 0
	for r := range results {
		if id == "" {
			id = r.Operation.ID
		}
		if r.Operation.ID != id {
			t.Fatal("processes accepted different operations")
		}
		if !r.Joined {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created %d operations", created)
	}
}
