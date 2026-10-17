package taskfused

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func execFaultSQL(t *testing.T, s *Store, query string) {
	t.Helper()
	if _, err := s.db.Exec(query); err != nil {
		t.Fatal(err)
	}
}

// No engine hooks: a trigger rejects a statement, or inserts a deferred foreign
// key violation whose statements succeed but COMMIT must reject the transaction.
func installFault(t *testing.T, s *Store, target, kind string) func() {
	t.Helper()
	action := "SELECT RAISE(ABORT,'injected statement failure');"
	if kind == "commit" {
		execFaultSQL(t, s, `CREATE TABLE fault_commit(parent TEXT REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED);`)
		action = `INSERT INTO fault_commit(parent) VALUES('missing-fault-parent');`
	}
	execFaultSQL(t, s, "CREATE TRIGGER fault_injection AFTER "+target+" BEGIN "+action+" END;")
	return func() {
		execFaultSQL(t, s, `DROP TRIGGER fault_injection;`)
		if kind == "commit" {
			execFaultSQL(t, s, `DROP TABLE fault_commit;`)
		}
	}
}

func storedSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	result := map[string][][]any{}
	for _, table := range []string{"operations", "attempts", "events", "retry_actions", "result_retention", "namespace_limits", "sqlite_sequence"} {
		rows, err := s.db.Query("SELECT * FROM " + table + " ORDER BY 1,2")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		data := [][]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			data = append(data, values)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		result[table] = data
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMutationRollbackAtStatementAndCommitBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"statement", "commit"} {
		for _, name := range []string{"submit", "claim", "reclaim", "exhaustion", "renew", "complete", "fail", "retry_schedule", "cancel", "retry", "prune", "limits_insert", "limits_update"} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				s := openTest(t, filepath.Join(t.TempDir(), "fault.db"))
				clock := newClock()
				s.now = clock.now
				target := "INSERT ON events WHEN NEW.kind='submitted'"
				var action func() error
				switch name {
				case "submit":
					action = func() error { _, err := s.Submit(ctx, request()); return err }
				case "claim":
					submitTest(t, s, request())
					target = "INSERT ON events WHEN NEW.kind='claimed'"
					action = func() error { _, err := s.Claim(ctx, claimRequest()); return err }
				case "reclaim", "exhaustion":
					r := request()
					if name == "exhaustion" {
						r.MaxAttempts = 1
					}
					submitTest(t, s, r)
					claimTest(t, s)
					clock.advance(time.Second)
					target = "INSERT ON events WHEN NEW.kind='claimed'"
					if name == "exhaustion" {
						target = "INSERT ON events WHEN NEW.kind='failed'"
					}
					action = func() error {
						_, err := s.Claim(ctx, claimRequest())
						if name == "exhaustion" && errors.Is(err, ErrNoWork) {
							return nil
						}
						return err
					}
				case "renew":
					submitTest(t, s, request())
					c := claimTest(t, s)
					clock.advance(10 * time.Millisecond)
					target = "UPDATE ON attempts WHEN NEW.lease_until_ns!=OLD.lease_until_ns"
					action = func() error { _, err := s.Renew(ctx, c, time.Second); return err }
				case "complete":
					submitTest(t, s, request())
					c := claimTest(t, s)
					target = "INSERT ON events WHEN NEW.kind='succeeded'"
					action = func() error { return s.Complete(ctx, c, []byte("payload")) }
				case "fail", "retry_schedule":
					r := request()
					if name == "fail" {
						r.MaxAttempts = 1
					}
					submitTest(t, s, r)
					c := claimTest(t, s)
					event := "failed"
					if name == "retry_schedule" {
						event = "retry_scheduled"
					}
					target = "INSERT ON events WHEN NEW.kind='" + event + "'"
					action = func() error { return s.Fail(ctx, c, "handler failed") }
				case "cancel":
					op := submitTest(t, s, request())
					claimTest(t, s)
					target = "INSERT ON events WHEN NEW.kind='canceled'"
					action = func() error { _, err := s.Cancel(ctx, op.Namespace, op.ID); return err }
				case "retry":
					op, _ := failedTest(t, s)
					target = "INSERT ON events WHEN NEW.kind='retry_requested'"
					action = func() error { _, err := s.Retry(ctx, retryRequest(op)); return err }
				case "prune":
					op := submitTest(t, s, request())
					c := claimTest(t, s)
					if err := s.Complete(ctx, c, []byte("retained result")); err != nil {
						t.Fatal(err)
					}
					target = "INSERT ON events WHEN NEW.kind='result_pruned'"
					action = func() error {
						_, err := s.PruneResults(ctx, PruneRequest{Namespace: op.Namespace, Before: clock.now().Add(time.Second)})
						return err
					}
				case "limits_insert", "limits_update":
					target = "INSERT ON namespace_limits"
					if name == "limits_update" {
						if err := s.SetLimits(ctx, request().Namespace, Limits{MaxActive: 3}); err != nil {
							t.Fatal(err)
						}
						target = "UPDATE ON namespace_limits"
					}
					action = func() error { return s.SetLimits(ctx, request().Namespace, Limits{MaxActive: 1, MaxOperations: 5}) }
				}
				disable := installFault(t, s, target, kind)
				before := storedSnapshot(t, s)
				err := action()
				if err == nil {
					t.Fatal("faulted mutation reported success")
				}
				var storageErr sqlite3.Error
				if !errors.As(err, &storageErr) || storageErr.Code != sqlite3.ErrConstraint {
					t.Fatalf("unexpected injected error: %v", err)
				}
				if kind == "commit" && storageErr.ExtendedCode != sqlite3.ErrConstraintForeignKey {
					t.Fatalf("expected deferred commit failure: %v", err)
				}
				if after := storedSnapshot(t, s); after != before {
					t.Fatalf("%s failure left partial durable mutation", kind)
				}
				disable()
				if err := action(); err != nil {
					t.Fatalf("same action could not recover after fault removal: %v", err)
				}
				if storedSnapshot(t, s) == before {
					t.Fatal("successful recovery did not change durable state")
				}
				var violations int
				if err := s.db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil || violations != 0 {
					t.Fatalf("foreign keys after recovery: %d %v", violations, err)
				}
			})
		}
	}
}

