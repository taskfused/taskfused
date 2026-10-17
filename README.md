# TaskFused

Durable shared execution for Go applications.

Equivalent requests can share one persistent operation. TaskFused records explicit
operation identities, runs registered Go handlers with leases and bounded retries,
and stores results and attempt history in local SQLite. Restarted workers can
recover expired attempts without changing the operation ID or input.

**Status: early execution prototype.** Submission, duplicate joining, leased claims,
renewal, worker concurrency, retries, stored results, crash recovery, shared waiting,
explicit operation cancellation, failed-operation retry and paginated event
inspection exist. HTTP endpoints, a UI, retention, and multi-host coordination
are not implemented. External effects require
handler-level idempotency; arbitrary exactly-once execution is not promised.

## End-to-end demonstration and measurements

```sh
go run ./examples/demo > report.json
```

One command checks 100 equivalent submissions sharing one operation/result, an
isolated wait timeout, an actual worker process kill and recovery, cooperative
cancellation with surviving work, and an idempotent operator retry with event
inspection. The harness re-executes itself for worker processes; it never executes
submitted commands. Progress goes to stderr and the JSON report goes to stdout.
Any failed correctness check makes the command fail. Temporary databases are
removed after cleanup; the report retains their logical size snapshots.

It then measures 1000 sequential durable submissions and trivial executions with
128-byte input/results, plus three fresh-process samples each with 0, 1 and 100
waiters. Parameters are shown by `go run ./examples/demo -h`. Timing runs should
use an ordinary binary; race builds are for correctness validation. CPU and Go
allocation measurements include waiter setup/teardown, and native SQLite memory
is not included in Go allocation totals.

See [the measured baseline and methodology](docs/MEASUREMENTS.md) and
[the raw report](docs/measurements/2026-10-17-linux-amd64.json). These are development
observations for one environment, not production capacity or durability promises.

## Try it

Requires Go 1.24 or later, CGO enabled, and a C compiler for the SQLite driver.

```sh
go build ./cmd/taskfused
./taskfused submit -db demo.db -namespace demo -handler echo -version v1 \
  -key example-1 -input 'hello' -join
```

Run the same submission again: `joined` becomes `true` and the operation ID stays
the same. Start the example worker in another terminal:

```sh
go run ./examples/worker -db demo.db
```

This example registers only an `echo` handler at version `v1` in namespace `demo`.
It returns the submitted bytes after a simulated delay. It never evaluates input
as code or runs arbitrary commands. Copy the operation ID from submission to inspect
its result or attempt history:

```sh
./taskfused get -db demo.db -namespace demo -id op_REPLACE_WITH_RETURNED_ID
./taskfused attempts -db demo.db -namespace demo -id op_REPLACE_WITH_RETURNED_ID
./taskfused wait -db demo.db -namespace demo -id op_REPLACE_WITH_RETURNED_ID -timeout 30s
```

Operation input and result JSON fields use base64 to preserve arbitrary bytes.
Successful empty results may omit the result field; use the state to distinguish
success. Retry policies are set at submission with `-max-attempts` (default 3) and
`-retry-delay` (default 1s). Go/JSON duration fields are integer nanoseconds; CLI
duration flags accept strings such as `50ms`. Retrying does not change input.

## Embed a worker

```go
store, err := taskfused.Open(ctx, "taskfused.db")
if err != nil {
    return err
}
defer store.Close()

worker, err := taskfused.NewWorker(store, taskfused.WorkerOptions{
    Namespace:     "account-a",
    Concurrency:   4,
    LeaseDuration: 30 * time.Second,
    Handlers: []taskfused.Registration{{
        Name: "report", Version: "v1",
        Handle: func(ctx context.Context, claim taskfused.Claim) ([]byte, error) {
            // Use claim.Operation.ID for external idempotency.
            // Each execution attempt has a separate claim.Attempt.ID and Token.
            return generateReport(ctx, claim.Operation.Input)
        },
    }},
})
if err != nil {
    return err
}
return worker.Run(ctx)
```

Submit from the same application or another process sharing the local database:

