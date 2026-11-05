# Licensing

Antresol Server (the Go server in this repository and its web interface) is
free software under the **GNU Affero General Public License, version 3 or
any later version** (`AGPL-3.0-or-later`). The full text is in
[`LICENSE`](LICENSE).

Copyright (c) 2026 Antresol Contributors. "Antresol Contributors" means
everyone who has contributed to this repository; each keeps the copyright in
their own contribution. The commit history records who contributed what.

Two parts of the web code are under the **Mozilla Public License 2.0**
(`MPL-2.0`) instead, and some third-party code keeps its own licence. This
file says exactly which files are which. When this file and a header in a
file disagree, tell us: it is a bug.

## Default: AGPL-3.0-or-later

Every file in this repository is `AGPL-3.0-or-later` unless the table below
or a file's own header says otherwise. That includes all of `cmd/`,
`internal/`, `scripts/`, the web interface outside the MPL-2.0 zones, the
build files and the documentation.

One exception to the whole of this file: the logo artwork in
`web/public/icons/` and `web/src/components/logoArt.ts` is under no licence
here, neither AGPL nor MPL. It belongs to its owner and sits in the tree only
so that the project builds as published; see
[TRADEMARKS.md](TRADEMARKS.md).

If you run a modified version of the server for other people over a network,
AGPL section 13 asks you to offer them its source. The server's about
endpoint and the «Исходный код · AGPL» link in the web interface exist for
that; point them at your own source.

## MPL-2.0 zones

These files ship inside the closed-source iPhone, iPad and Mac app or feed
it: the app's e-book reader is built from them, and the app's Russian
strings come from the web's catalogs. They are under MPL-2.0, which allows
that, and MPL-2.0 names the AGPL-3.0 as a Secondary License, so the AGPL web
interface may import them as well.

- **The native reader closure**: `web/reader-native.html` and the 9 files
  listed below. Each carries an `SPDX-License-Identifier: MPL-2.0` line, a
  copyright line and the MPL Exhibit A notice. `web/src/reader/LICENSE` has
  the MPL-2.0 text. The other files in `web/src/reader/` are **not** in the
  zone: they are the web interface's reader and stay `AGPL-3.0-or-later`.
- **The translations**: everything in `web/src/i18n/`, by directory, including
  files added there later. The Exhibit A notice for the directory is in
  `web/src/i18n/LICENSE`, together with the MPL-2.0 text.

Rules for the zones:

1. Code never moves from an AGPL file into an MPL zone. A zone file may
   import other zone files, the vendored code listed below and npm packages
   under MIT, BSD or MPL-2.0; nothing else, not even an `import type` of an
   AGPL module (it carries no code into the bundle, but the MPL file would
   depend on AGPL text). A type both sides need lives in the zone and the
   AGPL side imports it from there (e.g. `AnnotationColor`).
2. Contributions to a zone file are made under MPL-2.0 (see
   [CONTRIBUTING.md](CONTRIBUTING.md)).
3. A new file joins the reader closure only with a row in the table below and
   the header. `scripts/check-mpl-zone.mjs` builds the native reader in memory
   and fails when any module in it, or any file one of them imports (type-only
   imports included), is not in an MPL-2.0, MIT or BSD row, when
   a listed file is missing or lacks its header, and when a file outside the
   table claims MPL-2.0. CI and `make apple-store-check` run it.

The header of an MPL-2.0 file:

```ts
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2026 Antresol Contributors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
```

No zone file is "Incompatible With Secondary Licenses" (MPL Exhibit B is not
used).

## The table

The gate reads this table. The most specific row wins: a file row beats a
directory row (`/**`), and a deeper directory beats a shallower one.

<!-- mpl-zones:begin -->
| Path | Licence | What |
|---|---|---|
| `web/reader-native.html` | MPL-2.0 | the native reader's page |
| `web/src/reader/fb2text.ts` | MPL-2.0 | FB2 encoding fix-up |
| `web/src/reader/bookFile.ts` | MPL-2.0 | the reading file as foliate wants it |
| `web/src/reader/highlights.ts` | MPL-2.0 | highlights and selection |
| `web/src/reader/prefs.ts` | MPL-2.0 | reader settings |
| `web/src/reader/readerStyle.ts` | MPL-2.0 | the look of a book |
| `web/src/reader/sanitize.ts` | MPL-2.0 | scripts out of book content |
| `web/src/reader/native/main.ts` | MPL-2.0 | the native reader's entry |
| `web/src/reader/native/protocol.ts` | MPL-2.0 | messages between the app and the reader |
| `web/src/reader/native/screenPages.ts` | MPL-2.0 | screen page counts |
| `web/src/i18n/**` | MPL-2.0 | translations (directory rule) |
| `web/src/vendor/foliate/**` | MIT | foliate-js, vendored |
| `web/src/vendor/foliate/vendor/zip.js` | BSD-3-Clause | zip.js 2.8.22, as bundled by foliate-js |
| `web/src/vendor/foliate/vendor/fflate.js` | MIT | fflate 0.8.2, as bundled by foliate-js |
<!-- mpl-zones:end -->

`web/src/vendor/foliate/` is a copy of foliate-js (MIT); see its `NOTICE` for
the commit and what was changed. The changes made here to it (the `pdf.js`
stub and the marked page-turn code in `paginator.js`) are offered under the
same MIT terms.

## Third-party software

The server binary and the web bundle contain third-party Go modules and npm
packages under their own licences. [`THIRD_PARTY_LICENSES.md`](THIRD_PARTY_LICENSES.md)
lists every one with its licence text; it is generated from the build by
`scripts/third-party-licenses.sh` and must be regenerated when dependencies
change.

`third_party/` holds a patched copy of a Go module under its original
licence (see the files there).

## The name and the logo

The licences above cover code. They do not grant any right to the name
"Antresol" or to its logo; see [TRADEMARKS.md](TRADEMARKS.md).
