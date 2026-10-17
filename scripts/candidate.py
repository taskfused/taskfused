#!/usr/bin/env python3
# Copyright 2026 TaskFused. All rights reserved.
"""Build/verify local evaluation candidates. Never tags, uploads or publishes."""
import argparse
import base64
import gzip
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import shlex
import shutil
import signal
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request

BINARIES = {"taskfused": "./cmd/taskfused", "taskfused-worker": "./examples/worker", "taskfused-demo": "./examples/demo"}
MAX_EXPANDED = 128 * 1024 * 1024


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(path):
    result = hashlib.sha256()
    with Path(path).open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            result.update(block)
    return result.hexdigest()


def run(args, cwd=None, env=None, timeout=180):
    result = subprocess.run([str(x) for x in args], cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)
    require(result.returncode == 0, f"command failed: {args[0]} {args[1:]}\n{result.stderr[-4000:]}")
    return result.stdout.strip()


def native_host():
    require(platform.system() == "Linux" and platform.machine() in ("x86_64", "amd64"), "candidate binaries currently require a native Linux amd64 host")


def extract_checked(archive, destination, prefix=None):
    """Extract only bounded regular files/directories, never links or traversal."""
    seen, expanded = set(), 0
    for member in archive:
        name = member.name.rstrip("/")
        parts = PurePosixPath(name).parts
        require(name and not name.startswith("/") and "\\" not in name and "//" not in name, "unsafe archive path")
        require(all(part not in (".", "..") for part in parts) and len(name) <= 512, "unsafe archive path")
        require(prefix is None or parts[0] == prefix, "unexpected archive root")
        require(name not in seen and len(seen) < 1024, "duplicate/excess archive members")
        require(member.isfile() or member.isdir(), "archive links/special files are not accepted")
        require(member.size >= 0, "negative archive size")
        expanded += member.size
        require(expanded <= MAX_EXPANDED, "archive expansion exceeds bound")
        seen.add(name)
        path = destination.joinpath(*parts)
        if member.isdir():
            path.mkdir(parents=True, exist_ok=True)
            path.chmod(0o755)
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            source = archive.extractfile(member)
            require(source is not None, "missing archive file data")
            with source, path.open("xb") as target:
                shutil.copyfileobj(source, target)
            path.chmod(0o755 if member.mode & 0o111 else 0o644)
    require(seen, "empty archive")


def pack_tree(root, archive_path, epoch):
    with archive_path.open("wb") as target:
        with gzip.GzipFile(filename="", mode="wb", fileobj=target, mtime=0) as compressed:
            with tarfile.open(mode="w", fileobj=compressed, format=tarfile.USTAR_FORMAT) as archive:
                for path in [root] + sorted(root.rglob("*")):
                    require(not path.is_symlink(), "candidate tree contains a link")
                    name = root.name if path == root else root.name + "/" + path.relative_to(root).as_posix()
                    info = tarfile.TarInfo(name)
                    info.uid = info.gid = 0
                    info.uname = info.gname = ""
                    info.mtime = epoch
                    info.mode = 0o755 if path.is_dir() or path.parent.name == "bin" else 0o644
                    if path.is_dir():
                        info.type = tarfile.DIRTYPE
                        archive.addfile(info)
                    else:
                        info.size = path.stat().st_size
                        with path.open("rb") as source:
                            archive.addfile(info, source)


def check_files(root):
    lines = (root / "SHA256SUMS").read_text().splitlines()
    expected = {}
    for line in lines:
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9_./-]+)", line)
        require(match is not None, "invalid internal checksum entry")
        checksum, name = match.groups()
        require(name not in expected and name != "SHA256SUMS" and ".." not in PurePosixPath(name).parts, "invalid checksum path")
        expected[name] = checksum
    actual = {p.relative_to(root).as_posix() for p in root.rglob("*") if p.is_file() and p != root / "SHA256SUMS"}
    require(actual == set(expected), "missing or unexpected candidate files")
    for name, checksum in expected.items():
        require(digest(root / name) == checksum, f"checksum mismatch: {name}")