```go
submission, err := store.Submit(ctx, taskfused.Request{
    Namespace: "account-a", Handler: "report", Version: "v1",
    Key: "october-2026", Input: []byte(`{"month":"2026-10"}`),
    JoinExisting: true,
})
```

The identity tuple is `(namespace, handler, version, key)`. Input comparison is byte
for byte; callers must canonicalize JSON if they require semantic equality.
Different input or retry policy at an existing identity is a conflict. Explicitly
joining an existing identity returns its current state, including stored success
or failure, and never starts an extra execution. Use a new key for new work.

For custom execution loops, the lower-level APIs are `Claim`, `Renew`, `Complete`,
`Fail`, and `Attempts`. All mutations verify live attempt ownership. Treat storage
or ownership errors as meaningful; a runner does not silently swallow them.

## Crash recovery demo

Run the example worker with `-delay 10s`. Submit a new `echo` operation and wait for
the worker to print its operation/attempt IDs. Kill the worker process, then start
a new worker with the same database. Its three-second lease must expire before the
replacement can claim the operation. Inspect `attempts`: the original is expired,
the successor has a different attempt ID and larger token, and the operation ID
remains unchanged. Graceful shutdown also leaves unfinished work for lease recovery.
For precise process control, build the example binary first rather than killing a
`go run` wrapper. See [the execution contract](docs/CONTRACT.md) for retry limits.

## Shared waiting and operation cancellation

`store.Wait(ctx, namespace, id)` returns the stored operation when it succeeds,
fails, or is canceled. Execution outcomes are in `Operation.State`; an API error
means the wait/read itself failed. Canceling this context stops only that caller's
wait. It never cancels the operation, its handler, or other waiters. A context
already canceled before the call returns its context error even for terminal work.
If a deadline and terminal read race, either observed outcome is possible.

Waiting reads immediately and shares one local SQLite poller per Store, namespace
and operation, polling every 100ms. Independent processes still observe changes
through SQLite. Each caller reads its own final outcome; terminal records are not
cached across retries. The last departing waiter stops its poller. Store.Close
stops active waiters with ErrClosed. See docs/MEASUREMENTS.md for measured costs.
The CLI wait timeout defaults to 30 seconds and must be positive. Its successful
exit means a terminal outcome was retrieved; inspect `state` for execution success.

Explicitly cancel shared work with `store.Cancel(ctx, namespace, id)` or:

```sh
./taskfused cancel -db demo.db -namespace demo -id op_REPLACE_WITH_RETURNED_ID
```

Cancellation atomically marks queued, retrying, or running work `canceled`, revokes
its active attempt, and prevents any subsequent result/failure commit or claim.
A running handler observes cancellation cooperatively on its next heartbeat,
which occurs every third of its worker lease duration. The runner treats this as
normal handled work and keeps serving other operations.

A canceled terminal record means database authority was revoked. It does not mean
external code has already stopped or that effects were undone. A handler ignoring
its context may continue running; its result cannot overwrite cancellation. A
canceled attempt's finish time records revocation, not confirmed handler exit.

Cancel and result/final-failure commits are serialized: the first terminal commit
wins. Canceling an already succeeded/failed/canceled operation leaves its outcome
unchanged and returns `changed: false`. Repeated cancellation adds no duplicate
event. Joining canceled work returns its canceled outcome without rerunning it.
Use a new identity for intentionally new work. Worker shutdown remains distinct:
it leaves unfinished work for lease recovery rather than canceling the operation.

## Inspect events and retry failed work

`get` exposes `retry_eligible`, `attempt_count`, `max_attempts`, `attempt_limit`,
and `retry_cycle_start`. `max_attempts` remains the immutable submission policy;
`attempt_limit` is the effective lifetime claim limit, initially the same value.
Automatic failures and crashes consume lifetime attempt numbers. Manual retry
never resets these numbers or ownership tokens.

An explicit retry accepts only failed work and requires an action key plus the
failed attempt count observed by the operator:

```sh
./taskfused retry -db demo.db -namespace demo -id op_REPLACE_WITH_RETURNED_ID \
  -action-key repair-1 -expected-attempts 1 -additional-attempts 1
```

