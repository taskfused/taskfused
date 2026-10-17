# Resume TaskFused

Updated: 2026-10-17. Completed milestone: shared waiting and operation cancellation.

## Read first

Read README.md, docs/CONTRACT.md, and docs/DEVELOPMENT_PLAN.md. Repository:
https://github.com/taskfused/taskfused. Intended site: TaskFused.com; no website
repository or hosting target has been provided. Work is authorized on this code
repository. No release license has been selected; NOTICE reserves rights.

## Current implementation

Milestones 1–2 provide durable submissions, explicit duplicate joining, identity
and retry-policy conflict checks, namespace-qualified lookup, a local SQLite store,
leased execution, durable attempts and increasing tokens, renewal, guarded
result/failure commits, bounded retries/backoff, and expired-attempt recovery.
Workers have immutable handler registries, RunOnce and bounded-concurrency Run.
The engine supports one host with a local filesystem and needs CGO/a C compiler.

Milestone 3 adds schema v3, read-only Wait, and explicit Cancel. Wait polls every
100ms, works across processes, and returns terminal Operation records with nil API
error even for failed/canceled outcomes. Its caller context only stops that wait.
Cancel marks queued/retry_wait/running work canceled and atomically revokes the
active attempt. A canceled attempt's finish time is revocation, not observed
handler exit. There is no pending cancellation-request flag or acknowledgment.

Success/final failure versus cancellation is first-terminal-commit-wins. Cancel is
idempotent and leaves existing terminal outcomes unchanged. Joining canceled work
returns its stored outcome without rerunning it. Renew/Complete/Fail on canceled
work return ErrOperationCanceled; workers treat that as normal handled work rather
than stopping siblings. Cooperative handlers detect cancellation on heartbeat;
uncooperative or already-claimed handlers may still execute external code, but
cannot commit a later result. Worker shutdown still leaves work for lease recovery.

CLI: submit/get/attempts/wait/cancel. Wait has a positive timeout, default 30s; a
timeout changes no operation. A successful CLI wait retrieves any terminal outcome,
so inspect the state. The example worker handles echo/v1 in namespace demo.

Versions 1 and 2 migrate transactionally to v3, preserving IDs, active attempts,
results, events, leases and retry policy. No HTTP/UI, operator retry, retention,
or multi-host coordination exists yet. There is no public release.

## Validation

Milestone 2 GitHub CI passed before this session. Milestone 3 passed locally on
Linux with Go 1.26.1: go test -race -count=1 ./..., go vet ./..., go build ./...,
CLI/example-worker builds, formatting checks and diff checks.

New coverage includes 100 equivalent submissions sharing one operation/result,
one canceled waiter while 99 others succeed, wait deadlines and namespace lookup,
failed/canceled terminal waits, independent result bytes, cross-process waiting,
queued/retry-wait/running cancellation, idempotence/event count, late renewal and
result/failure rejection, cancellation racing success and retry scheduling,
rollback on a cancellation-event failure, unaffected sibling work, uncooperative
handler behavior, v2 migration preserving live ownership/results/events, and
rollback on migration failure. Existing submission, lease, retry and crash tests
remain passing. CLI tests cover wait deadlines and explicit cancellation.

A local CLI/example-worker integration verified that a timed-out wait leaves work
active, two separate wait processes retrieve the same result, canceling a running
operation returns a durable canceled outcome/attempt, and the same runner executes
subsequent work successfully before graceful shutdown.

## Exact next step

Implement operator inspection and explicit failed-operation retry.
First specify retry semantics: failed-only retry, action idempotency, preserved
operation/input, increasing attempt numbers/tokens, and budget accounting that does
not silently change the original immutable submission policy. Canceled work remains
terminal; intentional new work uses a new identity. Do not reset ownership counters
or accidentally make an old attempt valid again.

Add namespace-qualified event inspection with bounded pagination/cursors. Expose
retry eligibility and attempt/event details through the CLI. Add failure/race tests
for retry versus cancellation and duplicate operator actions. Keep schema migration
explicit if new budget/generation fields are needed. Then build a reproducible
shared-execution/recovery demonstration. HTTP/UI follow these semantics; storage
retention, measured polling load, and broader operational limits remain later work.

## Repository transport and session discipline

The GitHub connector can write this repository. Direct HTTPS git push has no CLI
credentials here. Publish verified files with connector tree/commit/ref operations,
using the expected remote head. Fetch afterward and verify local/remote trees match;
local git can align to the connector-created commit. Never force-push over unseen
changes. Do not reproduce or seek private credentials.

Keep each change scoped to a coherent milestone. Update this file with observed
checks and remaining behavior. Do not claim elapsed session hours or exactly-once
external effects. No license or website work is implied by continuing code.
