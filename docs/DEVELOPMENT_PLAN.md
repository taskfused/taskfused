# TaskFused development plan

Updated: 2026-10-17. This is the initial implementation plan, not a release promise.

## Product boundary

A small Go engine for durable operations with explicit identity and duplicate-work
joining. Start with an embedded library and local SQLite. Add a CLI and HTTP facade
over the same engine. Support one host first. Do not describe this as distributed
execution until a separate multi-host design and failure tests exist.

The recovered earlier proposals were a local task-graph runtime (October 7), a
coalescing primitive (October 12), and durable task execution (October 15). This
plan uses durable operations as the foundation and explicit coalescing as the
product's distinguishing behavior. Automatic stage fusion is not a v0.1 feature.

## Work sequence

Use sessions of about two to three hours as planning units. Completion is measured
by acceptance criteria, not time spent. Each session updates RESUME.md with changes,
checks, limitations, and the exact next action. The initial ten-session budget can
expand if crash and ownership testing exposes more work.

Sessions 1–2: durable submission and operation identity. Establish schema and
application marker, opaque input contract, atomic operation/event acceptance,
explicit duplicate joining, conflicts, and namespace-qualified lookup. Provide
submit/get CLI commands. Acceptance: concurrent callers accept one identity,
conflicting input is refused, and accepted work survives an abrupt process exit.
Completed. See RESUME.md for validation.

Sessions 3–4: execution contract and worker core. First settle terminal-state
joining, retries, cancellation, ownership, and schema migration. Implement handler
registration, claims, attempts, lease renewal, bounded concurrency, retry backoff,
result commit, and expired-lease recovery. Acceptance: restart recovers unfinished
work and expired attempts cannot renew or commit after losing ownership. Test
expiry even before a successor claims; do not rely only on matching a token.
Completed: schema v2, handler registry, bounded worker runner, attempts/tokens,
renewal, retries, results, and local subprocess/crash checks. Explicit operation
cancellation remains in the following milestone; worker shutdown is separate.

Sessions 5–6: shared execution. Add waiting with independent caller contexts,
shared durable result retrieval, and explicit cooperative cancellation. Acceptance:
100 equivalent submissions share one live operation; canceling a wait does not
cancel its handler; different namespaces and versions never share work. Recovery
may add attempts and must not be presented as one lifetime execution.

Sessions 7–8: failure testing and limits. Faults before and after claim, around
handler effects and result commit, process termination, ownership expiry, exhausted
retries, input/result size limits, retention, and shutdown. Use deterministic clocks
where useful and real subprocess tests at transaction boundaries. Report actual
storage growth and latency. Acceptance: documented guarantees hold under injected
failures, with clear handling of uncertain acceptance and external idempotency.

Sessions 9–10: usable prototype. Add CLI inspection/retry, minimal HTTP endpoints,
a local status page, two demos, and runnable documentation. The main demo submits
100 equivalent report requests, kills a worker, restarts it, and shows attempts and
shared results. A second demo shows independent work proceeding concurrently.
Service authentication and a safe default bind address must precede remote use.
No public hosting is implied by this milestone.

## Deliberately deferred

Multi-host coordination, Redis/Postgres backends, automatic graph-stage fusion,
workflow graphs, cron scheduling, arbitrary shell jobs, AI agent orchestration,
connector marketplaces, and completed-result freshness caching. Inspect ukue's
actual implementation before deciding on reuse; no dependency on ukue has been
introduced. TaskFused owns operation identity and shared execution semantics.

## Release gate

A first public release requires tested execution/recovery, clear operational
limits, honest at-least-once semantics, a selected license, CI, reproducible demos,
and a versioned release procedure. The public repository is a prototype with an
all-rights-reserved notice. Local execution/recovery now exist, but a public
release still needs the remaining operational and release work. TaskFused.com can
document the prototype once a separate site target is provided. Do not create a replacement site or choose hosting implicitly.
