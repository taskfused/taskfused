package taskfused

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestCancelQueuedAndRetryWaitIsIdempotent(t *testing.T) {
	for _, state := range []string{StateQueued, StateRetryWait} {
		t.Run(state, func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
			clock := newClock()
			s.now = clock.now
			op := submitTest(t, s, request())
			if state == StateRetryWait {
				c := claimTest(t, s)
				if err := s.Fail(context.Background(), c, "retry me"); err != nil {
					t.Fatal(err)
				}
			}
			result, err := s.Cancel(context.Background(), op.Namespace, op.ID)
			if err != nil || !result.Changed || result.Operation.State != StateCanceled || result.Operation.CompletedAt == nil {
				t.Fatalf("cancel: %+v %v", result, err)
			}
			again, err := s.Cancel(context.Background(), op.Namespace, op.ID)
			if err != nil || again.Changed || !again.Operation.CompletedAt.Equal(*result.Operation.CompletedAt) {
				t.Fatalf("repeat cancel: %+v %v", again, err)
			}
			clock.advance(time.Hour)
			if _, err := s.Claim(context.Background(), claimRequest()); !errors.Is(err, ErrNoWork) {
				t.Fatalf("canceled work reclaimed: %v", err)
			}
			joined, err := s.Submit(context.Background(), request())
			if err != nil || !joined.Joined || joined.Operation.State != StateCanceled {
				t.Fatalf("canceled join: %+v %v", joined, err)
			}
			var count int
			if err := s.db.QueryRow("SELECT count(*) FROM events WHERE kind='canceled'").Scan(&count); err != nil || count != 1 {
				t.Fatalf("cancel events: %d %v", count, err)
			}
			history, err := s.Attempts(context.Background(), op.Namespace, op.ID)
			expected := 0
			if state == StateRetryWait {
				expected = 1
			}
			if err != nil || len(history) != expected {
				t.Fatalf("history: %+v %v", history, err)
			}
			if expected == 1 && history[0].State != StateFailed {
				t.Fatal("cancellation rewrote an already failed attempt")
			}
		})
	}
}

func TestCancelRunningRevokesOwnershipImmediately(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	op := submitTest(t, s, request())
	c := claimTest(t, s)
	result, err := s.Cancel(context.Background(), op.Namespace, op.ID)
	if err != nil || !result.Changed {
		t.Fatalf("cancel: %+v %v", result, err)
	}
	for name, action := range map[string]func() error{
		"success": func() error { return s.Complete(context.Background(), c, []byte("stale")) },
		"failure": func() error { return s.Fail(context.Background(), c, "stale error") },
		"renew":   func() error { _, err := s.Renew(context.Background(), c, time.Second); return err },
	} {
		if err := action(); !errors.Is(err, ErrOperationCanceled) {
			t.Fatalf("%s after cancel: %v", name, err)
		}
	}
	history, err := s.Attempts(context.Background(), op.Namespace, op.ID)
	if err != nil || len(history) != 1 || history[0].State != StateCanceled || history[0].FinishedAt == nil {
		t.Fatalf("attempt revocation: %+v %v", history, err)
	}
	if got := getTest(t, s, op); got.Result != nil || got.State != StateCanceled {
		t.Fatalf("stale result stored: %+v", got)
	}
}

func TestCancelPreservesEarlierTerminalSuccessOrFailure(t *testing.T) {
	for _, state := range []string{StateSucceeded, StateFailed} {
		t.Run(state, func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
			r := request()
			r.MaxAttempts = 1
			op := submitTest(t, s, r)
			c := claimTest(t, s)
			var err error
			if state == StateSucceeded {
				err = s.Complete(context.Background(), c, []byte("committed"))
			} else {
				err = s.Fail(context.Background(), c, "committed error")
			}
			if err != nil {
				t.Fatal(err)
			}
			before := getTest(t, s, op)
			result, err := s.Cancel(context.Background(), op.Namespace, op.ID)
			if err != nil || result.Changed || result.Operation.State != state || string(result.Operation.Result) != string(before.Result) || result.Operation.LastError != before.LastError || !result.Operation.CompletedAt.Equal(*before.CompletedAt) {
				t.Fatalf("terminal overwritten: %+v %v", result, err)
			}
			var count int
			if err := s.db.QueryRow("SELECT count(*) FROM events WHERE kind='canceled'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("terminal cancel event: %d %v", count, err)
			}
		})
	}
}

