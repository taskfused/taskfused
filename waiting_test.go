package taskfused

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestHundredJoinedWaitersOneCanceledCaller(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "shared.db"))
	ctx, cancelAll := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelAll()
	submissions := make(chan Submission, 100)
	errs := make(chan error, 100)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.Submit(ctx, request())
			if err != nil {
				errs <- err
				return
			}
			submissions <- result
		}()
	}
	wg.Wait()
	close(submissions)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var op Operation
	created, total := 0, 0
	for sub := range submissions {
		total++
		if op.ID == "" {
			op = sub.Operation
		}
		if op.ID != sub.Operation.ID {
			t.Fatal("joined different operations")
		}
		if !sub.Joined {
			created++
		}
	}
	if created != 1 || total != 100 {
		t.Fatalf("created=%d submissions=%d", created, total)
	}
	type outcome struct {
		op  Operation
		err error
	}
	outcomes := make(chan outcome, 99)
	canceledWaiter := make(chan error, 1)
	waiterCtx, cancelWaiter := context.WithCancel(ctx)
	go func() { _, err := s.Wait(waiterCtx, op.Namespace, op.ID); canceledWaiter <- err }()
	for i := 0; i < 99; i++ {
		go func() { got, err := s.Wait(ctx, op.Namespace, op.ID); outcomes <- outcome{got, err} }()
	}
	cancelWaiter()
	select {
	case err := <-canceledWaiter:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("wait canceled: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("waiter did not stop")
	}
	if got := getTest(t, s, op); got.State != StateQueued || got.AttemptCount != 0 {
		t.Fatalf("wait canceled shared work: %+v", got)
	}
	worker := workerTest(t, s, time.Second, func(context.Context, Claim) ([]byte, error) { return []byte("one shared result"), nil })
	if worked, err := worker.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("worker: %v %v", worked, err)
	}
	for i := 0; i < 99; i++ {
		select {
		case outcome := <-outcomes:
			if outcome.err != nil || outcome.op.ID != op.ID || outcome.op.State != StateSucceeded || string(outcome.op.Result) != "one shared result" {
				t.Fatalf("wait outcome: %+v %v", outcome.op, outcome.err)
			}
		case <-ctx.Done():
			t.Fatal("shared waiters did not finish")
		}
	}
	history, err := s.Attempts(ctx, op.Namespace, op.ID)
	if err != nil || len(history) != 1 {
		t.Fatalf("execution count: %+v %v", history, err)
	}
	var cancelEvents int
	if err := s.db.QueryRow("SELECT count(*) FROM events WHERE kind='canceled'").Scan(&cancelEvents); err != nil || cancelEvents != 0 {
		t.Fatalf("wait wrote cancel events: %d %v", cancelEvents, err)
	}
}

func TestWaitDeadlineAndLookupErrors(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	op := submitTest(t, s, request())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Wait(ctx, op.Namespace, op.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait deadline: %v", err)
	}
	if getTest(t, s, op).State != StateQueued {
		t.Fatal("wait timeout changed work")
	}
	for _, ns := range []string{"other", op.Namespace} {
		id := op.ID
		if ns == op.Namespace {
			id = "missing"
		}
		if _, err := s.Wait(context.Background(), ns, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("lookup: %v", err)
		}
	}
	if _, err := s.Wait(context.Background(), "", op.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid lookup: %v", err)
	}
}

func TestWaitReturnsStoredFailureAndCancellationAsOutcomes(t *testing.T) {
	for _, state := range []string{StateFailed, StateCanceled} {
		t.Run(state, func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
			r := request()
			r.MaxAttempts = 1
			op := submitTest(t, s, r)
			if state == StateFailed {
				c := claimTest(t, s)
				if err := s.Fail(context.Background(), c, "failed handler"); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.Cancel(context.Background(), op.Namespace, op.ID); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Wait(context.Background(), op.Namespace, op.ID)
			if err != nil || got.State != state || got.LastError == "" {
				t.Fatalf("outcome: %+v %v", got, err)
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := s.Wait(canceled, op.Namespace, op.ID); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled wait: %v", err)
			}
		})
	}
}

func TestWaitAcrossProcessAndStoreBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	s := openTest(t, path)
	op := submitTest(t, s, request())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prototype := helper(path)
	cmd := exec.CommandContext(ctx, prototype.Args[0], prototype.Args[1:]...)
	cmd.Env = append(prototype.Env, "TASKFUSED_TEST_ACTION=wait")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	decoder := json.NewDecoder(stdout)
	var ready map[string]string
	if err := decoder.Decode(&ready); err != nil || ready["status"] != "waiting" {
		t.Fatalf("wait helper readiness: %+v %v", ready, err)
	}
	worker := workerTest(t, s, time.Second, func(context.Context, Claim) ([]byte, error) { return []byte("cross-process result"), nil })
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var got Operation
	if err := decoder.Decode(&got); err != nil || got.ID != op.ID || string(got.Result) != "cross-process result" {
		t.Fatalf("process wait: %+v %v", got, err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestWaitReturnsIndependentResultBytes(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	op := submitTest(t, s, request())
	c := claimTest(t, s)
	if err := s.Complete(context.Background(), c, []byte("result")); err != nil {
		t.Fatal(err)
	}
	first, err := s.Wait(context.Background(), op.Namespace, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	first.Result[0] = 'X'
	second, err := s.Wait(context.Background(), op.Namespace, op.ID)
	if err != nil || string(second.Result) != "result" {
		t.Fatalf("shared mutable bytes: %+v %v", second, err)
	}
}
