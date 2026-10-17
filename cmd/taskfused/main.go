// Copyright 2026 TaskFused. All rights reserved.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/taskfused/taskfused"
	"github.com/taskfused/taskfused/localhttp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "taskfused:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taskfused <submit|get|attempts|events|wait|cancel|retry|usage|set-limits|prune-results|serve> [flags]; use -h after a command")
	}
	if args[0] == "serve" {
		return serve(ctx, args[1:], errOut)
	}
	switch args[0] {
	case "submit", "get", "attempts", "events", "wait", "cancel", "retry", "usage", "set-limits", "prune-results":
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	path := fs.String("db", "taskfused.db", "local SQLite database path")
	namespace := fs.String("namespace", "", "required operation namespace")
	var handler, version, key, input, id string
	var join bool
	var maxAttempts int
	var retryDelay time.Duration
	var timeout time.Duration
	var after int64
	var limit, expectedAttempts, additionalAttempts int
	var actionKey, before string
	var maxOperations, maxActive int64
	var cutoff time.Time
	if args[0] == "submit" {
		fs.StringVar(&handler, "handler", "", "required handler name")
		fs.StringVar(&version, "version", "", "required handler version")
		fs.StringVar(&key, "key", "", "required durable operation key")
		fs.StringVar(&input, "input", "", "UTF-8 input (library API also accepts arbitrary bytes)")
		fs.IntVar(&maxAttempts, "max-attempts", 3, "initial execution attempt budget (1–100)")
		fs.DurationVar(&retryDelay, "retry-delay", time.Second, "initial exponential retry delay (1ms–1h)")
		fs.BoolVar(&join, "join", false, "join an existing operation with identical input")
	} else if args[0] == "set-limits" {
		fs.Int64Var(&maxOperations, "max-operations", 0, "maximum retained identities; zero unlimited (replaces prior policy)")
		fs.Int64Var(&maxActive, "max-active", 0, "maximum queued/running/retry_wait operations; zero unlimited")
	} else if args[0] == "prune-results" {
		fs.StringVar(&before, "before", "", "required exclusive completion cutoff (RFC3339)")
		fs.IntVar(&limit, "limit", 50, "maximum succeeded results to remove (1–200); irreversible")
	} else if args[0] != "usage" {
		fs.StringVar(&id, "id", "", "required operation ID")
		if args[0] == "events" {
			fs.Int64Var(&after, "after", 0, "exclusive event sequence cursor")
			fs.IntVar(&limit, "limit", 50, "event page size (1–200)")
		}
		if args[0] == "retry" {
			fs.StringVar(&actionKey, "action-key", "", "required durable retry action key")
			fs.IntVar(&expectedAttempts, "expected-attempts", 0, "required failed attempt count observed by the operator")
			fs.IntVar(&additionalAttempts, "additional-attempts", 1, "additional claims to grant (1–100)")
		}
		if args[0] == "wait" {
			fs.DurationVar(&timeout, "timeout", 30*time.Second, "positive wait timeout; does not cancel the operation")
		}
	}
	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *namespace == "" {
		return fmt.Errorf("-namespace is required")
	}
	if args[0] != "submit" && args[0] != "usage" && args[0] != "set-limits" && args[0] != "prune-results" && id == "" {
		return fmt.Errorf("-id is required")
	}
	if args[0] == "submit" && (handler == "" || version == "" || key == "") {
		return fmt.Errorf("-handler, -version and -key are required")
	}
	if args[0] == "wait" && timeout <= 0 {
		return fmt.Errorf("-timeout must be positive")
	}
	if args[0] == "retry" && (actionKey == "" || expectedAttempts < 1) {
		return fmt.Errorf("-action-key and positive -expected-attempts are required")
	}
	if args[0] == "set-limits" {
		supplied := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { supplied[f.Name] = true })
		if !supplied["max-operations"] || !supplied["max-active"] {
			return fmt.Errorf("-max-operations and -max-active are both required; zero means unlimited")
		}
		if maxOperations < 0 || maxActive < 0 {
			return fmt.Errorf("limits must be nonnegative")
		}
	}
	if args[0] == "prune-results" {
		var err error
		cutoff, err = time.Parse(time.RFC3339Nano, before)
		if err != nil || cutoff.IsZero() {
			return fmt.Errorf("-before requires a nonzero RFC3339 completion cutoff")
		}
		if limit < 1 || limit > taskfused.MaxPruneBatch {
			return fmt.Errorf("-limit must be 1–200")
		}
	}
	store, err := taskfused.Open(ctx, *path)
	if err != nil {
		return err
	}
	defer store.Close()
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if args[0] == "set-limits" {
		limits := taskfused.Limits{MaxOperations: maxOperations, MaxActive: maxActive}
		if err := store.SetLimits(ctx, *namespace, limits); err != nil {
			return err
		}
		return encoder.Encode(limits)
	}
	if args[0] == "usage" {
		result, err := store.Usage(ctx, *namespace)
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	}
	if args[0] == "prune-results" {
		result, err := store.PruneResults(ctx, taskfused.PruneRequest{Namespace: *namespace, Before: cutoff, Limit: limit})
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	}
	if args[0] == "submit" {
		result, err := store.Submit(ctx, taskfused.Request{Namespace: *namespace, Handler: handler, Version: version, Key: key, Input: []byte(input), JoinExisting: join, MaxAttempts: maxAttempts, RetryDelay: retryDelay})
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	}
	if args[0] == "attempts" {
		result, err := store.Attempts(ctx, *namespace, id)
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	}
	if args[0] == "events" {
		result, err := store.Events(ctx, *namespace, id, taskfused.EventQuery{After: after, Limit: limit})
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	}
	if args[0] == "retry" {
		result, err := store.Retry(ctx, taskfused.RetryRequest{Namespace: *namespace, ID: id, ActionKey: actionKey, ExpectedAttempts: expectedAttempts, AdditionalAttempts: additionalAttempts})
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	}
	if args[0] == "cancel" {
		result, err := store.Cancel(ctx, *namespace, id)
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	}
	if args[0] == "wait" {
		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		result, err := store.Wait(waitCtx, *namespace, id)
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	}
	result, err := store.Get(ctx, *namespace, id)
	if err != nil {
		return err
	}
	return encoder.Encode(result)
}

// serve leaves handler registration/execution to a separate embedding worker.
func serve(ctx context.Context, args []string, errOut io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(errOut)
	path := fs.String("db", "taskfused.db", "local SQLite database path")
	address := fs.String("listen", "127.0.0.1:8080", "literal loopback IP:port only")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	listener, err := localhttp.Listen(*address)
	if err != nil {
		return err
	}
	defer listener.Close()
	store, err := taskfused.Open(ctx, *path)
	if err != nil {
		return err
	}
	defer store.Close()
	fmt.Fprintln(errOut, "TaskFused local status: http://"+listener.Addr().String())
	return localhttp.Serve(ctx, store, listener)
}
