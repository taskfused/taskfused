# Candidate dependency notices

TaskFused remains all rights reserved under NOTICE. These dependency terms do not
grant a license to TaskFused itself or authorize a public release of the candidate.

Candidate archives include licenses/go-sqlite3-MIT.txt copied from the selected
mattn/go-sqlite3 module and licenses/Go-BSD.txt copied from the build toolchain.
The dependency module/version/checksums and Go version are recorded in MANIFEST.json.
The current candidate builder stops if the dependency graph adds modules or uses
a replacement; review notices before extending the packaging recipe.

The go-sqlite3 driver uses the MIT license (copyright 2014 Yasuhiro Matsumoto).
Its bundled SQLite amalgamation includes the following copyright disclaimer:

    The author disclaims copyright to this source code. In place of
    a legal notice, here is a blessing:

    May you do good and not evil.
    May you find forgiveness for yourself and forgive others.
    May you share freely, never taking more than you give.

SQLite is compiled from that bundled amalgamation; this recipe does not request
the optional system libsqlite3 build tag. CGO native executables can depend on the
host C runtime and other system libraries; no system runtime libraries are copied
into the archive. Their presence, ABI compatibility and applicable system licensing
remain part of the deployment environment. Do not describe these as portable static
binaries. Dependency review is still required before any public distribution.
