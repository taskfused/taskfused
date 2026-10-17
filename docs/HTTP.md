# Local HTTP interface

Run the CLI server against the same local SQLite database used by your workers:

```sh
go run ./cmd/taskfused serve -db demo.db -listen 127.0.0.1:8080
```

Open http://127.0.0.1:8080/ for the read-only status page. Enter a namespace to
inspect usage and optionally an operation ID to inspect state and the first 50
events. The page displays pruned results explicitly, escapes stored text and has
no mutation controls, auto-refresh, JavaScript or external resources. Payload
contents are available through the API; the page displays result availability and
length. Usage and operation/event reads are separate snapshots and can change
between reads. Event API pagination is required beyond the first 50 events.

The server does not execute handlers. Start a separate registered Go worker, for
example `go run ./examples/worker -db demo.db` for demo/echo/v1. The existing CLI
set-limits and prune-results commands remain the administrative interface; neither
has an HTTP route. No schema change or data migration is introduced by HTTP.

## Routes

In the following paths, N is the URL-escaped namespace and ID is the operation ID.

| Method | Path | Body / query | Success |
| --- | --- | --- | --- |
| POST | /api/namespaces/N/operations | Submission JSON | 201 created; 200 joined |
| GET | /api/namespaces/N/operations/ID | None | 200 Operation |
| GET | /api/namespaces/N/operations/ID/wait | Optional timeout duration, default/max 30s | 200 terminal Operation |
| GET | /api/namespaces/N/operations/ID/attempts | None | 200 attempts array |
| GET | /api/namespaces/N/operations/ID/events | after exclusive sequence, limit 1–200 (default 50) | 200 EventPage |
| POST | /api/namespaces/N/operations/ID/cancel | Empty JSON object | 200 Cancellation |
| POST | /api/namespaces/N/operations/ID/retry | Retry JSON | 200 RetryResult |
| GET | /api/namespaces/N/usage | None | 200 Usage |
| GET | / | namespace and optional id query | 200 HTML |

Submission body (namespace comes only from the path):

```json
{"handler":"echo","version":"v1","key":"example-1","input":"aGVsbG8=","join_existing":true,"max_attempts":3,"retry_delay":1000000000}
```

Input is base64 for arbitrary bytes, not a plain text string. Result bytes are also
base64. Retry delay is integer nanoseconds, not a duration string; omitted/zero
policy values use the core defaults. Input/result bounds remain 1 MiB each.

```sh
curl -H 'Content-Type: application/json' \
  --data '{"handler":"echo","version":"v1","key":"example-1","input":"aGVsbG8=","join_existing":true}' \
  http://127.0.0.1:8080/api/namespaces/demo/operations
curl 'http://127.0.0.1:8080/api/namespaces/demo/operations/op_REPLACE/wait?timeout=10s'
curl -H 'Content-Type: application/json' --data '{}' \
  http://127.0.0.1:8080/api/namespaces/demo/operations/op_REPLACE/cancel
```

Retry body:

```json
{"action_key":"operator-action-1","expected_attempts":1,"additional_attempts":1}
```

Use the same action key and parameters after an uncertain retry response. Use the
same operation identity/input/policy with join_existing=true after uncertain Submit
acceptance. A disconnected request or deadline does not prove that a preceding
commit failed. Cancellation may have committed before a connection was lost too;
repeat it or inspect the operation. No HTTP layer changes core transaction rules.

Execution failure and operation cancellation are retrieved outcomes, so Wait
returns 200 with state=failed or canceled, not a transport error. Always inspect
state, last_error and result_pruned. Succeeded empty results and pruned results may
both omit result; the marker distinguishes them. Timing out/disconnecting from
Wait only abandons that caller. It never cancels the operation or another waiter.
HTTP waiters share the same Store pollers as embedded callers.

## Errors and bounds

Application errors use an envelope such as:

```json
{"error":{"code":"capacity","message":"namespace admission limit reached: max_active"}}
```

| Status | Meaning |
| --- | --- |
| 400 | Invalid JSON/fields, input bounds, retry parameters or query values |
| 403 | Unexpected Host or disallowed browser origin/site |
| 404 | Missing operation in this namespace or unknown route |
| 405 | Unsupported method; supported endpoint methods are GET or POST |
| 409 | Identity/policy/duplicate conflict or invalid/stale/conflicting retry state/budget |
| 413 | Mutation body exceeds 2 MiB |
| 415 | Mutation Content-Type is not application/json |
| 429 | Durable namespace admission capacity reached |
| 503 | Request canceled, shutdown or Wait Store closed |
| 504 | Application request/wait deadline exceeded |
| 500 | Other internal/storage error, with details withheld from the client |

