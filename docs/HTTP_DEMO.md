# Reproducible HTTP recovery demo

```sh
go run ./examples/demo -http > http-report.json
```

The demo starts a real HTTP server on an ephemeral IPv4 loopback port in the parent
process and re-executes its own binary as registered Go worker child processes.
It needs no separately installed CLI/worker executable. Database files are private
temporary files, removed after workers and the server stop. Progress is stderr,
and the correctness report is JSON on stdout. Any failed check exits nonzero.
The entire run is bounded by a two-minute parent context.

Use `-requests` to choose 2–1000 equivalent submissions (default 100). Run over IPv6
on a host that supports it with:

```sh
go run ./examples/demo -http -http-listen '[::1]:0' > http-ipv6-report.json
```

The listener still accepts only numeric loopback addresses. A missing IPv6 loopback
causes this explicit command to fail; it does not silently switch to IPv4. The CI
IPv6 serving test skips only when its listener cannot be opened. The existing
storage/CPU measurement flags apply to the default embedded demo, not -http mode.
The original default demo and its measurement artifacts remain unchanged.

## Scenarios and boundaries

The HTTP adapter exercises the same operation scenarios as the embedded demo,
rather than maintaining a separate definition of success. It is private demo
scaffolding, not a separately supported SDK. HTTP_PROXY and other environment proxy
settings are bypassed for its loopback client.

The first scenario sends 100 equivalent submissions over HTTP, verifies one creation
and 99 joins, abandons one wait with a client context deadline, checks that work is
still queued, then starts a worker and receives the shared result for the other
99 waiters. HTTP attempt inspection confirms one successful execution attempt.

The crash worker acknowledges its committed claim, then the parent calls
Process.Kill. No graceful handler/Store shutdown is substituted for the crash.
A replacement recovers the same operation after the one-second lease expires.
HTTP result and attempt inspection must show two distinct attempts, increasing
ownership tokens and one stored successful result. Rejecting an obsolete result
commit remains a direct Go worker API assertion: worker completion has no HTTP
endpoint. Tokens fence durable results; the demo does not claim exactly-once
external side effects.

Cancellation is sent over HTTP to running work. The handler acknowledges context
cancellation on renewal, while a separate registered handler subsequently completes
another operation. Retry first returns a failed terminal result, then an HTTP
operator action grants one extra attempt. Replaying the action must not apply it
a second time; the original submission policy and monotonic attempt/token history
remain intact. Two-event HTTP pages reconstruct all six retry scenario events.

A separate queued operation tests an actual HTTP504 with timeout=20ms, while the
client itself still has time to receive the response. A following GET confirms
that timeout did not execute/cancel work; an explicit HTTP Cancel ends that demo
operation. This distinguishes server wait timeout from client-side abandonment.

Retention is deliberately an explicit Go administrative call, not a new HTTP
mutation endpoint. Batches of two prune four succeeded results, then HTTP GET
observes each result_pruned marker with succeeded state and original input intact.
An HTTP retry-action replay and a duplicate submission join remain read-only after
pruning. Usage read over HTTP verifies retained identity/attempt/action counts,
input bytes and the four new prune events. Neither pruning nor inspection may
start work. Finally, the demo fetches the read-only status page and shuts down the
server before closing SQLite/removing its files.

## Captured runs

The committed ordinary-build reports are
[IPv4](measurements/2026-10-17-http-demo-ipv4.json) and
[IPv6](measurements/2026-10-17-http-demo-ipv6.json). Both were captured on Linux amd64,
Go 1.26.1, from parent 6ccaeef with modified=true, with the source changes shipped
in the same milestone as these reports. This is correctness evidence, not a
throughput benchmark, controlled IPv4/IPv6 comparison or recovery SLA.

Both runs observed 100 submissions, one creation, 99 joins, 99 successful waiters
and one independently timed-out caller. The killed process reported signal:killed;
recovery preserved identity and increased the ownership token from 1 to 2.
Kill-to-observed-success was approximately 1.102s in each run, including the
one-second lease, SQLite access, replacement startup and 100ms Wait polling.
This value is specific to the demonstration settings and host conditions.

| Record/payload | Before explicit pruning | After |
| --- | ---: | ---: |
| Operation identities | 6 | 6 |
| Active operations | 0 | 0 |
| Attempts | 7 | 7 |
| Retry-action records | 1 | 1 |
| Input bytes | 78 | 78 |
| Result bytes | 52 | 0 |
| Pruned results | 0 | 4 |
| Events | 22 | 26 |

The tracked API responses were six HTTP201, 223 HTTP200 and one HTTP504 per run.
The disconnected caller has no received status and the separate page request is
not part of this API counter. No physical database-shrink claim is made.

## Verification

CI runs the 100-request HTTP scenario using race-instrumented worker subprocesses,
including the actual kill and retention checks. The earlier embedded demo remains
race-tested too. HTTP integration tests separately verify IPv6 serving where
available and real stalled-header/stalled-body connections against the production
read deadlines. They verify that stalled clients create no operation/event records
and an independent client still works. Existing malformed JSON/body-size/browser
boundary checks and active-wait shutdown tests remain enabled.

The demo fetches HTML but does not render it visually. Browser rendering remains
unverified in this environment because no browser executable is installed. Remote
hosting/authentication, arbitrary jobs, release licensing and public capacity
claims are outside this demo.
