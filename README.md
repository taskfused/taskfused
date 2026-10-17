# TaskFused

Durable shared execution for Go applications.

TaskFused is being built to give equivalent requests a shared, persistent operation:
submit work, join an existing operation explicitly, and retrieve its status or result
after a restart. The intended engine will support at-least-once execution with
leases, bounded retries, and ownership checks on result commits.

**Current status: submission milestone, not an execution engine yet.** This version
persists queued operations, joins identical submissions, rejects conflicting input,
and retrieves operations by namespace and ID. It does not run handlers, recover
interrupted executions, schedule retries, or store completed results.

## Quick start

Requires Go 1.24 or later, CGO enabled, and a C compiler for the SQLite driver.
The CLI is a local development interface, not a remotely accessible service.

```sh
go build ./cmd/taskfused
./taskfused submit -db demo.db -namespace demo -handler report -version v1 \
  -key october-2026 -input '{"month":"2026-10"}' -join
```

Run the same command again: `joined` becomes `true`, and the operation ID stays
the same. Change the input without changing the identity: submission fails with a
conflict. Omit `-join` when submitting an existing identity: submission fails with
a duplicate error. Copy the returned ID to inspect the persisted operation:

```sh
./taskfused get -db demo.db -namespace demo -id op_REPLACE_WITH_RETURNED_ID
```

The JSON `input` field uses base64 to preserve arbitrary bytes. The CLI's `-input`
argument accepts UTF-8 text; the Go API accepts byte slices. The default input
limit is 1 MiB. Do not put sensitive input into command-line arguments on shared
machines; the embedding application should call the library directly.

## Go API

```go
store, err := taskfused.Open(ctx, "taskfused.db")
if err != nil {
    return err
}
defer store.Close()

submission, err := store.Submit(ctx, taskfused.Request{
    Namespace:    "account-a",
    Handler:      "report",
    Version:      "v1",
    Key:          "october-2026",
    Input:        []byte(`{"month":"2026-10"}`),
    JoinExisting: true,
})
```

An operation's identity is the tuple `(namespace, handler, version, key)`. Input is
compared byte for byte; JSON with different whitespace is different input. The
caller must canonicalize input if semantic equality is desired. The API never
infers equivalence from similar payloads. Namespace-qualified reads reduce
accidental cross-account access but do not replace authentication or authorization.

A key currently identifies one operation for the lifetime of its stored record.
Joining an existing key is durable identity lookup, not a freshness-based result
cache. The execution milestone must define how joining behaves for each terminal
state before terminal states are implemented. Use a new key for deliberately new
work.

## Storage and guarantees

Accepted submissions commit the operation and one `submitted` event atomically in
SQLite, with WAL mode, FULL synchronization, and a five-second busy timeout.
Concurrent submissions are serialized by write transactions, including across
processes on the same host. Durability remains subject to the filesystem and
hardware honoring SQLite's synchronization requests. Cancellation or an I/O error
around commit can leave the caller uncertain of acceptance; resubmit with the same
identity and `JoinExisting: true` to resolve that uncertainty.

This prototype supports a local filesystem on one host. It is not a multi-host
coordination service. SQLite may create `-wal` and `-shm` sidecars; do not copy only
the main database file while it is open. Use SQLite-aware backups. The database is
not encrypted; protect the database directory and its sidecars with OS permissions.

The planned guarantee is at-least-once handler execution, not exactly-once external
side effects. Stable operation IDs will let handlers pass idempotency keys to
external systems. Leases and ownership tokens will govern which attempt can commit
its result, without claiming to undo external actions by an obsolete worker.

## Development

```sh
go test -race -count=1 ./...
go vet ./...
go build ./cmd/taskfused
```

See [the development plan](docs/DEVELOPMENT_PLAN.md), [behavioral contract](docs/CONTRACT.md),
and [resume instructions](RESUME.md). TaskFused.com is the intended project domain;
this milestone does not create or publish a website.

## Rights

Copyright 2026 TaskFused. All rights reserved. See [NOTICE](NOTICE). No TaskFused
release license has been selected. The SQLite driver is a separate dependency
under its own license.
