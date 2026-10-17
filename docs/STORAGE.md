# Storage limits and result retention

Schema v5 adds explicit namespace admission limits, logical usage inspection and
bounded result pruning. They are disabled by default: a namespace without a limits
row, or with zero limits, has unlimited admission. No background retention task is
started. The embedding application controls when to prune and who may change policy.

## Admission

```sh
go run ./cmd/taskfused set-limits -db demo.db -namespace demo \
  -max-operations 10000 -max-active 100
go run ./cmd/taskfused usage -db demo.db -namespace demo
```

Both limit flags are required because set-limits replaces the complete policy.
Use explicit zeros to remove limits. The library equivalents are SetLimits with
Limits and Usage. Policies persist across close/reopen and apply to every Store
and process using this database. Namespace labels are not authentication.

MaxOperations counts all durable operation identities, including succeeded, failed,
canceled and result-pruned records. MaxActive counts queued, running and retry_wait
records; it counts operations, not waiters, attempts or simultaneously executing
physical handler bodies. Expired running attempts still count until recovery makes
the operation terminal. Cancel/final failure/success frees an active slot. A caller
abandoning Wait does not free one. Lowering limits does not interrupt existing work;
it only blocks further admission while the relevant count is at or above the limit.

New Submit identities check both limits. Joining an existing identity creates no
work and remains available at capacity, with the usual input/policy/conflict checks.
A new operator retry checks MaxActive, but does not consume another identity slot.
An already-applied retry action remains replayable at capacity. Automatic retries
and recovery keep the same active slot; Claim/Renew/Complete/Fail do not acquire new
slots. A pending operation can therefore finish even after limits are lowered.
Rejections return ErrCapacity without inserting operation, event or action records.

Admission checks and mutations share an immediate SQLite write transaction. The
policy update is itself an atomic SQLite write. Competing processes cannot both
claim the last slot. A transaction that started before a new policy commits can
finish under the prior policy. Counts use namespace indexes rather than mutable
process-local counters; checks and Usage still cost work proportional to retained
records and are not intended to be called on every polling tick.

These are logical admission controls, not disk byte quotas. Each input/result is
already bounded to 1 MiB, but attempts, events, errors, indexes and WAL also consume
space. Identities and history accumulate, and a total-identity cap deliberately
stays occupied after completion. Operators must monitor actual files and available
filesystem space. These controls cannot make a disk-full result commit succeed.

## Remove completed result bytes

```sh
go run ./cmd/taskfused prune-results -db demo.db -namespace demo \
  -before 2026-10-01T00:00:00Z -limit 50
```

PruneResults takes a PruneRequest with Namespace, Before and Limit. Before is an
exclusive completion-time cutoff. The batch has at most 200 results; library zero
Limit defaults to 50, while the CLI requires 1–200. Repeat until OperationIDs is
empty. Results are ordered by exact UTC completion timestamp, then operation ID;
fixed-width indexed timestamps preserve nanosecond cutoff comparisons. Results
completed exactly at Before remain untouched. The cutoff can include future times
when an operator explicitly requests that. There is no automatic expiry timer.

Only succeeded operations are eligible. The transaction removes result bytes,
sets ResultPruned=true, removes the retention index record and appends exactly one
result_pruned event per operation. An empty successful result is also eligible and
can be marked pruned with zero removed bytes. Returned RemovedBytes is the sum of
removed payload lengths, not a change in database file size. A batch either commits
in full or rolls back. Repeating after an uncertain commit is safe: an already
pruned record is ineligible, so no second prune event is appended.

Get, Wait, Submit joins and retry-action replays keep returning a succeeded record
with ResultPruned=true and no Result. This flag distinguishes deliberate pruning
from an empty successful result. A read racing pruning may observe the earlier
payload or the later pruned marker; it gets no retention reservation. Already
returned byte slices are not revoked. Applications requiring long-lived results
must copy/archive them before authorizing pruning.

Input bytes, input hash, identity, original policy, state, completion time, attempt
numbers/tokens, attempt errors and retry-action keys are retained. Succeeded work
cannot be reopened; pruning never queues execution, permits a new claim, restores
a stale worker's authority or makes an old retry action apply again. Submit still
compares the original input byte-for-byte, with no hash-only equality shortcut.
Queued/running/retry_wait/failed/canceled work is untouched. Failed input stays
available for a later explicit retry. There is no identity/history deletion API.

SQLite can reuse freed payload pages, but pruning can temporarily grow WAL and
leaves table/index/history overhead. No automatic VACUUM, checkpoint or physical
file truncation is performed. Pruning is not secure erasure: old pages, WAL and
backups can retain earlier bytes. It also does not release MaxOperations slots.

## Inspect usage and upgrade

Usage is one consistent SQLite SELECT snapshot for a namespace. It reports current
limits, operation/active counts, retained input/result byte lengths, pruned count,
and attempt/event/retry-action counts. It excludes SQLite allocation, indexes,
WAL/SHM, strings outside input/result and native memory. Missing namespaces return
zero usage and unlimited limits. This is inspection, not a frozen future capacity
reservation or a substitute for filesystem monitoring.

Open migrates older supported schemas atomically to v5. Existing successful results
remain available and receive retention metadata; none are pruned by migration.
Backfill reads metadata in batches of 200 and preserves exact completion times.
Malformed successful completion timestamps reject and roll back the migration.
The transaction can still take time proportional to existing successful results.
Stop all older processes before upgrading; mixed-version workers with already-open
connections are not supported. Older binaries cannot open the upgraded schema.
Use a SQLite-aware backup before upgrading a database you need to restore.

Validation covers competing Store admissions and a real child process, policy
persistence, joins and retry replays at capacity, running/backoff slots, namespace
isolation, exclusive nanosecond cutoffs, empty results, exact duplicate input,
stale completion rejection, pruning rollback, bounded batches and repeated calls,
restart behavior, v4 backfill across multiple metadata batches, migration rollback,
and the CLI paths. Earlier execution, process-crash and shared-wait tests still run.
