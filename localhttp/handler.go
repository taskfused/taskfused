// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package localhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/taskfused/taskfused"
)

const MaxBodyBytes = 2 << 20
const MaxWait = 30 * time.Second

type apiError struct {
	status        int
	code, message string
}

func (e *apiError) Error() string { return e.message }
func bad(message string) error    { return &apiError{400, "invalid_request", message} }

// NewHandler enforces an exact numeric-loopback authority (IP:port) on every
// request. Serve supplies the actual bound address. Embedders must also enforce
// loopback binding and suitable transport timeouts; prefer Serve for normal use.
func NewHandler(store *taskfused.Store, authority string) (http.Handler, error) {
	return NewHandlerWithLogger(store, authority, slog.Default())
}

// NewHandlerWithLogger reports internal HTTP500 failures using safe metadata only:
// method, registered route pattern, error type and SQLite numeric codes. Raw error
// messages, request bodies, paths, query values and operation identifiers are omitted.
// A nil logger uses slog.Default. The logger must support concurrent requests.
func NewHandlerWithLogger(store *taskfused.Store, authority string, logger *slog.Logger) (http.Handler, error) {
	if logger == nil {
		logger = slog.Default()
	}
	host, port, err := net.SplitHostPort(authority)
	ip := net.ParseIP(host)
	n, perr := strconv.Atoi(port)
	if store == nil || err != nil || ip == nil || !ip.IsLoopback() || perr != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("handler: Store and literal loopback authority required")
	}
	mux := http.NewServeMux()
	wrap := func(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			duration := 5 * time.Second
			if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/wait") {
				duration = MaxWait
			}
			ctx, cancel := context.WithTimeout(r.Context(), duration)
			defer cancel()
			if err := fn(w, r.WithContext(ctx)); err != nil {
				if status := writeError(w, err); status == http.StatusInternalServerError {
					attrs := []any{"method", r.Method, "route", r.Pattern, "error_type", fmt.Sprintf("%T", err)}
					var storageErr sqlite3.Error
					if errors.As(err, &storageErr) {
						attrs = append(attrs, "sqlite_code", int(storageErr.Code), "sqlite_extended_code", int(storageErr.ExtendedCode))
					}
					logger.ErrorContext(r.Context(), "taskfused request failed", attrs...)
				}
			}
		}
	}
	mux.HandleFunc("POST /api/namespaces/{namespace}/operations", wrap(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Handler     string        `json:"handler"`
			Version     string        `json:"version"`
			Key         string        `json:"key"`
			Input       []byte        `json:"input"`
			Join        bool          `json:"join_existing"`
			MaxAttempts int           `json:"max_attempts"`
			RetryDelay  time.Duration `json:"retry_delay"`
		}
		if err := decode(w, r, &body); err != nil {
			return err
		}
		sub, err := store.Submit(r.Context(), taskfused.Request{Namespace: r.PathValue("namespace"), Handler: body.Handler, Version: body.Version, Key: body.Key, Input: body.Input, JoinExisting: body.Join, MaxAttempts: body.MaxAttempts, RetryDelay: body.RetryDelay})
		if err != nil {
			return err
		}
		status := http.StatusCreated
		if sub.Joined {
			status = http.StatusOK
		}
		return writeJSON(w, status, sub)
	}))
	prefix := "/api/namespaces/{namespace}/operations/{id}"
	mux.HandleFunc("GET "+prefix, wrap(func(w http.ResponseWriter, r *http.Request) error {
		op, err := store.Get(r.Context(), r.PathValue("namespace"), r.PathValue("id"))
		if err != nil {
			return err
		}
		return writeJSON(w, 200, op)
	}))
	mux.HandleFunc("GET "+prefix+"/wait", wrap(func(w http.ResponseWriter, r *http.Request) error {
		duration := MaxWait
		if value := r.URL.Query().Get("timeout"); value != "" {
			var err error
			duration, err = time.ParseDuration(value)
			if err != nil || duration <= 0 || duration > MaxWait {
				return bad("timeout must be positive and at most 30s")
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), duration)
		defer cancel()
		op, err := store.Wait(ctx, r.PathValue("namespace"), r.PathValue("id"))
		if err != nil {
			return err
		}
		return writeJSON(w, 200, op)
	}))
	mux.HandleFunc("GET "+prefix+"/attempts", wrap(func(w http.ResponseWriter, r *http.Request) error {
		result, err := store.Attempts(r.Context(), r.PathValue("namespace"), r.PathValue("id"))
		if err != nil {
			return err
		}
		return writeJSON(w, 200, result)
	}))
	mux.HandleFunc("GET "+prefix+"/events", wrap(func(w http.ResponseWriter, r *http.Request) error {
		var q taskfused.EventQuery
		if value := r.URL.Query().Get("after"); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return bad("after must be a nonnegative integer")
			}
			q.After = n
		}
		if value := r.URL.Query().Get("limit"); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				return bad("limit must be an integer from 1 to 200")
			}
			if n < 1 {
				return bad("limit must be an integer from 1 to 200")
			}
			q.Limit = n
		}
		result, err := store.Events(r.Context(), r.PathValue("namespace"), r.PathValue("id"), q)
		if err != nil {
			return err
		}
		return writeJSON(w, 200, result)
	}))
	mux.HandleFunc("POST "+prefix+"/cancel", wrap(func(w http.ResponseWriter, r *http.Request) error {
		var body struct{}
		if err := decode(w, r, &body); err != nil {
			return err
		}
		result, err := store.Cancel(r.Context(), r.PathValue("namespace"), r.PathValue("id"))
		if err != nil {
			return err
		}
		return writeJSON(w, 200, result)
	}))
	mux.HandleFunc("POST "+prefix+"/retry", wrap(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			ActionKey  string `json:"action_key"`
			Expected   int    `json:"expected_attempts"`
			Additional int    `json:"additional_attempts"`
		}
		if err := decode(w, r, &body); err != nil {
			return err
		}
		result, err := store.Retry(r.Context(), taskfused.RetryRequest{Namespace: r.PathValue("namespace"), ID: r.PathValue("id"), ActionKey: body.ActionKey, ExpectedAttempts: body.Expected, AdditionalAttempts: body.Additional})
		if err != nil {
			return err
		}
		return writeJSON(w, 200, result)
	}))
	mux.HandleFunc("GET /api/namespaces/{namespace}/usage", wrap(func(w http.ResponseWriter, r *http.Request) error {
		result, err := store.Usage(r.Context(), r.PathValue("namespace"))
		if err != nil {
			return err
		}
		return writeJSON(w, 200, result)
	}))
	mux.HandleFunc("GET /{$}", wrap(statusPage(store)))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &apiError{404, "not_found", "route not found"})
	})
	// Dispatch manually checks allowed methods because ServeMux's automatic 405
	// response is plain text. Keep application errors uniformly JSON.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		if r.Host != authority || (r.URL.IsAbs() && r.URL.Host != authority) {
			writeError(w, &apiError{403, "forbidden", "unexpected Host"})
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+authority {
			writeError(w, &apiError{403, "forbidden", "cross-origin requests are not allowed"})
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, &apiError{403, "forbidden", "cross-site requests are not allowed"})
			return
		}
		// Avoid implicit HEAD responses for GET endpoints, and reject unsupported verbs.
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			writeError(w, &apiError{405, "method_not_allowed", "use GET or POST"})
			return
		}
		_, pattern := mux.Handler(r)
		if pattern == "/" {
			// Probe the other allowed method solely to identify method mismatches.
			probe := r.Clone(r.Context())
			if r.Method == http.MethodGet {
				probe.Method = http.MethodPost
			} else {
				probe.Method = http.MethodGet
			}
			_, other := mux.Handler(probe)
			if other != "/" && other != "" {
				w.Header().Set("Allow", probe.Method)
				writeError(w, &apiError{405, "method_not_allowed", "method not allowed for route"})
				return
			}
		}
		mux.ServeHTTP(w, r)
	}), nil
}

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return &apiError{415, "unsupported_media_type", "Content-Type must be application/json"}
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			return &apiError{413, "body_too_large", "request body exceeds 2 MiB"}
		}
		return bad("cannot read request body")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return bad("body must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return bad("invalid JSON object or unknown field")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return bad("body must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
	return nil
}

