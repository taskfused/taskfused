// Copyright 2026 TaskFused. All rights reserved.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/taskfused/taskfused"
	"github.com/taskfused/taskfused/localhttp"
)

type httpDemoReport struct {
	GeneratedAt        time.Time         `json:"generated_at"`
	Go                 string            `json:"go"`
	OS                 string            `json:"os"`
	Arch               string            `json:"arch"`
	BuildSettings      map[string]string `json:"build_settings"`
	ListenerFamily     string            `json:"listener_family"`
	Functional         functionalReport  `json:"functional_over_http"`
	ServerWaitDeadline bool              `json:"http_504_preserves_pending_work"`
	Retention          retentionReport   `json:"retention"`
	HTTPStatuses       map[int]int       `json:"observed_api_response_statuses"`
	ShutdownCompleted  bool              `json:"shutdown_completed"`
	Notes              []string          `json:"notes"`
}
type retentionReport struct {
	Before               taskfused.Usage `json:"before"`
	After                taskfused.Usage `json:"after"`
	PrunedResults        int             `json:"pruned_results"`
	RemovedBytes         int64           `json:"removed_result_bytes"`
	MarkersObserved      int             `json:"pruned_markers_observed_over_http"`
	RetryReplayPreserved bool            `json:"retry_replay_preserved"`
}

func runHTTPDemo(parent context.Context, n int, address string) (result httpDemoReport, err error) {
	if n < 2 || n > 1000 {
		return result, fmt.Errorf("HTTP requests must be 2–1000")
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	directory, err := os.MkdirTemp("", "taskfused-http-demo-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "http.db")
	store, err := taskfused.Open(ctx, path)
	if err != nil {
		return result, err
	}
	defer store.Close()
	listener, err := localhttp.Listen(address)
	if err != nil {
		return result, err
	}
	serverCtx, stopServer := context.WithCancel(ctx)
	stopped := make(chan error, 1)
	go func() { stopped <- localhttp.Serve(serverCtx, store, listener) }()
	// Always join serving before closing SQLite or removing its directory.
	serverJoined := false
	defer func() {
		stopServer()
		if !serverJoined {
			if stopErr := <-stopped; err == nil && stopErr != nil {
				err = stopErr
			}
		}
	}()
	transport := &http.Transport{Proxy: nil, MaxIdleConns: 128, MaxIdleConnsPerHost: 128, MaxConnsPerHost: 128, ResponseHeaderTimeout: 35 * time.Second, IdleConnTimeout: time.Second}
	defer transport.CloseIdleConnections()
	client := &httpOperations{base: "http://" + listener.Addr().String(), client: &http.Client{Transport: transport, Timeout: 40 * time.Second}, admin: store, statuses: map[int]int{}, submitted: map[string]string{}}
	executable, err := os.Executable()
	if err != nil {
		return result, err
	}
	fmt.Fprintln(os.Stderr, "HTTP demo: shared submissions and independent caller timeout...")
	shared, err := checkShared(ctx, client, executable, path, n)
	if err != nil {
		return result, fmt.Errorf("HTTP shared: %w", err)
	}
	fmt.Fprintln(os.Stderr, "HTTP demo: kill committed worker, replace it and inspect recovery...")
	crash, err := checkCrash(ctx, client, executable, path)
	if err != nil {
		return result, fmt.Errorf("HTTP crash: %w", err)
	}
	fmt.Fprintln(os.Stderr, "HTTP demo: cooperative cancellation, survivor and durable retry replay...")
	cancellation, err := checkCancellation(ctx, client, executable, path)
	if err != nil {
		return result, fmt.Errorf("HTTP cancellation: %w", err)
	}
	retry, err := checkRetry(ctx, client, executable, path)
	if err != nil {
		return result, fmt.Errorf("HTTP retry: %w", err)
	}
	if err := client.checkServerWaitDeadline(ctx); err != nil {
		return result, err
	}
	fmt.Fprintln(os.Stderr, "HTTP demo: prune explicitly through Go administration and inspect HTTP markers...")
	retained, err := checkHTTPRetention(ctx, client)
	if err != nil {
		return result, err
	}
	// Test page availability through the same actual listener. Detailed HTML and
	// injection behavior remain covered by localhttp integration tests.
	req, err := http.NewRequestWithContext(ctx, "GET", client.base+"/?namespace=demo", nil)
	if err != nil {
		return result, err
	}
	response, err := client.client.Do(req)
	if err != nil {
		return result, err
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		return result, fmt.Errorf("status page HTTP%d", response.StatusCode)
	}
	stopServer()
	err = <-stopped
	serverJoined = true
	if err != nil {
		return result, err
	}
	settings := map[string]string{}
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "-race", "CGO_ENABLED", "GOAMD64", "vcs.revision", "vcs.modified":
				settings[setting.Key] = setting.Value
			}
		}
	}
	family := "IPv4"
	if strings.Contains(listener.Addr().String(), "[") {
		family = "IPv6"
	}
	result = httpDemoReport{GeneratedAt: time.Now().UTC(), Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, BuildSettings: settings, ListenerFamily: family, Functional: functionalReport{Shared: shared, Crash: crash, Cancellation: cancellation, Retry: retry}, ServerWaitDeadline: true, Retention: retained, HTTPStatuses: client.statuses, ShutdownCompleted: true, Notes: []string{
		"Correctness demo, not a throughput benchmark or recovery SLA. Private temporary SQLite files are removed after shutdown.",
		"The HTTP server runs on an ephemeral numeric loopback listener in this process; registered workers are real child processes of the same executable.",
		"Process.Kill follows a committed-claim acknowledgment and bypasses graceful worker/store cleanup. Recovery includes the one-second lease and polling.",
		"Shared requests and outcome inspection use HTTP. Stale worker completion and explicit result pruning use the Go API; neither administrative/worker action has an HTTP route.",
		"One shared caller uses a client context deadline; a separate queued operation verifies an actual HTTP504 server wait timeout without canceling work.",
		"Only demo built-in Go handlers execute. No arbitrary submitted commands, remote access or release license is introduced.",
	}}
	return result, nil
}

