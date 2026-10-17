package main

import (
	"bytes"
	"context"
	"encoding/json"
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
