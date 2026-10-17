# TaskFused readiness review

Reviewed 2026-10-17 after candidate packaging. TaskFused is ready for local
prototype evaluation within its stated single-host Go/SQLite scope. The core and
loopback interface run end to end, with executable demos and fault tests. A clean-checkout native candidate build/verification process is now implemented.
There is no tagged public release or authorized public binary distribution. This review is not
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
| Candidate packaging | Clean committed export, provenance for three binaries, normalized archive/checksums, fresh-cache rebuild comparison and extracted-artifact smoke tests |
| Measurements | Preserved ordinary-build baseline and shared-poller comparison, with explicit workload/host caveats |

The full local vet/race/build suite passes on Linux amd64 with Go 1.26.1 and CGO.
GitHub CI uses Ubuntu and Go 1.26.x and now gates native candidate packaging. go.mod declares Go 1.24; minimum-version and
other-OS qualification are not established by that CI. SQLite is bundled through
mattn/go-sqlite3 and requires a C toolchain. Schema is v5; older processes must stop
before migration. No automatic mixed-version rolling upgrade is supported.

## Work before a public prototype release

| Item | State / concrete completion condition |
| --- | --- |
| License decision | NOTICE reserves all rights. Select the intended release license before publishing a tagged source/binary release; dependency notices remain separate. |
| Versioned packaging | Local candidate-commit IDs, native CGO builds, checksums and extracted-artifact checks are implemented. Choose a public version/tag policy before release. |
| Release automation | CI now builds/rebuilds and smoke-tests native candidates without uploading them. Public tag/release publication remains intentionally absent and requires explicit authorization. |
| Compatibility statement | COMPATIBILITY.md scopes candidates to Linux amd64/Go 1.26.x/schema v5 and records dynamic runtime limitations. Public v0 compatibility/version policy still needs a decision. |
| User-facing validation | Extracted CLI/worker/server/demo quickstart is automated; browser visual rendering remains unverified without a browser runtime. |

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

## Next decision point

The local prototype and candidate validation path are implemented. Review the
candidate manifest/quickstart and select the public release license, version policy
and desired initial binary targets before adding publication automation. Do not
publish or tag implicitly. Packaging currently has one qualified native target;
request additional targets only with their own actual build and runtime checks.
Physical filesystem qualification and broader capacity testing remain prerequisites
for stronger production claims, not hidden additions to the local prototype scope.
