#!/bin/sh
# Writes THIRD_PARTY_LICENSES.md for the core build (Antresol Server):
# every Go module of `go build ./cmd/vault`, the Go standard
# library, and every npm package and vendored file whose code is in the
# library web bundle (the public tree's: the web build's edition is the
# tree's), each with its licence's full text. The web bundle is
# built (with source maps) into a temp dir; web/dist is left alone.
#
#   scripts/third-party-licenses.sh <out.md>
#
# Needs the Go module cache (go mod download) and web/node_modules.
set -eu
OUT=$1
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
(
	cd web
	npx vite build --sourcemap --outDir "$tmp/web" --emptyOutDir &&
		npx vite build -c vite.sw.config.ts --sourcemap --outDir "$tmp/web" --emptyOutDir false
) >"$tmp/vite.log" 2>&1 || { tail -30 "$tmp/vite.log" >&2; exit 1; }
node scripts/third-party-licenses.mjs "$tmp/web" >"$tmp/out.md"
mv "$tmp/out.md" "$OUT"
echo "third-party-licenses: wrote $OUT"
