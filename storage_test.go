package taskfused

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func usageTest(t *testing.T, s *Store, ns string) Usage {
	t.Helper()
	u, err := s.Usage(context.Background(), ns)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAdmissionSerializedAcrossStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "capacity.db")
	s := openTest(t, path)
	other := openTest(t, path)
	ns := request().Namespace
	if err := s.SetLimits(ctx, ns, Limits{MaxOperations: 5, MaxActive: 3}); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		sub Submission
		err error
	}
	outcomes := make(chan outcome, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := request()
			r.Key = fmt.Sprint(i)
			store := s
			if i%2 == 0 {
				store = other
			}
			sub, err := store.Submit(ctx, r)
			outcomes <- outcome{sub, err}
		}(i)
	}
	wg.Wait()
	close(outcomes)
	accepted := []Operation{}
	for o := range outcomes {
		if o.err == nil {
			accepted = append(accepted, o.sub.Operation)
		} else if !errors.Is(o.err, ErrCapacity) {
			t.Fatal(o.err)
		}
	}
	if len(accepted) != 3 {
		t.Fatalf("admitted %d", len(accepted))
	}
	u := usageTest(t, other, ns)
	if u.Operations != 3 || u.Active != 3 || u.Events != 3 {
		t.Fatalf("admission leaked writes: %+v", u)
	}
	r := request()
	r.Key = accepted[0].Key
	if sub, err := other.Submit(ctx, r); err != nil || !sub.Joined {
		t.Fatalf("join at capacity: %+v %v", sub, err)
	}
	r.Input = []byte("different")
	if _, err := s.Submit(ctx, r); !errors.Is(err, ErrConflict) {
		t.Fatalf("capacity hid conflict: %v", err)
	}
	for _, op := range accepted {
		if _, err := s.Cancel(ctx, ns, op.ID); err != nil {
			t.Fatal(err)
		}
	}
	for i := 20; i < 22; i++ {
		r := request()
		r.Key = fmt.Sprint(i)
		op := submitTest(t, other, r)
		if _, err := s.Cancel(ctx, ns, op.ID); err != nil {
			t.Fatal(err)
		}
	}
	r = request()
	r.Key = "over-total"
	if _, err := other.Submit(ctx, r); !errors.Is(err, ErrCapacity) {
		t.Fatalf("terminal identities did not consume total limit: %v", err)
	}
	r.Namespace = "other-account"
	submitTest(t, s, r)
	s.Close()
	other.Close()
	reopened := openTest(t, path)
	if u := usageTest(t, reopened, ns); u.Limits.MaxOperations != 5 || u.Active != 0 || u.Operations != 5 {
		t.Fatalf("durability: %+v", u)
	}
	if err := reopened.SetLimits(ctx, ns, Limits{}); err != nil {
		t.Fatal(err)
	}
	r.Namespace = ns
	submitTest(t, reopened, r)
}

