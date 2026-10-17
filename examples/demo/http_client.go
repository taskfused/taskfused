// Copyright 2026 TaskFused. All rights reserved.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/taskfused/taskfused"
)

// This adapter is demo scaffolding, not a separately versioned HTTP SDK.
type httpOperations struct {
	base      string
	client    *http.Client
	admin     *taskfused.Store
	mu        sync.Mutex
	statuses  map[int]int
	submitted map[string]string
}

func (h *httpOperations) request(ctx context.Context, method, path string, body, result any) error {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.base+path, input)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	h.mu.Lock()
	h.statuses[response.StatusCode]++
	h.mu.Unlock()
	// The largest normal operation response includes 1MiB each of input/result,
	// base64-encoded. Bound demo response reads independently of the request cap.
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 4<<20 {
		return fmt.Errorf("HTTP response exceeded demo bound")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusGatewayTimeout {
			return context.DeadlineExceeded
		}
		return fmt.Errorf("%s %s returned HTTP %d: %s", method, path, response.StatusCode, data)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
func operationPath(namespace, id string) string {
	return "/api/namespaces/" + url.PathEscape(namespace) + "/operations/" + url.PathEscape(id)
}
func (h *httpOperations) Submit(ctx context.Context, r taskfused.Request) (taskfused.Submission, error) {
	body := struct {
		Handler     string        `json:"handler"`
		Version     string        `json:"version"`
		Key         string        `json:"key"`
		Input       []byte        `json:"input"`
		Join        bool          `json:"join_existing"`
		MaxAttempts int           `json:"max_attempts"`
		RetryDelay  time.Duration `json:"retry_delay"`
	}{r.Handler, r.Version, r.Key, r.Input, r.JoinExisting, r.MaxAttempts, r.RetryDelay}
	var sub taskfused.Submission
	err := h.request(ctx, "POST", "/api/namespaces/"+url.PathEscape(r.Namespace)+"/operations", body, &sub)
	if err == nil {
		h.mu.Lock()
		h.submitted[r.Handler] = sub.Operation.ID
		h.mu.Unlock()
	}
	return sub, err
}
func (h *httpOperations) Get(ctx context.Context, ns, id string) (taskfused.Operation, error) {
	var result taskfused.Operation
	err := h.request(ctx, "GET", operationPath(ns, id), nil, &result)
	return result, err
}
func (h *httpOperations) Wait(ctx context.Context, ns, id string) (taskfused.Operation, error) {
	var result taskfused.Operation
	err := h.request(ctx, "GET", operationPath(ns, id)+"/wait", nil, &result)
	// Client context cancellation is the caller's timeout; it never sends Cancel.
	if err != nil && ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, err
}
func (h *httpOperations) Attempts(ctx context.Context, ns, id string) ([]taskfused.Attempt, error) {
	var result []taskfused.Attempt
	err := h.request(ctx, "GET", operationPath(ns, id)+"/attempts", nil, &result)
	return result, err
}
func (h *httpOperations) Cancel(ctx context.Context, ns, id string) (taskfused.Cancellation, error) {
	var result taskfused.Cancellation
	err := h.request(ctx, "POST", operationPath(ns, id)+"/cancel", struct{}{}, &result)
	return result, err
}
func (h *httpOperations) Retry(ctx context.Context, r taskfused.RetryRequest) (taskfused.RetryResult, error) {
	body := map[string]any{"action_key": r.ActionKey, "expected_attempts": r.ExpectedAttempts, "additional_attempts": r.AdditionalAttempts}
	var result taskfused.RetryResult
	err := h.request(ctx, "POST", operationPath(r.Namespace, r.ID)+"/retry", body, &result)
	return result, err
}
func (h *httpOperations) Events(ctx context.Context, ns, id string, q taskfused.EventQuery) (taskfused.EventPage, error) {
	var result taskfused.EventPage
	err := h.request(ctx, "GET", operationPath(ns, id)+"/events?after="+strconv.FormatInt(q.After, 10)+"&limit="+strconv.Itoa(q.Limit), nil, &result)
	return result, err
}
func (h *httpOperations) Complete(ctx context.Context, c taskfused.Claim, result []byte) error {
	// Deliberately a worker-only Go call. The public HTTP API cannot commit results.
	return h.admin.Complete(ctx, c, result)
}
func (h *httpOperations) usage(ctx context.Context) (taskfused.Usage, error) {
	var result taskfused.Usage
	err := h.request(ctx, "GET", "/api/namespaces/demo/usage", nil, &result)
	return result, err
}

func (h *httpOperations) checkServerWaitDeadline(ctx context.Context) error {
	sub, err := h.Submit(ctx, demoRequest("deadline", "server-timeout"))
	if err != nil {
		return err
	}
	var result taskfused.Operation
	err = h.request(ctx, "GET", operationPath("demo", sub.Operation.ID)+"/wait?timeout=20ms", nil, &result)
	if !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("expected HTTP504 wait deadline, got %v", err)
	}
	op, err := h.Get(ctx, "demo", sub.Operation.ID)
	if err != nil {
		return err
	}
	if op.State != taskfused.StateQueued || op.AttemptCount != 0 {
		return fmt.Errorf("server wait timeout changed pending work")
	}
	_, err = h.Cancel(ctx, "demo", op.ID)
	return err
}
