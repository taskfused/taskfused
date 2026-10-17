// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0

package taskfused

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func failedTest(t *testing.T, s *Store) (Operation, Claim) {
	t.Helper()
	r := request()
	r.MaxAttempts = 1
	op := submitTest(t, s, r)
	c := claimTest(t, s)
	if err := s.Fail(context.Background(), c, "original failure"); err != nil {
		t.Fatal(err)
	}
	return getTest(t, s, op), c
}
func retryRequest(op Operation) RetryRequest {
	return RetryRequest{Namespace: op.Namespace, ID: op.ID, ActionKey: "operator-action-1", ExpectedAttempts: op.AttemptCount, AdditionalAttempts: 1}
}

func TestOperatorRetryPreservesIdentityPolicyCountersAndReplays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	s := openTest(t, path)
	clock := newClock()
	s.now = clock.now
	op, old := failedTest(t, s)
	if !op.RetryEligible || op.AttemptLimit != 1 || op.MaxAttempts != 1 {
		t.Fatalf("eligibility: %+v", op)
	}
	r := retryRequest(op)
	r.AdditionalAttempts = 2
	granted, err := s.Retry(context.Background(), r)
	if err != nil || !granted.Applied || granted.Operation.AttemptLimit != 3 || granted.Operation.RetryCycleStart != 1 || granted.Operation.MaxAttempts != 1 || granted.Operation.CompletedAt != nil || granted.Operation.LastError != "" {
		t.Fatalf("grant: %+v %v", granted, err)
	}
	originalRequest := request()
	originalRequest.MaxAttempts = 1
	joined, err := s.Submit(context.Background(), originalRequest)
	if err != nil || !joined.Joined || joined.Operation.ID != op.ID || joined.Operation.InputSHA256 != op.InputSHA256 {
		t.Fatalf("submission policy changed: %+v %v", joined, err)
	}
	if err := s.Complete(context.Background(), old, []byte("obsolete")); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old claim revived: %v", err)
	}
	second := claimTest(t, s)
	if second.Attempt.Number != 2 || second.Attempt.Token <= old.Attempt.Token {
		t.Fatalf("counters reset: %+v", second)
	}
	if err := s.Fail(context.Background(), second, "retry failure"); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Second - time.Nanosecond)
	if _, err := s.Claim(context.Background(), claimRequest()); !errors.Is(err, ErrNoWork) {
		t.Fatalf("early cycle retry: %v", err)
	}
	clock.advance(time.Nanosecond)
	third := claimTest(t, s)
	if third.Attempt.Number != 3 || third.Attempt.Token != 3 {
		t.Fatalf("third claim: %+v", third)
	}
	if err := s.Fail(context.Background(), third, "second cycle exhausted"); err != nil {
		t.Fatal(err)
	}
	replay, err := s.Retry(context.Background(), r)
	if err != nil || replay.Applied || replay.Operation.State != StateFailed || replay.Operation.AttemptLimit != 3 || !replay.Action.RequestedAt.Equal(granted.Action.RequestedAt) {
		t.Fatalf("replay granted more work: %+v %v", replay, err)
	}
	s.Close()
	reopened := openTest(t, path)
	reopened.now = clock.now
	replay, err = reopened.Retry(context.Background(), r)
	if err != nil || replay.Applied || replay.Action.AttemptLimit != 3 {
		t.Fatalf("restart replay: %+v %v", replay, err)
	}
	stale := r
	stale.ActionKey = "new-stale-action"
	if _, err := reopened.Retry(context.Background(), stale); !errors.Is(err, ErrRetryStale) {
		t.Fatalf("stale action: %v", err)
	}
	changed := r
	changed.AdditionalAttempts = 1
	if _, err := reopened.Retry(context.Background(), changed); !errors.Is(err, ErrRetryConflict) {
		t.Fatalf("changed action parameters: %v", err)
	}
	next := r
	next.ActionKey = "operator-action-2"
	next.ExpectedAttempts = 3
	next.AdditionalAttempts = 1
	if _, err := reopened.Retry(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	fourth := claimTest(t, reopened)
	if fourth.Attempt.Number != 4 || fourth.Attempt.Token != 4 {
		t.Fatalf("lifetime counters: %+v", fourth)
	}
	if err := reopened.Complete(context.Background(), fourth, []byte("fixed")); err != nil {
		t.Fatal(err)
	}
	replay, err = reopened.Retry(context.Background(), r)
	if err != nil || replay.Applied || replay.Operation.State != StateSucceeded || replay.Action.AttemptLimit != 3 || replay.Operation.AttemptLimit != 4 {
		t.Fatalf("successful replay: %+v %v", replay, err)
	}
	page, err := reopened.Events(context.Background(), op.Namespace, op.ID, EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	grants := 0
	for _, e := range page.Events {
		if e.Kind == "retry_requested" {
			grants++
			if e.ActionKey == r.ActionKey && (e.ExpectedAttempts != 1 || e.AdditionalAttempts != 2 || e.AttemptLimit != 3) {
				t.Fatalf("historical grant detail: %+v", e)
			}
		}
	}
	if grants != 2 {
		t.Fatalf("grant events: %d", grants)
	}
}

func TestConcurrentRetryActionAppliesOnceAcrossStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	stores := []*Store{openTest(t, path), openTest(t, path), openTest(t, path), openTest(t, path)}
	op, _ := failedTest(t, stores[0])
	r := retryRequest(op)
	var wg sync.WaitGroup
	results := make(chan RetryResult, 32)
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := stores[i%4].Retry(context.Background(), r)
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	applied, total := 0, 0
	for result := range results {
		total++
		if result.Applied {
			applied++
		}
		if result.Action.AttemptLimit != 2 || result.Operation.AttemptLimit != 2 {
			t.Fatalf("duplicate grant: %+v", result)
		}
	}
	if applied != 1 || total != 32 {
		t.Fatalf("applied=%d total=%d", applied, total)
	}
	var count int
	if err := stores[0].db.QueryRow("SELECT count(*) FROM retry_actions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("durable actions: %d %v", count, err)
	}
}