func TestRetryAdmissionAndReplay(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "retry.db"))
	ctx := context.Background()
	failed, _ := failedTest(t, s)
	r := request()
	r.Key = "occupies-slot"
	pending := submitTest(t, s, r)
	if err := s.SetLimits(ctx, r.Namespace, Limits{MaxOperations: 1, MaxActive: 1}); err != nil {
		t.Fatal(err)
	}
	before := usageTest(t, s, r.Namespace)
	action := retryRequest(failed)
	if _, err := s.Retry(ctx, action); !errors.Is(err, ErrCapacity) {
		t.Fatalf("retry bypassed active limit: %v", err)
	}
	if after := usageTest(t, s, r.Namespace); after != before {
		t.Fatalf("rejected retry mutated data: %+v -> %+v", before, after)
	}
	if _, err := s.Cancel(ctx, r.Namespace, pending.ID); err != nil {
		t.Fatal(err)
	}
	grant, err := s.Retry(ctx, action)
	if err != nil || !grant.Applied {
		t.Fatalf("existing identity subject to total limit: %+v %v", grant, err)
	}
	replay, err := s.Retry(ctx, action)
	if err != nil || replay.Applied {
		t.Fatalf("replay at capacity: %+v %v", replay, err)
	}
	c := claimTest(t, s)
	if err := s.Complete(ctx, c, []byte("recovered")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneResults(ctx, PruneRequest{Namespace: r.Namespace, Before: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	replay, err = s.Retry(ctx, action)
	if err != nil || replay.Applied || !replay.Operation.ResultPruned || replay.Operation.State != StateSucceeded {
		t.Fatalf("pruned replay: %+v %v", replay, err)
	}
}

func TestPruneCutoffIsolationAndIdentity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "retention.db")
	s := openTest(t, path)
	clock := newClock()
	s.now = clock.now
	original := request()
	op := submitTest(t, s, original)
	c := claimTest(t, s)
	payload := []byte(strings.Repeat("x", 65536))
	if err := s.Complete(ctx, c, payload); err != nil {
		t.Fatal(err)
	}
	completed := clock.now()
	r := PruneRequest{Namespace: op.Namespace, Before: completed, Limit: 1}
	if p, err := s.PruneResults(ctx, r); err != nil || len(p.OperationIDs) != 0 {
		t.Fatalf("cutoff must be exclusive: %+v %v", p, err)
	}
	// A completion only one nanosecond later must not be pruned by this cutoff.
	clock.advance(time.Nanosecond)
	newReq := request()
	newReq.Key = "newer"
	newer := submitTest(t, s, newReq)
	newClaim := claimTest(t, s)
	if err := s.Complete(ctx, newClaim, []byte{}); err != nil {
		t.Fatal(err)
	}
	r.Before = clock.now()
	wrong := r
	wrong.Namespace = "other"
	if p, err := s.PruneResults(ctx, wrong); err != nil || len(p.OperationIDs) != 0 {
		t.Fatalf("namespace isolation: %+v %v", p, err)
	}
	p, err := s.PruneResults(ctx, r)
	if err != nil || len(p.OperationIDs) != 1 || p.OperationIDs[0] != op.ID || p.RemovedBytes != int64(len(payload)) {
		t.Fatalf("prune: %+v %v", p, err)
	}
	got := getTest(t, s, op)
	if !got.ResultPruned || got.Result != nil || got.State != StateSucceeded || string(got.Input) != string(original.Input) || !got.CompletedAt.Equal(completed) {
		t.Fatalf("identity/outcome changed: %+v", got)
	}
	if getTest(t, s, newer).ResultPruned {
		t.Fatal("newer/empty result pruned early")
	}
	if joined, err := s.Submit(ctx, original); err != nil || !joined.Joined || !joined.Operation.ResultPruned {
		t.Fatalf("pruned duplicate: %+v %v", joined, err)
	}
	changed := original
	changed.Input = []byte("changed")
	if _, err := s.Submit(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("lost exact input comparison: %v", err)
	}
	if _, err := s.Claim(ctx, claimRequest()); !errors.Is(err, ErrNoWork) {
		t.Fatalf("pruning requeued work: %v", err)
	}
	waited, err := s.Wait(ctx, op.Namespace, op.ID)
	if err != nil || !waited.ResultPruned {
		t.Fatalf("pruned Wait: %+v %v", waited, err)
	}
	r.Before = r.Before.Add(time.Nanosecond)
	p, err = s.PruneResults(ctx, r)
	if err != nil || len(p.OperationIDs) != 1 || p.RemovedBytes != 0 {
		t.Fatalf("empty result pruning: %+v %v", p, err)
	}
	before := usageTest(t, s, op.Namespace)
	if p, err := s.PruneResults(ctx, r); err != nil || len(p.OperationIDs) != 0 {
		t.Fatalf("repeat: %+v %v", p, err)
	}
	if after := usageTest(t, s, op.Namespace); after != before || after.ResultBytes != 0 || after.PrunedResults != 2 || after.Attempts != 2 || after.Events != 8 {
		t.Fatalf("usage/history: %+v", after)
	}
	if err := s.Complete(ctx, c, []byte("late")); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("late result restored bytes: %v", err)
	}
	s.Close()
	s = openTest(t, path)
	if !getTest(t, s, op).ResultPruned {
		t.Fatal("prune did not survive restart")
	}
}

