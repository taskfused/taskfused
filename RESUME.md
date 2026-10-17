# Resume TaskFused

Updated: 2026-10-17. Completed milestone: leased execution and crash recovery.

## Read first

Read README.md, docs/CONTRACT.md, and docs/DEVELOPMENT_PLAN.md. Repository:
https://github.com/taskfused/taskfused. Intended site: TaskFused.com; no website
repository or hosting target has been provided. Work is authorized on this code
repository. No release license has been selected; NOTICE reserves rights.

## Current implementation

Milestone 1's durable submissions, explicit duplicate joining, conflict checks and
namespace-qualified lookup remain. Milestone 2 adds a transactional schema-v1-to-v2
migration, queued/running/retry_wait/succeeded/failed states, durable attempts,
increasing ownership tokens, lease renewal, guarded result/failure commits,
exponential retry delays, bounded retry budgets, and lazy expired-attempt recovery.

Low-level APIs: Claim, Renew, Complete, Fail, Attempts. Worker API: NewWorker with
an immutable handler registry, RunOnce, and Run with bounded concurrency. Workers
renew while executing, convert handler panics/oversized results into failures,
and cooperate with cancellation. Stale ownership and storage errors stop the
runner; they are not silently swallowed. Expired ownership is rejected even before
a successor claims. Tokens protect database commits, not external effects.

CLI: submit/get/attempts, with retry-policy flags. examples/worker registers an echo
handler in namespace demo at v1 and can be used for crash demonstrations. The
SQLite driver requires CGO and a C compiler. Only one host/local filesystem is
supported; worker shutdown leaves unfinished attempts for lease-expiry recovery.

Joining a terminal operation returns its stored outcome; it never reruns it.
Retry policy is immutable for a given identity. Crashed claims consume budget.
Explicit operator retry, shared waiting, operation cancellation, retention,
HTTP endpoints and a UI are not yet implemented. There is no public release.

## Validation

Milestone 1 GitHub CI completed successfully before this session. Milestone 2
passed locally on Linux with Go 1.26.1: go vet ./..., go test -race -count=1 ./...,
go build ./..., CLI build and example-worker build, and formatting/diff checks.

Tests include exact lease-expiry boundaries, stale renewal/success/failure before
and after reclamation, non-shortening renewal, retry timing/budgets, crash budget
exhaustion, concurrent claims and competing terminal commits, claim/result event
rollback, v1 migration with preserved identity/events and valid foreign keys,
handler failures/panics/oversized results, heartbeat renewal, lease-loss
cancellation, bounded worker concurrency, shutdown recovery, registry isolation,
and CLI retry flags/attempt history. Existing submission tests remain passing.

An end-to-end local binary check submitted an echo operation, observed its first
claim, killed the worker with SIGKILL, restarted a worker on the same SQLite file,
and verified expired/succeeded attempt history, a larger token, unchanged operation
ID, recovered result bytes, terminal joining, and graceful replacement shutdown.

## Exact next step

Implement shared waiting and explicit cooperative operation cancellation, beginning
with their race semantics in docs/CONTRACT.md. A caller canceling its wait must not
cancel the shared operation. Define cancellation versus success, queued/retry-wait
cancellation, and lease-loss behavior before adding a cancellation flag/schema v3.
Cancellation must never claim to undo external side effects. Add tests for 100
joined callers, one canceled waiter, other waiters receiving the shared outcome,
and operation cancellation racing result commit.

Then add operator retry with an explicit budget/identity policy, event inspection,
and a reproducible user-facing demo. Keep HTTP/UI after these semantics, and keep
multi-host coordination and automatic graph fusion deferred.

## Repository transport and session discipline

The GitHub connector can write this repository. Direct HTTPS git push has no CLI
credentials here. Publish verified files with connector tree/commit/ref operations,
using the expected remote head. Fetch afterward and verify local/remote trees match;
local git can remain aligned to the connector-created commit. Never force-push over
unseen changes. Do not reproduce or seek private credentials.

Keep each change scoped to a coherent milestone. Update this file with observed
checks and remaining behavior. Do not claim elapsed session hours or exactly-once
external side effects. No license or website work is implied by continuing code.