def stop_process(process):
    if process.poll() is None:
        process.send_signal(signal.SIGINT)
        try:
            process.wait(timeout=8)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
            raise ValueError("packaged process did not stop cooperatively")
    require(process.returncode == 0, f"packaged process exited {process.returncode}")


def smoke(root, manifest):
    cli = root / "bin/taskfused"
    for name in BINARIES:
        binary = root / "bin" / name
        flag = "version" if name == "taskfused" else "-version"
        info = json.loads(run([binary, flag]))
        for key in ("version", "revision", "go", "os", "arch", "schema"):
            require(info[key] == manifest[key], f"binary provenance mismatch: {name}/{key}")
        require(info["cgo"] and not info["modified"], "candidate must be a clean CGO build")
    with tempfile.TemporaryDirectory(prefix="taskfused-smoke-") as temporary:
        directory = Path(temporary)
        db = directory / "quickstart.db"

        def command(verb, *args):
            return json.loads(run([cli, verb, "-db", db, "-namespace", "demo", *args]))

        args = ("-handler", "echo", "-version", "v1", "-key", "candidate-smoke", "-input", "hello", "-join")
        first = command("submit", *args)
        joined = command("submit", *args)
        op_id = first["operation"]["id"]
        require(not first["joined"] and joined["joined"] and joined["operation"]["id"] == op_id, "packaged join failed")
        with (directory / "worker.log").open("wb") as log:
            worker = subprocess.Popen([str(root / "bin/taskfused-worker"), "-db", str(db), "-delay", "10ms"], stdout=log, stderr=log)
            try:
                outcome = command("wait", "-id", op_id, "-timeout", "10s")
                require(outcome["state"] == "succeeded" and base64.b64decode(outcome["result"]) == b"hello", "packaged worker/result failed")
                require(len(command("attempts", "-id", op_id)) == 1, "unexpected packaged attempts")
            finally:
                stop_process(worker)
        command("set-limits", "-max-operations", "1", "-max-active", "1")
        command("prune-results", "-before", "9999-12-31T23:59:59Z", "-limit", "1")
        outcome = command("get", "-id", op_id)
        require(outcome["state"] == "succeeded" and outcome["result_pruned"], "packaged prune marker failed")
        rejected = subprocess.run([str(cli), "submit", "-db", str(db), "-namespace", "demo", "-handler", "echo", "-version", "v1", "-key", "over-limit"], capture_output=True, timeout=10)
        require(rejected.returncode != 0 and command("usage")["operations"] == 1, "packaged admission limit failed")
        with (directory / "server.log").open("wb") as log:
            server = subprocess.Popen([str(cli), "serve", "-db", str(db), "-listen", "127.0.0.1:0"], stdout=log, stderr=log)
            try:
                deadline = time.monotonic() + 10
                address = None
                while time.monotonic() < deadline:
                    match = re.search(r"http://127\.0\.0\.1:[0-9]+", (directory / "server.log").read_text())
                    if match:
                        address = match.group()
                        break
                    require(server.poll() is None, "packaged server exited before startup")
                    time.sleep(0.02)
                require(address is not None, "packaged server did not start")
                opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
                with opener.open(address + "/api/namespaces/demo/usage", timeout=5) as response:
                    require(json.load(response)["pruned_results"] == 1, "packaged HTTP usage failed")
                with opener.open(address + "/?namespace=demo&id=" + op_id, timeout=5) as response:
                    require("Pruned — succeeded" in response.read().decode(), "packaged status HTML failed")
            finally:
                stop_process(server)
        report = json.loads(run([root / "bin/taskfused-demo", "-http", "-requests", "10"], timeout=120))
        shared = report["functional_over_http"]["shared"]
        require(shared["joined"] == 9 and shared["successful_waiters"] == 9, "packaged demo shared work failed")
        require(report["functional_over_http"]["crash"]["stale_commit_rejected"] and report["shutdown_completed"], "packaged recovery/shutdown failed")
        require(report["retention"]["retry_replay_preserved"] and report["retention"]["pruned_results"] == 4, "packaged retention replay failed")
    return {"version": manifest["version"], "revision": manifest["revision"], "target": "linux/amd64", "binaries_checked": list(BINARIES), "quickstart": "passed", "http_demo": "passed", "published": False}


