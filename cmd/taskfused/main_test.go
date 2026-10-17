package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

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
