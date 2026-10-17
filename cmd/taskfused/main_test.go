package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/taskfused/taskfused"
)

func TestSubmitJoinGetCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.db")
	args := []string{"submit", "-db", path, "-namespace", "demo", "-handler", "report", "-version", "v1", "-key", "october", "-input", "hello", "-join"}
	var out, errOut bytes.Buffer
	if err := run(context.Background(), args, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var first taskfused.Submission
	if err := json.Unmarshal(out.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(context.Background(), args, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var joined taskfused.Submission
	if err := json.Unmarshal(out.Bytes(), &joined); err != nil || !joined.Joined || joined.Operation.ID != first.Operation.ID {
		t.Fatalf("join: %+v %v", joined, err)
	}
	out.Reset()
	if err := run(context.Background(), []string{"get", "-db", path, "-namespace", "demo", "-id", first.Operation.ID}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var got taskfused.Operation
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || string(got.Input) != "hello" {
		t.Fatalf("get: %+v %v", got, err)
	}
}

func TestCLIValidationAndHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"submit"}, {"get", "-namespace", "demo"}, {"submit", "unexpected"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"submit", "-h"}, &bytes.Buffer{}, &out); err != nil || out.Len() == 0 {
		t.Fatalf("help: %v", err)
	}
}

func TestRetryFlagsAndAttemptHistoryCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.db")
	var out, errOut bytes.Buffer
	args := []string{"submit", "-db", path, "-namespace", "demo", "-handler", "echo", "-version", "v1", "-key", "retry", "-max-attempts", "2", "-retry-delay", "50ms"}
	if err := run(context.Background(), args, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var result taskfused.Submission
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Operation.MaxAttempts != 2 || result.Operation.RetryDelay != 50*time.Millisecond {
		t.Fatalf("policy: %+v", result.Operation)
	}
	s, err := taskfused.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Claim(context.Background(), taskfused.ClaimRequest{Namespace: "demo", Handler: "echo", Version: "v1", LeaseDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(context.Background(), c, []byte("result")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	out.Reset()
	if err := run(context.Background(), []string{"attempts", "-db", path, "-namespace", "demo", "-id", result.Operation.ID}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var history []taskfused.Attempt
	if err := json.Unmarshal(out.Bytes(), &history); err != nil || len(history) != 1 || history[0].State != taskfused.StateSucceeded {
		t.Fatalf("history: %+v %v", history, err)
	}
}

func TestWaitTimeoutAndExplicitCancelCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.db")
	var out, errOut bytes.Buffer
	if err := run(context.Background(), []string{"submit", "-db", path, "-namespace", "demo", "-handler", "echo", "-version", "v1", "-key", "cancel-demo"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var submitted taskfused.Submission
	if err := json.Unmarshal(out.Bytes(), &submitted); err != nil {
		t.Fatal(err)
	}
	id := submitted.Operation.ID
	out.Reset()
	if err := run(context.Background(), []string{"wait", "-db", path, "-namespace", "demo", "-id", id, "-timeout", "10ms"}, &out, &errOut); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait timeout: %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("timed out wait emitted a terminal outcome")
	}
	if err := run(context.Background(), []string{"get", "-db", path, "-namespace", "demo", "-id", id}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var queued taskfused.Operation
	if err := json.Unmarshal(out.Bytes(), &queued); err != nil || queued.State != taskfused.StateQueued {
		t.Fatalf("wait timeout canceled work: %+v %v", queued, err)
	}
	out.Reset()
	if err := run(context.Background(), []string{"cancel", "-db", path, "-namespace", "demo", "-id", id}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var canceled taskfused.Cancellation
	if err := json.Unmarshal(out.Bytes(), &canceled); err != nil || !canceled.Changed || canceled.Operation.State != taskfused.StateCanceled {
		t.Fatalf("cancel output: %+v %v", canceled, err)
	}
	out.Reset()
	if err := run(context.Background(), []string{"wait", "-db", path, "-namespace", "demo", "-id", id}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var terminal taskfused.Operation
	if err := json.Unmarshal(out.Bytes(), &terminal); err != nil || terminal.State != taskfused.StateCanceled {
		t.Fatalf("wait outcome: %+v %v", terminal, err)
	}
	if err := run(context.Background(), []string{"wait", "-db", path, "-namespace", "demo", "-id", id, "-timeout", "0s"}, &out, &errOut); err == nil {
		t.Fatal("accepted zero wait timeout")
	}
}

func TestRetryAndPaginatedEventsCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.db")
	var out, errOut bytes.Buffer
	if err := run(context.Background(), []string{"submit", "-db", path, "-namespace", "demo", "-handler", "echo", "-version", "v1", "-key", "retry-demo", "-max-attempts", "1"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var submitted taskfused.Submission
	if err := json.Unmarshal(out.Bytes(), &submitted); err != nil {
		t.Fatal(err)
	}
	s, err := taskfused.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Claim(context.Background(), taskfused.ClaimRequest{Namespace: "demo", Handler: "echo", Version: "v1", LeaseDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(context.Background(), c, "failed"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	args := []string{"retry", "-db", path, "-namespace", "demo", "-id", submitted.Operation.ID, "-action-key", "repair-1", "-expected-attempts", "1", "-additional-attempts", "2"}
	for _, applied := range []bool{true, false} {
		out.Reset()
		if err := run(context.Background(), args, &out, &errOut); err != nil {
			t.Fatal(err)
		}
		var result taskfused.RetryResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Applied != applied || result.Operation.AttemptLimit != 3 || result.Operation.MaxAttempts != 1 {
			t.Fatalf("retry output: %+v %v", result, err)
		}
	}
	after := int64(0)
	count := 0
	for {
		out.Reset()
		if err := run(context.Background(), []string{"events", "-db", path, "-namespace", "demo", "-id", submitted.Operation.ID, "-limit", "2", "-after", fmt.Sprint(after)}, &out, &errOut); err != nil {
			t.Fatal(err)
		}
		var page taskfused.EventPage
		if err := json.Unmarshal(out.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		count += len(page.Events)
		after = page.NextAfter
		if !page.HasMore {
			break
		}
	}
	if count != 4 {
		t.Fatalf("event count: %d", count)
	}
	if err := run(context.Background(), []string{"retry", "-db", path, "-namespace", "demo", "-id", submitted.Operation.ID}, &out, &errOut); err == nil {
		t.Fatal("retry accepted missing action identity")
	}
}

func TestStorageCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.db")
	ctx := context.Background()
	invoke := func(args ...string) ([]byte, error) {
		t.Helper()
		var out, errOut bytes.Buffer
		full := append([]string{args[0], "-db", path, "-namespace", "demo"}, args[1:]...)
		err := run(ctx, full, &out, &errOut)
		return out.Bytes(), err
	}
	if _, err := invoke("set-limits", "-max-operations", "1"); err == nil {
		t.Fatal("omitted active limit accepted")
	}
	if _, err := invoke("set-limits", "-max-operations", "1", "-max-active", "1"); err != nil {
		t.Fatal(err)
	}
	data, err := invoke("submit", "-handler", "echo", "-version", "v1", "-key", "a", "-input", "hello", "-join")
	if err != nil {
		t.Fatal(err)
	}
	var sub taskfused.Submission
	if err := json.Unmarshal(data, &sub); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("submit", "-handler", "echo", "-version", "v1", "-key", "b"); !errors.Is(err, taskfused.ErrCapacity) {
		t.Fatalf("capacity: %v", err)
	}
	s, err := taskfused.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Claim(ctx, taskfused.ClaimRequest{Namespace: "demo", Handler: "echo", Version: "v1", LeaseDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, c, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	data, err = invoke("usage")
	if err != nil {
		t.Fatal(err)
	}
	var u taskfused.Usage
	if err := json.Unmarshal(data, &u); err != nil || u.ResultBytes != 7 || u.Active != 0 || u.Limits.MaxActive != 1 {
		t.Fatalf("usage: %s %v", data, err)
	}
	cutoff := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	data, err = invoke("prune-results", "-before", cutoff, "-limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	var p taskfused.PruneResult
	if err := json.Unmarshal(data, &p); err != nil || len(p.OperationIDs) != 1 || p.RemovedBytes != 7 {
		t.Fatalf("prune: %s %v", data, err)
	}
	data, err = invoke("wait", "-id", sub.Operation.ID, "-timeout", "1s")
	if err != nil {
		t.Fatal(err)
	}
	var op taskfused.Operation
	if err := json.Unmarshal(data, &op); err != nil || !op.ResultPruned || op.State != taskfused.StateSucceeded {
		t.Fatalf("wait: %s %v", data, err)
	}
	for _, args := range [][]string{{"prune-results"}, {"prune-results", "-before", "invalid"}, {"prune-results", "-before", cutoff, "-limit", "201"}, {"set-limits", "-max-operations", "-1", "-max-active", "0"}} {
		if _, err := invoke(args...); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