def verify(archive_path, checksum_path):
    native_host()
    archive_path, checksum_path = Path(archive_path), Path(checksum_path)
    require(archive_path.stat().st_size <= MAX_EXPANDED, "compressed archive exceeds bound")
    expected = checksum_path.read_text().strip()
    require(expected == f"{digest(archive_path)}  {archive_path.name}", "archive checksum mismatch")
    prefix = archive_path.name.removesuffix(".tar.gz")
    require(re.fullmatch(r"taskfused-candidate-[0-9a-f]{12}-linux-amd64", prefix), "invalid candidate archive name")
    with tempfile.TemporaryDirectory(prefix="taskfused-verify-") as temporary:
        with tarfile.open(archive_path, "r:gz") as archive:
            extract_checked(archive, Path(temporary), prefix)
        root = Path(temporary) / prefix
        check_files(root)
        manifest = json.loads((root / "MANIFEST.json").read_text())
        require(re.fullmatch(r"[0-9a-f]{40}", manifest["revision"]), "invalid candidate revision")
        require(manifest["version"] == "candidate-" + manifest["revision"][:12], "invalid candidate version")
        require(prefix == "taskfused-" + manifest["version"] + "-linux-amd64", "candidate filename/provenance mismatch")
        require(manifest["os"] == "linux" and manifest["arch"] == "amd64" and manifest["schema"] == 5, "unsupported candidate compatibility")
        require(manifest["license_status"] == "all-rights-reserved; no public release authorized", "unexpected candidate license status")
        return smoke(root, manifest)