func limitPages(t *testing.T, s *Store) func() {
	t.Helper()
	var pages int
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	execFaultSQL(t, s, fmt.Sprintf("PRAGMA max_page_count=%d", pages))
	return func() { execFaultSQL(t, s, "PRAGMA max_page_count=1073741824") }
}
func expectSQLite(t *testing.T, err error, code sqlite3.ErrNo) {
	t.Helper()
	var se sqlite3.Error
	if !errors.As(err, &se) || se.Code != code {
		t.Fatalf("expected SQLite code %d, got %v", code, err)
	}
}

func TestSQLiteFullRollsBackSubmissionAndResult(t *testing.T) {
	for _, name := range []string{"submit", "complete"} {
		t.Run(name, func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "full.db"))
			clock := newClock()
			s.now = clock.now
			ctx := context.Background()
			payload := []byte(strings.Repeat("x", MaxResultBytes))
			var action func() error
			if name == "submit" {
				r := request()
				r.Input = payload
				action = func() error { _, err := s.Submit(ctx, r); return err }
			} else {
				submitTest(t, s, request())
				c := claimTest(t, s)
				action = func() error { return s.Complete(ctx, c, payload) }
			}
			restore := limitPages(t, s)
			before := storedSnapshot(t, s)
			expectSQLite(t, action(), sqlite3.ErrFull)
			if storedSnapshot(t, s) != before {
				t.Fatal("SQLITE_FULL left partial records/result")
			}
			restore()
			if err := action(); err != nil {
				t.Fatalf("could not recover after lifting page limit: %v", err)
			}
		})
	}
}

func TestReadOnlySQLiteRejectsWritesWithoutLosingReadableWork(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "readonly.db")
	writer := openTest(t, path)
	op := submitTest(t, writer, request())
	writer.Close()
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("_foreign_keys", "on")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	readOnly := &Store{db: db, now: time.Now}
	defer readOnly.Close()
	before := storedSnapshot(t, readOnly)
	r := request()
	r.Key = "cannot-write"
	_, err = readOnly.Submit(ctx, r)
	expectSQLite(t, err, sqlite3.ErrReadonly)
	if storedSnapshot(t, readOnly) != before {
		t.Fatal("read-only mutation changed data")
	}
	if got := getTest(t, readOnly, op); got.State != StateQueued || string(got.Input) != string(op.Input) {
		t.Fatalf("read-only Get: %+v", got)
	}
	reopened := openTest(t, path)
	submitTest(t, reopened, r)
}

func TestWriterContentionReturnsBusyAndCanBeRetried(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "locked.db")
	s := openTest(t, path)
	other := openTest(t, path)
	execFaultSQL(t, other, "PRAGMA busy_timeout=25")
	before := storedSnapshot(t, other)
	holder, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	_, err = other.Submit(ctx, request())
	expectSQLite(t, err, sqlite3.ErrBusy)
	if storedSnapshot(t, other) != before {
		t.Fatal("busy admission wrote records")
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	submitTest(t, other, request())
}

