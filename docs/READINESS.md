# TaskFused readiness review

Reviewed 2026-10-17 after the storage-fault milestone. TaskFused is ready for local
prototype evaluation within its stated single-host Go/SQLite scope. The core and
loopback interface run end to end, with executable demos and fault tests. There
is no tagged public release or prepared binary distribution. This review is not
a production reliability certification.

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
| Measurements | Preserved ordinary-build baseline and shared-poller comparison, with explicit workload/host caveats |

The full local vet/race/build suite passes on Linux amd64 with Go 1.26.1 and CGO.
GitHub CI uses Ubuntu and Go 1.26.x. go.mod declares Go 1.24; minimum-version and
other-OS qualification are not established by that CI. SQLite is bundled through
mattn/go-sqlite3 and requires a C toolchain. Schema is v5; older processes must stop
before migration. No automatic mixed-version rolling upgrade is supported.

## Work before a public prototype release

| Item | State / concrete completion condition |
| --- | --- |
| License decision | NOTICE reserves all rights. Select the intended release license before publishing a tagged source/binary release; dependency notices remain separate. |
| Versioned packaging | Define a version/tag policy, supported targets and repeatable CGO build process; produce checksums and validate artifacts from a clean checkout. |
| Release automation | CI currently tests/builds; it does not publish TaskFused releases or attach tested binary artifacts. Add a deliberately gated release workflow. |
| Compatibility statement | Freeze the intended v0 API/schema expectations and document supported Go/OS/architecture targets based on actual checks. |
| User-facing validation | Run the documented quickstart from the packaged artifact and visually check the status page when a browser runtime is available. |

No license, version tag, release publication or hosting choice is implied by this
review. The website still needs its separate repository/hosting target. Remote
authentication is intentionally outside the local product scope, not a feature
silently promised for the release. Local processes can access every namespace.

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

## Next engineering session

Prepare a clean-checkout release candidate process without publishing it: a local
build/checksum/verification script, explicit candidate compatibility notes and a
quickstart smoke check against the produced executable. Run it with the current
all-rights-reserved notice intact. Public licensing and publication are separate
decisions; do not choose a license or push a release tag implicitly. Further scope
should follow observed defects or the release checklist above rather than adding
new orchestration features.
