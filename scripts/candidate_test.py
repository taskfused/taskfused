# Copyright 2026 TaskFused. All rights reserved.
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import candidate


class CandidateIntegrityTests(unittest.TestCase):
    def test_rejects_traversal_links_duplicates_and_oversize(self):
        for name, kind in (("../escape", "file"), ("/absolute", "file"), ("root/link", "link"), ("root/duplicate", "duplicate"), ("root/large", "large")):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                data = io.BytesIO()
                with tarfile.open(fileobj=data, mode="w") as archive:
                    entry = tarfile.TarInfo(name)
                    if kind == "link":
                        entry.type = tarfile.SYMTYPE
                        entry.linkname = "/tmp/escape"
                    elif kind == "large":
                        # The extractor must reject the header before reading data.
                        entry.size = candidate.MAX_EXPANDED + 1
                    archive.addfile(entry)
                    if kind == "duplicate":
                        archive.addfile(entry)
                data.seek(0)
                with tarfile.open(fileobj=data, mode="r:") as archive:
                    with self.assertRaises(ValueError):
                        candidate.extract_checked(archive, Path(temporary), "root")

    def test_archive_normalizes_permissions_ownership_and_mtimes(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            root = directory / "candidate"
            (root / "bin").mkdir(parents=True)
            executable = root / "bin/tool"
            executable.write_bytes(b"same executable")
            (root / "NOTICE").write_text("same notice")
            first, second = directory / "one.tgz", directory / "two.tgz"
            candidate.pack_tree(root, first, 1234567890)
            executable.chmod(0o600)
            os.utime(executable, (1800000000, 1800000000))
            candidate.pack_tree(root, second, 1234567890)
            self.assertEqual(candidate.digest(first), candidate.digest(second))
            with tarfile.open(first) as archive:
                for member in archive:
                    self.assertEqual(member.uid, 0)
                    self.assertEqual(member.gid, 0)
                    self.assertEqual(member.mtime, 1234567890)
                self.assertEqual(archive.getmember("candidate/bin/tool").mode, 0o755)

    def test_checksum_rejects_tampering_and_unlisted_files(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            data = root / "NOTICE"
            data.write_text("notice")
            (root / "SHA256SUMS").write_text(f"{candidate.digest(data)}  NOTICE\n")
            candidate.check_files(root)
            data.write_text("tampered")
            with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                candidate.check_files(root)
            data.write_text("notice")
            (root / "docs").mkdir()
            (root / "docs/SHA256SUMS").write_text("unlisted file")
            with self.assertRaisesRegex(ValueError, "unexpected candidate files"):
                candidate.check_files(root)

    def test_wrong_archive_checksum_does_not_execute_payload(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / ("taskfused-candidate-" + "a" * 12 + "-linux-amd64.tar.gz")
            archive.write_bytes(b"not an archive")
            checksum = root / "archive.sha256"
            checksum.write_text("0" * 64 + "  " + archive.name)
            with patch.object(candidate, "native_host"), patch.object(candidate, "smoke") as smoke:
                with self.assertRaisesRegex(ValueError, "archive checksum mismatch"):
                    candidate.verify(archive, checksum)
                smoke.assert_not_called()

    def test_dirty_checkout_is_rejected_before_build_or_output(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo = root / "repo"
            repo.mkdir()
            candidate.run(["git", "init", "-q", repo])
            (repo / "tracked").write_text("source")
            candidate.run(["git", "add", "tracked"], cwd=repo)
            candidate.run(["git", "-c", "user.name=Candidate Test", "-c", "user.email=candidate@example.invalid", "commit", "-qm", "fixture"], cwd=repo)
            (repo / "untracked.go").write_text("not committed")
            previous = Path.cwd()
            try:
                os.chdir(repo)
                with patch.object(candidate, "native_host"):
                    with self.assertRaisesRegex(ValueError, "clean checkout"):
                        candidate.build(root / "output", "no-such-go")
            finally:
                os.chdir(previous)
            self.assertFalse((root / "output").exists())


if __name__ == "__main__":
    unittest.main()
