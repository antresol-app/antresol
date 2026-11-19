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

## Addons

An addon brings its own strings; the core catalogs above do not hold them. Each
addon is translated in its own repository, in three places:

- **Web part.** The `strings` of its `registerAddon` call: one object per language
  (`ru`, `en`), key to text (the template keeps them in `web/src/index.tsx`, larger
  addons in `web/src/strings.ts`). Every label and title the addon contributes
  (`routes`, `manageTiles`, `profileMenu`, …) is a key of these strings, and its own
  pages read them with `useAddonT`. Copy the `en` object, translate the values, keep
  the keys and the `{placeholders}`. When the language is new to the core, add its
  code to the `strings` type in `web/src/addons/registry.ts` (as an optional key, so
  addons without it still type-check). A key missing from the catalog of the
  interface language shows as itself, so until an addon has your language its
  labels show as keys.
- **Manifest.** `name`, `description` and the `title` of each `notify` topic in
  `antresol-addon.json` are `{ "ru": …, "en": … }` objects. The app shows the interface
  language's text, else the other one.
- **Server part.** What the addon's program says in words (errors, report lines,
  letters) it localizes itself: the profile's language arrives as `lang` in
  `X-Antresol-User` (see [the contract](addons/contract.md)). An addon built like
  the core keeps the Russian source text as the key and registers
  `catalog_<code>_*.go` files in its `internal/strings` the way steps 3–4 do; the
  `Languages` list it uses is copied from the core by `make corecopy`, so add the code
  to the core first.

Run `make test` in the addon: an addon with server catalogs has a test like the
core's, which fails on a key without a translation.

## Apps

The apps live in their own repositories and use the platform's resources:

- Android: `values-<code>/strings.xml` in [antresol-android](https://github.com/antresol-app/antresol-android).
- Apple TV: `<code>.lproj/Localizable.strings` (app and Top Shelf) in [antresol-tvos](https://github.com/antresol-app/antresol-tvos).

## Checks

Run `make test` (Go) and `npm --prefix web test`, and add a screenshot of a few screens
in your language to the pull request.