func TestCompetingNewRetryActionsRequireCurrentFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	first := openTest(t, path)
	second := openTest(t, path)
	op, _ := failedTest(t, first)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for i, s := range []*Store{first, second} {
		go func(i int, s *Store) {
			<-start
			r := retryRequest(op)
			r.ActionKey = fmt.Sprintf("action-%d", i)
			_, err := s.Retry(context.Background(), r)
			errs <- err
		}(i, s)
	}
	close(start)
	wins, rejected := 0, 0
	for i := 0; i < 2; i++ {
		err := <-errs
		if err == nil {
			wins++
		} else if errors.Is(err, ErrRetryState) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || rejected != 1 || getTest(t, first, op).AttemptLimit != 2 {
		t.Fatalf("competing actions: winners=%d rejected=%d", wins, rejected)
	}
}

func TestRetryAndCancelAreSerializedAndReplayCannotResurrect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	first := openTest(t, path)
	second := openTest(t, path)
	op, _ := failedTest(t, first)
	r := retryRequest(op)
	start := make(chan struct{})
	retryErr := make(chan error, 1)
	type outcome struct {
		c   Cancellation
		err error
	}
	cancelOut := make(chan outcome, 1)
	go func() { <-start; _, err := first.Retry(context.Background(), r); retryErr <- err }()
	go func() {
		<-start
		c, err := second.Cancel(context.Background(), op.Namespace, op.ID)
		cancelOut <- outcome{c, err}
	}()
	close(start)
	if err := <-retryErr; err != nil {
		t.Fatal(err)
	}
	out := <-cancelOut
	if out.err != nil {
		t.Fatal(out.err)
	}
	got := getTest(t, first, op)
	if out.c.Changed && got.State != StateCanceled || !out.c.Changed && got.State != StateQueued {
		t.Fatalf("serialized ordering: %+v %+v", out, got)
	}
	if _, err := first.Cancel(context.Background(), op.Namespace, op.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := first.Retry(context.Background(), r)
	if err != nil || replay.Applied || replay.Operation.State != StateCanceled {
		t.Fatalf("replay resurrected canceled work: %+v %v", replay, err)
	}
	fresh := r
	fresh.ActionKey = "new-action"
	if _, err := first.Retry(context.Background(), fresh); !errors.Is(err, ErrRetryState) {
		t.Fatalf("canceled retry: %v", err)
	}
}

