# Adding a language

Antresol's interface is in Russian and English. A new language is one pull request.
Translate the English catalogs; the Russian ones are the source of meaning when English is unclear.

## Web interface

1. Copy `web/src/i18n/en/` to `web/src/i18n/<code>/` (an ISO 639-1 code, e.g. `de`) and
   `web/src/i18n/en.ts` to `web/src/i18n/<code>.ts`; translate the strings, keep the keys.
   TypeScript checks that every key is there (`npm --prefix web run typecheck`).
2. Register it in `web/src/i18n/core.ts`: the `Lang` type, `LANGS`, the catalog loader,
   `localeOf` (e.g. `de-DE`) and the plural rule in `pluralForm` if it differs from English.

## Server messages

3. Add the code to `Languages` in `internal/i18n/i18n.go`.
4. Copy each `internal/i18n/catalog_en_*.go` to `catalog_<code>_*.go`, change `Register(EN, …)`
   and `RegisterPlural(EN, …)` to your code, and translate the values. Keys are the Russian
   source strings: leave them as they are.

## Apps

The apps live in their own repositories and use the platform's resources:

- Android: `values-<code>/strings.xml` in [antresol-android](https://github.com/antresol-app/antresol-android).
- Apple TV: `<code>.lproj/Localizable.strings` (app and Top Shelf) in [antresol-tvos](https://github.com/antresol-app/antresol-tvos).

## Checks

Run `make test` (Go) and `npm --prefix web test`, and add a screenshot of a few screens
in your language to the pull request.
