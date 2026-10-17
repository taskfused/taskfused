# Candidate compatibility scope

The current packaging target is native Linux amd64, GOAMD64=v1, built with CGO and
a stable Go 1.26.x toolchain. Go 1.26.1/Linux amd64 is the observed local validation
environment. CI runs Ubuntu with Go 1.26.x and builds/verifies the native candidate.
A C compiler is needed to build; packaged executables do not need Go or a compiler
to run, but require a compatible host C runtime. MANIFEST.json records the build
host/runtime/compiler. No minimum glibc baseline, static-link guarantee or arbitrary
Linux-distribution portability is established by these tests.

go.mod still declares Go 1.24. That source minimum has not been independently
qualified by the candidate gate. macOS/Windows/arm64 and other toolchains can be
separate future targets after actual builds/tests; no binaries for them are promised.

Candidate IDs bind behavior to one Git commit. They are not public semver releases.
Library APIs and CLI/HTTP wire behavior are documented for that commit. Before a
public v0 tag, choose and document a version policy; no long-term API compatibility
promise is implied by a private candidate. HTTP input/result bytes use base64,
retry_delay uses integer nanoseconds and namespace is not authentication.

SchemaVersion and executable metadata currently report SQLite schema v5. Open
migrates supported older TaskFused schemas transactionally and refuses newer or
unrecognized databases. Stop old processes before upgrade; do not run mixed-schema
workers with already-open connections. Older binaries cannot open schema v5.
Use SQLite-aware backups for valuable data; there is no automatic downgrade or
backup/restore service. Candidate verification explicitly gates on schema v5 and
must be reviewed when a future schema changes.

Deploy one host with local SQLite WAL/FULL, registered Go handlers, explicit
identities and destination-level idempotency for external effects. Only literal
loopback HTTP binding is supported; no proxy/tunnel/remote-auth deployment is
qualified. Storage quotas, retention and error boundaries remain as documented in
STORAGE.md, HTTP.md and FAULTS.md. Physical power-loss/VFS durability and browser
visual rendering are not newly qualified by successful packaging.