func TestRetryGrantAndEventRollbackTogether(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	op, _ := failedTest(t, s)
	r := retryRequest(op)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_manual_retry BEFORE INSERT ON events WHEN NEW.kind='retry_requested' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retry(context.Background(), r); err == nil {
		t.Fatal("retry accepted without event")
	}
	got := getTest(t, s, op)
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM retry_actions").Scan(&count); err != nil || count != 0 || got.State != StateFailed || got.AttemptLimit != 1 || !got.RetryEligible {
		t.Fatalf("partial retry: %+v actions=%d err=%v", got, count, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_manual_retry"); err != nil {
		t.Fatal(err)
	}
	if result, err := s.Retry(context.Background(), r); err != nil || !result.Applied {
		t.Fatalf("retry after rollback: %+v %v", result, err)
	}
}

func TestRetryBudgetBoundariesAndValidation(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	op, _ := failedTest(t, s)
	for _, modify := range []func(*RetryRequest){
		func(r *RetryRequest) { r.ExpectedAttempts = 0 }, func(r *RetryRequest) { r.ExpectedAttempts = MaxLifetimeAttempts + 1 },
		func(r *RetryRequest) { r.AdditionalAttempts = 0 }, func(r *RetryRequest) { r.AdditionalAttempts = 101 },
		func(r *RetryRequest) { r.ActionKey = " " }, func(r *RetryRequest) { r.ActionKey = "nul\x00" }, func(r *RetryRequest) { r.Namespace = "" },
	} {
		r := retryRequest(op)
		modify(&r)
		if _, err := s.Retry(context.Background(), r); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid retry: %v", err)
		}
	}
	wrong := retryRequest(op)
	wrong.Namespace = "other"
	if _, err := s.Retry(context.Background(), wrong); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong namespace: %v", err)
	}
	// Seed the numeric lifetime boundary without performing 999 durable executions.
	if _, err := s.db.Exec("UPDATE operations SET attempt_count=999,attempt_limit=999,ownership_token=999 WHERE id=?", op.ID); err != nil {
		t.Fatal(err)
	}
	r := retryRequest(op)
	r.ExpectedAttempts = 999
	r.AdditionalAttempts = 2
	if _, err := s.Retry(context.Background(), r); !errors.Is(err, ErrRetryBudget) {
		t.Fatalf("over lifetime limit: %v", err)
	}
	r.AdditionalAttempts = 1
	if _, err := s.Retry(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	c := claimTest(t, s)
	if c.Attempt.Number != 1000 || c.Attempt.Token != 1000 {
		t.Fatalf("limit claim: %+v", c)
	}
	if err := s.Fail(context.Background(), c, "last allowed failure"); err != nil {
		t.Fatal(err)
	}
	got := getTest(t, s, op)
	if got.RetryEligible {
		t.Fatal("exhausted operation advertised eligibility")
	}
	r.ActionKey = "over-limit"
	r.ExpectedAttempts = 1000
	if _, err := s.Retry(context.Background(), r); !errors.Is(err, ErrRetryBudget) {
		t.Fatalf("exhausted lifetime limit: %v", err)
	}
}

func TestRetriedWorkCrashRecoveryUsesExtendedBudget(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	op, _ := failedTest(t, s)
	r := retryRequest(op)
	r.AdditionalAttempts = 2
	if _, err := s.Retry(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	second := claimTest(t, s)
	clock.advance(time.Second)
	third := claimTest(t, s)
	if third.Attempt.Number != 3 || third.Attempt.Token != 3 {
		t.Fatalf("extended crash budget ignored: %+v", third)
	}
	if err := s.Complete(context.Background(), second, []byte("stale")); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired retry owner: %v", err)
	}
	clock.advance(time.Second)
	if _, err := s.Claim(context.Background(), claimRequest()); !errors.Is(err, ErrNoWork) {
		t.Fatalf("extended budget not exhausted: %v", err)
	}
	got := getTest(t, s, op)
	if got.State != StateFailed || got.AttemptCount != 3 || got.MaxAttempts != 1 || got.AttemptLimit != 3 {
		t.Fatalf("crash exhaustion: %+v", got)
	}
}
