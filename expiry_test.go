// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package taskfused

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func countRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// keyed returns a request with its own handler and key, so Claim picks it.
func keyed(handler string) Request {
	r := request()
	r.Handler = handler
	r.Key = handler
	return r
}

func claimFor(t *testing.T, s *Store, r Request) Claim {
	t.Helper()
	c, err := s.Claim(context.Background(), ClaimRequest{Namespace: r.Namespace, Handler: r.Handler, Version: r.Version, LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// One operation in every state: succeeded, failed, canceled, queued, running.
func everyState(t *testing.T, s *Store) map[string]Operation {
	t.Helper()
	ctx := context.Background()
	ops := map[string]Operation{}
	for _, name := range []string{"succeeded", "failed", "canceled", "queued", "running"} {
		r := keyed(name)
		if name == "failed" {
			r.MaxAttempts = 1
		}
		ops[name] = submitTest(t, s, r)
	}
	if err := s.Complete(ctx, claimFor(t, s, keyed("succeeded")), []byte("done")); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(ctx, claimFor(t, s, keyed("failed")), "boom"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(ctx, ops["canceled"].Namespace, ops["canceled"].ID); err != nil {
		t.Fatal(err)
	}
	claimFor(t, s, keyed("running"))
	for name, op := range ops {
		ops[name] = getTest(t, s, op)
		if string(ops[name].State) != name {
			t.Fatalf("%s is %s", name, ops[name].State)
		}
	}
	return ops
}

func TestExpireIdentitiesDeletesOnlyFinishedWorkPastRetention(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	ops := everyState(t, s)
	ns := request().Namespace

	// No retention set: nothing is ever expired.
	clock.advance(1000 * time.Hour)
	if r, err := s.ExpireIdentities(ctx, ExpireRequest{Namespace: ns}); err != nil || len(r.OperationIDs) != 0 || r.Cutoff != nil {
		t.Fatalf("without retention: %+v %v", r, err)
	}
	if err := s.SetLimits(ctx, ns, Limits{IdentityRetention: 2000 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ExpireIdentities(ctx, ExpireRequest{Namespace: ns}); err != nil || len(r.OperationIDs) != 0 || r.Cutoff == nil {
		t.Fatalf("inside retention: %+v %v", r, err)
	}
	if err := s.SetLimits(ctx, ns, Limits{IdentityRetention: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if u, err := s.Usage(ctx, ns); err != nil || u.Limits.IdentityRetention != time.Hour || u.Operations != 5 {
		t.Fatalf("usage before: %+v %v", u, err)
	}
	r, err := s.ExpireIdentities(ctx, ExpireRequest{Namespace: ns})
	if err != nil || len(r.OperationIDs) != 3 {
		t.Fatalf("expire: %+v %v", r, err)
	}
	gone := map[string]bool{}
	for _, id := range r.OperationIDs {
		gone[id] = true
	}
	for name, op := range ops {
		_, err := s.Get(ctx, op.Namespace, op.ID)
		finished := name == "succeeded" || name == "failed" || name == "canceled"
		if finished != gone[op.ID] {
			t.Fatalf("%s: expired=%v", name, gone[op.ID])
		}
		if finished && !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s still readable: %v", name, err)
		}
		if !finished && err != nil {
			t.Fatalf("%s lost: %v", name, err)
		}
	}
	for _, table := range []string{"events", "attempts", "retry_actions", "result_retention", "identity_expiry"} {
		for id := range gone {
			if n := countRows(t, s, "SELECT count(*) FROM "+table+" WHERE operation_id=?", id); n != 0 {
				t.Fatalf("%s keeps %d rows of an expired operation", table, n)
			}
		}
	}
	if u, err := s.Usage(ctx, ns); err != nil || u.Operations != 2 {
		t.Fatalf("usage after: %+v %v", u, err)
	}
	// The identity is free: the same key is new work, not a join.
	again, err := s.Submit(ctx, keyed("succeeded"))
	if err != nil || again.Joined || again.Operation.ID == ops["succeeded"].ID || again.Operation.State != StateQueued {
		t.Fatalf("resubmit: %+v %v", again, err)
	}
	// Different input under the old key is no longer a conflict either.
	other := keyed("failed")
	other.Input = []byte("something else")
	if _, err := s.Submit(ctx, other); err != nil {
		t.Fatalf("resubmit with other input: %v", err)
	}
	var violations int
	if err := s.db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("foreign keys: %d %v", violations, err)
	}
}

func TestExpireIdentitiesBatchesAndDrains(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	ns := request().Namespace
	for i := 0; i < 5; i++ {
		op := submitTest(t, s, keyed("job-"+string(rune('a'+i))))
		if _, err := s.Cancel(ctx, ns, op.ID); err != nil {
			t.Fatal(err)
		}
		clock.advance(time.Second)
	}
	if err := s.SetLimits(ctx, ns, Limits{IdentityRetention: time.Minute}); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Hour)
	var sizes []int
	for {
		r, err := s.ExpireIdentities(ctx, ExpireRequest{Namespace: ns, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.OperationIDs) == 0 {
			break
		}
		sizes = append(sizes, len(r.OperationIDs))
	}
	if len(sizes) != 3 || sizes[0] != 2 || sizes[1] != 2 || sizes[2] != 1 {
		t.Fatalf("batches %v", sizes)
	}
	for _, bad := range []int{-1, MaxExpireBatch + 1} {
		if _, err := s.ExpireIdentities(ctx, ExpireRequest{Namespace: ns, Limit: bad}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("limit %d: %v", bad, err)
		}
	}
	if _, err := s.ExpireIdentities(ctx, ExpireRequest{Namespace: ""}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty namespace: %v", err)
	}
	if err := s.SetLimits(ctx, ns, Limits{IdentityRetention: -time.Second}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative retention: %v", err)
	}
}

// Completion time decides, with the cutoff exclusive: work finished later
// than now minus retention stays.
func TestExpireIdentitiesUsesCompletionTime(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	ns := request().Namespace
	old := submitTest(t, s, keyed("old"))
	if _, err := s.Cancel(ctx, ns, old.ID); err != nil {
		t.Fatal(err)
	}
	// Submitted long ago, finished just now.
	late := submitTest(t, s, keyed("late"))
	clock.advance(50 * time.Minute)
	if _, err := s.Cancel(ctx, ns, late.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLimits(ctx, ns, Limits{IdentityRetention: 30 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	r, err := s.ExpireIdentities(ctx, ExpireRequest{Namespace: ns})
	if err != nil || len(r.OperationIDs) != 1 || r.OperationIDs[0] != old.ID {
		t.Fatalf("expire: %+v %v", r, err)
	}
	if !r.Cutoff.Equal(clock.now().Add(-30 * time.Minute)) {
		t.Fatalf("cutoff %v", r.Cutoff)
	}
}

// An operator retry reopens failed work, which then can't expire until it
// finishes again; its new completion time counts.
func TestReopenedOperationLeavesExpiry(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	ns := request().Namespace
	r := keyed("flaky")
	r.MaxAttempts = 1
	op := submitTest(t, s, r)
	if err := s.Fail(ctx, claimFor(t, s, r), "boom"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "SELECT count(*) FROM identity_expiry WHERE operation_id=?", op.ID); n != 1 {
		t.Fatalf("failed operation not filed: %d", n)
	}
	if _, err := s.Retry(ctx, RetryRequest{Namespace: ns, ID: op.ID, ActionKey: "again", ExpectedAttempts: 1, AdditionalAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "SELECT count(*) FROM identity_expiry WHERE operation_id=?", op.ID); n != 0 {
		t.Fatalf("reopened operation still filed: %d", n)
	}
	if err := s.SetLimits(ctx, ns, Limits{IdentityRetention: time.Minute}); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Hour)
	if r, err := s.ExpireIdentities(ctx, ExpireRequest{Namespace: ns}); err != nil || len(r.OperationIDs) != 0 {
		t.Fatalf("queued retry expired: %+v %v", r, err)
	}
	if err := s.Complete(ctx, claimFor(t, s, r), []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ExpireIdentities(ctx, ExpireRequest{Namespace: ns}); err != nil || len(r.OperationIDs) != 0 {
		t.Fatalf("just finished, expired anyway: %+v %v", r, err)
	}
	clock.advance(2 * time.Minute)
	if r, err := s.ExpireIdentities(ctx, ExpireRequest{Namespace: ns}); err != nil || len(r.OperationIDs) != 1 {
		t.Fatalf("finished again, not expired: %+v %v", r, err)
	}
}

func TestExpireDueDrainsEveryNamespaceWithRetention(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	for _, ns := range []string{"a", "b", "keep"} {
		for i := 0; i < MaxExpireBatch+3; i++ {
			r := request()
			r.Namespace = ns
			r.Key = ns + string(rune('A'+i%26)) + string(rune('a'+i/26))
			op := submitTest(t, s, r)
			if _, err := s.Cancel(ctx, ns, op.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, ns := range []string{"a", "b"} {
		if err := s.SetLimits(ctx, ns, Limits{IdentityRetention: time.Second}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetLimits(ctx, "keep", Limits{MaxActive: 5}); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Minute)
	n, err := s.ExpireDue(ctx)
	if err != nil || n != 2*(MaxExpireBatch+3) {
		t.Fatalf("ExpireDue: %d %v", n, err)
	}
	if u, _ := s.Usage(ctx, "keep"); u.Operations != MaxExpireBatch+3 {
		t.Fatalf("namespace without retention lost identities: %+v", u)
	}
	if n, err := s.ExpireDue(ctx); err != nil || n != 0 {
		t.Fatalf("second ExpireDue: %d %v", n, err)
	}
}

// A schema-5 file gains the table, the column and the finished operations it
// already holds.
func TestMigrationV5ToV6FilesFinishedWork(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v5.db")
	s := openTest(t, path)
	clock := newClock()
	s.now = clock.now
	ops := everyState(t, s)
	execFaultSQL(t, s, `DROP TRIGGER identity_expiry_terminal; DROP TRIGGER identity_expiry_reopened; DROP TABLE identity_expiry; ALTER TABLE namespace_limits DROP COLUMN identity_retention_ns; PRAGMA user_version=5;`)
	s.Close()
	s = openTest(t, path)
	s.now = clock.now
	if n := countRows(t, s, "SELECT count(*) FROM identity_expiry"); n != 3 {
		t.Fatalf("backfilled %d", n)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 6 {
		t.Fatalf("version %d %v", version, err)
	}
	if err := s.SetLimits(ctx, request().Namespace, Limits{IdentityRetention: time.Minute}); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Hour)
	r, err := s.ExpireIdentities(ctx, ExpireRequest{Namespace: request().Namespace})
	if err != nil || len(r.OperationIDs) != 3 {
		t.Fatalf("expire after migration: %+v %v", r, err)
	}
	if _, err := s.Get(ctx, ops["running"].Namespace, ops["running"].ID); err != nil {
		t.Fatal(err)
	}
}
