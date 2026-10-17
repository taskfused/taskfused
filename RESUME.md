# Resume TaskFused

Updated: 2026-10-17. Completed milestone: storage admission and result retention.

## Read first

Read README.md, docs/CONTRACT.md, docs/DEVELOPMENT_PLAN.md, docs/MEASUREMENTS.md and docs/STORAGE.md.
Repository: https://github.com/taskfused/taskfused. Intended site: TaskFused.com;
no website repository/hosting target has been provided. Work is authorized on this
code repository. No release license is selected; NOTICE reserves rights.

## Current engine

The core is schema v5. It supports durable explicit-identity submissions,
duplicate joining, immutable input/original policy, local SQLite WAL/FULL, leased
at-least-once attempts with increasing tokens, bounded retries and backoff, durable
results/history, process-crash recovery, shared waiting and cooperative cancellation.
Wait contexts do not cancel work. Cancel revokes database authority, not external
effects; canceled/succeeded work stays terminal. Explicit failed-only operator
retry uses durable idempotent action keys and observed failure counts, grants up to
100 additional claims per action (1000 lifetime total), and does not reset ownership.
Events are namespace-qualified and paginated. Read the contract before changes.

CLI: submit/get/attempts/events/wait/cancel/retry/usage/set-limits/prune-results. The example worker supports echo
and a deterministic first-attempt failure flag. No HTTP/UI, multi-host support or public release exists. Namespace is not authentication. Go/CGO and a C
compiler are required. Stop older processes before schema upgrades; mixed-version workers are unsupported.

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

## Shared waiting milestone completed

Wait now shares repeated pending reads through one local poller per Store,
namespace and operation. Each caller performs its immediate read and final read;
terminal outcomes are never cached across retry cycles. Caller cancellation only
detaches that subscriber. The last departure cancels the poller. Store.Close marks
the Store closed, cancels and joins pollers, then closes SQLite; Wait exposes
ErrClosed. Registry entries are removed before publishing terminal notifications,
and old poller cleanup cannot delete a replacement. No schema change.

Race tests cover shared lifetime, cancellation/reattachment, old-cleanup overlap,
active Wait shutdown, repeated Close, failure/retry freshness and independent input,
result and completion-time values; existing 100-caller and cross-process tests pass.
Local go vet, go test -race -count=1 ./... and go build ./... passed. The full
ordinary-build process demo and isolated measurements passed. New report:
docs/measurements/2026-10-17-shared-wait-linux-amd64.json (parent8388d422,
modified=true); original report preserved. For 100 waiters, median CPU fell from
84.145ms to 18.805ms and Go allocated bytes from 3,890,088 to 549,264 in the 1s
samples. Single-waiter allocations increased slightly; write latency was mixed.
See docs/MEASUREMENTS.md for limitations. These are short samples, not an SLA.

## Storage milestone completed

Schema v5 adds namespace_limits and a result_retention index plus ResultPruned.
SetLimits replaces durable MaxOperations/MaxActive policy; zeros mean unlimited.
New Submit checks both limits under its immediate write transaction. Retry checks
active count only after replay/state/stale/budget validation. Joins and already
applied retry actions still work at capacity. Counts include retained identities;
active means queued/running/retry_wait. Lowering limits never interrupts work.
Usage is a single SELECT snapshot of logical counts/payload bytes, not disk usage.

PruneResults is explicit, succeeded-only, 1–200 records (zero defaults50), ordered
by exclusive completion cutoff then ID. It removes result bytes, sets ResultPruned,
appends result_pruned event and removes retention metadata in the same transaction.
Inputs, identities, completion/state, attempt history and retry action keys remain.
No auto-prune, input/history deletion, checkpoint, VACUUM or secure erasure. Pruning
cannot free identity slots or permit reexecution. Empty successful results can be
marked pruned with zero bytes removed. Get/Wait/joins/replays expose the marker.

Migration backfills successful completion metadata in batches of200, using fixed
UTC nanosecond strings to avoid variable RFC3339 fractional ordering issues. Bad
successful completion timestamps cause full migration rollback. Old processes
must stop before Open upgrades. Old binaries refuse the new schema.

CLI adds usage/set-limits/prune-results; both policy flags are required to replace
limits explicitly. Local vet/race tests/build passed. New tests cover competing
Stores and a child process, persistent policy, capacity rejection rollback, replay
at capacity, running/backoff states, namespace isolation, exact cutoff precision,
empty results, identity/input retention, stale completion rejection, injected event
failure rollback, bounded batches, restart,201-row migration and rollback, and CLI.
The race suite includes the existing real-process crash/demo harness. Previous
measurement artifacts are unchanged; no new throughput/file-shrink claim is made.

## Exact next step

Add a small loopback-only HTTP interface and read-only local status page on top of
these APIs. Define explicit request/response/error mapping, body/time limits and
shutdown behavior first. Use no remote binding or authentication assumptions;
namespace labels are not authorization. Include usage and clear pruned-result
states; keep limit changes and pruning explicit administrative actions. Test HTTP
submission/join/wait/cancel/retry and shutdown without weakening the core contract.
Keep arbitrary shell jobs, workflow graphs, multi-host execution and automatic
fusion deferred. Broaden operational disk/IO fault handling as justified; the
logical admission controls are not filesystem quotas. A website/release still
needs its explicit target/license. The ten-session estimate is a planning budget.

## Repository transport and session discipline

Use GitHub connector tree/commit/ref writes with expected remote head; direct git
push lacks CLI credentials. Read source in small batches to avoid tool-output
truncation. Fetch and verify staged/remote trees before aligning local HEAD. Never
overwrite unseen changes. Keep coherent milestones, update these instructions and
report observed checks. No license or website work is implied by continuing code.
