# Builds and the `sources` seam

This repository builds one server, the core build of Antresol Server:

```sh
npm --prefix web ci && npm --prefix web run build   # the web bundle (web/dist)
go build ./cmd/vault                                # the server, with web/dist embedded
```

No build tags are needed, and none should be passed. `make library` builds
the same; `make check-library` builds and checks the result
(`scripts/check-library-edition.sh`).

Build settings (`make VAR=value`, or `-ldflags "-X
<module>/internal/version.Name=value"` by hand; the Dockerfile takes them as
`--build-arg`):

- `VERSION`: the version the server reports (`dev` by default).
- `COMMIT`: the source revision (the Makefile asks git; without it the go
  tool's VCS stamp is used).
- `SOURCE_URL`: the source offer of `GET /api/about` and the «Source code ·
  AGPL» link in the web app (AGPL §13). Release builds set it to their public
  tag; empty (the default) means no link. `VAULT_SOURCE_URL` overrides it at
  run time; set but empty, it removes the link.

At run time the server needs `ffmpeg` and `ffprobe` 6.1 or newer: remux and
transcode pass `-readrate_initial_burst` (`internal/media/hls.go`), which
ffmpeg 6.0 does not have.

## What the seam is

Some code is written so that another build can add features to the core
without changing it. Such a build is not part of this repository; the core
never needs it and never links to it. You will see the seam in three places.

- **Go.** A file that starts with `//go:build !sources` is the core's side
  of a seam: the core's version of something a build with `-tags sources`
  replaces (for example `cmd/vault/wire_library.go`, which wires the core
  server). Files tagged `sources` are not in this repository. Keep the
  `!sources` files building and tested like any other; do not add a
  `//go:build sources` file here.
- **Web.** A module `X.stub.ts` or `X.stub.tsx` stands for a module `X` that
  another build adds. TypeScript (`moduleSuffixes`) and Vite
  (`resolve.extensions`) resolve `X` to the real module when it exists and
  to the stub otherwise, so core code imports `./X` and works either way.
  Stubs render nothing, return empty values and keep the real module's
  types. The edition is decided by whether `web/src/app/routesFull.tsx`
  exists; in this repository it does not, so `__LIBRARY_EDITION__` is `true`.
- **Database.** Core migrations live in `internal/db/migrations` and are
  numbered as `internal/db/migrations/README.md` says; numbers from 10001 up
  belong to other builds. A database opened by another build and then by the
  core (or back) keeps every setting: the core rewrites only the settings it
  knows and leaves the others as they are stored.

## Tests

Every test in this repository must pass in this repository. A test of
something only another build has does not belong here, not even behind a
`!__LIBRARY_EDITION__` condition. `it.runIf(__LIBRARY_EDITION__)` is fine: it
runs here.

## Names

`vault` is the code name: the binary (`cmd/vault`), the `VAULT_*`
environment variables, the `X-Vault-*` headers and `vault.db` keep it.
