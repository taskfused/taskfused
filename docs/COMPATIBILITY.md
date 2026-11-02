# Compatibility

## Systems

Each release ships binaries for Linux x86-64 and ARM64 (static, so they need no
particular glibc), macOS Apple silicon and Intel, and Windows x86-64. Since 0.2
they are pure Go (`CGO_ENABLED=0`, with modernc.org/sqlite as the SQLite driver).
The release workflow builds them with the Go 1.26.x toolchain and runs `scripts/smoke.py` on each one on its own kind of machine: both
Linux architectures, Apple silicon and Windows. The macOS Intel build is checked
for its architecture but not run, because the runners are Apple silicon. The
smoke check covers submission and joining, a real worker run, waiting, limits and
pruning, the loopback HTTP API and status page, a clean exit on interrupt, and the
HTTP recovery demo, which kills a real worker process and recovers its lease.

The test workflow runs the full suite on Linux, macOS and Windows, the race
detector on Linux, and the smoke check on Windows binaries on a Windows runner, on
every push and pull request. The macOS and Windows binaries call the operating
system's own libraries and are not signed; macOS may ask you to allow them the
first time.

Building from source needs Go 1.26 or later (as go.mod declares) and no C
compiler. CI builds with Go 1.26.x.

## Versions

Releases are tagged vX.Y.Z from `Version` in `taskfused.go`, and the Go module is
versioned by the same tags. Before 1.0, a minor version (0.X.0) may change the Go
API, the CLI or the HTTP wire format; patch versions (0.X.Y) don't. Release notes
and the commit history say what changed. HTTP input and result bytes use base64,
`retry_delay` uses integer nanoseconds, and a namespace is not authentication.

## Database schema

`SchemaVersion` and every binary's `version` output report SQLite schema v6. Open
migrates older TaskFused schemas (v1 to v5) transactionally and refuses newer or
unrecognized databases. Stop old processes before an upgrade; don't run workers of
different versions on one database. Older binaries can't open a schema v6 database,
and there is no automatic downgrade, so keep a SQLite-aware backup of valuable data
before upgrading.

## Deployment scope

One host, with local SQLite in WAL mode and FULL synchronization, registered Go
handlers, explicit identities and destination-level idempotency for external
effects. Only literal loopback HTTP binding is supported; no proxy, tunnel or
remote-authentication deployment is qualified. Storage quotas, retention and error
boundaries are as documented in STORAGE.md, HTTP.md and FAULTS.md. Physical
power-loss and VFS-level durability, and browser visual rendering of the status
page, have not been qualified.
