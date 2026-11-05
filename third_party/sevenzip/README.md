# sevenzip (patched for Vault)

A copy of [github.com/bodgit/sevenzip](https://github.com/bodgit/sevenzip)
**v1.6.5** (BSD-3-Clause, see `LICENSE`; upstream README in
`README.upstream.md`), used through a `replace` directive in Vault's `go.mod`.
Tests, test data and CI files are left out. v1.6.5 was the latest release
on 2026-10-25 and has no fix for the problem below.

## Why

The 7z header is untrusted input (manga comes from torrent trackers). In
v1.6.5 every count read from the header (files, streams, folders, coders…)
becomes the length of a slice that is allocated *before* the data it
describes is read, bounded only by `math.MaxUint32`. A 41-byte `.cb7` whose
FilesInfo claims 2^32−1 files makes `readFilesInfo` call
`make([]FileHeader, 4294967295)` (~480 GB): on Linux that is an unrecoverable
`fatal error: runtime: out of memory` and the whole server dies on every
library scan. A compressed (LZMA) header can also be a bomb that feeds the
parser without end.

## The patch (search for `vault patch`)

- `types.go` `checkUint64`: any header count above `MaxHeaderCount`
  (1 048 576) is rejected with `errUint64TooLarge`.
- `types.go` `readFilesInfo`: more than `MaxFiles` (100 000) entries is
  rejected before `make([]FileHeader, files)`.
- `reader.go`: the decoded (compressed) header is read through
  `io.LimitReader(…, MaxEncodedHeader)` (64 MB).

The limits are exported variables. The regression test is
`TestSevenZipHeaderCountBomb` in `internal/comics/hardening_test.go`
(upstream v1.6.5: the test process is killed; patched: an error in
milliseconds). To update: copy the new upstream release here, re-apply the
three `vault patch` hunks, run `go test ./internal/comics/`; drop this copy
and the `replace` once upstream caps header counts itself.
