// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0

package taskfused

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func workerTest(t *testing.T, s *Store, lease time.Duration, h Handler) *Worker {
	t.Helper()
	w, err := NewWorker(s, WorkerOptions{Namespace: request().Namespace, LeaseDuration: lease, Handlers: []Registration{{Name: request().Handler, Version: request().Version, Handle: h}}})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWorkerFailurePanicAndOversizedResult(t *testing.T) {
	for _, mode := range []string{"error", "panic", "oversize", "long_error"} {
		t.Run(mode, func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
			r := request()
			r.MaxAttempts = 1
			op := submitTest(t, s, r)
			w := workerTest(t, s, time.Second, func(context.Context, Claim) ([]byte, error) {
				switch mode {
				case "panic":
					panic("broken handler")
				case "oversize":
					return make([]byte, MaxResultBytes+1), nil
				case "long_error":
					return nil, errors.New(strings.Repeat("x", MaxErrorBytes+10))
				default:
					return nil, fmt.Errorf("failed handler")
				}
			})
			if worked, err := w.RunOnce(context.Background()); err != nil || !worked {
				t.Fatalf("worker: %v %v", worked, err)
			}
			got := getTest(t, s, op)
			if got.State != StateFailed || got.LastError == "" || len(got.LastError) > MaxErrorBytes {
				t.Fatalf("handler failure: %+v", got)
			}
			if mode == "panic" && !strings.Contains(got.LastError, "panic") {
				t.Fatalf("panic missing: %s", got.LastError)
			}
		})
	}
}

func TestWorkerRenewsLeaseDuringLongHandler(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	op := submitTest(t, s, request())
	lease := 300 * time.Millisecond
	w := workerTest(t, s, lease, func(ctx context.Context, c Claim) ([]byte, error) {
		timer := time.NewTimer(650 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return []byte("done"), nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if worked, err := w.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("long handler: %v %v", worked, err)
	}
	history, err := s.Attempts(context.Background(), op.Namespace, op.ID)
	if err != nil || len(history) != 1 || !history[0].LeaseUntil.After(history[0].StartedAt.Add(lease)) {
		t.Fatalf("renewal: %+v %v", history, err)
	}
	if getTest(t, s, op).State != StateSucceeded {
		t.Fatal("long handler did not succeed")
	}
}

func TestWorkerLeaseLossCancelsHandlerAndRejectsResult(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	op := submitTest(t, s, request())
	started := make(chan struct{})
	w := workerTest(t, s, MinLease, func(ctx context.Context, c Claim) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return []byte("stale result"), nil // Deliberately ignore the cancellation error.
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := w.RunOnce(ctx); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("handler did not start")
	}
	clock.advance(MinLease)
	select {
	case err := <-done:
		if !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("lease loss: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("lease loss did not stop handler")
	}
	got := getTest(t, s, op)
	if got.State != StateRunning || got.Result != nil {
		t.Fatalf("stale worker committed: %+v", got)
	}
	current := claimTest(t, s)
	if err := s.Complete(context.Background(), current, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerCancellationLeavesRecoverableAttempt(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	clock := newClock()
	s.now = clock.now
	op := submitTest(t, s, request())
	ctx, cancel := context.WithCancel(context.Background())
	w := workerTest(t, s, time.Second, func(ctx context.Context, c Claim) ([]byte, error) { cancel(); return nil, ctx.Err() })
	if worked, err := w.RunOnce(ctx); !worked || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v %v", worked, err)
	}
	if getTest(t, s, op).State != StateRunning {
		t.Fatal("worker cancellation became an operation failure")
	}
	clock.advance(time.Second)
	recovered := claimTest(t, s)
	if recovered.Attempt.Number != 2 {
		t.Fatalf("recovery after cancel: %+v", recovered)
	}
}

func TestRunBoundsConcurrencyAndStops(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	for i := 0; i < 5; i++ {
		r := request()
		r.Key = fmt.Sprintf("key-%d", i)
		submitTest(t, s, r)
	}
	var active, peak atomic.Int32
	started := make(chan struct{}, 5)
	w, err := NewWorker(s, WorkerOptions{Namespace: request().Namespace, Concurrency: 2, Handlers: []Registration{{Name: request().Handler, Version: request().Version, Handle: func(ctx context.Context, c Claim) ([]byte, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("workers did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
	if peak.Load() != 2 || active.Load() != 0 {
		t.Fatalf("concurrency peak=%d active=%d", peak.Load(), active.Load())
	}
}

func TestWorkerRegistryAndNoWork(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	r := request()
	r.Handler = "not-registered"
	op := submitTest(t, s, r)
	w := workerTest(t, s, time.Second, func(context.Context, Claim) ([]byte, error) { return nil, nil })
	if worked, err := w.RunOnce(context.Background()); err != nil || worked {
		t.Fatalf("unregistered: %v %v", worked, err)
	}
	if getTest(t, s, op).State != StateQueued {
		t.Fatal("worker consumed unregistered handler")
	}
	registrations := []Registration{{Name: request().Handler, Version: request().Version, Handle: func(context.Context, Claim) ([]byte, error) { return []byte("original"), nil }}}
	w, err := NewWorker(s, WorkerOptions{Namespace: request().Namespace, Handlers: registrations})
	if err != nil {
		t.Fatal(err)
	}
	registrations[0].Handle = nil // The caller's slice must not mutate the worker registry.
	target := submitTest(t, s, request())
	if worked, err := w.RunOnce(context.Background()); err != nil || !worked {
		t.Fatalf("registry copy: %v %v", worked, err)
	}
	if string(getTest(t, s, target).Result) != "original" {
		t.Fatal("registry was mutated")
	}
	invalid := []WorkerOptions{{}, {Namespace: "x", Handlers: []Registration{{Name: "h", Version: "v"}}}, {Namespace: "x", Handlers: []Registration{{Name: "h", Version: "v", Handle: registrations[0].Handle}}, Concurrency: 129}}
	for _, o := range invalid {
		if _, err := NewWorker(s, o); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid worker: %v", err)
		}
	}
}
