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
result; joining a failed operation returns its stored failure. Neither reruns it.
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
Unknown applications and future schema versions are rejected.

## Worker lifecycle and cancellation

Workers have an immutable registry of Go handlers and claim only matching versions
within their configured namespace. `RunOnce` executes at most one claim and renews
its lease every third of the configured duration. A persisted handler failure is
handled work; storage and ownership failures are returned to the caller.

`Run` bounds concurrent handlers (default one, maximum 128). A store/ownership
failure stops the runner and cancels sibling handlers. The embedding process can
inspect the error and restart the runner. RunOnce callers must bound their own
concurrency. Multiple worker processes can share a local database on one host.

Handler context cancellation is cooperative. Renewal failure cancels the handler
context and prevents that runner from committing its result. A handler that ignores
context cannot be forcibly stopped by Go. Stable operation IDs should be used as
idempotency keys in external systems that support them; a stale worker can still
perform external actions even though SQLite rejects its result commit.

Canceling a worker is shutdown, not operation cancellation: unfinished claims stay
running until expiration/recovery and consume their current attempt. A canceled
submission/read affects only that call. Shared waiting and explicit operation
cancellation are not implemented yet. Their later API must keep waiter cancellation
separate from cancellation of the shared operation.
