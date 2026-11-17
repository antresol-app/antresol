#!/bin/sh
# Tests scripts/check-library-edition.sh --graph on small temp modules (a
# copy of the script beside each), so nothing here builds the real server:
#   1. a clean graph passes;
#   2. a sources-build package (internal/online) in the graph fails;
#   3. a graph go cannot list (a package that does not compile its
#      imports) is refused, never read as an empty, clean graph.
#
#   sh scripts/check-library-edition_test.sh
set -u
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
trap 'exit 130' INT TERM HUP
fail=0
no() { echo "FAIL: $*"; fail=1; }

# tree NAME MAIN: a module github.com/antresol-app/antresol whose
# cmd/vault/main.go is MAIN, with an empty internal/online package.
tree() {
	t=$tmp/$1
	mkdir -p "$t/scripts" "$t/cmd/vault" "$t/internal/online"
	cp "$root/scripts/check-library-edition.sh" "$t/scripts/"
	printf 'module github.com/antresol-app/antresol\n\ngo 1.21\n' >"$t/go.mod"
	printf 'package online\n\n// On is set.\nconst On = true\n' >"$t/internal/online/online.go"
	printf '%s\n' "$2" >"$t/cmd/vault/main.go"
}
run() { # run NAME: sets rc, out
	rc=0
	out=$(cd "$tmp/$1" && GOWORK=off GOFLAGS=-mod=mod sh scripts/check-library-edition.sh --graph 2>&1) || rc=$?
}

tree clean 'package main

func main() {}'
run clean
[ "$rc" = 0 ] || no "clean graph: exit $rc: $out"

tree online 'package main

import "github.com/antresol-app/antresol/internal/online"

func main() { println(online.On) }'
run online
[ "$rc" = 1 ] && echo "$out" | grep -q 'internal/online' || no "online in the graph: exit $rc: $out"

tree broken 'package main

import "github.com/antresol-app/antresol/internal/gone"

func main() { gone.X() }'
run broken
[ "$rc" != 0 ] || no "a graph go cannot list passed: $out"
echo "$out" | grep -q 'go list' || no "the refusal does not name go list: $out"
echo "$out" | grep -q 'PASS' && no "a graph go cannot list printed PASS: $out"

[ "$fail" = 0 ] && echo "check-library-edition_test: ok" || exit 1
