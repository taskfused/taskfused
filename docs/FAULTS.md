# Storage faults and uncertain acknowledgments

The fault suite exercises the existing SQLite transaction boundaries without adding
production fault hooks or changing schema v5. Run `go test -race -count=1 ./...`.
Fixtures are private temporary databases. These tests are evidence for the scoped
local prototype, not a power-loss/filesystem durability qualification.

## Statement and commit boundaries

A 26-case matrix covers 13 mutations with two failure modes:

| Mutation | Changes that must roll back together |
| --- | --- |
| Submit | Identity/input/policy and submitted event |
| Initial claim | Attempt, operation ownership/count and claimed event |
| Expired-attempt recovery | Old expiry/history, new attempt/token and operation ownership |
| Exhausted-attempt recovery | Expired attempt, final failure/state and events |
| Renew | Operation and attempt lease timestamps |
| Complete | Result/state, attempt, retention metadata and succeeded event |
| Final Fail | Failure/state, attempt and failed event |
| Automatic retry scheduling | Error, attempt, backoff/state and retry_scheduled event |
| Running Cancel | Attempt revocation, terminal operation and canceled event |
| Operator Retry | Action key, budget/state and retry_requested event |
| PruneResults | Payload removal, marker, retention metadata and prune event |
| Initial limits policy | Complete new namespace policy |
| Replaced limits policy | Complete policy replacement |

Statement faults use SQLite triggers with RAISE(ABORT). Commit faults use a trigger
that inserts an invalid DEFERRABLE INITIALLY DEFERRED foreign key reference. The
mutation statements can succeed, but the final transaction commit returns a foreign
key constraint error. SetLimits uses one atomic autocommit statement, so its deferred
constraint is checked when that statement's transaction finishes.

The suite snapshots all production tables plus SQLite's sequence metadata before
and after each rejected action. It requires exact equality, then removes the fault
and verifies the same action can succeed with consistent foreign keys. An additional
multi-result pruning test checks that an entire batch, including earlier payload
and event changes, is restored when its commit fails. Existing migration rollback
and process-kill recovery tests remain enabled.

## Full storage, read-only access and writer contention

SQLite max_page_count is set to the database's current page count, then a maximum
1 MiB input/result forces allocation beyond that limit. Both Submit and Complete
must return SQLITE_FULL without partial records; the same action succeeds after
lifting the page ceiling. This is a real SQLite full-storage error caused by a
controlled per-database page quota. It does not fill the host disk, exhaust WAL
storage or inject a kernel ENOSPC/fsync failure.

A read-only SQLite URI connection must reject a new submission with SQLITE_READONLY,
retain the existing data and allow Get. A normal writable connection can continue
afterward. This validates driver-level read-only behavior, not filesystem permission
handling for every OS, and does not add a public read-only Open mode.

An independent Store holds an immediate write transaction while another Store with
a shortened test busy timeout attempts submission. SQLITE_BUSY must leave the
snapshot unchanged; the submission succeeds once the holder releases its lock.
Production Open retains its five-second SQLite busy timeout. Native lock waits and
scheduling mean application context deadlines are not hard real-time guarantees.

Worker tests reject a lease renewal write, require cooperative handler cancellation
and retain the running operation for expiry recovery. Another test fails result
commit after the handler's simulated external effect. Recovery invokes the handler
again. Without application deduplication the effect happens twice; deduplicating by
operation ID keeps one simulated effect. The test's in-memory stand-in is not an
implementation of durable external-service idempotency. Applications need that
protection in their own effect destination. Fencing database results does not
provide exactly-once external execution.

## Lost acknowledgment after successful commit

HTTP tests run the real handlers against SQLite, let a submission/retry grant commit,
then close the connection before sending its successful response. The caller sees
a transport failure. Repeating the same Submit identity/input/policy with joining
returns the single existing operation. Repeating the same retry action key and
parameters returns Applied=false and its original budget. Counts prove there is
only one operation/submission event and one retry-action/grant event.

These tests deliberately know that the commit succeeded; the caller cannot infer
that from the failed response. They complement rejected-commit tests rather than
simulating every possible SQLite I/O acknowledgment failure.

## Recovery rules

Resolve uncertain Submit acceptance by reusing the same namespace, handler, version,
key, input and original policy with joining enabled. Do not invent a new key merely
because a response was lost. Resolve an uncertain Retry by repeating its exact
action key and parameters. A new action key is a new operator action, not a probe.

A Claim error is not permission to run the handler. If ownership was durably issued
but acknowledgment was lost, lease expiry permits later recovery and can consume
an attempt. If Complete fails after a side effect, inspect the durable operation
and restore storage access before resuming workers. Worker store errors are surfaced
to the embedding supervisor; no unbounded hidden storage retry loop is introduced.
A rolled-back result remains running until recovery or a still-valid owner retries
completion. A result already committed remains terminal. Calling Fail or submitting
new work does not undo an external effect.

Cancel can be repeated or inspected after an uncertain response. Result pruning can
repeat the same cutoff/batch request: committed markers prevent duplicate pruning
events. SetLimits replaces the whole policy atomically. None of these operations
frees physical storage reliably during a full-disk incident: pruning itself needs
room for SQLite writes/WAL/history. Admission counts are not filesystem quotas.

## Internal diagnostics

localhttp.NewHandler uses slog.Default for internal HTTP500 diagnostics. Embedders
can choose a logger with NewHandlerWithLogger; nil also selects the default. Serve
and the CLI use the normal default logger, which writes to stderr unless the host
changes it. Known client/domain conflicts, capacity rejections and ordinary request
cancellation/deadlines are not logged as internal failures.

The log includes only method, registered route pattern, Go error type and numeric
SQLite code/extended code when available. It omits raw error text, request payload,
namespace/ID/key values, raw URL/path and query values. The client still receives
the generic internal_error envelope. A test injects a private SQLite error message
and private request fields, then verifies useful codes/pattern are present and the
private values appear in neither the diagnostic nor response. This is HTTP internal
error reporting, not a general-purpose audit log, metrics system or a replacement
for the embedding application's worker/error handling.

## Limits of the evidence

Not exercised: arbitrary SQLite VFS I/O failures, actual filesystem/disk exhaustion,
partial device writes, fsync failure, abrupt host power loss, network filesystems
or every OS/filesystem combination. Process.Kill tests remain process-crash tests.
SQLite WAL/FULL is configured, but hardware durability assumptions remain external.
Use SQLite-aware backups; this milestone neither implements nor validates an
operational backup/restore service. See READINESS.md for the remaining release work.
