// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/taskfused/taskfused"
)

type workerEvent struct {
	Type  string          `json:"type"`
	Claim taskfused.Claim `json:"claim"`
}

type childProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	events  chan workerEvent
	done    chan struct{}
	stderr  bytes.Buffer
	err     error // Written before done closes; read only after that synchronization.
}

func startChildWorker(ctx context.Context, executable, path, mode string) (*childProcess, error) {
	p := &childProcess{events: make(chan workerEvent, 32), done: make(chan struct{})}
	p.command = exec.CommandContext(ctx, executable, "-child-worker", mode, "-child-db", path)
	p.command.Stderr = &p.stderr
	input, err := p.command.StdinPipe()
	if err != nil {
		return nil, err
	}
	p.input = input
	output, err := p.command.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	if err := p.command.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	go func() {
		defer close(p.events)
		decoder := json.NewDecoder(output)
		for {
			var event workerEvent
			if err := decoder.Decode(&event); err != nil {
				return
			}
			select {
			case p.events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { p.err = p.command.Wait(); close(p.done) }()
	return p, nil
}

func (p *childProcess) next(ctx context.Context, kind, operationID string) (workerEvent, error) {
	for {
		select {
		case e, ok := <-p.events:
			if !ok {
				return workerEvent{}, fmt.Errorf("worker event stream closed before %s", kind)
			}
			if e.Type == kind && e.Claim.Operation.ID == operationID {
				return e, nil
			}
		case <-p.done:
			return workerEvent{}, fmt.Errorf("worker stopped before %s: %v: %s", kind, p.err, p.stderr.String())
		case <-ctx.Done():
			return workerEvent{}, ctx.Err()
		}
	}
}

func (p *childProcess) stop() error {
	p.input.Close()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		p.command.Process.Kill()
		<-p.done
		return fmt.Errorf("worker did not shut down cooperatively: %s", p.stderr.String())
	}
	if p.err != nil {
		return fmt.Errorf("worker exit: %w: %s", p.err, p.stderr.String())
	}
	return nil
}

func (p *childProcess) kill() (string, error) {
	if err := p.command.Process.Kill(); err != nil {
		return "", err
	}
	<-p.done
	p.input.Close()
	if p.err == nil {
		return "", fmt.Errorf("deliberately killed worker reported a successful exit")
	}
	var exit *exec.ExitError
	if !errors.As(p.err, &exit) {
		return "", p.err
	}
	return p.err.Error(), nil
}

func runChildWorker(parent context.Context, mode, path string) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	s, err := taskfused.Open(ctx, path)
	if err != nil {
		return err
	}
	defer s.Close()
	var mu sync.Mutex
	emit := func(kind string, c taskfused.Claim) error {
		mu.Lock()
		defer mu.Unlock()
		return json.NewEncoder(os.Stdout).Encode(workerEvent{Type: kind, Claim: c})
	}
	registration := func(name string, body taskfused.Handler) taskfused.Registration {
		return taskfused.Registration{Name: name, Version: "v1", Handle: func(ctx context.Context, c taskfused.Claim) ([]byte, error) {
			if err := emit("started", c); err != nil {
				return nil, err
			}
			return body(ctx, c)
		}}
	}
	echo := func(delay time.Duration) taskfused.Handler {
		return func(ctx context.Context, c taskfused.Claim) ([]byte, error) {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				return c.Operation.Input, nil
			}
		}
	}
	var handlers []taskfused.Registration
	switch mode {
	case "shared":
		handlers = []taskfused.Registration{registration("shared", echo(200*time.Millisecond))}
	case "crash-block":
		handlers = []taskfused.Registration{registration("crash", func(ctx context.Context, c taskfused.Claim) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() })}
	case "crash-recover":
		handlers = []taskfused.Registration{registration("crash", echo(10*time.Millisecond))}
	case "cancel":
		handlers = []taskfused.Registration{
			registration("cancel", func(ctx context.Context, c taskfused.Claim) ([]byte, error) {
				<-ctx.Done()
				if err := emit("cancellation_observed", c); err != nil {
					return nil, err
				}
				return nil, ctx.Err()
			}),
			registration("survivor", echo(10*time.Millisecond)),
		}
	case "retry":
		handlers = []taskfused.Registration{registration("retry", func(ctx context.Context, c taskfused.Claim) ([]byte, error) {
			if c.Attempt.Number == 1 {
				return nil, fmt.Errorf("deterministic first-attempt failure")
			}
			return c.Operation.Input, nil
		})}
	default:
		return fmt.Errorf("unknown internal worker mode %q", mode)
	}
	w, err := taskfused.NewWorker(s, taskfused.WorkerOptions{Namespace: "demo", Handlers: handlers, Concurrency: 2, LeaseDuration: time.Second, PollInterval: 20 * time.Millisecond})
	if err != nil {
		return err
	}
	err = w.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
