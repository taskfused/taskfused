# TaskFused readiness review

Reviewed 2026-10-17 for the v0.1.0 release. TaskFused is ready for use within its
stated single-host Go/SQLite scope. The core and loopback interface run end to end,
with executable demos and fault tests. It is open source under the Apache License
2.0, and releases are automated: raising `Version` publishes binaries for Linux,
macOS and Windows, each smoke-tested on its own kind of machine (RELEASING.md).
This review is not a production reliability certification.

## Implemented and checked

| Area | Current evidence |
| --- | --- |
| Durable identity and joins | Exact input/original-policy checks, namespace/version isolation, concurrent submissions and restart tests |
| Leased execution | Registered Go handlers, bounded worker concurrency, increasing attempts/tokens, renewal and stale-result rejection |
| Failure/recovery | Bounded automatic retries, backoff, cancellation, real worker Process.Kill and lease recovery |
| Manual retry | Failed-only, observed-count check, durable action replay and lifetime budget cap |
| Shared waiting | Independent contexts, one local poller per operation group, cross-process observation and shutdown tests |
| Capacity/retention | Durable admission limits, logical usage, bounded explicit result pruning and preserved identities/actions |
| Local interface | CLI, loopback HTTP API, browser boundary checks and read-only escaped status page |
| Demonstrations | Embedded and HTTP self-starting scenarios, ordinary IPv4/IPv6 reports and race-instrumented killed workers |
| Storage fault behavior | Statement/deferred-commit rollback, SQLITE_FULL page quota, read-only connection, contention and lost HTTP acknowledgments |
| Diagnostics | Metadata-only internal HTTP500 logs with SQLite codes and privacy tests |
| Releases | Static Linux (x86-64, ARM64), macOS (Apple silicon, Intel) and Windows builds; scripts/smoke.py runs submit/join, worker, wait, limits, pruning, HTTP, interrupt shutdown and the killed-worker HTTP demo on each, except Intel macOS; SHA256SUMS; tag and release only after every check passes |
| Measurements | Preserved ordinary-build baseline and shared-poller comparison, with explicit workload/host caveats |

The full vet/race/build suite passes on Linux amd64 (recorded in test/results/),
and the test workflow runs the suite on Linux and macOS with Go 1.26.x, the race
detector on Linux, and the smoke check on a Windows runner. Windows runs the smoke
check, not the full suite. go.mod declares Go 1.24; that minimum is not separately tested. SQLite is
bundled through mattn/go-sqlite3 and requires a C toolchain to build. Schema is v5;
older processes must stop before migration. No mixed-version rolling upgrade is
supported. See COMPATIBILITY.md for the version policy.

## Decided for v0.1.0

| Item | Decision |
| --- | --- |
| License | Apache License 2.0, with NOTICE and THIRD_PARTY_LICENSES.md |
| Versions | vX.Y.Z tags from `Version` in taskfused.go; before 1.0 a minor version may break compatibility, a patch may not |
| Release automation | The release workflow builds, smoke-tests, tags and publishes after the test workflow passes; no manual step |
| Targets | Linux x86-64/ARM64 (static), macOS Apple silicon/Intel, Windows x86-64 |

Remote authentication is intentionally outside the local product scope, not a
feature silently promised for the release. Local processes can access every
namespace. Browser visual rendering of the status page is still unverified.

## Production qualification remains separate

Before making stronger durability/capacity claims, test real filesystem/VFS I/O
faults, physical disk/WAL exhaustion and backup/restore on the supported deployment
stack. Qualify target OS/toolchain combinations and realistic retained-history,
payload and contention workloads. The recorded timings are development observations,
not a recovery SLA or fleet capacity estimate. Browser rendering has not been
visually verified in this environment because no browser executable is installed.

A user must supply effect-destination idempotency where repeated external actions
matter. Identity retention remains permanent; result pruning does not free identity
slots, erase old bytes securely or guarantee file shrinkage. No multi-host backend,
workflow graph, arbitrary command runner, automatic fusion, remote hosting or
metrics/audit service is required to complete the scoped local prototype.

## What comes next

Not yet decided. CLAUDE.md records the agreed next step once there is one.
Physical filesystem qualification and broader capacity testing remain
prerequisites for stronger production claims, not hidden additions to the scope.
