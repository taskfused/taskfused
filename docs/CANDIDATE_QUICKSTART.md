# TaskFused local candidate quickstart

This is an all-rights-reserved local evaluation candidate, not a public release.
Use a native Linux amd64 host compatible with the build host's C runtime. Read
MANIFEST.json and NOTICE first. The binary archive includes no Go/C compiler and
no system C runtime; those runtimes must already be available on the target host.
Use the unpacked candidate directory as your current directory.

Inspect provenance without opening a database:

```sh
./bin/taskfused version
./bin/taskfused-worker -version
./bin/taskfused-demo -version
```

Create/join work in one terminal:

```sh
./bin/taskfused submit -db demo.db -namespace demo -handler echo -version v1 \
  -key example-1 -input hello -join
```

Run the packaged example worker in another terminal:

```sh
./bin/taskfused-worker -db demo.db -delay 100ms
```

Copy the returned operation ID, then inspect it:

```sh
./bin/taskfused wait -db demo.db -namespace demo -id op_REPLACE -timeout 10s
./bin/taskfused attempts -db demo.db -namespace demo -id op_REPLACE
./bin/taskfused usage -db demo.db -namespace demo
```

The example registers only demo/echo/v1 and echoes bytes. It does not run submitted
commands. JSON payload bytes use base64. Stop the worker with Ctrl-C. External
side effects in your own Go handlers require destination-level idempotency.

Start the read-only status page and API, with worker execution remaining separate:

```sh
./bin/taskfused serve -db demo.db -listen 127.0.0.1:8080
```

Open http://127.0.0.1:8080/ and enter namespace demo and optionally the operation ID.
This local service is not authenticated; do not expose it remotely. Ctrl-C drains
requests and stops the server without canceling durable work.

Run the complete packaged HTTP recovery demonstration in private temporary files:

```sh
./bin/taskfused-demo -http > http-report.json
```

It starts its own server and registered worker subprocesses, deliberately kills a
worker, checks recovery and cleans up after itself. It needs no installed Go tools
or separately installed TaskFused binary. See docs/HTTP_DEMO.md for exact boundaries.
Explicit storage limits/pruning are documented in docs/STORAGE.md.

SHA256SUMS covers every regular file except itself; the outer .sha256 file covers
the compressed archive. Checksums detect changes relative to the supplied checksum
but do not authenticate who supplied it. Repository tools/candidate.py can verify
and execute the smoke checks for a trusted candidate; Python 3.10+ is required for
that verifier, not for running the three executables.
