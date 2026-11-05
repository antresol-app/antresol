#!/bin/sh
# Checks the core build of the server: the binary published as
# Antresol Server, built with `go build` over the web bundle of
# `npm run build` in the public tree (no web/src/app/routesFull.tsx, see
# docs/build-tags.md). It must contain no
# acquisition code at all — not code that is switched off:
#
#   1. no package of the sources build's features in the build graph
#      (acquire, torrent, indexer, online, follow, migrate, musicimport) and
#      no torrent or WebRTC module (anacrolix/*, pion/*) in `go version -m`
#      (the core packages organize, disk, releaseprefs, mediaimport and
#      playlistimport are allowed: both editions build them);
#   2. no "magnet:" in the binary or in the web bundle embedded in it, nor
#      any extra word of the file LIBRARY_EDITION_WORDS names (one extended
#      regex per line, "#" lines ignored), when it is set;
#   3. the bundle in the binary is the one in web/dist (so 2 really read it).
#
#   scripts/check-library-edition.sh             build both into a temp dir, check
#   scripts/check-library-edition.sh BINARY...   check built binaries against web/dist
#
# Exit status 0: every check passed.
set -eu
# A public build: no GOFLAGS of the caller (vault's GNUmakefile sets the sources build's).
export GOFLAGS=
# The words, matched case-insensitively (spell Cyrillic cases out: grep -i
# folds ASCII only under LC_ALL=C).
WORDS='magnet:'
if [ -n "${LIBRARY_EDITION_WORDS:-}" ]; then
	[ -f "$LIBRARY_EDITION_WORDS" ] || { echo "check-library-edition: $LIBRARY_EDITION_WORDS: no such file" >&2; exit 2; }
	extra=$(grep -v -e '^#' -e '^$' "$LIBRARY_EDITION_WORDS" | paste -s -d '|' -)
	[ -z "$extra" ] || WORDS="$WORDS|$extra"
fi
cd "$(dirname "$0")/.."

FULL_PKGS='internal/(acquire|torrent|indexer|online|follow|migrate|musicimport)($|/)'
FULL_MODS='anacrolix|pion|/dht'

fail=0
bad() { echo "check-library-edition: FAIL: $*" >&2; fail=1; }
ok() { echo "check-library-edition: ok: $*"; }

# words prints how many times the words occur in a file (0: none).
words() { LC_ALL=C grep -a -o -i -E "$WORDS" "$1" | wc -l | tr -d ' '; }

if [ $# -eq 0 ]; then
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	echo "check-library-edition: building the web bundle (npm run build)…"
	if ! npm --prefix web run build >"$tmp/web.log" 2>&1; then
		tail -30 "$tmp/web.log" >&2
		exit 1
	fi
	echo "check-library-edition: building the server (no build tags)…"
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$tmp/antresol-server" ./cmd/vault
	set -- "$tmp/antresol-server"
fi

# 1. The build graph.
pkgs=$(go list -deps ./cmd/vault | grep -E "github.com/antresol-app/antresol/$FULL_PKGS" || true)
[ -z "$pkgs" ] && ok "no sources-build package in the build graph" || bad "sources-build packages in the build graph: $pkgs"
mods=$(go list -deps -f '{{with .Module}}{{.Path}}{{end}}' ./cmd/vault | sort -u | grep -E "$FULL_MODS" || true)
[ -z "$mods" ] && ok "no torrent/WebRTC module in the build graph" || bad "modules in the build graph: $mods"

# 2. The web bundle on disk.
[ -f web/dist/index.html ] || { bad "web/dist/index.html missing: build the web bundle first (npm --prefix web run build)"; exit 1; }
n=0
for f in $(find web/dist -type f \( -name '*.js' -o -name '*.mjs' -o -name '*.css' -o -name '*.html' -o -name '*.json' -o -name '*.webmanifest' \)); do
	c=$(words "$f")
	if [ "$c" != 0 ]; then
		bad "$f: $c matches: $(LC_ALL=C grep -a -o -i -E "$WORDS" "$f" | LC_ALL=C sort -u | head -5 | tr '\n' ' ')"
		n=$((n + c))
	fi
done
[ "$n" = 0 ] && ok "web/dist: 0 matches"
asset=$(sed -n 's/.*src="\/assets\/\(index-[^"]*\.js\)".*/\1/p' web/dist/index.html | head -1)
[ -n "$asset" ] || bad "web/dist/index.html names no assets/index-*.js"

# 3. Each binary.
for bin in "$@"; do
	[ -f "$bin" ] || { bad "$bin: no such file"; continue; }
	m=$(go version -m "$bin" | grep -E "^[[:space:]]+(dep|mod|=>)" | grep -E "$FULL_MODS" || true)
	[ -z "$m" ] && ok "$bin: go version -m lists no anacrolix/pion/dht module" || bad "$bin: modules: $m"
	c=$(words "$bin")
	[ "$c" = 0 ] && ok "$bin: 0 matches of the words" ||
		bad "$bin: $c matches: $(LC_ALL=C grep -a -o -i -E "$WORDS" "$bin" | LC_ALL=C sort -u | head -8 | tr '\n' ' ')"
	if [ -n "$asset" ]; then
		LC_ALL=C grep -a -q -F "$asset" "$bin" && ok "$bin: embeds web/dist ($asset)" || bad "$bin: does not embed web/dist ($asset): rebuild it after the web bundle"
	fi
done

[ "$fail" = 0 ] && echo "check-library-edition: PASS" || echo "check-library-edition: FAILED" >&2
exit "$fail"