func TestConcurrentCancelAndCompleteChooseOneTerminalOutcome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	first := openTest(t, path)
	second := openTest(t, path)
	for i := 0; i < 12; i++ {
		r := request()
		r.Key = fmt.Sprintf("race-%d", i)
		op := submitTest(t, first, r)
		c := claimTest(t, first)
		start := make(chan struct{})
		completed := make(chan error, 1)
		type cancelOutcome struct {
			result Cancellation
			err    error
		}
		canceled := make(chan cancelOutcome, 1)
		go func() { <-start; completed <- first.Complete(context.Background(), c, []byte("answer")) }()
		go func() {
			<-start
			result, err := second.Cancel(context.Background(), op.Namespace, op.ID)
			canceled <- cancelOutcome{result, err}
		}()
		close(start)
		completeErr := <-completed
		outcome := <-canceled
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		got := getTest(t, first, op)
		switch got.State {
		case StateSucceeded:
			if completeErr != nil || outcome.result.Changed || string(got.Result) != "answer" {
				t.Fatalf("success race: %+v %v %+v", got, completeErr, outcome)
			}
		case StateCanceled:
			if !errors.Is(completeErr, ErrOperationCanceled) || !outcome.result.Changed || got.Result != nil {
				t.Fatalf("cancel race: %+v %v %+v", got, completeErr, outcome)
			}
		default:
			t.Fatalf("nonterminal race result: %+v", got)
		}
		var count int
		if err := first.db.QueryRow("SELECT count(*) FROM events WHERE operation_id=? AND kind IN ('succeeded','canceled')", op.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("terminal events: %d %v", count, err)
		}
	}
}

func TestCancelEventFailureRollsBackOperationAndAttempt(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	op := submitTest(t, s, request())
	c := claimTest(t, s)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_cancel BEFORE INSERT ON events WHEN NEW.kind='canceled' BEGIN SELECT RAISE(ABORT,'injected cancel failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(context.Background(), op.Namespace, op.ID); err == nil {
		t.Fatal("cancel succeeded without event")
	}
	got := getTest(t, s, op)
	history, err := s.Attempts(context.Background(), op.Namespace, op.ID)
	if err != nil || got.State != StateRunning || history[0].State != StateRunning {
		t.Fatalf("partial cancellation: %+v %+v %v", got, history, err)
	}
	if err := s.Complete(context.Background(), c, []byte("not revoked")); err != nil {
		t.Fatalf("rolled-back cancellation lost ownership: %v", err)
	}
}