func TestResultCommitFailureCanRepeatEffectsUnlessHandlerDeduplicates(t *testing.T) {
	for _, deduplicate := range []bool{false, true} {
		t.Run(fmt.Sprint(deduplicate), func(t *testing.T) {
			ctx := context.Background()
			s := openTest(t, filepath.Join(t.TempDir(), "effects.db"))
			clock := newClock()
			s.now = clock.now
			op := submitTest(t, s, request())
			invocations, effects := 0, 0
			seen := map[string]bool{}
			worker := workerTest(t, s, MinLease, func(_ context.Context, c Claim) ([]byte, error) {
				invocations++
				if !deduplicate || !seen[c.Operation.ID] {
					effects++
					seen[c.Operation.ID] = true
				}
				return []byte("effect complete"), nil
			})
			disable := installFault(t, s, "INSERT ON events WHEN NEW.kind='succeeded'", "commit")
			worked, err := worker.RunOnce(ctx)
			if !worked || err == nil {
				t.Fatalf("result fault not surfaced: %v %v", worked, err)
			}
			got := getTest(t, s, op)
			if got.State != StateRunning || got.AttemptCount != 1 || got.Result != nil {
				t.Fatalf("failed commit persisted outcome: %+v", got)
			}
			disable()
			clock.advance(MinLease)
			worked, err = worker.RunOnce(ctx)
			if !worked || err != nil {
				t.Fatalf("expiry recovery: %v %v", worked, err)
			}
			got = getTest(t, s, op)
			want := 2
			if deduplicate {
				want = 1
			}
			if got.State != StateSucceeded || got.AttemptCount != 2 || invocations != 2 || effects != want {
				t.Fatalf("effect boundary: state=%s attempts=%d invocations=%d effects=%d", got.State, got.AttemptCount, invocations, effects)
			}
		})
	}
}

func TestDeferredPruneCommitFailureRestoresWholeBatch(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "prune-commit.db"))
	for i := 0; i < 3; i++ {
		r := request()
		r.Key = fmt.Sprint(i)
		submitTest(t, s, r)
		c := claimTest(t, s)
		if err := s.Complete(ctx, c, []byte("keep")); err != nil {
			t.Fatal(err)
		}
	}
	disable := installFault(t, s, "INSERT ON events WHEN NEW.kind='result_pruned'", "commit")
	before := storedSnapshot(t, s)
	pruneRequest := PruneRequest{Namespace: request().Namespace, Before: time.Now().Add(time.Hour), Limit: 2}
	if _, err := s.PruneResults(ctx, pruneRequest); err == nil {
		t.Fatal("batch commit unexpectedly succeeded")
	}
	if storedSnapshot(t, s) != before {
		t.Fatal("failed commit removed part of result batch/history")
	}
	disable()
	for _, want := range []int{2, 1, 0} {
		result, err := s.PruneResults(ctx, pruneRequest)
		if err != nil || len(result.OperationIDs) != want {
			t.Fatalf("recovered prune batch: %+v want%d %v", result, want, err)
		}
	}
}

func TestRenewalStorageFailureCancelsHandlerAndAllowsExpiryRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s := openTest(t, filepath.Join(t.TempDir(), "renewal.db"))
	clock := newClock()
	s.now = clock.now
	op := submitTest(t, s, request())
	observed := false
	worker := workerTest(t, s, MinLease, func(ctx context.Context, _ Claim) ([]byte, error) {
		<-ctx.Done()
		observed = true
		return nil, ctx.Err()
	})
	disable := installFault(t, s, "UPDATE ON attempts WHEN NEW.state='running'", "statement")
	worked, err := worker.RunOnce(ctx)
	if !worked || err == nil || !observed {
		t.Fatalf("renewal failure not propagated cooperatively: %v %v observed=%v", worked, err, observed)
	}
	var se sqlite3.Error
	if !errors.As(err, &se) {
		t.Fatalf("renewal error was replaced: %v", err)
	}
	got := getTest(t, s, op)
	if got.State != StateRunning || got.AttemptCount != 1 {
		t.Fatalf("renewal error manufactured outcome: %+v", got)
	}
	disable()
	clock.advance(MinLease)
	recovery := workerTest(t, s, MinLease, func(context.Context, Claim) ([]byte, error) { return []byte("recovered"), nil })
	if worked, err := recovery.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("renewal recovery: %v %v", worked, err)
	}
	if got := getTest(t, s, op); got.State != StateSucceeded || got.AttemptCount != 2 {
		t.Fatalf("recovered outcome: %+v", got)
	}
}
