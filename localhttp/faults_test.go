package localhttp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/taskfused/taskfused"
)

func TestInternalErrorsLogCodesWithoutPrivateRequestOrStorageText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.db")
	store, err := taskfused.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER fail_private BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'PRIVATE-SQL-ERROR'); END;`); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	server := httptest.NewUnstartedServer(nil)
	handler, err := NewHandlerWithLogger(store, server.Listener.Addr().String(), slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	defer server.Close()
	responseBody := call(t, server, "POST", "/api/namespaces/PRIVATE-NAMESPACE/operations?secret=PRIVATE-QUERY", map[string]any{"handler": "PRIVATE-HANDLER", "version": "v1", "key": "PRIVATE-KEY", "input": []byte("PRIVATE-INPUT")}, 500)
	// Server.Close joins request completion before the non-concurrent log buffer is read.
	server.Close()
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
		t.Fatalf("missing structured failure log: %s %v", logs.Bytes(), err)
	}
	if record["method"] != "POST" || record["route"] != "POST /api/namespaces/{namespace}/operations" || record["sqlite_code"] != float64(19) || record["error_type"] == nil {
		t.Fatalf("incomplete internal diagnostic: %s", logs.Bytes())
	}
	for _, private := range []string{"PRIVATE-SQL-ERROR", "PRIVATE-NAMESPACE", "PRIVATE-QUERY", "PRIVATE-HANDLER", "PRIVATE-KEY", "PRIVATE-INPUT", "UFJJVkFURS1JTlBVVA=="} {
		if bytes.Contains(logs.Bytes(), []byte(private)) || bytes.Contains(responseBody, []byte(private)) {
			t.Fatalf("diagnostic leaked %s", private)
		}
	}
	usage, err := store.Usage(context.Background(), "PRIVATE-NAMESPACE")
	if err != nil || usage.Operations != 0 || usage.Events != 0 {
		t.Fatalf("error response left partial submission: %+v %v", usage, err)
	}
}

func TestLostAcknowledgmentsResolveBySubmissionAndActionIdentity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ack.db")
	store, err := taskfused.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := httptest.NewUnstartedServer(nil)
	handler, err := NewHandler(store, server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var loseNext atomic.Bool
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if loseNext.CompareAndSwap(true, false) {
			// Run the real handler to completion, keeping its successful response off the
			// wire, then drop the connection after the SQLite commit is known to finish.
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			if recorder.Code != 200 && recorder.Code != 201 {
				t.Errorf("unexpected response before loss: %d %s", recorder.Code, recorder.Body.Bytes())
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		handler.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()
	drop := func(path, body string) {
		t.Helper()
		loseNext.Store(true)
		req, err := http.NewRequest("POST", server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		if response != nil {
			response.Body.Close()
		}
		if err == nil {
			t.Fatal("dropped acknowledgment unexpectedly reached caller")
		}
	}
	pathAPI := "/api/namespaces/demo/operations"
	body := `{"handler":"echo","version":"v1","key":"uncertain","input":"aGVsbG8=","join_existing":true,"max_attempts":1}`
	drop(pathAPI, body)
	var object map[string]any
	if err := json.Unmarshal([]byte(body), &object); err != nil {
		t.Fatal(err)
	}
	data := call(t, server, "POST", pathAPI, object, 200)
	var joined taskfused.Submission
	if err := json.Unmarshal(data, &joined); err != nil || !joined.Joined {
		t.Fatalf("uncertain submit resolution: %s %v", data, err)
	}
	usage, err := store.Usage(ctx, "demo")
	if err != nil || usage.Operations != 1 || usage.Events != 1 {
		t.Fatalf("lost acknowledgment duplicated submission: %+v %v", usage, err)
	}
	c := claim(t, store)
	if err := store.Fail(ctx, c, "initial failure"); err != nil {
		t.Fatal(err)
	}
	retry := `{"action_key":"same-action","expected_attempts":1,"additional_attempts":1}`
	pathRetry := operationPathForTest(joined.Operation.ID) + "/retry"
	drop(pathRetry, retry)
	// Use a fresh map: submission-only keys must not travel in the retry request.
	var retryObject map[string]any
	if err := json.Unmarshal([]byte(retry), &retryObject); err != nil {
		t.Fatal(err)
	}
	data = call(t, server, "POST", pathRetry, retryObject, 200)
	var replay taskfused.RetryResult
	if err := json.Unmarshal(data, &replay); err != nil || replay.Applied || replay.Operation.AttemptLimit != 2 || replay.Operation.AttemptCount != 1 {
		t.Fatalf("uncertain action resolution: %s %v", data, err)
	}
	usage, err = store.Usage(ctx, "demo")
	if err != nil || usage.RetryActions != 1 || usage.Events != 4 {
		t.Fatalf("lost action acknowledgment duplicated grant: %+v %v", usage, err)
	}
	// A normal GET continues working after both deliberately dropped connections.
	response, err := server.Client().Get(server.URL + operationPathForTest(joined.Operation.ID))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
}

func operationPathForTest(id string) string { return "/api/namespaces/demo/operations/" + id }
