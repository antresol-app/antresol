#!/bin/sh
# Builds the published Antresol Server archives: Linux (x86-64, ARM64) and
# macOS (Apple Silicon, Intel). They are the core build: the web UI of
# `npm run build` and the server of `go build` (run in the public tree), with
# no acquisition code in either (docs/build-tags.md);
# scripts/check-library-edition.sh checks every binary before it is packed.
#
#   scripts/release-server.sh 0.2.0 <out-dir> <dir with LICENSE and THIRD_PARTY_LICENSES.md>
#
# The licence files are in the root of this repository (.github/workflows/release.yml
# passes it); scripts/third-party-licenses.sh writes THIRD_PARTY_LICENSES.md.
set -eu
# A public build: no GOFLAGS of the caller (vault's GNUmakefile sets the sources build's).
export GOFLAGS=
V=$1 OUT=$2 LIC=$3
cd "$(dirname "$0")/.."
mkdir -p "$OUT"
npm --prefix web run build >"$OUT/web.log" 2>&1 || { tail -30 "$OUT/web.log"; exit 1; }
# The source offer (GET /api/about, AGPL §13): the public tag of this
# release, unless SOURCE_URL says otherwise (set but empty: no link).
SOURCE_URL=${SOURCE_URL-https://github.com/antresol-app/antresol/tree/v$V}
COMMIT=${COMMIT:-$(git rev-parse HEAD 2>/dev/null || true)}
VPKG=github.com/antresol-app/antresol/internal/version
LDFLAGS="-s -w -X $VPKG.Version=$V -X $VPKG.Commit=$COMMIT -X $VPKG.SourceURL=$SOURCE_URL"
bins=
for t in linux/amd64 linux/arm64 darwin/arm64 darwin/amd64; do
	os=${t%/*} ar=${t#*/}
	d=$OUT/antresol-server-$V-$os-$ar
	rm -rf "$d" && mkdir -p "$d"
	CGO_ENABLED=0 GOOS=$os GOARCH=$ar go build -trimpath -ldflags "$LDFLAGS" -o "$d/antresol-server" ./cmd/vault
	bins="$bins $d/antresol-server"
done
# shellcheck disable=SC2086 # one argument per binary
scripts/check-library-edition.sh $bins
for t in linux/amd64 linux/arm64 darwin/arm64 darwin/amd64; do
	os=${t%/*} ar=${t#*/}
	n=antresol-server-$V-$os-$ar
	d=$OUT/$n
	cp "$LIC/LICENSE" "$LIC/THIRD_PARTY_LICENSES.md" "$d/"
	printf 'Antresol Server %s\n\nInstall: https://sidun-av.github.io/antresol/install.html\nRequires ffmpeg and ffprobe 6.1 or newer on PATH.\n' "$V" >"$d/README.txt"
	tar -czf "$OUT/$n.tar.gz" -C "$OUT" "$n"
done
cd "$OUT" && shasum -a 256 antresol-server-"$V"-*.tar.gz >SHA256SUMS && cat SHA256SUMS
