// Copyright 2026 TaskFused. All rights reserved.
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/taskfused/taskfused"
)

// operations lets the same correctness scenarios exercise either the embedded
// Store or the local HTTP transport. Worker-only Complete remains a Go assertion.
type operations interface {
	Submit(context.Context, taskfused.Request) (taskfused.Submission, error)
	Get(context.Context, string, string) (taskfused.Operation, error)
	Wait(context.Context, string, string) (taskfused.Operation, error)
	Attempts(context.Context, string, string) ([]taskfused.Attempt, error)
	Cancel(context.Context, string, string) (taskfused.Cancellation, error)
	Retry(context.Context, taskfused.RetryRequest) (taskfused.RetryResult, error)
	Events(context.Context, string, string, taskfused.EventQuery) (taskfused.EventPage, error)
	Complete(context.Context, taskfused.Claim, []byte) error
}

type functionalReport struct {
	Shared       sharedReport       `json:"shared"`
	Crash        crashReport        `json:"crash"`
	Cancellation cancellationReport `json:"cancellation"`
	Retry        retryReport        `json:"retry"`
}
type sharedReport struct {
	Requests          int            `json:"requests"`
	NewOperations     int            `json:"new_operations"`
	Joined            int            `json:"joined"`
	SuccessfulWaiters int            `json:"successful_waiters"`
	TimedOutWaiters   int            `json:"timed_out_waiters"`
	Attempts          int            `json:"attempts"`
	WaitElapsed       latencySummary `json:"wait_elapsed_including_demo_delay"`
}
type crashReport struct {
	KillExit            string  `json:"kill_exit"`
	Attempts            int     `json:"attempts"`
	OldToken            int64   `json:"old_token"`
	NewToken            int64   `json:"new_token"`
	SameOperation       bool    `json:"same_operation"`
	StaleCommitRejected bool    `json:"stale_commit_rejected"`
	RecoveryMS          float64 `json:"kill_to_observed_success_ms"`
}
type cancellationReport struct {
	State             string  `json:"state"`
	ObservedByHandler bool    `json:"observed_by_handler"`
	DetectionMS       float64 `json:"cancel_to_handler_observation_ms"`
	SurvivorSucceeded bool    `json:"survivor_succeeded"`
}
type retryReport struct {
	AppliedActions      int      `json:"applied_actions"`
	Replays             int      `json:"replays"`
	Attempts            int      `json:"attempts"`
	OriginalMaxAttempts int      `json:"original_max_attempts"`
	AttemptLimit        int      `json:"attempt_limit"`
	Events              int      `json:"events"`
	EventKinds          []string `json:"event_kinds"`
}

func demoRequest(handler, key string) taskfused.Request {
	return taskfused.Request{Namespace: "demo", Handler: handler, Version: "v1", Key: key, Input: []byte("shared result"), JoinExisting: true}
}

