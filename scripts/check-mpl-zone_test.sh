#!/bin/sh
# Tests scripts/check-mpl-zone.mjs on a copy of the tree (web/ without
# node_modules and dist, which are linked; LICENSING.md; the gate), so
# nothing here touches the real files:
#   1. the clean tree passes;
#   2. an import of AGPL code (src/reader/comicText.ts) planted in
#      reader/native/main.ts fails;
#   3. an MPL file of the closure without its SPDX line fails;
#   4. LICENSING.md naming a file that does not exist fails;
#   5. an MPL SPDX line on a reader file LICENSING.md does not list fails;
#   6. a type-only import of AGPL code (erased from the bundle, but the
#      MPL source still names and depends on it) fails.
#
#   sh scripts/check-mpl-zone_test.sh
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
[ -d "$root/web/node_modules" ] || { echo "check-mpl-zone_test: run npm --prefix web ci first" >&2; exit 2; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
trap 'exit 130' INT TERM HUP

fresh() {
	rm -rf "$tmp/t"
	mkdir -p "$tmp/t/scripts"
	cp "$root/LICENSING.md" "$tmp/t/" 2>/dev/null || :
	cp "$root/scripts/check-mpl-zone.mjs" "$tmp/t/scripts/" 2>/dev/null || :
	(cd "$root" && tar -cf - --exclude=web/node_modules --exclude=web/dist --exclude='web/test-results' \
		--exclude='web/playwright-report' web) | tar -xf - -C "$tmp/t"
	ln -s "$root/web/node_modules" "$tmp/t/web/node_modules"
}
fail=0
gate() { node "$tmp/t/scripts/check-mpl-zone.mjs" "$tmp/t" >"$tmp/out" 2>&1; }
expect() { # expect 0|1 name
	want=$1; shift
	if gate; then got=0; else got=1; fi
	if [ "$got" != "$want" ]; then
		echo "FAIL: $* (exit $got, expected $want)"
		sed 's/^/    /' "$tmp/out"
		fail=1
	else
		echo "ok: $*"
	fi
}

fresh
expect 0 "clean tree passes"
grep -q '^check-mpl-zone: ok, 25 modules' "$tmp/out" || { echo "FAIL: clean tree is not 25 modules:"; sed 's/^/    /' "$tmp/out"; fail=1; }

fresh
main="$tmp/t/web/src/reader/native/main.ts"
printf "import { GONE_TITLE } from '../comicText'\n(globalThis as Record<string, unknown>).plantedAgpl = GONE_TITLE\n" >>"$main"
expect 1 "AGPL import planted in reader/native/main.ts"
grep -q 'src/reader/comicText.ts' "$tmp/out" || { echo "FAIL: the AGPL module is not named"; fail=1; }

fresh
f="$tmp/t/web/src/reader/fb2text.ts"
grep -v 'SPDX-License-Identifier' "$f" >"$tmp/x" && cp "$tmp/x" "$f"
expect 1 "MPL file without its SPDX line"

fresh
printf '| `web/src/reader/nosuch.ts` | MPL-2.0 |\n' >"$tmp/x"
awk -v add="$(cat "$tmp/x")" '/<!-- mpl-zones:end -->/ { print add } { print }' "$tmp/t/LICENSING.md" >"$tmp/y" && cp "$tmp/y" "$tmp/t/LICENSING.md"
expect 1 "LICENSING.md names a missing file"

fresh
f="$tmp/t/web/src/reader/comicNav.ts"
{ echo '// SPDX-License-Identifier: MPL-2.0'; cat "$f"; } >"$tmp/x" && cp "$tmp/x" "$f"
expect 1 "MPL SPDX line on a reader file outside the zone list"

fresh
printf "import type { Profile } from '../../api/types'\nexport type PlantedAgplType = Profile\n" >>"$main"
expect 1 "type-only AGPL import planted in reader/native/main.ts"
grep -q 'src/api/types.ts' "$tmp/out" || { echo "FAIL: the AGPL type module is not named"; fail=1; }

[ "$fail" = 0 ] || exit 1
echo "check-mpl-zone_test: ok"
