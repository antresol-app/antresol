# Governance

## Today: one maintainer

Antresol has one maintainer, who started the project, owns the name and the
logo (see [TRADEMARKS.md](TRADEMARKS.md)) and has the final say on what goes
in. The maintainer works with AI assistance and says so (see
[CONTRIBUTING.md](CONTRIBUTING.md)).

What that means in practice:

- **Scope and design.** The maintainer decides what is in scope and how the
  interface looks. Large or visual changes start as an issue so that the
  decision comes before the work.
- **Merging.** Pull requests are reviewed and merged by the maintainer. The
  maintainer may also push directly to `main`; CI runs on every push.
- **Releases.** Releases are tags on `main`. Binaries and images are built
  from the tag by the public CI, never on a private machine.
- **The App Store app.** The app is closed source and is published by a
  licensee of the maintainer. Its needs (a stable API, the MPL-2.0 reader
  and translations) are protected by tests and by `.github/CODEOWNERS`.
- **Response times.** This is a spare-time project. Expect a first answer to
  an issue or a pull request within two weeks; ping the thread if there is
  none.

## Growing

When someone has contributed good work over several months and wants to help
maintain the project, the maintainer may invite them as a maintainer. With
two or more maintainers:

- every change goes through a pull request reviewed by another maintainer;
- decisions that change scope, licences or governance are made by consensus
  of the maintainers, and the founding maintainer decides when there is
  none;
- this file is updated with the list of maintainers.

## Licences and contributions

Contributions are accepted under the licence of the files they change, with
a DCO sign-off and without a CLA (see [LICENSING.md](LICENSING.md)). The
project cannot relicense contributed code without its authors' consent.

## Changes to this document

Changes to this file are made by pull request and merged by the maintainer.
