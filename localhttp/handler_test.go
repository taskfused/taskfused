// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0

package localhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taskfused/taskfused"
)

func fixture(t *testing.T) (*taskfused.Store, *httptest.Server) {
	t.Helper()
	s, err := taskfused.Open(context.Background(), filepath.Join(t.TempDir(), "http.db"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	h, err := NewHandler(s, server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = h
	server.Start()
	t.Cleanup(func() { server.Close(); s.Close() })
	return s, server
}

func call(t *testing.T, server *httptest.Server, method, path string, body any, want int) []byte {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s: status %d want %d: %s", method, path, res.StatusCode, want, data)
	}
	if want >= 400 {
		var envelope struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(data, &envelope); err != nil || envelope.Error.Code == "" {
			t.Fatalf("non-JSON error: %s %v", data, err)
		}
	}
	return data
}

func submit(t *testing.T, server *httptest.Server, key string, max int) taskfused.Submission {
	t.Helper()
	data := call(t, server, "POST", "/api/namespaces/demo/operations", map[string]any{"handler": "echo", "version": "v1", "key": key, "input": []byte("hello"), "join_existing": true, "max_attempts": max}, 201)
	var sub taskfused.Submission
	if err := json.Unmarshal(data, &sub); err != nil {
		t.Fatal(err)
	}
	return sub
}

func claim(t *testing.T, s *taskfused.Store) taskfused.Claim {
	t.Helper()
	c, err := s.Claim(context.Background(), taskfused.ClaimRequest{Namespace: "demo", Handler: "echo", Version: "v1", LeaseDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHTTPSharedExecutionAndWaiting(t *testing.T) {
	s, server := fixture(t)
	sub := submit(t, server, "shared", 1)
	body := map[string]any{"handler": "echo", "version": "v1", "key": "shared", "input": []byte("hello"), "join_existing": true, "max_attempts": 1}
	data := call(t, server, "POST", "/api/namespaces/demo/operations", body, 200)
	var joined taskfused.Submission
	json.Unmarshal(data, &joined)
	if !joined.Joined || joined.Operation.ID != sub.Operation.ID {
		t.Fatalf("join: %s", data)
	}
	body["input"] = []byte("changed")
	call(t, server, "POST", "/api/namespaces/demo/operations", body, 409)
	path := "/api/namespaces/demo/operations/" + sub.Operation.ID
	call(t, server, "GET", path+"/wait?timeout=10ms", nil, 504)
	got, err := s.Get(context.Background(), "demo", sub.Operation.ID)
	if err != nil || got.State != taskfused.StateQueued || got.AttemptCount != 0 {
		t.Fatalf("wait timeout mutated work: %+v %v", got, err)
	}
	// An independent HTTP waiter must survive another caller timing out.
	waited := make(chan *http.Response, 1)
	errs := make(chan error, 1)
	go func() {
		res, err := server.Client().Get(server.URL + path + "/wait?timeout=3s")
		if err != nil {
			errs <- err
			return
		}
		waited <- res
	}()
	c := claim(t, s)
	if err := s.Complete(context.Background(), c, []byte("shared result")); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-waited:
		defer res.Body.Close()
		var op taskfused.Operation
		err := json.NewDecoder(res.Body).Decode(&op)
		if err != nil || res.StatusCode != 200 || string(op.Result) != "shared result" {
			t.Fatalf("wait: %+v %v", op, err)
		}
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP wait did not complete")
	}
	call(t, server, "GET", path+"/attempts", nil, 200)
	data = call(t, server, "GET", path+"/events?limit=2", nil, 200)
	var page taskfused.EventPage
	json.Unmarshal(data, &page)
	if len(page.Events) != 2 || !page.HasMore {
		t.Fatalf("event page: %s", data)
	}
	call(t, server, "GET", path+"/events?after="+jsonNumber(page.NextAfter), nil, 200)
	call(t, server, "GET", "/api/namespaces/other/operations/"+sub.Operation.ID, nil, 404)
	if _, err := s.PruneResults(context.Background(), taskfused.PruneRequest{Namespace: "demo", Before: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	data = call(t, server, "GET", path+"/wait", nil, 200)
	var pruned taskfused.Operation
	json.Unmarshal(data, &pruned)
	if !pruned.ResultPruned || pruned.State != taskfused.StateSucceeded {
		t.Fatalf("pruned HTTP result: %s", data)
	}
	data = call(t, server, "GET", "/?namespace=demo&id="+sub.Operation.ID, nil, 200)
	if !bytes.Contains(data, []byte("Pruned — succeeded")) {
		t.Fatalf("missing prune status: %s", data)
	}
}

func jsonNumber(n int64) string { data, _ := json.Marshal(n); return string(data) }

func TestHTTPCancelRetryAndCapacity(t *testing.T) {
	s, server := fixture(t)
	sub := submit(t, server, "retry", 1)
	path := "/api/namespaces/demo/operations/" + sub.Operation.ID
	c := claim(t, s)
	if err := s.Fail(context.Background(), c, "failure"); err != nil {
		t.Fatal(err)
	}
	call(t, server, "GET", path+"/wait", nil, 200) // Failed execution is a retrieved outcome.
	retry := map[string]any{"action_key": "action-1", "expected_attempts": 1, "additional_attempts": 1}
	data := call(t, server, "POST", path+"/retry", retry, 200)
	var result taskfused.RetryResult
	json.Unmarshal(data, &result)
	if !result.Applied {
		t.Fatalf("grant: %s", data)
	}
	if err := s.SetLimits(context.Background(), "demo", taskfused.Limits{MaxActive: 1, MaxOperations: 1}); err != nil {
		t.Fatal(err)
	}
	data = call(t, server, "POST", path+"/retry", retry, 200)
	json.Unmarshal(data, &result)
	if result.Applied {
		t.Fatalf("replayed grant: %s", data)
	}
	call(t, server, "POST", "/api/namespaces/demo/operations", map[string]any{"handler": "echo", "version": "v1", "key": "over-limit"}, 429)
	call(t, server, "POST", path+"/cancel", map[string]any{}, 200)
	call(t, server, "GET", path+"/wait", nil, 200)
	call(t, server, "POST", path+"/cancel", map[string]any{}, 200)
	retry["action_key"] = "action-2"
	call(t, server, "POST", path+"/retry", retry, 409)
	data = call(t, server, "GET", "/api/namespaces/demo/usage", nil, 200)
	var usage taskfused.Usage
	json.Unmarshal(data, &usage)
	if usage.Active != 0 || usage.RetryActions != 1 || usage.Operations != 1 {
		t.Fatalf("usage: %s", data)
	}
}

func TestHTTPValidationAndBrowserBoundary(t *testing.T) {
	s, server := fixture(t)
	path := "/api/namespaces/demo/operations"
	for _, test := range []struct {
		body, content string
		status        int
	}{
		{`{}`, "text/plain", 415}, {`null`, "application/json", 400}, {`[]`, "application/json", 400}, {`{"unknown":1}`, "application/json", 400},
		{`{} {}`, "application/json", 400}, {`{"handler":"echo","version":"v1","key":"a","input":"!badbase64"}`, "application/json", 400},
		{strings.Repeat("x", MaxBodyBytes+1), "application/json", 413},
	} {
		req, _ := http.NewRequest("POST", server.URL+path, strings.NewReader(test.body))
		req.Header.Set("Content-Type", test.content)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != test.status {
			t.Fatalf("validation status %d want %d", res.StatusCode, test.status)
		}
	}
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Host = "attacker.invalid" }, func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") }, func(r *http.Request) { r.Header.Set("Origin", "null") }, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }} {
		req, _ := http.NewRequest("POST", server.URL+path, strings.NewReader(`{"handler":"echo","version":"v1","key":"blocked"}`))
		req.Header.Set("Content-Type", "application/json")
		change(req)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatalf("browser boundary: %d", res.StatusCode)
		}
	}
	usage, err := s.Usage(context.Background(), "demo")
	if err != nil || usage.Operations != 0 {
		t.Fatalf("rejected requests wrote data: %+v %v", usage, err)
	}
	call(t, server, "POST", "/api/namespaces/demo/usage", map[string]any{}, 405)
	call(t, server, "PUT", path, map[string]any{}, 405)
	call(t, server, "GET", "/does-not-exist", nil, 404)
	call(t, server, "GET", path+"/missing/wait?timeout=31s", nil, 400)
	call(t, server, "GET", path+"/missing/events?after=bad", nil, 400)
	call(t, server, "GET", path+"/missing/events?limit=0", nil, 400)
	call(t, server, "GET", "/?id=missing", nil, 400)
	// Same-origin is accepted, but never emits permissive CORS headers.
	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	req.Header.Set("Origin", server.URL)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Access-Control-Allow-Origin") != "" || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("same origin: %+v", res)
	}
}

func TestStatusPageEscapesOperationText(t *testing.T) {
	_, server := fixture(t)
	sub := submit(t, server, `<script>alert("x")</script>`, 1)
	data := call(t, server, "GET", "/?namespace=demo&id="+url.QueryEscape(sub.Operation.ID), nil, 200)
	if bytes.Contains(data, []byte("<script>")) || !bytes.Contains(data, []byte("&lt;script&gt;")) {
		t.Fatalf("unescaped operation text: %s", data)
	}
}

func TestHTTPClientCancellationLeavesWork(t *testing.T) {
	s, server := fixture(t)
	sub := submit(t, server, "disconnect", 1)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/namespaces/demo/operations/"+sub.Operation.ID+"/wait", nil)
	done := make(chan error, 1)
	go func() {
		res, err := server.Client().Do(req)
		if res != nil {
			res.Body.Close()
		}
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("client cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request stuck")
	}
	op, err := s.Get(context.Background(), "demo", sub.Operation.ID)
	if err != nil || op.State != taskfused.StateQueued {
		t.Fatalf("disconnect changed work: %+v %v", op, err)
	}
}