func runFunctional(parent context.Context, executable, path string, n int) (functionalReport, error) {
	s, err := taskfused.Open(parent, path)
	if err != nil {
		return functionalReport{}, err
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	shared, err := checkShared(ctx, s, executable, path, n)
	if err != nil {
		return functionalReport{}, fmt.Errorf("shared work: %w", err)
	}
	crash, err := checkCrash(ctx, s, executable, path)
	if err != nil {
		return functionalReport{}, fmt.Errorf("crash recovery: %w", err)
	}
	canceled, err := checkCancellation(ctx, s, executable, path)
	if err != nil {
		return functionalReport{}, fmt.Errorf("cancellation: %w", err)
	}
	retried, err := checkRetry(ctx, s, executable, path)
	if err != nil {
		return functionalReport{}, fmt.Errorf("operator retry: %w", err)
	}
	return functionalReport{Shared: shared, Crash: crash, Cancellation: canceled, Retry: retried}, nil
}

func checkShared(ctx context.Context, s operations, executable, path string, n int) (sharedReport, error) {
	submissions := make(chan taskfused.Submission, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.Submit(ctx, demoRequest("shared", "equivalent"))
			if err != nil {
				errs <- err
				return
			}
			submissions <- result
		}()
	}
	wg.Wait()
	close(errs)
	close(submissions)
	for err := range errs {
		return sharedReport{}, err
	}
	result := sharedReport{Requests: n}
	var operation taskfused.Operation
	for submission := range submissions {
		if operation.ID == "" {
			operation = submission.Operation
		}
		if submission.Operation.ID != operation.ID {
			return sharedReport{}, fmt.Errorf("equivalent submissions created different operations")
		}
		if submission.Joined {
			result.Joined++
		} else {
			result.NewOperations++
		}
	}
	if result.NewOperations != 1 || result.Joined != n-1 {
		return sharedReport{}, fmt.Errorf("unexpected submission counts: %+v", result)
	}
	waitCtx, stopWait := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err := s.Wait(waitCtx, "demo", operation.ID)
	stopWait()
	if !errors.Is(err, context.DeadlineExceeded) {
		return sharedReport{}, fmt.Errorf("expected isolated wait timeout, got %v", err)
	}
	result.TimedOutWaiters = 1
	current, err := s.Get(ctx, "demo", operation.ID)
	if err != nil {
		return sharedReport{}, err
	}
	if current.State != taskfused.StateQueued || current.AttemptCount != 0 {
		return sharedReport{}, fmt.Errorf("wait timeout changed shared operation")
	}
	type outcome struct {
		op      taskfused.Operation
		elapsed time.Duration
		err     error
	}
	outcomes := make(chan outcome, n-1)
	for i := 0; i < n-1; i++ {
		go func() {
			start := time.Now()
			op, err := s.Wait(ctx, "demo", operation.ID)
			outcomes <- outcome{op, time.Since(start), err}
		}()
	}
	worker, err := startChildWorker(ctx, executable, path, "shared")
	if err != nil {
		return sharedReport{}, err
	}
	defer worker.stop()
	if _, err := worker.next(ctx, "started", operation.ID); err != nil {
		return sharedReport{}, err
	}
	durations := []time.Duration{}
	for i := 0; i < n-1; i++ {
		var observed outcome
		select {
		case observed = <-outcomes:
		case <-ctx.Done():
			return sharedReport{}, ctx.Err()
		}
		if observed.err != nil {
			return sharedReport{}, observed.err
		}
		if observed.op.ID != operation.ID || observed.op.State != taskfused.StateSucceeded || string(observed.op.Result) != "shared result" {
			return sharedReport{}, fmt.Errorf("waiters did not share the stored outcome")
		}
		result.SuccessfulWaiters++
		durations = append(durations, observed.elapsed)
	}
	history, err := s.Attempts(ctx, "demo", operation.ID)
	if err != nil {
		return sharedReport{}, err
	}
	if len(history) != 1 || history[0].State != taskfused.StateSucceeded {
		return sharedReport{}, fmt.Errorf("expected one successful execution attempt")
	}
	result.Attempts = len(history)
	result.WaitElapsed = summarize(durations)
	if err := worker.stop(); err != nil {
		return sharedReport{}, err
	}
	return result, nil
}

func checkCrash(ctx context.Context, s operations, executable, path string) (crashReport, error) {
	submission, err := s.Submit(ctx, demoRequest("crash", "interrupted"))
	if err != nil {
		return crashReport{}, err
	}
	worker, err := startChildWorker(ctx, executable, path, "crash-block")
	if err != nil {
		return crashReport{}, err
	}
	defer worker.stop()
	first, err := worker.next(ctx, "started", submission.Operation.ID)
	if err != nil {
		return crashReport{}, err
	}
	killedAt := time.Now()
	exit, err := worker.kill()
	if err != nil {
		return crashReport{}, err
	}
	replacement, err := startChildWorker(ctx, executable, path, "crash-recover")
	if err != nil {
		return crashReport{}, err
	}
	defer replacement.stop()
	outcome, err := s.Wait(ctx, "demo", submission.Operation.ID)
	if err != nil {
		return crashReport{}, err
	}
	elapsed := time.Since(killedAt)
	if outcome.ID != first.Claim.Operation.ID || outcome.State != taskfused.StateSucceeded || string(outcome.Result) != "shared result" {
		return crashReport{}, fmt.Errorf("replacement did not recover the same operation/result")
	}
	history, err := s.Attempts(ctx, "demo", outcome.ID)
	if err != nil {
		return crashReport{}, err
	}
	if len(history) != 2 || history[0].State != "expired" || history[1].State != taskfused.StateSucceeded || history[1].Token <= history[0].Token || history[0].ID == history[1].ID {
		return crashReport{}, fmt.Errorf("invalid recovery attempt history: %+v", history)
	}
	stale := s.Complete(ctx, first.Claim, []byte("obsolete"))
	if !errors.Is(stale, taskfused.ErrLeaseLost) {
		return crashReport{}, fmt.Errorf("stale result was not rejected: %v", stale)
	}
	if err := replacement.stop(); err != nil {
		return crashReport{}, err
	}
	return crashReport{KillExit: exit, Attempts: len(history), OldToken: history[0].Token, NewToken: history[1].Token, SameOperation: true, StaleCommitRejected: true, RecoveryMS: milliseconds(elapsed)}, nil
}