func TestCanceledHandlerDoesNotStopSiblingWork(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	firstRequest := request()
	firstRequest.Key = "cancel-this"
	op := submitTest(t, s, firstRequest)
	otherRequest := request()
	otherRequest.Key = "keep-this"
	other := submitTest(t, s, otherRequest)
	cancelStarted, keepStarted, cancelObserved := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseOther := make(chan struct{})
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	worker, err := NewWorker(s, WorkerOptions{Namespace: op.Namespace, Concurrency: 2, LeaseDuration: 600 * time.Millisecond, Handlers: []Registration{{Name: op.Handler, Version: op.Version, Handle: func(ctx context.Context, c Claim) ([]byte, error) {
		switch c.Operation.Key {
		case "cancel-this":
			close(cancelStarted)
			<-ctx.Done()
			close(cancelObserved)
			return []byte("stale result"), nil
		case "keep-this":
			close(keepStarted)
			select {
			case <-releaseOther:
				return []byte("kept result"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		default:
			return []byte("runner still alive"), nil
		}
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	for _, signal := range []chan struct{}{cancelStarted, keepStarted} {
		select {
		case <-signal:
		case <-ctx.Done():
			t.Fatal("handlers did not start")
		}
	}
	if _, err := s.Cancel(ctx, op.Namespace, op.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelObserved:
	case <-ctx.Done():
		t.Fatal("handler did not observe cancellation")
	}
	close(releaseOther)
	got, err := s.Wait(ctx, other.Namespace, other.ID)
	if err != nil || got.State != StateSucceeded || string(got.Result) != "kept result" {
		t.Fatalf("sibling stopped: %+v %v", got, err)
	}
	thirdRequest := request()
	thirdRequest.Key = "third"
	third := submitTest(t, s, thirdRequest)
	got, err = s.Wait(ctx, third.Namespace, third.ID)
	if err != nil || got.State != StateSucceeded || string(got.Result) != "runner still alive" {
		t.Fatalf("runner stopped on normal cancellation: %+v %v", got, err)
	}
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runner shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop")
	}
}

func TestCancellationDoesNotClaimUncooperativeHandlerStopped(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	op := submitTest(t, s, request())
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worker := workerTest(t, s, time.Second, func(context.Context, Claim) ([]byte, error) {
		close(started)
		<-release
		return []byte("must not commit"), nil
	})
	done := make(chan error, 1)
	go func() { _, err := worker.RunOnce(ctx); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("handler did not start")
	}
	if _, err := s.Cancel(ctx, op.Namespace, op.ID); err != nil {
		close(release)
		t.Fatal(err)
	}
	got, err := s.Wait(ctx, op.Namespace, op.ID)
	if err != nil || got.State != StateCanceled {
		close(release)
		t.Fatalf("canceled wait: %+v %v", got, err)
	}
	select {
	case err := <-done:
		close(release)
		t.Fatalf("reported stopped before handler returned: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("normal cancellation was a runner error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("handler did not return")
	}
	if got := getTest(t, s, op); got.State != StateCanceled || got.Result != nil {
		t.Fatalf("uncooperative result committed: %+v", got)
	}
}

func TestCancelLookupAndContextIsolation(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	op := submitTest(t, s, request())
	if _, err := s.Cancel(context.Background(), "other", op.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong namespace: %v", err)
	}
	if _, err := s.Cancel(context.Background(), op.Namespace, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing operation: %v", err)
	}
	if _, err := s.Cancel(context.Background(), "", op.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid lookup: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Cancel(ctx, op.Namespace, op.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled API call: %v", err)
	}
	if getTest(t, s, op).State != StateQueued {
		t.Fatal("failed cancellation call changed work")
	}
}

func TestCancelRacingRetrySchedulingCannotLeaveRunnableWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	first := openTest(t, path)
	second := openTest(t, path)
	op := submitTest(t, first, request())
	c := claimTest(t, first)
	start := make(chan struct{})
	failed := make(chan error, 1)
	type outcome struct {
		result Cancellation
		err    error
	}
	canceled := make(chan outcome, 1)
	go func() { <-start; failed <- first.Fail(context.Background(), c, "try again") }()
	go func() {
		<-start
		result, err := second.Cancel(context.Background(), op.Namespace, op.ID)
		canceled <- outcome{result, err}
	}()
	close(start)
	failErr := <-failed
	cancelResult := <-canceled
	if failErr != nil && !errors.Is(failErr, ErrOperationCanceled) {
		t.Fatal(failErr)
	}
	if cancelResult.err != nil || !cancelResult.result.Changed {
		t.Fatalf("cancel: %+v", cancelResult)
	}
	if getTest(t, first, op).State != StateCanceled {
		t.Fatal("retry overwrote cancellation")
	}
	if _, err := first.Claim(context.Background(), claimRequest()); !errors.Is(err, ErrNoWork) {
		t.Fatalf("canceled retry reclaimed: %v", err)
	}
}
