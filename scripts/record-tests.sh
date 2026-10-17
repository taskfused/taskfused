#!/bin/sh
# Copyright TaskFused.com 2026
# SPDX-License-Identifier: Apache-2.0
#
# Runs every test, the race detector, both self-checking demos and the smoke
# check on static release-style binaries, and writes the output to
# test/results/ with a note of the machine they ran on. The numbers in the
# README and on taskfused.com come from these files.
#
#   sh scripts/record-tests.sh
#
# Needs Go, a C compiler and Python 3; the smoke step also needs musl-gcc
# (Debian/Ubuntu: musl-tools) and is skipped with a note when it is missing.
set -eu
cd "$(dirname "$0")/.."
out=test/results
mkdir -p "$out"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

header() {
	echo "# $1"
	echo "# recorded $(date -u '+%Y-%m-%d %H:%M UTC') at $(git rev-parse --short HEAD)$(test -z "$(git status --porcelain)" || echo ' plus uncommitted changes'), TaskFused $(sed -n 's/^const Version = "\(.*\)"$/\1/p' taskfused.go), $(go version | cut -d' ' -f3-)"
	cpu=$(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2- | sed 's/^ //' || true)
	test -n "$cpu" || cpu=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo 'unknown CPU')
	echo "# $(uname -sr), $cpu, $(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo '?') CPUs"
	echo "# command: $2"
	echo
}

go vet ./...

cmd="go test -count=1 -v ./..."
{ header "Every test, verbose" "$cmd"; $cmd 2>&1; } > "$out/tests.txt"

cmd="go test -race -count=1 ./..."
{ header "Every test with the race detector" "$cmd"; $cmd 2>&1; } > "$out/race.txt"

cmd="python3 -B -m unittest discover -s scripts -p *_test.py -v"
{ header "The release plan's cases" "$cmd"; $cmd 2>&1; } > "$out/release-plan.txt"

# The demos print progress on stderr and a JSON report on stdout; each fails
# on any failed correctness check. The reports carry their own environment and
# build details, and are the raw numbers.
go build -o "$work/taskfused-demo" ./examples/demo
"$work/taskfused-demo" > "$out/demo.json" 2>"$work/demo.log" || { cat "$work/demo.log" >&2; exit 1; }
"$work/taskfused-demo" -http > "$out/demo-http.json" 2>"$work/http.log" || { cat "$work/http.log" >&2; exit 1; }

if command -v musl-gcc >/dev/null 2>&1; then
	mkdir "$work/bin"
	for p in cmd/taskfused:taskfused examples/worker:taskfused-worker examples/demo:taskfused-demo; do
		CC=musl-gcc CGO_ENABLED=1 go build -trimpath -tags 'sqlite_omit_load_extension netgo osusergo' \
			-ldflags '-s -w -linkmode external -extldflags "-static"' -o "$work/bin/${p#*:}" "./${p%:*}"
	done
	cmd="python3 scripts/smoke.py BIN"
	{ header "The release smoke check on static Linux binaries built as the release workflow builds them" "$cmd"; python3 scripts/smoke.py "$work/bin" 2>&1; } > "$out/smoke.txt"
else
	echo "musl-gcc not found: smoke.txt not recorded" >&2
fi

grep -E '^(ok|FAIL|--- FAIL)' "$out/tests.txt" "$out/race.txt" || true
tail -n 1 "$out/release-plan.txt" "$out/smoke.txt" 2>/dev/null || true
echo "results written to $out/"
