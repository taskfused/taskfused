# How TaskFused 0.1 was built

A record of what was built, in order, up to the first release. It says nothing
about what comes next: [CLAUDE.md](../CLAUDE.md) is the one place for that, and
the next step is agreed with the owner before it goes there.

## Scope

A small Go engine for durable operations with explicit identity and duplicate-work
joining: an embedded library on local SQLite, with a CLI and a loopback HTTP API
over the same engine, on one host. It grew out of three earlier proposals (a local
task-graph runtime, a coalescing primitive and durable task execution), taking
durable operations as the foundation and explicit coalescing as the behavior that
sets it apart.

## Milestones

1. **Durable submission and identity.** Schema, opaque inputs, atomic acceptance
   of each operation with its event, explicit joining, conflicts and
   namespace-scoped lookup. Concurrent callers accept one identity, different
   input is refused, and accepted work survives an abrupt process exit.
2. **Execution and workers (schema v2).** Handler registration, claims, attempts
   with increasing tokens, lease renewal, bounded concurrency, retries with
   backoff, stored results and expired-lease recovery, checked with real
   subprocess crashes.
3. **Shared execution (schema v3).** Waiting with independent caller contexts,
   shared stored outcomes and explicit cooperative cancellation. 100 equivalent
   submissions share one operation; canceling a wait never cancels the work.
4. **Operator retry and events (schema v4).** Failed-only retry actions that are
   safe to repeat, immutable original policy, a lifetime attempt cap and paged
   event history.
5. **Demo harness and measurements.** A self-starting Go demo that kills a real
   worker process and measures submission, execution and waiting; one shared
   poller per operation cut the cost of many waiters
   ([MEASUREMENTS.md](MEASUREMENTS.md)).
6. **Limits and retention (schema v5).** Durable admission limits per namespace,
   usage reporting and bounded pruning of succeeded results
   ([STORAGE.md](STORAGE.md)).
7. **Local HTTP API and status page.** Loopback-only binding, Host and origin
   checks, bounded bodies and deadlines, and a read-only status page
   ([HTTP.md](HTTP.md)), with the same demo run over HTTP
   ([HTTP_DEMO.md](HTTP_DEMO.md)).
8. **Storage faults.** Failures forced mid-statement and at commit on 13 kinds
   of write, a full database, a read-only file, lock contention and replies lost
   after commit ([FAULTS.md](FAULTS.md)).
9. **Release (v0.1.0, October 2026).** Apache 2.0, CI on Linux and macOS with a
   Windows smoke check, recorded results in `test/results`, and automated releases
   of smoke-tested binaries for Linux, macOS and Windows ([RELEASING.md](../RELEASING.md),
   [READINESS.md](READINESS.md), [COMPATIBILITY.md](COMPATIBILITY.md)).

## Left out on purpose

Multi-host coordination, Redis or Postgres backends, automatic fusion of workflow
stages, workflow graphs, cron scheduling, running arbitrary shell jobs, agent
orchestration and freshness-based result caching. None of these is promised; any
of them would be a new decision with the owner.
