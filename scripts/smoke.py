#!/usr/bin/env python3
# Copyright TaskFused.com 2026
# SPDX-License-Identifier: Apache-2.0
"""Checks a directory of built TaskFused binaries on the machine that runs them.

    python3 scripts/smoke.py BIN_DIR [EXPECTED_VERSION]

BIN_DIR holds taskfused, taskfused-worker and taskfused-demo (with .exe on
Windows). Each must report the expected version (taskfused.Version from the
checkout unless given) and a CGO build. Then, in a private temporary
directory, it submits and joins one operation, runs it with the real worker,
waits for the echoed result, sets admission limits, prunes the result,
checks the limit rejects a new submission, serves the loopback HTTP API and
status page, stops the worker and the server with an interrupt (CTRL_BREAK
on Windows) and expects a clean exit, and finally runs the packaged HTTP
recovery demo with ten shared requests, which kills a real worker process and
recovers its lease. The release workflow runs it on every binary it ships,
on the machine type that binary is for; any failure stops the release.
"""
import base64
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
WINDOWS = os.name == "nt"
NAMES = ("taskfused", "taskfused-worker", "taskfused-demo")


class Failure(Exception):
    pass


def require(condition, message):
    if not condition:
        raise Failure(message)


def run(args, timeout=180):
    result = subprocess.run([str(a) for a in args], capture_output=True, text=True, timeout=timeout)
    require(result.returncode == 0, f"{Path(str(args[0])).name} {' '.join(str(a) for a in args[1:])} exited {result.returncode}:\n{result.stderr[-4000:]}")
    return result.stdout.strip()


def start(args, log):
    flags = subprocess.CREATE_NEW_PROCESS_GROUP if WINDOWS else 0
    return subprocess.Popen([str(a) for a in args], stdout=log, stderr=log, creationflags=flags)


def stop(process, name):
    """An interrupt, then a clean exit within eight seconds."""
    if process.poll() is None:
        process.send_signal(signal.CTRL_BREAK_EVENT if WINDOWS else signal.SIGINT)
        try:
            process.wait(timeout=8)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
            raise Failure(f"{name} did not stop after an interrupt")
    require(process.returncode == 0, f"{name} exited {process.returncode} after an interrupt")


def source_version():
    text = (ROOT / "taskfused.go").read_text(encoding="utf-8")
    found = re.findall(r'^const Version = "(.*)"$', text, re.M)
    require(len(found) == 1, "taskfused.go has no single Version constant")
    return found[0]


def smoke(bin_dir, version):
    suffix = ".exe" if WINDOWS else ""
    binaries = {name: Path(bin_dir).resolve() / (name + suffix) for name in NAMES}
    for name, path in binaries.items():
        require(path.is_file(), f"missing {path}")
        info = json.loads(run([path, "version" if name == "taskfused" else "-version"]))
        require(info["version"] == version, f"{name} reports version {info['version']}, expected {version}")
        require(info["cgo"] and info["schema"] == 5, f"{name} is not a CGO schema-5 build: {info}")
        print(f"{name}: {info['version']} {info['os']}/{info['arch']} revision {info['revision'][:12]}", flush=True)
    cli = binaries["taskfused"]
    with tempfile.TemporaryDirectory(prefix="taskfused-smoke-") as temporary:
        directory = Path(temporary)
        db = directory / "smoke.db"

        def command(verb, *args):
            return json.loads(run([cli, verb, "-db", db, "-namespace", "demo", *args]))

        args = ("-handler", "echo", "-version", "v1", "-key", "smoke", "-input", "hello", "-join")
        first = command("submit", *args)
        joined = command("submit", *args)
        op_id = first["operation"]["id"]
        require(not first["joined"] and joined["joined"] and joined["operation"]["id"] == op_id, "the second submission did not join the first")
        with (directory / "worker.log").open("wb") as log:
            worker = start([binaries["taskfused-worker"], "-db", db, "-delay", "10ms"], log)
            try:
                outcome = command("wait", "-id", op_id, "-timeout", "15s")
                require(outcome["state"] == "succeeded" and base64.b64decode(outcome["result"]) == b"hello", f"the worker did not echo the input: {outcome}")
                require(len(command("attempts", "-id", op_id)) == 1, "the operation ran more than once")
            finally:
                stop(worker, "taskfused-worker")
        print("submit, join, worker and wait: passed", flush=True)
        command("set-limits", "-max-operations", "1", "-max-active", "1")
        command("prune-results", "-before", "9999-12-31T23:59:59Z", "-limit", "1")
        outcome = command("get", "-id", op_id)
        require(outcome["state"] == "succeeded" and outcome["result_pruned"], "the result was not marked pruned")
        rejected = subprocess.run([str(cli), "submit", "-db", str(db), "-namespace", "demo", "-handler", "echo", "-version", "v1", "-key", "over-limit"], capture_output=True, timeout=30)
        require(rejected.returncode != 0 and command("usage")["operations"] == 1, "the admission limit did not reject a new operation")
        print("limits and pruning: passed", flush=True)
        log_path = directory / "server.log"
        with log_path.open("wb") as log:
            server = start([cli, "serve", "-db", db, "-listen", "127.0.0.1:0"], log)
            try:
                deadline = time.monotonic() + 15
                address = None
                while time.monotonic() < deadline and address is None:
                    match = re.search(r"http://127\.0\.0\.1:[0-9]+", log_path.read_text(errors="replace"))
                    if match:
                        address = match.group()
                    else:
                        require(server.poll() is None, "the server exited before it started")
                        time.sleep(0.05)
                require(address is not None, "the server did not start")
                opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
                with opener.open(address + "/api/namespaces/demo/usage", timeout=10) as response:
                    require(json.load(response)["pruned_results"] == 1, "HTTP usage is wrong")
                with opener.open(address + "/?namespace=demo&id=" + op_id, timeout=10) as response:
                    require("Pruned" in response.read().decode(), "the status page does not show the pruned result")
            finally:
                stop(server, "taskfused serve")
        print("HTTP API and status page: passed", flush=True)
    report = json.loads(run([binaries["taskfused-demo"], "-http", "-requests", "10"], timeout=300))
    over_http = report["functional_over_http"]
    require(over_http["shared"]["joined"] == 9 and over_http["shared"]["successful_waiters"] == 9, "the demo's shared operation failed")
    require(over_http["crash"]["stale_commit_rejected"] and report["shutdown_completed"], "the demo's worker kill and recovery failed")
    require(report["retention"]["retry_replay_preserved"] and report["retention"]["pruned_results"] == 4, "the demo's retry replay or pruning failed")
    print("HTTP recovery demo with a killed worker: passed", flush=True)


def main():
    if len(sys.argv) not in (2, 3):
        sys.exit("usage: python3 scripts/smoke.py BIN_DIR [EXPECTED_VERSION]")
    try:
        smoke(sys.argv[1], sys.argv[2] if len(sys.argv) == 3 else source_version())
    except (Failure, subprocess.TimeoutExpired, KeyError, ValueError, OSError) as e:
        print(f"smoke check failed: {e}", file=sys.stderr)
        sys.exit(1)
    print("smoke check passed")


if __name__ == "__main__":
    main()
