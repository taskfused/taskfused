# Resume TaskFused

Updated: 2026-10-17. Completed milestone: runnable demo and measured baseline.

## Read first

Read README.md, docs/CONTRACT.md, docs/DEVELOPMENT_PLAN.md and docs/MEASUREMENTS.md.
Repository: https://github.com/taskfused/taskfused. Intended site: TaskFused.com;
no website repository/hosting target has been provided. Work is authorized on this
code repository. No release license is selected; NOTICE reserves rights.

## Current engine

The core remains schema v4. It supports durable explicit-identity submissions,
duplicate joining, immutable input/original policy, local SQLite WAL/FULL, leased
at-least-once attempts with increasing tokens, bounded retries and backoff, durable
results/history, process-crash recovery, shared waiting and cooperative cancellation.
Wait contexts do not cancel work. Cancel revokes database authority, not external
effects; canceled/succeeded work stays terminal. Explicit failed-only operator
retry uses durable idempotent action keys and observed failure counts, grants up to
100 additional claims per action (1000 lifetime total), and does not reset ownership.
Events are namespace-qualified and paginated. Read the contract before changes.

CLI: submit/get/attempts/events/wait/cancel/retry. The example worker supports echo
and a deterministic first-attempt failure flag. No HTTP/UI, retention, multi-host
support or public release exists. Namespace is not authentication. Go/CGO and a C
compiler are required. This milestone changes the demo/docs, not engine semantics.

## New runnable harness

Run `go run ./examples/demo > report.json`, or build examples/demo first.
The harness re-executes its own binary; it needs no installed worker executable.
It checks 100 equivalent submissions/99 successful waiters/one isolated timeout,
actual Process.Kill recovery, stale-result rejection, cooperative cancellation with
surviving work, idempotent operator retry and complete paginated events. A worker
confirms its committed claim before the parent kills it. Stdin EOF is used only
for graceful cleanup, never as the crash mechanism.

Default measurements: 1000 sequential unique submissions and trivial RunOnce
executions with 128-byte input/results; live and closed main/WAL/SHM sizes; three
isolated process samples each at zero/one/100 waiters for one second. CPU uses
process getrusage on Linux/macOS; other platforms report it unavailable. Go
allocation churn excludes SQLite/native allocations. Wait SQL-read counts are not
instrumented. All databases are private temporary files removed after cleanup.
Workload input bytes are capped at 64 MiB. Progress is stderr; report JSON is stdout.

## Observed baseline

See docs/MEASUREMENTS.md and docs/measurements/2026-10-17-linux-amd64.json for exact
values, environment, method, all samples and caveats. This is an ordinary Linux
amd64/Go 1.26.1/SQLite 3.50.4 build in temporary storage whose physical backend was
not characterized, not a capacity/SLA or durability qualification.

Observed p50: Submit 1.007ms; trivial RunOnce 2.131ms. Submit maximum was 147.982ms,
retained in the report with unknown cause. The 1000 completed one-attempt operations
added 1,392,640 logical bytes after close above an 81,920-byte empty baseline; live
WAL/SHM footprint was materially larger. Do not treat amortized bytes/op as fixed.

One-second waiting medians: idle 0.193ms process CPU/768 Go allocated bytes;
one waiter 6.938ms/41,632 bytes; 100 waiters 84.145ms/3,890,088 bytes. No file growth
or operation/attempt/event mutation was observed. Allocation churn is not retained
memory. Short-run noise and host activity prevent scale extrapolation.

## Validation

Milestone 4 GitHub CI passed before this session. Milestone 5 passed local
race-enabled tests, vet and build on Linux/Go 1.26.1. The new race-tested harness
integration uses actual child processes, 100 shared requests, real worker kill,
cooperative cancellation, retry/event inspection, storage record counts, isolated
waiting samples, workload bounds and nearest-rank percentile checks. No performance
threshold is asserted in CI. Earlier engine/CLI tests remain passing.

The full ordinary-build default harness ran successfully and produced the committed
raw report. Its build metadata records the parent revision with modified=true,
because the measurement was captured before this milestone's commit. Source and
report are shipped together; future comparisons must identify their code/config.

## Exact next step

Use the baseline to improve shared waiting before expanding interfaces. The current
Wait polls per caller even when callers share the same operation. Consider one
local poller per Store/namespace/operation with independent subscribers, while
retaining periodic SQLite polling for cross-process changes. Prove independent
wait cancellation, independent result bytes, no lost wakeups, cleanup when the last
waiter leaves, Store.Close behavior, and fresh observations after an operator retry
reopens failed work. Do not leave cached terminal outcomes attached to a later cycle.
Compare before/after with the existing isolated-process harness and preserve this
baseline rather than overwriting it. A slower/mixed result is still a valid finding.

Then add practical storage/capacity and payload-retention controls. Distinguish
removing payload bytes from deleting identity/action tombstones: deleting those
records can re-execute an old submission or retry action. Define retention guarantees
before schema changes. History may be much larger for multi-attempt work than this
one-attempt storage sample. HTTP/local UI follow these limits; remote use needs an
explicit authentication/binding design. Multi-host and automatic graph fusion stay
deferred. The original ten-session estimate is a planning budget, not a release claim.

## Repository transport and session discipline

Use GitHub connector tree/commit/ref writes with expected remote head; direct git
push lacks CLI credentials. Read source in small batches to avoid tool-output
truncation. Fetch and verify staged/remote trees before aligning local HEAD. Never
overwrite unseen changes. Keep coherent milestones, update these instructions and
report observed checks. No license or website work is implied by continuing code.
