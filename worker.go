// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package taskfused

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Handler receives a stable operation ID and a fresh attempt ID. It must cooperate
// with context cancellation and use external idempotency for repeatable effects.
type Handler func(context.Context, Claim) ([]byte, error)

type Registration struct {
	Name    string
	Version string
	Handle  Handler
}

type WorkerOptions struct {
	Namespace     string
	Handlers      []Registration
	Concurrency   int
	LeaseDuration time.Duration
	PollInterval  time.Duration
}

// Worker has an immutable handler registry and is safe for concurrent RunOnce
// calls. Run supplies bounded concurrency; direct callers must bound their calls.
type Worker struct {
	store   *Store
	options WorkerOptions
	next    atomic.Uint64
}

func NewWorker(s *Store, o WorkerOptions) (*Worker, error) {
	if s == nil || len(o.Handlers) == 0 {
		return nil, fmt.Errorf("%w: store and handlers are required", ErrInvalid)
	}
	if o.Concurrency == 0 {
		o.Concurrency = 1
	}
	if o.LeaseDuration == 0 {
		o.LeaseDuration = 30 * time.Second
	}
	if o.PollInterval == 0 {
		o.PollInterval = 100 * time.Millisecond
	}
	if o.Concurrency < 1 || o.Concurrency > 128 || !validLease(o.LeaseDuration) || o.PollInterval < time.Millisecond || o.PollInterval > time.Minute {
		return nil, fmt.Errorf("%w: invalid worker limits", ErrInvalid)
	}
	seen := map[[2]string]bool{}
	for _, h := range o.Handlers {
		if err := validate(Request{Namespace: o.Namespace, Handler: h.Name, Version: h.Version, Key: "worker"}); err != nil {
			return nil, err
		}
		key := [2]string{h.Name, h.Version}
		if h.Handle == nil || seen[key] {
			return nil, fmt.Errorf("%w: missing or duplicate handler", ErrInvalid)
		}
		seen[key] = true
	}
	o.Handlers = append([]Registration(nil), o.Handlers...)
	return &Worker{store: s, options: o}, nil
}

// RunOnce claims and executes at most one operation, renewing its lease while the
// handler runs. A persisted handler failure counts as handled work, not a runner
// error. Explicit operation cancellation is normal handled work. Other
// store/ownership errors are returned. Worker cancellation leaves unfinished work
// for expiry recovery and consumes the current attempt's budget.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	start := int((w.next.Add(1) - 1) % uint64(len(w.options.Handlers)))
	for i := 0; i < len(w.options.Handlers); i++ {
		h := w.options.Handlers[(start+i)%len(w.options.Handlers)]
		claim, err := w.store.Claim(ctx, ClaimRequest{Namespace: w.options.Namespace, Handler: h.Name, Version: h.Version, LeaseDuration: w.options.LeaseDuration})
		if errors.Is(err, ErrNoWork) {
			continue
		}
		if err != nil {
			return false, err
		}
		err = w.execute(ctx, claim, h.Handle)
		if errors.Is(err, ErrOperationCanceled) {
			return true, nil
		}
		return true, err
	}
	return false, nil
}

func invoke(ctx context.Context, c Claim, h Handler) (result []byte, err error) {
	defer func() {
		if p := recover(); p != nil {
			result = nil
			err = fmt.Errorf("handler panic: %v", p)
		}
	}()
	return h(ctx, c)
}

func (w *Worker) execute(ctx context.Context, c Claim, h Handler) error {
	handlerCtx, cancelHandler := context.WithCancel(ctx)
	defer cancelHandler()
	renewalCtx, stopRenewal := context.WithCancel(ctx)
	defer stopRenewal()
	renewed := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(w.options.LeaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-renewalCtx.Done():
				renewed <- nil
				return
			case <-ticker.C:
				if _, err := w.store.Renew(renewalCtx, c, w.options.LeaseDuration); err != nil {
					// A renewal canceled because the handler has already returned is normal.
					if renewalCtx.Err() != nil {
						renewed <- nil
						return
					}
					cancelHandler()
					renewed <- err
					return
				}
			}
		}
	}()
	result, handlerErr := invoke(handlerCtx, c, h)
	stopRenewal()
	renewalErr := <-renewed // Prevent renewal racing with final result commit.
	if err := ctx.Err(); err != nil {
		return err
	}
	if renewalErr != nil {
		return renewalErr
	}
	if len(result) > MaxResultBytes && handlerErr == nil {
		handlerErr = fmt.Errorf("handler result exceeds %d bytes", MaxResultBytes)
	}
	if handlerErr != nil {
		message := handlerErr.Error()
		if strings.TrimSpace(message) == "" {
			message = "handler returned an error without a message"
		}
		if len(message) > MaxErrorBytes {
			message = message[:MaxErrorBytes]
		}
		return w.store.Fail(ctx, c, message)
	}
	return w.store.Complete(ctx, c, result)
}

// Run serves registered handlers until canceled or a storage/ownership failure
// stops the worker. It waits for handlers to return; Go cannot forcibly terminate
// a handler that ignores context. Independent processes may run their own workers.
func (w *Worker) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, w.options.Concurrency)
	for i := 0; i < w.options.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if runCtx.Err() != nil {
					return
				}
				worked, err := w.RunOnce(runCtx)
				if err != nil {
					if runCtx.Err() == nil {
						errs <- err
						cancel()
					}
					return
				}
				if !worked {
					timer := time.NewTimer(w.options.PollInterval)
					select {
					case <-runCtx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}
		}()
	}
	wg.Wait()
	select {
	case err := <-errs:
		return err
	default:
		return ctx.Err()
	}
}