func checkHTTPRetention(ctx context.Context, h *httpOperations) (retentionReport, error) {
	var result retentionReport
	before, err := h.usage(ctx)
	if err != nil {
		return result, err
	}
	if before.Operations != 6 || before.Active != 0 || before.Attempts != 7 || before.RetryActions != 1 {
		return result, fmt.Errorf("unexpected pre-retention usage: %+v", before)
	}
	result.Before = before
	for {
		// Retention remains an explicit embedding/CLI administrative action.
		pruned, err := h.admin.PruneResults(ctx, taskfused.PruneRequest{Namespace: "demo", Before: time.Now().Add(time.Second), Limit: 2})
		if err != nil {
			return result, err
		}
		if len(pruned.OperationIDs) == 0 {
			break
		}
		result.PrunedResults += len(pruned.OperationIDs)
		result.RemovedBytes += pruned.RemovedBytes
		for _, id := range pruned.OperationIDs {
			op, err := h.Get(ctx, "demo", id)
			if err != nil {
				return result, err
			}
			if op.State != taskfused.StateSucceeded || !op.ResultPruned || op.Result != nil || string(op.Input) != "shared result" {
				return result, fmt.Errorf("HTTP pruned outcome lost identity/input/state")
			}
			result.MarkersObserved++
		}
	}
	if result.PrunedResults != 4 || result.RemovedBytes != before.ResultBytes {
		return result, fmt.Errorf("retention payload counts: %+v", result)
	}
	replay, err := h.Retry(ctx, taskfused.RetryRequest{Namespace: "demo", ID: h.submitted["retry"], ActionKey: "demo-repair", ExpectedAttempts: 1, AdditionalAttempts: 1})
	if err != nil {
		return result, err
	}
	if replay.Applied || !replay.Operation.ResultPruned || replay.Operation.State != taskfused.StateSucceeded {
		return result, fmt.Errorf("pruning invalidated durable action replay")
	}
	result.RetryReplayPreserved = true
	joined, err := h.Submit(ctx, demoRequest("shared", "equivalent"))
	if err != nil {
		return result, err
	}
	if !joined.Joined || !joined.Operation.ResultPruned {
		return result, fmt.Errorf("pruning invalidated submission join")
	}
	after, err := h.usage(ctx)
	if err != nil {
		return result, err
	}
	if after.Operations != before.Operations || after.Attempts != before.Attempts || after.RetryActions != before.RetryActions || after.Events != before.Events+4 || after.PrunedResults != 4 || after.ResultBytes != 0 || after.InputBytes != before.InputBytes || after.Active != 0 {
		return result, fmt.Errorf("retention changed protected history: %+v -> %+v", before, after)
	}
	result.After = after
	return result, nil
}
