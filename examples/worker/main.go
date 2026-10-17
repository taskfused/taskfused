// Copyright 2026 TaskFused. All rights reserved.
// An intentionally small worker with one built-in echo handler. It is not a
// general command runner and never evaluates submitted text as executable code.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/taskfused/taskfused"
)

func main() {
	path := flag.String("db", "demo.db", "local SQLite path")
	delay := flag.Duration("delay", time.Second, "simulated handler duration")
	failUntil := flag.Int("fail-until-attempt", 0, "demo only: fail attempts up to this lifetime number")
	concurrency := flag.Int("concurrency", 2, "maximum concurrent handlers")
	flag.Parse()
	if *failUntil < 0 {
		fmt.Fprintln(os.Stderr, "-fail-until-attempt must be nonnegative")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := taskfused.Open(ctx, *path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer s.Close()
	w, err := taskfused.NewWorker(s, taskfused.WorkerOptions{
		Namespace: "demo", Concurrency: *concurrency, LeaseDuration: 3 * time.Second,
		Handlers: []taskfused.Registration{{Name: "echo", Version: "v1", Handle: func(ctx context.Context, c taskfused.Claim) ([]byte, error) {
			fmt.Printf("operation=%s attempt=%s number=%d token=%d\n", c.Operation.ID, c.Attempt.ID, c.Attempt.Number, c.Attempt.Token)
			timer := time.NewTimer(*delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				if c.Attempt.Number <= *failUntil {
					return nil, fmt.Errorf("simulated failure on attempt %d", c.Attempt.Number)
				}
				return c.Operation.Input, nil
			}
		}}},
	})
	if err == nil {
		err = w.Run(ctx)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
