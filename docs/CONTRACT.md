# Behavioral contract

## Identity and submission

Submission requires a nonblank namespace, handler, version, and operation key.
Each identity component is at most 256 bytes and cannot contain NUL. Leading and
trailing whitespace is significant in an otherwise nonblank component. Inputs are
opaque bytes, at most 1 MiB; nil and empty input represent the same empty payload.

The unique identity is `(namespace, handler, version, key)`. A new identity creates
one queued operation with a random 128-bit identifier and UTC timestamp. The record
and submission event commit in one transaction before success is reported.

For an existing identity, different input returns `ErrConflict`. Different retry
policy returns `ErrPolicyConflict`. Equal input/policy with `JoinExisting: false`
returns `ErrDuplicate`; with joining enabled it returns the same operation and
`Joined: true`. Joining does not append a submission event or execution attempt.
Retry policy defaults are normalized before comparison: zero max attempts means
three; zero retry delay means one second. Joining a custom-policy operation must
supply that same policy. Policies cannot be silently changed by later submissions.

Joining applies in every state. Joining a succeeded operation returns its stored
result; joining failed or canceled work returns its stored terminal outcome.
Joining never reruns terminal work.
Use a new operation key for intentionally new work. There is no freshness-based
result cache, terminal-state TTL, record deletion, or explicit operator retry yet.

A lookup or attempt-history query requires namespace and ID. A wrong namespace
returns `ErrNotFound`. Namespace is not a verified security credential; the
embedding application must supply authorized namespaces and protect the store.

## States and attempts

States are queued -> running -> succeeded; handler failure transitions running ->
retry_wait -> running while budget remains, otherwise running -> failed. A claim
on expired running work marks the previous attempt expired and either issues a
new attempt or fails the operation when its budget is exhausted. Recovery is lazy:
workers must claim the matching namespace/handler/version to process expirations.
An operation may still read as running after its lease expires until then.
Explicit cancellation transitions queued/running/retry_wait directly to canceled
and prevents recovery or retry scheduling for that operation.

Each claim creates a fresh attempt ID and increasing token. Operation ID, input,
and retry policy remain stable. Every issued claim consumes an attempt, even if
its worker crashes before invoking the handler. This is a bounded at-least-once
attempt model, not a guarantee of eventual execution in the presence of unlimited
crashes. Retry budgets are 1–100 claims, including the initial attempt.

Handler failures use exponential backoff: base delay after attempt 1, twice that
after attempt 2, and so on, capped at one hour. Base delay is 1ms–1h. Crash recovery
can reclaim immediately after expiration; it does not add this handler-error delay.
There is no jitter or permanent/retryable error classification in this milestone.

Successful result bytes are limited to 1 MiB. Stored error messages are limited to
8192 bytes. Worker handlers returning oversized results are recorded as failures;
worker error messages are truncated to the limit. The low-level APIs reject invalid
sizes. Handler panics become recorded failures under the same retry budget.

## Ownership and transactions

Lease durations are 100ms–1h. Claim, renew, completion, failure, and their state
changes use immediate SQLite write transactions. The clock is sampled after write
ownership is acquired. Live ownership requires matching operation/namespace,
current attempt ID/token, running state, and a lease deadline strictly later than
the transaction's sampled time. Equality to the deadline is already expired.

Expiry alone invalidates renewal, success, and failure commits. No replacement
claim is needed to revoke authority. Renewal never shortens an existing lease.
Once replaced, an old attempt cannot overwrite a new attempt's state or result.
These are serialized ownership checks, not external fencing of a handler's effects.

All persisted times use the host wall clock; the supported deployment assumes a
correct, reasonably stable host clock. The library does not coordinate clock skew
across hosts. Network filesystems and multi-host coordination remain unsupported.