func checkCancellation(ctx context.Context, s operations, executable, path string) (cancellationReport, error) {
	submission, err := s.Submit(ctx, demoRequest("cancel", "stop-shared-work"))
	if err != nil {
		return cancellationReport{}, err
	}
	worker, err := startChildWorker(ctx, executable, path, "cancel")
	if err != nil {
		return cancellationReport{}, err
	}
	defer worker.stop()
	if _, err := worker.next(ctx, "started", submission.Operation.ID); err != nil {
		return cancellationReport{}, err
	}
	start := time.Now()
	canceled, err := s.Cancel(ctx, "demo", submission.Operation.ID)
	if err != nil {
		return cancellationReport{}, err
	}
	if !canceled.Changed || canceled.Operation.State != taskfused.StateCanceled {
		return cancellationReport{}, fmt.Errorf("operation was not canceled")
	}
	if _, err := worker.next(ctx, "cancellation_observed", submission.Operation.ID); err != nil {
		return cancellationReport{}, err
	}
	detection := time.Since(start)
	survivor, err := s.Submit(ctx, demoRequest("survivor", "keep-runner-alive"))
	if err != nil {
		return cancellationReport{}, err
	}
	outcome, err := s.Wait(ctx, "demo", survivor.Operation.ID)
	if err != nil {
		return cancellationReport{}, err
	}
	if outcome.State != taskfused.StateSucceeded {
		return cancellationReport{}, fmt.Errorf("canceling one operation stopped subsequent work")
	}
	record, err := s.Get(ctx, "demo", submission.Operation.ID)
	if err != nil {
		return cancellationReport{}, err
	}
	if record.State != taskfused.StateCanceled || record.Result != nil {
		return cancellationReport{}, fmt.Errorf("canceled work recorded a result")
	}
	if err := worker.stop(); err != nil {
		return cancellationReport{}, err
	}
	return cancellationReport{State: record.State, ObservedByHandler: true, DetectionMS: milliseconds(detection), SurvivorSucceeded: true}, nil
}

func checkRetry(ctx context.Context, s operations, executable, path string) (retryReport, error) {
	request := demoRequest("retry", "operator-repair")
	request.MaxAttempts = 1
	submission, err := s.Submit(ctx, request)
	if err != nil {
		return retryReport{}, err
	}
	worker, err := startChildWorker(ctx, executable, path, "retry")
	if err != nil {
		return retryReport{}, err
	}
	defer worker.stop()
	failed, err := s.Wait(ctx, "demo", submission.Operation.ID)
	if err != nil {
		return retryReport{}, err
	}
	if failed.State != taskfused.StateFailed || !failed.RetryEligible {
		return retryReport{}, fmt.Errorf("initial retry scenario did not fail")
	}
	action := taskfused.RetryRequest{Namespace: "demo", ID: failed.ID, ActionKey: "demo-repair", ExpectedAttempts: 1, AdditionalAttempts: 1}
	grant, err := s.Retry(ctx, action)
	if err != nil {
		return retryReport{}, err
	}
	replay, err := s.Retry(ctx, action)
	if err != nil {
		return retryReport{}, err
	}
	if !grant.Applied || replay.Applied {
		return retryReport{}, fmt.Errorf("retry action was not idempotent")
	}
	outcome, err := s.Wait(ctx, "demo", failed.ID)
	if err != nil {
		return retryReport{}, err
	}
	if outcome.ID != submission.Operation.ID || outcome.State != taskfused.StateSucceeded || outcome.AttemptCount != 2 || outcome.MaxAttempts != 1 || outcome.AttemptLimit != 2 {
		return retryReport{}, fmt.Errorf("retry changed identity/policy or failed to complete")
	}
	history, err := s.Attempts(ctx, "demo", outcome.ID)
	if err != nil {
		return retryReport{}, err
	}
	if len(history) != 2 || history[1].Token <= history[0].Token {
		return retryReport{}, fmt.Errorf("retry reset ownership")
	}
	kinds := []string{}
	after := int64(0)
	grants := 0
	for {
		page, err := s.Events(ctx, "demo", outcome.ID, taskfused.EventQuery{After: after, Limit: 2})
		if err != nil {
			return retryReport{}, err
		}
		for _, event := range page.Events {
			kinds = append(kinds, event.Kind)
			if event.Kind == "retry_requested" {
				grants++
				if event.ActionKey != "demo-repair" || event.ExpectedAttempts != 1 || event.AdditionalAttempts != 1 || event.AttemptLimit != 2 {
					return retryReport{}, fmt.Errorf("incorrect retry event detail")
				}
			}
		}
		if !page.HasMore {
			break
		}
		if page.NextAfter <= after {
			return retryReport{}, fmt.Errorf("event cursor stopped advancing")
		}
		after = page.NextAfter
	}
	if grants != 1 || len(kinds) != 6 {
		return retryReport{}, fmt.Errorf("retry event count: %v", kinds)
	}
	if err := worker.stop(); err != nil {
		return retryReport{}, err
	}
	return retryReport{AppliedActions: 1, Replays: 1, Attempts: len(history), OriginalMaxAttempts: outcome.MaxAttempts, AttemptLimit: outcome.AttemptLimit, Events: len(kinds), EventKinds: kinds}, nil
}
