# TaskFused development measurements, October 17, 2026

This is the first measured baseline, not a throughput claim or release qualification.
The [raw JSON report](measurements/2026-10-17-linux-amd64.json) contains every waiting
sample, file snapshot, latency summary and functional check. The harness source is
committed alongside this report. Build metadata records parent revision
`7d0ce936bbc46a19c478f3de8cfd3d9ad5e20b79` with `vcs.modified=true`, because
these measurements were taken from this milestone's working tree before commit.

## Reproduce

```sh
go build -o /tmp/taskfused-demo ./examples/demo
/tmp/taskfused-demo > report.json
```

The recorded binary was built without race instrumentation. Race tests validate
correctness separately and are unsuitable for comparison with this timing run.
The demo fails if its functional checks fail, sends progress to stderr, writes its
JSON report to stdout, and removes its temporary databases after child processes
have stopped. `go run ./examples/demo` also works; child processes re-execute the
current binary rather than requiring a separately installed worker executable.

Defaults are 100 equivalent submissions, 1000 distinct storage/latency operations,
128-byte input and 128-byte result, and three fresh-process samples each for zero,
one and 100 waiters at a requested one-second duration. Options are `-requests`,
`-operations`, `-payload-bytes`, `-samples` and `-wait-duration`. Input workload is
bounded to 64 MiB, independently of the per-option bounds shown by `-h`.

## Environment and method

The run used go1.26.1, linux/amd64, CGO enabled, SQLite
3.50.4, 9 visible logical CPUs and GOMAXPROCS
9. The observed cgroup `cpu.max` was
`800000 100000`. Physical hardware and the filesystem
behind `os.MkdirTemp` were not characterized. Host activity was not controlled.
SQLite used the Store's WAL/FULL settings. This does not independently prove the
underlying filesystem/hardware honors durability synchronization.

Storage/latency operations were submitted sequentially under unique keys, then run
sequentially with a trivial echo handler. The shared-work scenario is a correctness
demonstration, not this latency workload. Percentiles use nearest rank. RunOnce
includes claim/attempt/event work, handler invocation, renewal-goroutine setup and
result/event commit; the handler performs no network work. There is no concurrency,
queue-saturation, remote storage or realistic application-handler benchmark here.

## Durable operation latency

| Measured call | Samples | p50 ms | p95 ms | p99 ms | Max ms | Mean ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Durable Submit | 1000 | 1.007 | 2.401 | 3.317 | 147.982 | 1.316 |
| Claim + trivial handler + Complete | 1000 | 2.131 | 4.843 | 8.574 | 15.596 | 2.511 |

The submission phase took 1316.5 ms and the execution phase
2511.6 ms. The maximum submission latency was
148.0 ms; the report retains this outlier rather than hiding
it behind percentiles. The run does not identify its cause and does not establish
a latency SLA or production capacity.

## Database growth

| Snapshot | Main bytes | WAL bytes | SHM bytes | Total logical bytes |
| --- | ---: | ---: | ---: | ---: |
| Empty, closed | 81,920 | 0 | 0 | 81,920 |
| Queued, live | 688,128 | 4,165,352 | 32,768 | 4,886,248 |
| Queued, closed | 692,224 | 0 | 0 | 692,224 |
| Completed, live | 1,470,464 | 4,165,352 | 32,768 | 5,668,584 |
| Completed, closed | 1,474,560 | 0 | 0 | 1,474,560 |

The final database held 1000 operations,
1000 attempts and 3000 events. Completed
closed growth above the empty baseline was 1,392,640 bytes
(1.328 MiB), an amortized
1392.64 bytes per completed
operation for this exact one-attempt, small-payload workload. This is not a fixed
record cost: page slack, larger input/results, failures and retry/action history
change it. No VACUUM or retention cleanup ran.

Snapshots report logical file lengths rather than allocated blocks. Closing the
last connection checkpoints/removes live sidecars in this run; that is why closed
and live totals differ. A running deployment needs space for WAL/SHM as well as the
main file. Copying only the live main file is not a valid backup procedure.

## Waiting overhead

Each waiting sample used a fresh child process and one pending operation in one
Store. No worker completed that operation. Wait performed its immediate read and
100ms polling until the sample deadline; zero waiters provided an idle baseline.
All nine samples left the operation queued, with zero attempts, one event and no
file growth. SQL read counts were not instrumented.

Each table entry is a component-wise median of three samples; it does not describe
one selected best run. CPU is process user plus system CPU, including SQLite/C
execution and waiter setup/teardown. Go allocations measure total allocation churn,
not retained/peak memory, and exclude SQLite/native allocations. CPU sampling is
implemented for Linux/macOS; other platforms explicitly report it unavailable.

| Waiters | Samples | Wall ms | Process CPU ms | CPU, one-core equivalent | Go allocated bytes | File growth bytes |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 0 | 3 | 1000.197 | 0.193 | 0.02% | 768 | 0 |
| 1 | 3 | 1001.418 | 6.938 | 0.69% | 41,632 | 0 |
| 100 | 3 | 1001.967 | 84.145 | 8.41% | 3,890,088 | 0 |

These short samples are sensitive to scheduling and runtime noise. They do show
that the current per-caller polling path has measurable CPU/allocation cost at 100
waiters. A later optimization can share a local poller per operation, but must keep
cross-process visibility, independent cancellation, independent result bytes and
correct behavior after explicit operator retry. The data does not justify linear
extrapolation to thousands of waiters.

## Functional checks in the same harness

The 100 equivalent submissions created one operation and one successful attempt.
One wait timed out without changing work; the other 99 received the shared result.
Their median elapsed time was 303.8 ms,
including process launch, a deliberate 200ms handler delay and Wait polling. It is
not a normal handler-execution latency measurement.

The first crash worker confirmed its claim and was killed through Process.Kill
(observed exit: `signal: killed`). A replacement recovered the same
operation with token 2 after token 1,
and a stale result commit was rejected. Kill-to-observed-success elapsed
1100.9 ms with a one-second demonstration
lease. Cancellation was observed cooperatively after
333.9 ms, consistent with this
worker's one-third-lease heartbeat interval plus scheduling/database effects;
subsequent work still succeeded. Neither value is a promised recovery/cancellation
bound for other configurations or machines.

The operator-retry scenario recorded an initial failure, one additional grant and
one replay. Attempt 2 succeeded with the original max-attempt policy unchanged,
and two-row event pages returned the complete six-event history. The harness never
runs arbitrary submitted commands and uses only built-in Go handlers.
