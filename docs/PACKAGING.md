# Local evaluation candidate packaging

This procedure builds and checks a private/local candidate without selecting a
license, creating a Git tag, uploading artifacts or publishing a release. NOTICE
continues to reserve all rights. Public source/binary distribution remains a
separate decision. Candidate IDs are candidate-<12-character-commit>, not a release
version or a promise of pre-1.0 API stability.

## Build a committed clean checkout

Requires native Linux amd64, Python 3.10+, Git, a stable Go 1.26.x toolchain and a
working C compiler. The current qualified environment uses Go 1.26.1. This is a
single-target recipe, not a cross-compilation matrix. Module sources must be in the
Go module cache or obtainable by the build environment.

```sh
go vet ./...
go test -race -count=1 ./...
go build ./...
python3 -B -m unittest discover -s scripts -p '*_test.py'
python3 scripts/candidate.py build --output /tmp/taskfused-candidate --rebuild
```

Use --go /absolute/path/to/go when Go is not on PATH. Output must be a new directory
outside the checkout; existing directories are refused. Build rejects tracked or
untracked working changes. Ignored files are not inputs: source is exported from
the exact HEAD commit to a private temporary tree before compilation. Module
checksums are verified. A changed/replaced dependency graph stops packaging until
its notices are reviewed.

The builder forces Linux/amd64, CGO=1, GOAMD64=v1, the local toolchain and empty
GOFLAGS/GOEXPERIMENT. It records compiler/CGO flags, Go/module checksums, source
commit/tree/epoch and host C-runtime information. It uses -trimpath, disables
automatic VCS stamping, removes the Go build ID/debug symbols, and stamps the
explicit candidate ID and full revision into all three executables.

--rebuild builds again using an independent fresh Go build cache and compares all
three executable hashes. This checks same-host/toolchain repeatability, not bit
identity across compiler/libc/OS versions. Native dependency/link requirements are
not made portable by GOAMD64=v1. These are not promised static executables.

## Contents and verification

The output directory contains one tar.gz archive, its outer .sha256 checksum and
verification.json. The archive contains bin/taskfused, bin/taskfused-worker,
bin/taskfused-demo, MANIFEST.json, internal SHA256SUMS, NOTICE, README, QUICKSTART,
documentation, verifier source and copied Go/go-sqlite3 dependency license notices.
SQLite's bundled copyright disclaimer is included in the third-party notes.
No database, source checkout, compiler or system C runtime is bundled.

Archive entry order, ownership, permissions and timestamps are normalized. Gzip
has a fixed timestamp and no embedded filename. The source commit epoch supplies
tar timestamps. Changing source provenance or the rebuild-check metadata changes
the manifest/archive even when executable behavior is unchanged.

After building, verification extracts the compressed archive into a fresh temporary
directory. It checks the outer hash, internal file checksums, exact file membership,
candidate naming/manifest compatibility and executable metadata. The extractor
rejects path traversal, duplicate entries, links/special files and oversized input.
A checksum does not authenticate its provider. Verify only trusted candidate code:
the smoke phase deliberately executes its binaries.

```sh
python3 scripts/candidate.py verify /tmp/taskfused-candidate/taskfused-candidate-COMMIT-linux-amd64.tar.gz \
  --checksum /tmp/taskfused-candidate/taskfused-candidate-COMMIT-linux-amd64.tar.gz.sha256
```

Replace COMMIT with the actual 12-character identifier. Verification creates private
temporary databases and runs the extracted CLI, example worker, HTTP server and
demo. It verifies exact versions/revisions, CGO/schema/target metadata, submit/join,
one echo execution/result, attempt inspection, admission rejection, result pruning,
HTTP usage/status HTML, cooperative process shutdown and the packaged HTTP demo
with 10 shared requests and an actual worker kill/recovery. No installed Go compiler
or separately built executable is used during this smoke phase. Visual browser
rendering is not part of this check.

The successful result is moved into the requested output directory only after
archive verification and smoke testing. Temporary build/verification files are
cleaned up on failure. verification.json reports checks and published=false; it is
not a signature or an assertion that the complete source test suite ran. Run the
source checks above first. CI does so, then runs the packaging gate with --rebuild.
It does not upload the candidate or create tags/releases.

## Compatibility and release boundary

See COMPATIBILITY.md for the current target and schema policy. Runtime metadata is
available via taskfused version, taskfused-worker -version and taskfused-demo -version
without opening a database or starting workers. Ordinary source builds identify as
development and use available Go VCS metadata; packaged candidates stamp committed
provenance and suppress working-tree metadata.

Before a public prototype release, select the TaskFused license, review dependency
notices, choose a version/tag policy and supported targets, and authorize publication.
A public release workflow is intentionally absent. This local gate can be used to
prepare reviewable artifacts without implying any of those decisions.
