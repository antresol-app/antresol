# Contributing to Antresol

Thank you for helping. Antresol is a small project maintained by one person
(see [GOVERNANCE.md](GOVERNANCE.md)), so a few rules keep it workable. Please
read them before you start; they save both of us time.

## Before you write code

- **Small fixes** (a bug, a typo, a missing test): open a pull request
  directly.
- **Large changes, new features and anything visual**: open an issue first
  and describe what you want to change and why. A screen or a feature that
  was not discussed may be declined even if the code is good, simply because
  it does not fit the design or the scope.
- **Scope.** Antresol plays and organises media its users already have. Pull
  requests that add content acquisition to the core (getting media from
  outside the user's own folders) are not accepted. Metadata (descriptions,
  artwork, ratings, subtitles from services with an API and a licence for it)
  is in scope.
- **Security problems** never go into a public issue: see
  [SECURITY.md](SECURITY.md).

English pull requests and issues are welcome; Russian is fine too. The
interface is in Russian and English: when you add a string, write the
Russian one if you can. If you cannot, a machine translation is fine; say so
in the pull request and the maintainer will fix the wording.

## Sign-off (DCO)

Every commit must be signed off under the
[Developer Certificate of Origin](https://developercertificate.org/): by
adding the line

```
Signed-off-by: Your Name <you@example.com>
```

(`git commit -s` does it) you certify that you wrote the change or otherwise
have the right to submit it under this project's licence. There is no CLA.
The sign-off is a human's statement: an AI tool cannot sign off, and a bot
account's commits are not accepted.

## Licences of contributions

- Your contribution is licensed under the licence of the file it changes:
  `AGPL-3.0-or-later` by default.
- **The reader closure and the translations are MPL-2.0**:
  `web/reader-native.html`, the nine reader files listed in
  [LICENSING.md](LICENSING.md), and everything in `web/src/i18n/`.
  Contributions there are made under MPL-2.0. They ship in the closed App
  Store app, so never copy code into them from AGPL files;
  `scripts/check-mpl-zone.mjs` checks the reader's imports.
- Do not add code you cannot license this way (copied from a project with an
  incompatible licence, from a site's answers without a licence, or
  generated output you have not checked for that).

## AI-assisted contributions

AI tools are allowed, and the maintainer uses them too. The rules:

- **Disclose it.** Say in the pull request which parts were written with an
  AI tool. A trailer such as `Assisted-by: <tool>` in the commit is welcome.
- **Understand it.** You are the author: you must be able to explain every
  line and answer review questions without asking the tool. Pull requests
  that look generated and unreviewed (unrelated changes, invented APIs,
  tests that test nothing) are closed.
- **You sign off**, not the tool (see above).

## Building and testing

You need Go 1.27, Node 22 (`.nvmrc`), and `ffmpeg`/`ffprobe` 6.1 or newer.

```sh
npm --prefix web ci
make build        # web/dist, then bin/vault
make test         # go test ./...
make test-web     # vitest
make e2e          # Playwright against a real bin/vault (run make build first)
```

`npm --prefix web run typecheck` must pass, and `gofmt` must be clean.
[docs/build-tags.md](docs/build-tags.md) explains the build: no build tags
are needed, and the `!sources` files and `.stub.ts` modules you will meet
are seams that keep the core independent of other builds.

**Names.** "vault" is the code name of the project. It stays in the binary
(`cmd/vault`), the `VAULT_*` environment variables, the `X-Vault-*` headers
and `vault.db`; do not rename them. The product is called Antresol.

### Tests

- Add a test for every fix and feature. A bug fix comes with a test that
  fails without it.
- **No mixed test files.** This repository builds the core: in the web code
  `__LIBRARY_EDITION__` is `true` and some modules are stubs. Every test here
  must pass in this tree. A test of something only another build has does
  not belong here, not even behind `it.runIf(!__LIBRARY_EDITION__)`;
  `it.runIf(__LIBRARY_EDITION__)` is fine, since it runs here. A test that
  is red for a reason you cannot fix in the same change is `test.fixme` with
  a one-line reason.
- Timing-sensitive tests must not depend on a fast machine.

### Database

Migrations live in `internal/db/migrations`; read its `README.md` for the
numbering. A shipped migration is never edited or renamed.

## UI rules

The web interface has one design system. Read [`web/DESIGN.md`](web/DESIGN.md)
before you touch a screen.

- Build screens from the components in `web/src/ui` and the tokens in
  `web/src/design/tokens.css`. No literal colours, sizes or spacing in module
  CSS; no one-off button, menu or field. If a component cannot do what you
  need, extend it (and its entry in the `/dev/gallery` page).
- Every string goes into the catalogs (`web/src/i18n/ru`, `web/src/i18n/en`).
- Touch targets are at least 44 pt; nothing is reachable only by hover; the
  layout works on a phone in both orientations.
- **Screenshots in the pull request** for every visible change: before and
  after, at 1x (a display without Retina scaling, or the browser zoom at
  100% on a 1x screen) on a desktop width, and at 375 px wide (phone). Use
  test or Creative Commons media in screenshots, never commercial films,
  music or book covers.

## Pull requests

- One change per pull request; keep unrelated refactoring out.
- Describe what changed and why, how you tested it, and link the issue.
- CI must be green. The maintainer may push small fixes to your branch or
  squash on merge.
- Changes to what the App Store app depends on (the API's shape, the
  reader closure, the translations) need the maintainer's review; see
  `.github/CODEOWNERS`. An API change must keep existing app versions
  working: add fields, never remove or rename one.

## Code of Conduct

Everyone follows the [Code of Conduct](CODE_OF_CONDUCT.md).