Use the actual `attempt_count` from the failed record, not always 1. The command
grants 1–100 additional claims (default 1), with a lifetime maximum of 1000. It
preserves identity, input, handler/version, original max-attempt policy and base
retry delay. A new cycle starts its automatic backoff from that base delay.
`retry_eligible` means a new one-claim action can be considered; larger grants
must also fit the remaining lifetime capacity. Canceled/succeeded work cannot be
reopened by a new retry action.

Persist and reuse the same action key and parameters if the response is uncertain.
The first accepted action returns `applied: true`. Repeating it returns
`applied: false`, its original grant, and the operation's current state. It adds no
budget, event or execution, even after another failure, success, cancellation, or
restart. Changed parameters at the same action key conflict. A different action
with an old failed attempt count cannot retry a later failure. Actions are scoped
to their operation; namespaces still require application-level authorization.

Inspect the transition history in bounded pages:

```sh
./taskfused events -db demo.db -namespace demo -id op_REPLACE_WITH_RETURNED_ID \
  -after 0 -limit 50
```

Continue with the returned `next_after` sequence. Pages default to 50 records and
are capped at 200. The exclusive cursor is global, so gaps from other operations
are normal. Pages are live rather than a frozen snapshot; after `has_more: false`,
the same cursor can read later appends. Retry events include their action key and
original expected/additional attempt counts and granted limit. Attempt errors and
execution times remain available through `attempts`.

To reproduce a failure/retry locally, start the example worker with
`-delay 20ms -fail-until-attempt 1`, submit a fresh echo identity with
`-max-attempts 1`, and wait for its failed outcome. Run the retry command above with
that ID/count, then wait again: attempt 2 succeeds under the same operation ID.
Repeat the retry action to verify replay, and inspect events/attempts to see the
single grant. The failure flag is only a deterministic demonstration mechanism;
it does not run submitted commands or change default echo behavior.

A Wait that already returned a failed outcome does not reattach when an operator
later retries the record. Invoke Wait again for the reopened operation. If Cancel
observes failed work before Retry, it is a terminal no-op and does not veto a later
explicit retry. If Retry queues first and Cancel follows, cancellation wins over
that queued cycle. Replaying the retry action cannot resurrect canceled work.

## Storage and operational limits

SQLite uses WAL mode, FULL synchronization, and a five-second busy timeout. Accepted
submissions atomically commit operation and submission event. Claims and completion
transitions also commit their attempt/event changes atomically. Durability depends
on filesystem/hardware honoring synchronization. An error around commit can leave
acceptance uncertain: look up or resubmit the same identity to resolve it.

Only a local filesystem on one host is supported. SQLite may create WAL/SHM sidecars;
use SQLite-aware backups rather than copying a live main file alone. The database
is not encrypted; protect its directory and sidecars with OS permissions. Namespace
qualification is not authentication. The embedding application must supply
authorized namespaces. The CLI is a local development interface, not a service.
Avoid secrets in CLI arguments on shared machines.

Handlers must cooperate with context cancellation. A worker losing its lease can
still be running external code; ownership tokens fence database commits, not
arbitrary remote effects. Host wall-clock stability is assumed. Tune leases to
exceed realistic scheduling and SQLite write delays; 100ms is an API minimum,
not an operational recommendation. Failed handler attempts back off exponentially;
crashed attempts consume budget and are reclaimed after expiration.

Database versions 1–3 are upgraded transactionally to version 4 on open. IDs,
inputs, existing events, active attempts, results and original retry/lease policy
are preserved. Newer schema versions are rejected.

## Development

```sh
go test -race -count=1 ./...
go vet ./...
go build ./...
```

See [the development plan](docs/DEVELOPMENT_PLAN.md), [behavioral contract](docs/CONTRACT.md),
and [resume instructions](RESUME.md). TaskFused.com is the intended project domain;
this repository does not publish a website.

## Rights

Copyright 2026 TaskFused. All rights reserved. See [NOTICE](NOTICE). No TaskFused
release license has been selected. Dependencies retain their respective licenses.