func TestPruneAtomicRollbackAndBoundedBatches(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "batch.db"))
	for i := 0; i < 3; i++ {
		r := request()
		r.Key = fmt.Sprint(i)
		submitTest(t, s, r)
		c := claimTest(t, s)
		if err := s.Complete(ctx, c, []byte("result")); err != nil {
			t.Fatal(err)
		}
	}
	// Force a failure after payload updates but before event insertion commits.
	if _, err := s.db.Exec(`CREATE TRIGGER reject_prune BEFORE INSERT ON events WHEN NEW.kind='result_pruned' BEGIN SELECT RAISE(ABORT,'injected prune failure'); END;`); err != nil {
		t.Fatal(err)
	}
	r := PruneRequest{Namespace: request().Namespace, Before: time.Now().Add(time.Hour), Limit: 2}
	before := usageTest(t, s, r.Namespace)
	if _, err := s.PruneResults(ctx, r); err == nil {
		t.Fatal("injected failure not surfaced")
	}
	if got := usageTest(t, s, r.Namespace); got != before {
		t.Fatalf("partial prune: %+v", got)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_prune`); err != nil {
		t.Fatal(err)
	}
	total := 0
	for {
		p, err := s.PruneResults(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.OperationIDs) > 2 {
			t.Fatal("batch unbounded")
		}
		total += len(p.OperationIDs)
		if len(p.OperationIDs) == 0 {
			break
		}
	}
	if total != 3 {
		t.Fatalf("pruned %d", total)
	}
}

func TestStorageValidation(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "validation.db"))
	ctx := context.Background()
	for _, ns := range []string{"", " ", "bad\x00", strings.Repeat("n", 257)} {
		if err := s.SetLimits(ctx, ns, Limits{}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("namespace accepted: %q %v", ns, err)
		}
		if _, err := s.Usage(ctx, ns); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := s.PruneResults(ctx, PruneRequest{Namespace: ns, Before: time.Now()}); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, limits := range []Limits{{MaxOperations: -1}, {MaxActive: -1}} {
		if err := s.SetLimits(ctx, "ns", limits); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, r := range []PruneRequest{{Namespace: "ns"}, {Namespace: "ns", Before: time.Now(), Limit: -1}, {Namespace: "ns", Before: time.Now(), Limit: 201}} {
		if _, err := s.PruneResults(ctx, r); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.PruneResults(canceled, PruneRequest{Namespace: "ns", Before: time.Now()}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if u := usageTest(t, s, "empty"); u != (Usage{}) {
		t.Fatalf("empty usage: %+v", u)
	}
}

func TestAdmissionLimitVisibleInChildProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process.db")
	s := openTest(t, path)
	submitTest(t, s, request())
	if err := s.SetLimits(context.Background(), request().Namespace, Limits{MaxActive: 1}); err != nil {
		t.Fatal(err)
	}
	cmd := helper(path)
	cmd.Env = append(cmd.Env, "TASKFUSED_TEST_KEY=new-child-operation")
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), ErrCapacity.Error()) {
		t.Fatalf("child admission: %s %v", output, err)
	}
	if u := usageTest(t, s, request().Namespace); u.Operations != 1 || u.Events != 1 {
		t.Fatalf("child wrote at capacity: %+v", u)
	}
}

func TestActiveLimitIncludesRunningAndRetryWait(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "active.db"))
	if err := s.SetLimits(ctx, request().Namespace, Limits{MaxActive: 1}); err != nil {
		t.Fatal(err)
	}
	op := submitTest(t, s, request())
	c := claimTest(t, s)
	r := request()
	r.Key = "another"
	if _, err := s.Submit(ctx, r); !errors.Is(err, ErrCapacity) {
		t.Fatalf("running did not count: %v", err)
	}
	if err := s.Fail(ctx, c, "transient"); err != nil {
		t.Fatal(err)
	}
	if getTest(t, s, op).State != StateRetryWait {
		t.Fatal("not in automatic backoff")
	}
	if _, err := s.Submit(ctx, r); !errors.Is(err, ErrCapacity) {
		t.Fatalf("retry_wait did not count: %v", err)
	}
	before := usageTest(t, s, op.Namespace)
	if p, err := s.PruneResults(ctx, PruneRequest{Namespace: op.Namespace, Before: time.Now().Add(time.Hour)}); err != nil || len(p.OperationIDs) != 0 {
		t.Fatalf("pruned active work: %+v %v", p, err)
	}
	if after := usageTest(t, s, op.Namespace); after != before {
		t.Fatalf("pruning changed active work: %+v", after)
	}
}

func TestPruneIgnoresFailedCanceledAndQueued(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "states.db"))
	failed, _ := failedTest(t, s)
	r := request()
	r.Key = "canceled"
	op := submitTest(t, s, r)
	if _, err := s.Cancel(ctx, r.Namespace, op.ID); err != nil {
		t.Fatal(err)
	}
	r.Key = "pending"
	submitTest(t, s, r)
	before := usageTest(t, s, r.Namespace)
	if p, err := s.PruneResults(ctx, PruneRequest{Namespace: r.Namespace, Before: time.Now().Add(time.Hour)}); err != nil || len(p.OperationIDs) != 0 {
		t.Fatalf("non-success pruned: %+v %v", p, err)
	}
	if after := usageTest(t, s, r.Namespace); after != before || !getTest(t, s, failed).RetryEligible {
		t.Fatalf("retry/history changed: %+v", after)
	}
}