func writeError(w http.ResponseWriter, err error) int {
	response := &apiError{500, "internal_error", "internal storage error"}
	var explicit *apiError
	switch {
	case errors.As(err, &explicit):
		response = explicit
	case errors.Is(err, taskfused.ErrInvalid):
		response = &apiError{400, "invalid_request", err.Error()}
	case errors.Is(err, taskfused.ErrNotFound):
		response = &apiError{404, "not_found", "operation not found"}
	case errors.Is(err, taskfused.ErrCapacity):
		response = &apiError{429, "capacity", err.Error()}
	case errors.Is(err, taskfused.ErrConflict), errors.Is(err, taskfused.ErrDuplicate), errors.Is(err, taskfused.ErrPolicyConflict), errors.Is(err, taskfused.ErrRetryState), errors.Is(err, taskfused.ErrRetryStale), errors.Is(err, taskfused.ErrRetryConflict), errors.Is(err, taskfused.ErrRetryBudget):
		response = &apiError{409, "conflict", err.Error()}
	case errors.Is(err, context.DeadlineExceeded):
		response = &apiError{504, "deadline_exceeded", "request or wait deadline exceeded"}
	case errors.Is(err, context.Canceled), errors.Is(err, taskfused.ErrClosed):
		response = &apiError{503, "unavailable", "request canceled or store closed"}
	}
	_ = writeJSON(w, response.status, map[string]any{"error": map[string]string{"code": response.code, "message": response.message}})
	return response.status
}
