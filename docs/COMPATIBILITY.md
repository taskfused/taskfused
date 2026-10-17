# Compatibility

## Systems

Each release ships binaries for Linux x86-64 and ARM64 (statically linked against
musl libc, so they need no particular glibc), macOS Apple silicon and Intel, and
Windows x86-64. The release workflow builds them with the Go 1.26.x toolchain and
CGO, and runs `scripts/smoke.py` on each one on its own kind of machine: both
Linux architectures, Apple silicon and Windows. The macOS Intel build is checked
for its architecture but not run, because the runners are Apple silicon. The
smoke check covers submission and joining, a real worker run, waiting, limits and
pruning, the loopback HTTP API and status page, a clean exit on interrupt, and the
HTTP recovery demo, which kills a real worker process and recovers its lease.

The test workflow runs the full suite on Linux and macOS, and the race detector on
Linux. Windows is covered by the release smoke check, not by the full test suite.
The macOS and Windows binaries link to the operating system's own C runtime and are
not signed; macOS may ask you to allow them the first time.

Building from source needs Go 1.24 or later (as go.mod declares), CGO and a C
compiler. CI builds with Go 1.26.x; the 1.24 minimum is declared, not separately
tested.

## Versions

Releases are tagged vX.Y.Z from `Version` in `taskfused.go`, and the Go module is
versioned by the same tags. Before 1.0, a minor version (0.X.0) may change the Go
API, the CLI or the HTTP wire format; patch versions (0.X.Y) don't. Release notes
and the commit history say what changed. HTTP input and result bytes use base64,
`retry_delay` uses integer nanoseconds, and a namespace is not authentication.

## Database schema

`SchemaVersion` and every binary's `version` output report SQLite schema v5. Open
migrates older TaskFused schemas (v1 to v4) transactionally and refuses newer or
unrecognized databases. Stop old processes before an upgrade; don't run workers of
different versions on one database. Older binaries can't open a schema v5 database,
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
