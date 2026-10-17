# Resume TaskFused

Updated: 2026-10-17.

## Read first

Read README.md, docs/CONTRACT.md, and docs/DEVELOPMENT_PLAN.md. Repository:
https://github.com/taskfused/taskfused. Intended site: TaskFused.com; no website
repository or hosting target has been provided. Work is authorized on this code
repository. Do not infer a release license; NOTICE reserves rights until selected.

## Current implementation

The repository began empty. Milestone 1 adds a Go library backed by local SQLite,
atomic operation/submission-event acceptance, explicit input and identity rules,
opt-in duplicate joining, conflicting-input rejection, namespace-qualified lookup,
and a submit/get CLI. CI checks formatting, vet, race tests, and build on Linux.
The SQLite driver requires CGO and a C compiler.

Only queued operations exist. No handlers execute. No leases, attempts, retries,
completion results, HTTP service, or UI exist. Do not claim worker recovery just
because submission persistence has been tested.

## Validation

Passed locally with Go 1.26.1 on Linux: go test ./..., go vet ./...,
and go test -race -count=1 ./.... The CLI build and smoke check also passed (submit, join, persistent get,
and conflicting-input rejection).
Tests cover independent connections and processes, abrupt process exit without
SQLite close, input conflicts, identity isolation, transaction rollback on event
failure, invalid requests, canceled contexts, foreign schemas, and CLI behavior.

## Exact next step

Implement milestone 2 after settling the execution contract. Start with terminal
state joining and retry/cancellation semantics in docs/CONTRACT.md; introduce a
schema migration from v1 rather than editing the initial schema in place. Then add
registered handlers, transactional claims, attempt IDs and increasing ownership
tokens. Lease expiration itself must invalidate renewal and result commit. Add
fake-clock ownership tests and subprocess recovery tests before adding HTTP or UI.

## Session discipline

Keep changes scoped to a coherent milestone. Run gofmt, go vet, go test -race, and
go build. Commit and push verified work, then record exact remaining behavior and
resume steps here. Do not report a two-to-three-hour session unless that amount of
work actually elapsed. Never promise exactly-once external side effects.