Claims, attempt records, result/failure transitions, and their events commit
atomically. Renewal updates operation and attempt deadlines atomically but does
not append heartbeat events. Database version 2 migrates version 1 transactionally,
preserving operation IDs, identity/input, submission events and their sequence IDs.
Version 3 adds the canceled state to operation/attempt domains while preserving
all v2 records, active ownership, stored results and events. Opening v1 runs both
migrations inside the same transaction. Unknown applications and future schema
versions are rejected.

## Worker lifecycle and cancellation

Workers have an immutable registry of Go handlers and claim only matching versions
within their configured namespace. `RunOnce` executes at most one claim and renews
its lease every third of the configured duration. A persisted handler failure is
handled work; storage and ownership failures are returned to the caller.

`Run` bounds concurrent handlers (default one, maximum 128). A store/ownership
failure stops the runner and cancels sibling handlers. Explicit operation
cancellation is normal handled work and does not stop the runner or its siblings.
The embedding process can inspect the error and restart the runner. RunOnce callers must bound their own
concurrency. Multiple worker processes can share a local database on one host.

Handler context cancellation is cooperative. Renewal failure cancels the handler
context and prevents that runner from committing its result. A handler that ignores
context cannot be forcibly stopped by Go. Stable operation IDs should be used as
idempotency keys in external systems that support them; a stale worker can still
perform external actions even though SQLite rejects its result commit.

Canceling a worker is shutdown, not operation cancellation: unfinished claims stay
running until expiration/recovery and consume their current attempt. A canceled
submission/read affects only that call.

## Shared waiting

Wait reads an operation immediately and polls every 100ms until succeeded, failed,
or canceled. These terminal records return with a nil API error, including failure
and cancellation: the caller must inspect the operation's state and error/result.
Lookup/storage errors return immediately. Namespace qualification matches Get.

Canceling a wait context changes no operation/attempt/event state, never releases
its lease, and cannot cancel another wait. An already canceled context wins before
any read. A concurrent terminal read and context cancellation may return whichever
is observed first. Each returned result slice is independent of other callers.
Waiting works across local processes/stores through SQLite; no notification broker
or in-memory subscription registry is assumed. Polling load is not benchmarked yet.

The CLI wait command has a positive timeout, default 30s. An execution failure or
operation cancellation is still a successfully retrieved terminal record, not a
CLI transport error. A wait deadline exits with an error and leaves work untouched.

## Explicit operation cancellation

Cancel transactionally marks queued/retry_wait/running work canceled, revokes the
current attempt if any, clears claim authority, and appends one cancellation event.
Its returned `Changed` distinguishes a new cancellation from an unchanged terminal
record. Already succeeded/failed/canceled records remain unchanged; no duplicate
cancellation event is written. A missing or wrong-namespace operation is not found.
A commit error can leave the caller uncertain; Get resolves the stored outcome.

Running attempts become canceled atomically with the operation. Their finish time
records authority revocation, not confirmed physical handler exit. This also applies
if the running attempt's lease had already expired but had not yet been reclaimed.
There is no separate cancellation-request flag or pending acknowledgment state.

Cancellation and success/final failure use the same serialized write boundary: the
first committed terminal state wins. If retry scheduling commits first, work is
still nonterminal and can be canceled; if cancellation commits first, failure cannot
schedule a retry. Canceled work is never claimed again. Joining it returns its
stored canceled outcome; intentional new work requires a new key.

Renew, Complete and Fail return ErrOperationCanceled for a namespace-qualified
canceled operation, distinct from ErrLeaseLost and from the caller's context error.
A worker discovering cancellation during renewal cancels its handler context.
Cancellation observed during final commit is also normal handled work. Other
operations continue. Detection latency is up to a heartbeat interval plus scheduling
and database delay. Context-ignoring handlers cannot be forcibly stopped by Go, but
their later results cannot overwrite cancellation. An already-issued claim can
begin handler code before its next cancellation check. External effects are not
undone, and canceled does not certify physical handler exit.
