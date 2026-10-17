# Behavioral contract

## Implemented in milestone 1

Submission requires a nonblank namespace, handler, version, and operation key.
Each identity component is at most 256 bytes and cannot contain NUL. Leading and
trailing whitespace is significant in an otherwise nonblank component. Inputs are
opaque bytes, at most 1 MiB; nil and empty input represent the same empty payload.

The unique identity is `(namespace, handler, version, key)`. A new identity creates
one queued operation with a random 128-bit identifier and UTC timestamp. The record
and submission event commit in one transaction before success is reported.

For an existing identity, different input returns `ErrConflict`. Equal input with
`JoinExisting: false` returns `ErrDuplicate`; equal input with joining enabled
returns the same operation and `Joined: true`. Joining does not append a new
submission event. There are no caller subscriptions or completion notifications
in this milestone. The returned byte slice is detached from the request's input.

A lookup requires both namespace and ID. A missing or mismatched namespace returns
`ErrNotFound`. The namespace is an application isolation mechanism, not a verified
security credential. The application must supply authorized namespaces.

The database has an application marker and schema version. Opening rejects an
unrecognized nonempty schema or a version newer than supported. Only the `queued`
state is accepted by schema version 1. Execution requires an explicit migration.

## Execution design to implement next

The proposed state machine is queued -> running -> succeeded, with running ->
retry_wait -> running and running -> failed. Cancellation needs an explicit
requested flag and cooperative handler behavior; it must not falsely report that
an external effect was undone. These states are not available in milestone 1.

A claim must transactionally issue a new attempt ID, increasing ownership token,
and lease expiration. Renew, retry, and result commits must check current attempt
ownership and a live lease. An expired attempt cannot renew itself back into
ownership or overwrite a successor's result. Recovery may execute a handler again.

Work identity and input remain stable across attempts. A handler receives the
operation ID for external idempotency and a separate attempt ID for diagnostics.
An operation ID cannot guarantee idempotency in an external system that ignores it.

Caller timeout means stop waiting. Explicit operation cancellation is separate.
Joining a queued or running operation must not let one waiter cancel everyone
else's work. Terminal-state joining, retention, resubmission after failure, retry
policy changes, and cancellation races must be specified before worker code lands.
An explicit retry should preserve operation identity and append an attempt; it
must never silently replace the original input.

Completed-result caching with freshness windows is a separate later policy, not an
implicit consequence of matching payloads. Task graphs and optimization across
graph stages are also separate from duplicate-operation joining.