def build(output, go, rebuild=False):
    native_host()
    repo = Path(run(["git", "rev-parse", "--show-toplevel"]))
    require(not run(["git", "status", "--porcelain=v1", "--untracked-files=all"], cwd=repo), "candidate build requires a clean checkout, including untracked files")
    revision = run(["git", "rev-parse", "HEAD"], cwd=repo)
    tree = run(["git", "rev-parse", "HEAD^{tree}"], cwd=repo)
    epoch = int(run(["git", "show", "-s", "--format=%ct", "HEAD"], cwd=repo))
    version = "candidate-" + revision[:12]
    output = Path(output).resolve()
    require(not output.exists() and output != repo and repo not in output.parents, "output must be a new directory outside the checkout")
    output.parent.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, GOOS="linux", GOARCH="amd64", GOAMD64="v1", CGO_ENABLED="1", GOTOOLCHAIN="local", GOFLAGS="", GOEXPERIMENT="", SOURCE_DATE_EPOCH=str(epoch))
    settings = json.loads(run([go, "env", "-json", "GOVERSION", "CC", "CGO_ENABLED", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS", "GOAMD64", "GOEXPERIMENT"], env=env))
    require(re.fullmatch(r"go1\.26\.[0-9]+", settings["GOVERSION"]), "candidate gate currently requires a stable Go 1.26.x toolchain")
    compiler = run(shlex.split(settings["CC"]) + ["--version"], env=env).splitlines()[0]
    with tempfile.TemporaryDirectory(prefix=".taskfused-candidate-", dir=output.parent) as temporary:
        work = Path(temporary)
        source = work / "source"
        source.mkdir()
        raw = subprocess.run(["git", "archive", "--format=tar", revision], cwd=repo, capture_output=True, check=True).stdout
        with tarfile.open(fileobj=io.BytesIO(raw), mode="r:") as archive:
            extract_checked(archive, source)
        print("Verifying modules and building three native candidate binaries...", file=sys.stderr)
        run([go, "mod", "verify"], cwd=source, env=env)
        dependency = json.loads(run([go, "list", "-m", "-json", "github.com/mattn/go-sqlite3"], cwd=source, env=env))
        modules = run([go, "list", "-m", "all"], cwd=source, env=env).splitlines()
        require(len(modules) == 2 and not dependency.get("Replace"), "dependency graph changed; review candidate notices before packaging")
        result = work / "result"
        result.mkdir()
        root = work / ("taskfused-" + version + "-linux-amd64")
        (root / "bin").mkdir(parents=True)
        flags = "-s -w -buildid= -X github.com/taskfused/taskfused/internal/buildinfo.Version=" + version + " -X github.com/taskfused/taskfused/internal/buildinfo.Revision=" + revision

        def compile_all(directory, build_env=env):
            directory.mkdir(parents=True, exist_ok=True)
            for name, package in BINARIES.items():
                print(f"Building {name}...", file=sys.stderr)
                run([go, "build", "-trimpath", "-buildvcs=false", "-ldflags", flags, "-o", directory / name, package], cwd=source, env=build_env)

        compile_all(root / "bin")
        if rebuild:
            print("Rebuilding and comparing binary SHA-256 values...", file=sys.stderr)
            second = work / "second-build"
            compile_all(second, dict(env, GOCACHE=str(work / "second-cache")))
            require(all(digest(root / "bin" / name) == digest(second / name) for name in BINARIES), "same-host rebuild differed")
        for name in ("README.md", "NOTICE"):
            shutil.copyfile(source / name, root / name)
        shutil.copytree(source / "docs", root / "docs")
        shutil.copyfile(source / "docs/CANDIDATE_QUICKSTART.md", root / "QUICKSTART.md")
        (root / "tools").mkdir()
        shutil.copyfile(source / "scripts/candidate.py", root / "tools/candidate.py")
        (root / "licenses").mkdir()
        shutil.copyfile(Path(dependency["Dir"]) / "LICENSE", root / "licenses/go-sqlite3-MIT.txt")
        goroot = Path(run([go, "env", "GOROOT"], env=env))
        shutil.copyfile(goroot / "LICENSE", root / "licenses/Go-BSD.txt")
        shutil.copyfile(source / "docs/THIRD_PARTY.md", root / "licenses/THIRD_PARTY.md")
        info = json.loads(run([root / "bin/taskfused", "version"]))
        manifest = {**info, "source_tree": tree, "source_commit_epoch": epoch, "build_settings": settings, "c_compiler": compiler, "host_libc": list(platform.libc_ver()), "dependency": {key: dependency[key] for key in ("Path", "Version", "Sum", "GoModSum")}, "license_status": "all-rights-reserved; no public release authorized", "build_flags": ["-trimpath", "-buildvcs=false", "-ldflags", flags], "same_host_binary_rebuild_checked": rebuild}
        (root / "MANIFEST.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
        files = sorted(p for p in root.rglob("*") if p.is_file())
        (root / "SHA256SUMS").write_text("".join(f"{digest(p)}  {p.relative_to(root).as_posix()}\n" for p in files))
        archive_path = result / (root.name + ".tar.gz")
        pack_tree(root, archive_path, epoch)
        checksum = result / (archive_path.name + ".sha256")
        checksum.write_text(f"{digest(archive_path)}  {archive_path.name}\n")
        print("Extracting archive and smoke-testing packaged executables...", file=sys.stderr)
        report = verify(archive_path, checksum)
        (result / "verification.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
        require(not output.exists(), "output appeared during build; refusing replacement")
        result.rename(output)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    builder = commands.add_parser("build", help="build and smoke-test a clean committed checkout")
    builder.add_argument("--output", required=True, type=Path)
    builder.add_argument("--go", default=shutil.which("go"))
    builder.add_argument("--rebuild", action="store_true", help="build twice and compare all three binary hashes")
    checker = commands.add_parser("verify", help="verify integrity, then execute trusted candidate binaries in a temporary directory")
    checker.add_argument("archive", type=Path)
    checker.add_argument("--checksum", required=True, type=Path)
    args = parser.parse_args()
    try:
        if args.command == "build":
            require(args.go, "Go executable required; use --go")
            report = build(args.output, str(Path(args.go).resolve()), args.rebuild)
        else:
            report = verify(args.archive, args.checksum)
        print(json.dumps(report, indent=2, sort_keys=True))
    except (ValueError, OSError, subprocess.SubprocessError, tarfile.TarError, KeyError, TypeError) as error:
        print(f"taskfused candidate: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
