# Resume TaskFused

Updated: 2026-10-17. Completed milestone: operator retry and event inspection.

## Read first

Read README.md, docs/CONTRACT.md, and docs/DEVELOPMENT_PLAN.md. Repository:
https://github.com/taskfused/taskfused. Intended site: TaskFused.com; no website
repository or hosting target has been provided. Work is authorized on this code
repository. No release license is selected; NOTICE reserves rights.

## Current implementation

Milestones 1–3 supply durable shared operations, immutable identity/input/policy,
local SQLite, namespace-qualified reads, lease ownership and increasing tokens,
bounded automatic retries, attempt/result history, crash recovery, shared waiting,
and explicit cooperative cancellation. Workers expose RunOnce and bounded Run.
Wait is read-only 100ms polling across processes. Cancel revokes database ownership,
not external effects; sibling work continues. See the contract for terminal races.

Milestone 4 adds schema v4, explicit failed-operation Retry, Events pagination,
CLI retry/events and eligibility/budget fields in get. Original MaxAttempts remains
immutable; AttemptLimit is the lifetime limit (initially MaxAttempts), and
RetryCycleStart restarts backoff without resetting attempt numbers/tokens.

Retry requires an operation-scoped action key, observed failed attempt count and
1–100 additional claims, capped at 1000 lifetime claims. Only failed work can be
reopened by a new action. State/budget/action/event update atomically. Repeating an
action returns its original grant and current operation with Applied=false, even
after another failure, success, cancellation or restart. Changed parameters conflict;
stale new actions cannot target a later failed count. Canceled work stays terminal.

Event pages default to 50, max 200, with exclusive global sequence cursors and
live append semantics. Retry events include the original grant metadata. The
example worker's -fail-until-attempt flag gives deterministic failure/retry demos;
it defaults off and never evaluates input as code. CLI commands are
submit/get/attempts/events/wait/cancel/retry. The store supports one host/local
filesystem, needs CGO/a C compiler, and migrates v1–v3 transactionally to v4.

No HTTP/UI, retention, multi-host support or public release exists. There are no
performance/compression/scale claims. Namespace is not authentication.

## Validation

Milestone 3 GitHub CI passed before this session. Milestone 4 passed locally on
Linux with Go 1.26.1: go test -race -count=1 ./..., go vet ./..., go build ./...,
CLI/example builds, formatting and diff checks.

Coverage includes 32 concurrent duplicate actions applying exactly one grant,
competing distinct actions, replay after later failure/restart/success/cancellation,
parameter conflicts, stale action rejection, immutable submission policy and joining,
monotonic attempt/token counters, cycle-relative backoff, lifetime-limit boundaries,
retry versus cancellation ordering, retry/event rollback, bounded event pagination
with global gaps and live appends, namespace isolation, old-schema preservation
and migration rollback. Legacy schema fixtures use fixed-column helpers rather
than current API projections, so they remain genuine v2/v3 databases. Existing
execution, cancellation, shared-waiting and crash tests continue to pass.

A CLI/example-worker integration forced the initial one-budget attempt to fail,
granted one additional attempt, replayed the same action, verified success with
unchanged identity/input/original policy and a larger token, and traversed all six
events in two-row pages with correct grant metadata and no duplicates.

## Exact next step

Build a reproducible end-to-end demonstration and measure operational behavior
before expanding interfaces. Prefer one runnable Go example/test harness showing
100 equivalent submissions sharing work, isolated waiter timeout, a worker crash
and recovery, cooperative cancellation, an operator retry, and event inspection.
Use separate processes where crash behavior is being demonstrated; do not simulate
recovery with only a graceful close. Keep a clear distinction between one operation
and multiple at-least-once attempts. Report measured latency, storage growth and
polling overhead with environment/workload, rather than promised throughput.

Then address practical storage/retention limits and inspection ergonomics. HTTP
and a local status UI follow the core demo/limits; authentication and safe binding
must be considered before remote use. Multi-host coordination, automatic graph
fusion and arbitrary shell execution stay deferred. Revisit the original ten-session
estimate after these measurements rather than claiming all release gates are met.

## Repository transport and session discipline

The GitHub connector writes this repository; direct HTTPS git push has no CLI
credentials here. Publish verified files with connector tree/commit/ref operations
and the expected remote head. Fetch and compare staged/remote trees before aligning
local HEAD. Never overwrite unseen changes. Read UTF-8 files in small batches when
passing tree content; a full-repository JSON dump can exceed output limits.

Keep each change scoped to a coherent milestone and update this file with observed
checks and remaining behavior. Do not claim elapsed session hours or exactly-once
external effects. No license or website work is implied by continuing code.
