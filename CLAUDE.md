# Notes for Claude

This file says where the next session starts and what comes next. What comes
next is decided with the owner before it goes here, and work starts only when
the owner says go.

## Releases

Releases are automated. To release, raise `Version` in `taskfused.go` and push to
`main`; never ask the owner to publish one. [RELEASING.md](RELEASING.md) has the
procedure and the checks to run afterwards.

## Where things stand

v0.1.0 is the first release (October 2026): Apache 2.0, CI on Linux and macOS
plus a Windows smoke check,
and automated releases with smoke-tested binaries for Linux, macOS and Windows.
The code is a single-host Go/SQLite prototype: durable explicit-identity
submissions, duplicate joining, leased at-least-once attempts, bounded retries,
crash recovery, shared waiting, cooperative cancellation, operator retry, event
pages, admission limits, result pruning, a CLI and a loopback HTTP API with a
read-only status page. Read [docs/CONTRACT.md](docs/CONTRACT.md) before changing
behavior; [docs/READINESS.md](docs/READINESS.md) lists what is checked and what
isn't, and [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md) the version policy.

Out of scope unless the owner decides otherwise: multi-host coordination, remote
authentication, workflow graphs, arbitrary command running, automatic fusion.

## Test results

`sh scripts/record-tests.sh` runs every test, the race detector, both demos and
the smoke check on static binaries, and writes them to `test/results/`. Every
number on the site and in the README comes from those files; record again after
any change that could move them. It needs Go, a C compiler, Python 3 and musl-gcc.
In a cloud session, fetch modules with `GOPROXY=direct`.

## Next

Round 2, approved: the TaskFused.com site, in `photozeitgeist/taskfused.com`, once
the owner has created that repo and given push access. It follows the low
maintenance workflow: the Hugo Launch template copied unchanged, content and
config values only; pages for about, contact (info@taskfused.com), quick start,
download and reference; posts for the launch, a plain-language explainer of the
category, how it was tested and the name; images drawn into static/images; every
number from `test/results/`.

After that, nothing is decided.