Mutation bodies must contain exactly one JSON object. Unknown fields and invalid
base64 are rejected. Cancellation takes {} rather than an empty request body. The
2 MiB encoded-body cap accommodates a maximum-size base64 input plus metadata;
core field bounds are independently enforced. GET query duration uses Go syntax
such as 500ms or 10s and must be positive and at most 30s. Event cursors must be
nonnegative integers; explicit event page limits are 1–200. Unknown optional query
parameters are ignored. JSON decoding otherwise uses Go encoding/json semantics.

Serve configures a 3s read-header timeout, 5s read timeout, 35s write timeout, 30s
idle timeout and 16 KiB MaxHeaderBytes. Normal application contexts have a 5s
deadline; Wait contexts have at most 30s, including caller-selected shorter waits.
These are transport/application limits, not a real-time SQLite scheduling promise.
There is no streaming, SSE, WebSocket, rate limiter or connection-count quota.
The listener only serves local traffic; this is not a public load-tested service.

On server context cancellation, all request base contexts are canceled and the
listener stops accepting connections. Shutdown gives handlers up to five seconds
to drain, then closes remaining connections if necessary. The CLI closes its Store
after serving returns. Embedded Serve never closes its caller-owned Store. Pending
HTTP waits end without mutating durable work. Separately running worker processes
are not stopped by server shutdown. CLI Ctrl-C initiates this shutdown sequence.

## Local access boundary

Only numeric loopback IPs are accepted (for example 127.0.0.1 or [::1]); port zero
selects an available port and the CLI prints the actual URL. Hostnames including
localhost, wildcard and non-loopback addresses are rejected before binding. Serve
also validates a listener supplied by an embedder. NewHandler alone is not a
listener and must only be embedded behind the same loopback/time-limit rules.

Every request must carry the exact bound IP:port Host. Browser Origin, when present,
must be the exact http origin; opaque/null or other origins are refused. Fetch
Metadata cross-site requests are refused. The server emits no CORS permission,
requires application/json for mutations, disables caching, uses nosniff and a
restrictive CSP including frame-ancestors 'none'. Proxy forwarding headers are
ignored. These checks reduce browser cross-origin/rebinding exposure; they are
not authentication. Local users/processes can read and mutate every namespace.
Do not expose this through a reverse proxy, tunnel or remote bind without a separate
authentication/authorization design. No credential or remote-hosting setup exists.

## Validation

Integration tests use real loopback HTTP connections and persistent SQLite stores
for submission/join, conflicts, wait deadlines, shared result retrieval, failed and
canceled outcomes, cancellation, retry replay at capacity, namespace isolation,
event pages, usage and pruned markers. Boundary tests cover body size/type/shape,
unknown fields, base64, invalid queries, Host/Origin/Fetch Metadata, methods and
escaped HTML. A real TCP shutdown test holds a pending wait, cancels Serve and
checks the response, listener closure and unchanged queued operation. Core race
and process-crash tests still run. A browser executable was unavailable during this
milestone, so automated HTML checks passed but visual browser rendering remains
unverified. No new throughput or remote-deployment claim is made.


## Run the end-to-end recovery scenario

`go run ./examples/demo -http > http-report.json` launches this interface and real
worker child processes against private temporary SQLite files. It exercises shared
submission/waiting, actual worker kill/recovery, cancellation, retry replay, event
pages, server/client wait deadlines and pruned-result inspection. Explicit pruning
and stale worker commits stay on the Go API. See HTTP_DEMO.md for boundaries,
commands and captured IPv4/IPv6 reports. Real slow-header/body tests and IPv6 serving
coverage now supplement the malformed-body and shutdown tests above. These checks
do not replace browser visual verification or production load/durability testing.


## Internal storage failures and diagnostics

Unknown/internal failures still return HTTP500 with a generic internal_error body.
The handler now logs safe metadata through slog.Default: method, registered route
pattern, Go error type and numeric SQLite codes when present. It does not log raw
error messages, request bodies, raw paths/queries, namespaces or operation IDs.
Embedders can select a logger with NewHandlerWithLogger. Expected client conflicts,
capacity rejection and request cancellation/deadlines do not create internal-error
logs. This is not a general audit/worker log. See FAULTS.md for privacy checks,
statement/commit faults and lost HTTP acknowledgment recovery rules.
