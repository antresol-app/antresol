# Antresol addon contract, version 1

An addon adds features to an Antresol server without being part of it. It is
a separate program, plus an optional web bundle that the app loads into its
screens. An admin installs it from a GitHub release (or, on a development
server, from a local folder); the server runs it, proxies the app's requests
to it, and gives it a small API of its own.

This document is the whole contract. It describes what the server does
today, and an addon may rely on everything written here.

## Licences

The addon SDK (`web/addon-sdk`, `@antresol/addon-sdk`) and the addon template
are under the **MIT License**: an addon may take any licence. The Antresol
server itself stays `AGPL-3.0-or-later`; an addon talks to it only through
this contract (a separate process, the proxy and the core API).

## Versioning

- The contract has a number. This server speaks **1**
  (`addons.ContractVersion`). A manifest whose `contract` is not 1 is
  refused, and an installed addon whose contract stops matching is shown as
  incompatible and not started.
- **Contract 1 never breaks.** Anything added later (more API, more slots,
  more manifest fields, more shared names) is an addition: an addon written
  for contract 1 keeps working on every server that speaks 1. A change that
  would break one gets a new contract number.
- **Frozen in contract 1:** the manifest, the process environment, the
  proxy, the core API, events, the slots, and the SDK's `registerAddon`,
  `addonApi`, `useAddonT`, `useEvents` and the shared modules' names below.
- **Best effort:** the SDK's `ui` namespace (the app's components) and its
  list-page helpers (`ViewMenu`, `ViewToolbar`, `useUrlView`,
  `useMediaSegments` and the media-segment helpers). It
  follows the app's design system and may change between server versions;
  an addon that uses it may need a rebuild after a server update.

## Trust: addons are not sandboxed

An addon runs as an ordinary process **with the server's rights**: the same
user, the same files. It can read the server's data, including its database
and media, whatever permissions its manifest asks for. The permissions below
limit only what the core API answers. Install only addons you trust; the
install dialog says so before anything is downloaded.

## Release layout

A release is a GitHub release of the addon's repository. Its assets:

| Asset | What |
| --- | --- |
| `antresol-addon.json` | the manifest (below) |
| one archive per platform | a `.tar.gz` holding `bin/addon`, the program |
| the web archive (optional) | a `.tar.gz` with `index.js` at its root |

The asset names are free; the manifest names them. Rules for every archive:

- gzip-compressed tar holding **only regular files and directories**. Symlinks,
  hardlinks, devices and FIFOs are refused, and so are absolute paths and
  `..` segments.
- **No pax global header**: an archive made by `git archive` carries one and
  is refused. Make archives with plain `tar` (`tar --format=ustar -C dir -czf
  out.tar.gz .`; on macOS also `--no-mac-metadata --no-xattrs` and
  `COPYFILE_DISABLE=1`, so that no `._` files or extended attributes go in).
- At most **512 MiB** downloaded per asset and 512 MiB unpacked.
- The sha256 of the downloaded file must equal the manifest's.
- Asset URLs, and every redirect they lead through, must be **https** on
  `github.com`, `objects.githubusercontent.com` or
  `release-assets.githubusercontent.com`; a redirect anywhere else fails the
  download.
- At most **100 000** entries per archive.

The server takes the repository's latest release through the GitHub API
(anonymous, cached for 10 minutes), downloads the asset for its own OS and
architecture, checks it and unpacks it. The admin first sees that release
(name, version, permissions, the warning) and installs **that version**:
`POST /api/addons {"url", "version"}` and `POST /api/addons/<id>/update
{"version"}` refuse when the latest release is another one by then
(«Релиз расширения изменился после просмотра…»), so nothing the admin has not
seen gets installed.

```
$VAULT_DATA_DIR/addons/<id>/<version>/bin/addon   the platform archive
$VAULT_DATA_DIR/addons/<id>/<version>/web/        the web archive
$VAULT_DATA_DIR/addons/<id>/data/                 the addon's own data, kept across versions
```

An update installs the new version beside the running one; the old one keeps
serving until the new one is healthy, then it is stopped and deleted. If the
new one does not start, it is deleted and the old one stays. Removing an
addon keeps `data/` unless the admin asks to delete it too. Removing it
always forgets its file holds; deleting its data too also forgets its file
references (below; the library files themselves stay).

### Local releases (development)

A server started with `VAULT_ADDONS_DEV=<dir>` (absolute, or relative to the
data directory) also installs `local:<name>`: the folder `<dir>/<name>` holds
`antresol-addon.json` and the assets it names, and the manifest's `id` must
be `<name>`. The same version installed again is not an update: raise the
version, or remove the addon first. `VAULT_ADDONS=off` turns addons off
entirely.

### Pinned releases

`VAULT_ADDONS_PIN=<id>@<version>[,<id>@<version>…]` pins addons to local
releases (`VAULT_ADDONS_DEV`): a deployment ships the releases with the
server and the server runs exactly those. At boot, before it starts the
other addons, the server brings each pinned addon to its pin from
`local:<id>`:

- not installed: installed;
- another version, older or newer: updated to the pinned one (`data/` kept;
  a downgrade keeps `data/` as the newer version left it);
- the same version from other files (the platform archive's or the web
  bundle's sha256 differs from the release's): installed again from the
  release (`data/` kept);
- installed from another source: replaced by the release with its `data/`
  kept; its notifications and file holds are dropped once the release is
  installed (a failed install keeps them);
- then switched on and started.

A pin that cannot be applied (the release is missing from
`VAULT_ADDONS_DEV`, has another version, cannot be installed again, or does
not start) is logged as `addons: pin <id>@<version>: <error>`; the server
keeps serving, and that addon stays stopped, whatever version is installed,
until a later start applies its pin. A malformed value (an empty part, a
part without one `@`, an id named twice, a bad id or version) or a pin while
`VAULT_ADDONS=off` stops the server at start.

The admin API refuses to install, update or remove a pinned addon, and an
install of its id from another source, with `409` «Расширение закреплено при
развёртывании». Switching it off is always allowed; switching it on only
once its pin has been applied since the server started: before that (early
in the start) or after a failed pin it answers `409` «Закреплённая версия
расширения не установлена». The list marks it `"pinned": true` and offers no
update for it, marks a failed pin `"pinFailed": true` (listing it even when
it is not installed), and the app shows it as locked.

## Manifest: `antresol-addon.json`

```json
{
  "id": "hello",
  "name": { "ru": "Привет", "en": "Hello" },
  "description": { "ru": "…", "en": "…" },
  "version": "0.1.0",
  "author": "…",
  "homepage": "https://github.com/…",
  "contract": 1,
  "platforms": {
    "linux/amd64": { "asset": "hello-linux-amd64.tar.gz", "sha256": "<64 hex>" },
    "linux/arm64": { "asset": "hello-linux-arm64.tar.gz", "sha256": "<64 hex>" },
    "darwin/arm64": { "asset": "hello-darwin-arm64.tar.gz", "sha256": "<64 hex>" }
  },
  "web": { "asset": "hello-web.tar.gz", "sha256": "<64 hex>" },
  "permissions": ["events"]
}
```

| Field | Rule |
| --- | --- |
| `id` | `^[a-z][a-z0-9-]{1,39}$`; not `web`, `preview`, `x`, `ui`, `run`, `addon`, `core`, `contract` or `locale`, and not starting with `addon-` or `core-` |
| `name`, `description` | `{ "ru", "en" }`; the app shows the interface language's, else the other; `name` must not be empty in both |
| `version` | semver without a leading `v` (`1.2.3`, `1.2.3-beta.1`); updates must be newer |
| `contract` | `1` |
| `platforms` | `"<GOOS>/<GOARCH>"` → asset, the keys only `linux/amd64`, `linux/arm64`, `darwin/arm64`; a server whose platform is missing refuses the install |
| `web` | optional; no web part when absent |
| `permissions` | any of `events`, `library.read`, `library.write`, `files.holds`, `metadata`, `images`, `play`, `notify`, `users.read`, `tracking`, `imports`, `requests`, `voiceover`, `progress`, each at most once; an unknown or repeated one is refused |
| `access` | optional: `admins` (default) or `adults` - who may open the addon's UI and proxy (see [Who gets an addon](#who-gets-an-addon)); anything else is refused |
| `hooks` | optional list of hooks the addon answers: `files.plan`, `files.pause`, `files.resume`, `files.stop`, `files.detach`, `files.attach`, `play.progress`, `library.pending`, `disk.usage`, `requests.open`, `requests.title`, `requests.search`, `tracking.titles`, `tracking.watched`, `notify.interest`, `voiceover.origLang`, `trailers.fetch`, `import.services`, `import.stop`, `import.resolve`, `import.search`, `migrate.leftovers`, `library.afterScan`, `home.continue`, `channels.ready`, `channels.candidates`, `channels.resolve`, `channels.release`, and `tracking.scan:<kind>` (kind `^[a-z][a-z0-9-]{0,19}$`); unknown or repeated ones are refused, so is a list with some of the four `channels.*` hooks but not all («Нужны все хуки channels.*»), and so is a `kind` that is `library` or another installed addon's (install and update alike, see [Tracking scanners](#tracking-scanners-trackingscankind)) |
| `notify` | optional list of `{ "topic", "title": { "ru", "en" }, "audience" }`; `topic` is `^[a-z][a-z0-9-]{0,29}$` and unique, `audience` is `admins` or `adults` |
| `author`, `homepage` | free text, shown before install |

Unknown fields, and anything after the JSON object, are refused. A platform asset name is a bare file name (no
`/` or `\`). Build `linux/amd64`, `linux/arm64` and `darwin/arm64` so that
the addon runs wherever the server does.

## The process

The server starts `bin/addon` with no arguments, in the data directory, in a
process group of its own, with the server's environment minus every
`VAULT_*` and `ANTRESOL_*` variable except the addon's own `ANTRESOL_<ID>_*`
(below), plus:

| Variable | Value |
| --- | --- |
| `ANTRESOL_ADDON_ID` | the addon's id |
| `ANTRESOL_ADDON_DATA_DIR` | `…/addons/<id>/data` (also the working directory) |
| `ANTRESOL_ADDON_SOCKET` | the Unix socket to serve HTTP on |
| `ANTRESOL_CORE_URL` | where the server listens, for the core API: `http://127.0.0.1:<port>` when `VAULT_LISTEN` binds every address (or names no host), else the host it binds |
| `ANTRESOL_ADDON_TOKEN` | the addon's token for the core API: **a fresh one on every start**; the previous one stops working |
| `ANTRESOL_CONTRACT` | `1` |
| `ANTRESOL_LOCALE` | the server's default language, `ru` or `en` |

The contract's variables:

This list is the code's, line for line; the server's tests compare
the two both ways.

```inventory-env
ANTRESOL_ADDON_ID
ANTRESOL_ADDON_DATA_DIR
ANTRESOL_ADDON_SOCKET
ANTRESOL_CORE_URL
ANTRESOL_ADDON_TOKEN
ANTRESOL_CONTRACT
ANTRESOL_LOCALE
```

- **Own variables.** The server's variables named `ANTRESOL_<ID>_*`, where
  `<ID>` is the addon's id upper-cased with `-` → `_` (`my-addon` →
  `ANTRESOL_MY_ADDON_*`), reach the addon unchanged: an operator configures
  it with them. No other addon gets them: an addon whose prefix starts with
  an installed addon's, or the other way round (`fake` and `fake-x`:
  `ANTRESOL_FAKE_` and `ANTRESOL_FAKE_X_`), is refused at install
  («Расширение «…» мешает «…»: их переменные окружения пересекаются»). The
  variables in the table above always have the server's values; the server
  env cannot replace them.

- **Health.** The program listens on `ANTRESOL_ADDON_SOCKET` and must answer
  `GET /health` with 200 within **15 s** of starting; otherwise the start
  fails and the process is stopped. The socket's path changes with every
  start.
- **Readiness.** An addon whose own start takes longer (a data import)
  answers `/health` with 200 and the JSON `{"ok": true, "ready": false}`
  until it is ready. The server then shows it as starting («Запускается»),
  asks `/health` again every **2 s** with no deadline, and calls it running
  once `/health` answers 200 with anything else: a body that is not JSON or
  has no boolean `ready` means ready, so an addon that never says `ready` is
  running at once. While it is starting its token is accepted by the core
  API, its proxy routes answer 503 «Расширение запускается», its web part is
  not loaded and its hooks count as down. An update stops the previous
  version as soon as the new one answers `/health` with 200, ready or not.
- **Output.** stdout and stderr go to the server's log and to the addon's log
  in «Расширения» (the newest 500 lines).
- **Stop.** SIGTERM to the process group, SIGKILL 10 s later.
- **Crashes.** A process that was healthy and exits is restarted, waiting 1,
  2, 4 … up to 60 s between tries; the wait resets after 5 minutes of running.
  A start that never became healthy is not retried.

## The proxy: the app → the addon

The app reaches the addon at `/api/addons/<id>/x/<path>`, any method; the
server forwards it to `/api/<path>` (query kept) on the addon's socket and
streams the answer back.

- **Who asks.** `X-Antresol-User` carries the signed-in profile as JSON,
  every non-ASCII character escaped as `\uXXXX`:
  `{"id":1,"uid":"…","name":"…","isAdmin":true,"isKid":false,"lang":"ru"}`.
  `uid` is the stable profile key (32 lower-case hex digits): `id` may be
  reused after a profile is deleted, a `uid` never is and never changes.
  Key per-profile rows by `uid`.
  A profile with a rating limit also carries `"maxRating"`, one of
  `0+`, `6+`, `12+`, `16+`, `18+` (a kid without one of its own: `12+`);
  absent or `null` means no limit. It is the limit the server's own rating
  gate applies: a video title or item whose rating is unknown or above it
  is hidden from the profile; music and books without a rating are shown.
  An addon serving such a profile applies the same rule (the server's
  `library.RatingAllowed`).
  A request from outside the home network also carries `"maxHeight"`, the
  height in pixels (`1080`, `720` or `480`) the server's «Качество вне
  дома» caps playback at; it is absent at home, when that setting is
  «Оригинал», and on hooks not run for an app's request (`play.progress`).
  It caps the addon's default («Авто») choice: an addon serving video
  picks no rendition taller than `maxHeight` by default, and takes the
  lower of it and any cap the app sent itself; a quality the viewer chose
  (or chose last for the title) still plays, as in the server's own player.
- **Media routes.** `/api/addons/<id>/x/media/…` is the prefix an addon
  serves its streams under (playlists, segments, subtitles), for every
  addon. There, and nowhere else under `/api/addons`, a **`GET` or `HEAD`**
  request may sign in with a media key (`?mk=<key>`) instead of a session,
  as on the server's own media routes, so that a native player (AirPlay,
  an app's video player) fetches them without the app's headers. Any other
  method needs a session, and so does a path that is escaped otherwise
  than by default (an encoded `/`, `%2F`, anywhere in it). The proxy still
  sends `X-Antresol-User` and keeps the query, `mk` included, and its refusals (kids, access, `x/admin/…`,
  non-canonical paths) still apply; the addon still gates the profile
  itself. The key is the profile's media key for all the server's media
  routes: an addon must not log or store it, and only hands it back in
  the URLs of the same answer (a playlist's segment URIs). An addon is not
  sandboxed (see [Trust](#trust-addons-are-not-sandboxed)), so this gives it nothing it could not reach.
- **No credentials.** The browser's `Cookie` and `Authorization`, and any
  `X-Antresol-*` header the browser sent, are dropped.
- **Answers.** `Set-Cookie`, `Clear-Site-Data`, `Service-Worker-Allowed`
  and every `Access-Control-*` header are dropped;
  `X-Content-Type-Options: nosniff` is set, and an answer that is not JSON (`application/json`, `*+json`) or
  `text/event-stream` also gets `Content-Security-Policy: sandbox`.
- **Requests with a body must be JSON** (`Content-Type: application/json`):
  the server's CSRF rule refuses other bodies on POST, PUT, PATCH and DELETE
  before they reach the proxy. A POST without a body is fine; the SDK's
  `post` always sends JSON.
- **Refusals by the server.** Who may use the proxy is in
  [Who gets an addon](#who-gets-an-addon): a profile it is not for gets
  403 «Недостаточно прав», a kid profile always 403 «Недоступно в детском
  профиле», and **`/api/addons/<id>/x/admin/…` is for admins only**, for
  every addon: the server refuses it to anyone else before the addon sees
  the request. The first segment counts in any case and escaping
  (`x/ADMIN/…`, `x/Admin/…`, `x/%61dmin/…`, `x/admin%2F…`, `x/admin/`,
  `x/admin?…`), and with spaces and dots trimmed from both of its ends
  (`x/admin%20/…`, `x/admin./…`, `x/%20admin/…`, `x/admin%C2%A0/…`).
  A proxied path that is not canonical once decoded gets
  **400** «Не удалось разобрать запрос» from the server, for every profile
  and never reaching the addon. That covers empty or dot segments
  (`x//…`, `x/./…`, `x/a/../…`; one trailing slash is fine), a path
  that still holds `%` (double encoding such as `%252e`), `\` or `;`,
  a path holding a control byte (`%00`-`%1F`, `%7F`, such as `x/admin%09/…`)
  and a path that is not valid UTF-8 once decoded (`x/%C0%AEadmin/…`).
  An addon that is still starting (see
  Readiness) answers **503** «Расширение запускается»; one that is off,
  stopped, crashed or not installed answers **503** «Расширение не запущено»
  (also when it goes away mid-request); one that is running but fails to
  answer within 60 s, 502.
- **Media.** The answer streams as it comes (no buffering), with the
  addon's status and body unchanged: a `410` (a stale stream link), a
  `404` or a `416` reaches the player as the addon sent it, never as a
  502. Range requests pass through. Proxied answers are never compressed
  by the server (a segment or playlist keeps its bytes and
  `Content-Length`); a non-JSON answer still carries
  `Content-Security-Policy: sandbox`, which a player's `fetch` of a
  segment ignores.
- **Connections.** The server keeps up to 16 idle connections to each
  running addon process, so the segments of a stream skip the dial. A
  pool belongs to one addon and one process: a connection is never
  reused for another addon, and a restart (a new process) or a stop
  closes the old process's pool.

## The core API v1: the addon → the server

`ANTRESOL_CORE_URL` + `/api/addon-core/v1/…`, with
`Authorization: Bearer <ANTRESOL_ADDON_TOKEN>`. The token of an addon that is
off or not running is refused (401). Bodies are JSON
(`Content-Type: application/json`), at most 64 KiB (`/library/import`,
`PUT /library/refs/{ref}` and `/library/owners`: 8 MiB; `POST /library/refs`,
`PUT /files/holds` and `PUT /imports/jobs/{id}/snapshot`: 32 MiB; `POST /events`:
1 MiB, over it 413 «Событие больше 1 МБ»). Errors are
`{"error": "<message>"}`.

| Call | Permission | Answer |
| --- | --- | --- |
| `GET /info` | — | `{"id", "version", "contract", "ffmpeg", "ffprobe", "publicUrl", "libraryRoots", "mediaRoot", "fileRoots", "missingRoots"}` (below) |
| `POST /events` `{"name", "data"}` | `events` | 204; the app gets the event (below) |
| `GET /library/tmdb/{movie\|series}/{tmdbId}` | `library.read` | the library's top-level `Item` (below), or 404 |
| `GET /library/tmdb/{movie\|series}?ids=1,2` | `library.read` | `{"<tmdbId>": itemId}` of those the library has; at most 500 ids |
| `GET /library/items/{id}` | `library.read` | `Item` |
| `GET /library/items/{id}/files` | `library.read` | `[File]`: the item's own files, then, for a series or a season, its episodes' files by season, episode, path (with `season`, `episode`) |
| `GET /library/find/title?kind&tmdbId&title&creator` | `library.read` | `Item`: a movie or series by `tmdbId`, an album (`music`) by artist (`creator`) and title, a book or audiobook by title and, optionally, author (`creator`); another `kind` or a missing field is 400 |
| `GET /library/find/album?artist&album` or `?mbid=` | `library.read` | `Item` (album; `mbid` is a MusicBrainz release group) |
| `GET /library/find/book?kind=book\|audiobook&author&title` | `library.read` | `Item` |
| `GET /library/series/{tmdbId}/episodes` | `library.read` | `[[season, episode]]` that have a file, in order |
| `GET /library/series/{tmdbId}/episode-items?userId=` | `library.read` | `{"episodes": [{"season", "episode", "itemId"}]}`: the pairs of `/episodes` in the same order, each with the id of the episode item that holds its file (the id `GET /progress` and `PUT /progress` take); the later episodes of a multi-episode file (`S01E01-E02`) carry the id of the item that holds it, unless an episode item of their own has a file. For profile `userId`, an adult one: a missing or unknown id is 400 «Нет такого профиля», a kid 400 «Детскому профилю это недоступно». A series over the profile's rating limit (or one with no rating, for a limited profile) answers `{"episodes": []}`; no series with that TMDB id is 404 «Не найдено», as on `/episodes` |
| `GET /library/series/{tmdbId}/folder?root=` | `library.read` | `{"path"}`: the absolute folder directly under `root` holding the series; `root` must be one of `libraryRoots` as given (else 400) |
| `GET /library/series/{tmdbId}/audio` | `library.read` | `SeriesAudio`; its `profiles` only when the addon also has `users.read` (left out otherwise) |
| `GET /library/albums/{id}/dir` | `library.read` | `{"path"}`: the album's folder |
| `GET /library/albums/{id}/tracks` | `library.read` | `[Track]` of every album in that folder, in album and playing order |
| `GET /library/files?under=<dir>` or `?path=<file>` | `library.read` | `[File]` below the folder, any depth, **without `probe`** (a whole root's summaries would run to hundreds of MB; fetch one file's with `?path=` or `/items/{id}/files`; the listing itself is not paginated and grows with the folder's file count), or the one file with it |
| `GET /library/files/{id}` | `library.read` | `File`, with `probe`; a file the library does not have, or one missing from disk, is 404 «Файл не найден» |
| `GET /library/paths` | `library.read` | `[path]` of every library file, missing ones too, in path order (streamed) |
| `GET /library/search?kind&title&limit` | `library.read` | `[Item]` of `kind` whose title, original title or alias equals `title` once normalised (case, ё, punctuation); `limit` at most 50, the default |
| `GET /library/refs/{ref}` | `library.read` | `[RefFile]` (`{"fileId", "itemId", "path"}`) of the calling addon's files with this ref, by path |
| `GET /library/tracks?ref=` | `library.read` | `[Track]` whose file carries the calling addon's ref |
| `GET /users` | `users.read` | `[User]` (`{"id", "uid", "name", "isAdmin", "isKid", "lang", "languages", "maxRating"}`); `uid` is the stable profile key, as in `X-Antresol-User` (`id` may be reused after a profile is deleted); `lang` is the interface language the apps show the profile in: its own choice, else the first of its own audio languages the apps have (`ru`, `en`), else the first of the household's, else the server's; `languages` is the profile's «Языки», its audio languages in order (`["en", "ru"]`): its own list, else the household's «Мои языки»; `[]` only when both are empty. Only this answer fills `languages`: in the `X-Antresol-User` header it is absent or `null`; `maxRating` is the profile's rating limit (`0+`, `6+`, `12+`, `16+`, `18+`; a kid without one of its own: `12+`), the same as in the header, and is omitted for a profile without one |
| `GET /settings/studios` | `library.read` | `{"studios": [name]}`: the household's favourite studios (the Settings screen's «Любимые озвучки»), best first, as saved there; `[]` when none |
| `GET /watched?userId&items&series` | `library.read` | `{"watched": {"<id>": true}}`: of the item ids (`items`: movies and other leaves, finished) and series ids (`series`: every episode finished), comma-separated, the ones profile `userId` watched; the rest are left out. Pass the id of the request's `X-Antresol-User`. At most 500 ids per list (400 «Не больше 500 id за раз»); a missing or unknown `userId` is 400 «Нет такого профиля» |
| `POST /library/import` `{"paths", "ref", "ownerUserId"}` | `library.write` | `ImportResult` (below): at most 10 000 paths, files or folders; `ref` and `ownerUserId` optional |
| `PUT /library/refs/{ref}` `{"paths"}` | `library.write` | `Bound` (below): afterwards the addon's `ref` is on exactly the listed files (at most 10 000); its other files with `ref` lose it; `paths` is required, `[]` clears the ref |
| `DELETE /library/refs/{ref}` | `library.write` | 204: the addon's `ref` leaves its files (the files stay) |
| `POST /library/refs` `{"refs": [{"ref", "path"}]}` | `library.write` | `Bound`: each path bound to its own ref, all at once (at most 500 000 pairs; a path given twice counts once, under its last ref) |
| `POST /library/owners` `{"ref"` or `"paths", "userId"}` | `library.write` | 204: profile `userId` becomes an owner of the titles of the addon's files with `ref`, or of the library files at `paths` (those it has); exactly one of `ref`, `paths`; an unknown profile is 400 |
| `POST /images` `{"url"}` | `images` | `{"name"}`: the `https` image (at most 20 MiB) in the server's image cache, shown by the app at `/api/images/{name}`; another scheme is 400; a failed download, or a redirect off `https`, 502 |
| `PUT /files/holds` `{"holds": [Hold]}` | `files.holds` | 204: the addon's whole set of holds (below) is replaced; `[]` clears it; `holds` is required. Any refused hold or path is 400 and the previous set stays; 503 when the server keeps no holds |
| `GET /files/holds` | `files.holds` | `{"holds": [Hold]}`: the addon's set as the server keeps it (saved across restarts, holds under a root missing on disk included, paths in the file browser's form), `[]` when none; 503 when the server keeps no holds |
| `GET\|HEAD /metadata/{source}/<path>?<query>` | `metadata` | the provider's own answer to `GET <path>?<query>` (below): its status, `Content-Type` and body (at most 8 MiB) |
| `POST /play/inputs` `InputRequest` | `play` | 201 `InputCreated` (`{"id", "expiresAt"}`): an external video the addon serves, registered for one profile to play (below); body at most 64 KiB |
| `DELETE /play/inputs/{id}` | `play` | 204: the addon's input is forgotten; another addon's or an unknown id is 404 «Видео не найдено» |
| `POST /notify` `NotifyRequest` | `notify` | 202 `{"queued": n}`: a line for the mail digests of the profiles that have the topic on (below); 503 when the server sends no letters |
| `POST /requests/approve` `ApproveRequest` | `requests` | `{"approved": n}`: the kids' requests of that title approved (below); 503 when the server has no kids' requests |
| `POST /tracking/titles` `{"kind", "tmdbId", "name", "poster"}` | `tracking` | `Title` (below): the tracked title, made when absent |
| `GET /tracking/titles?kind&tmdbId` | `tracking` | `Title`, or 404 «Тайтл не отслеживается» |
| `GET /tracking/titles/{id}` | `tracking` | `Title`, or 404 |
| `POST /tracking/titles/{id}/kick` | `tracking` | 204: the title is refreshed soon; an unknown id is 404 |
| `GET /tracking/titles/{id}/episodes` | `tracking` | `[Episode]`: its episode list, by season and number |
| `GET /tracking/titles/{id}/sources` | `tracking` | `[Source]`: its sources of every kind, by season, episode, kind and ref |
| `POST /voiceover/fit` `{"path", "fileId", "origLang"}` | `voiceover` | 202: the voice-over track at `path`, beside the library file `fileId`, is queued for its fit (below); anything else is 400 «Файл не найден в медиатеке»; 503 when the server fits nothing |
| `GET /voiceover/fit?path=` | `voiceover` | `FitState` (`{"state", "offsetMs", "tempo"}`, below), or 404 «Озвучка не найдена» when the track is not queued; 503 when the server fits nothing |
| `GET /progress?userId&items=` | `progress` | `{"progress": {"<itemId>": ProgressEntry}}` (below): profile `userId`'s progress in the movies and episodes among `items` (comma-separated, at most 500: 400 «Не больше 500 id за раз») it may see; an id with no progress, over the profile's rating limit, or no movie or episode is left out; a missing or unknown `userId` is 400 «Нет такого профиля», a kid 400 «Детскому профилю это недоступно» |
| `PUT /progress` `{"userId", "itemId", "positionMs", "durationMs", "at", "played"}` | `progress` | 204: the profile's position in a movie or an episode it may see, as the apps' players save it (below); body at most 64 KiB |
| `GET /imports/active-count` | `imports` | `{"count"}`: the albums of running import jobs being searched or downloaded (below) |
| `GET /imports/jobs?status=` | `imports` | `{"ids"}` of the import jobs in that status, oldest first |
| `GET /imports/jobs/{id}` | `imports` | `ImportJob` (below), or 404 «Не найдено» |
| `GET /imports/jobs/{id}/candidates?retryBefore=` | `imports` | `[ImportGroup]` a queue may start, oldest first: `waiting`, `waiting_space`, `waiting_download`, and `failed` ones due for their automatic retry (failed once, last changed at or before `retryBefore`, RFC 3339) |
| `GET /imports/jobs/{id}/groups?state=` | `imports` | `[ImportGroup]` of the job in that state (none: all), oldest first |
| `GET /imports/jobs/{id}/still-downloading` | `imports` | `{"downloading"}`: the job was not cancelled meanwhile |
| `GET /imports/groups/{id}/tracks?state=` | `imports` | `[ImportTrack]` of the album the job imports, in those states (`state` repeated) |
| `POST /imports/groups/{id}/move` `{"from", "to"}` | `imports` | 204, or 409 «Альбом уже взят» when it was not in `from` (compare-and-set) |
| `POST /imports/groups/{id}/wait` `{"key", "name", "done"}` | `imports` | 204: the album waits for the download `key` |
| `POST /imports/groups/{id}/give-up-wait` `{"key"}` | `imports` | 204: it waits for its own search, which leaves `key` out |
| `POST /imports/groups/{id}/start` `{"key", "bytes", "downloading", "missing"}` | `imports` | 204, or 409 «Альбом уже взят» when it is no longer `searching` |
| `POST /imports/groups/{id}/clock` `{"key", "clock"}` | `imports` | 204: the clock of its download `key` |
| `POST /imports/groups/{id}/given-up` `{"key", "slow"}` | `imports` | 204: `key` is listed among its dead (or slow) downloads |
| `POST /imports/groups/{id}/requeue` `{"key", "msg", "maxDead"}` | `imports` | 204: back in the queue, or failed with `msg` at its `maxDead`-th dead download |
| `POST /imports/groups/{id}/no-format` `{"trackIds", "msg"}` | `imports` | 204: settled `no_format` |
| `POST /imports/groups/{id}/fail` `{"trackIds", "msg", "retry"}` | `imports` | 204: settled `failed` |
| `POST /imports/groups/{id}/finish` `{"done", "failed", "album"}` | `imports` | 204: settled `done` |
| `POST /imports/groups/{id}/have` `{"have", "album", "done"}` | `imports` | 204: songs the library has are linked |
| `POST /imports/groups/{id}/not-found` `{"trackIds", "msg"}` | `imports` | 204: settled `not_found` |
| `POST /imports/groups/{id}/progress` `{"done"}` | `imports` | 204: the bytes of its download on disk, for the album list |
| `POST /imports/jobs/{id}/rebuild` | `imports` | 204: the job's playlists and stars from what it has brought |
| `GET /imports/open?userId&service` | `imports` | `{"open"}`: the profile has a job of the service in preview, matching or downloading |
| `POST /imports/jobs` `{"userId", "service", "reading": true}` | `imports` | `{"id"}`: an empty job in `reading`; a kid's `userId` is 404 |
| `PUT /imports/jobs/{id}/snapshot` `{"snapshot"}` | `imports` | 204: the reading job moves to `preview`; one no longer reading is 409; at most 32 MiB |
| `POST /imports/jobs/{id}/fail` `{"error"}` | `imports` | 204: the reading job failed; one no longer reading is 409 «Импорт уже начат» |
| `POST /imports/jobs/fail-reading` `{"error"}` | `imports` | `{"ids"}`: every job still reading failed — the server's own reads too (see [Imports](#imports), reading jobs) |
| `DELETE /imports/jobs/{id}` | `imports` | 204; a matching or downloading job is 409 «Импорт уже идёт» |
| `GET /imports/reads?userId` | `imports` | `{"ids"}` of the profile's jobs still reading — the server's own reads too |

Every route of the core API, as the server mounts it under
`/api/addon-core/v1` (`{kind}` is `movie` or `series`; `/metadata/{source}`
with and without a path). The list below is the code's, line for line; the server's tests compare
the two both ways.

```inventory-routes
GET /info
POST /events
GET /library/tmdb/{kind}
GET /library/tmdb/{kind}/{tmdbId}
GET /library/items/{id}
GET /library/items/{id}/files
GET /library/find/title
GET /library/find/album
GET /library/find/book
GET /library/series/{tmdbId}/episodes
GET /library/series/{tmdbId}/episode-items
GET /library/series/{tmdbId}/folder
GET /library/series/{tmdbId}/audio
GET /library/albums/{id}/dir
GET /library/albums/{id}/tracks
GET /library/files
GET /library/files/{id}
GET /library/paths
GET /library/search
GET /library/refs/{ref}
GET /library/tracks
GET /users
GET /settings/studios
GET /watched
POST /library/import
PUT /library/refs/{ref}
DELETE /library/refs/{ref}
POST /library/refs
POST /library/owners
POST /images
PUT /files/holds
GET /files/holds
GET /metadata/{source}
HEAD /metadata/{source}
GET /metadata/{source}/*
HEAD /metadata/{source}/*
POST /play/inputs
DELETE /play/inputs/{id}
POST /notify
POST /requests/approve
POST /tracking/titles
GET /tracking/titles
GET /tracking/titles/{id}
POST /tracking/titles/{id}/kick
GET /tracking/titles/{id}/episodes
GET /tracking/titles/{id}/sources
POST /voiceover/fit
GET /voiceover/fit
GET /progress
PUT /progress
GET /imports/active-count
GET /imports/jobs
GET /imports/jobs/{id}
GET /imports/jobs/{id}/candidates
GET /imports/jobs/{id}/groups
GET /imports/jobs/{id}/still-downloading
GET /imports/groups/{id}/tracks
POST /imports/groups/{id}/move
POST /imports/groups/{id}/wait
POST /imports/groups/{id}/give-up-wait
POST /imports/groups/{id}/start
POST /imports/groups/{id}/clock
POST /imports/groups/{id}/given-up
POST /imports/groups/{id}/requeue
POST /imports/groups/{id}/no-format
POST /imports/groups/{id}/fail
POST /imports/groups/{id}/finish
POST /imports/groups/{id}/have
POST /imports/groups/{id}/not-found
POST /imports/groups/{id}/progress
POST /imports/jobs/{id}/rebuild
GET /imports/open
POST /imports/jobs
PUT /imports/jobs/{id}/snapshot
POST /imports/jobs/{id}/fail
POST /imports/jobs/fail-reading
DELETE /imports/jobs/{id}
GET /imports/reads
```

A call without its permission gets 403, before anything else is checked.
Something the library does not have is 404; a malformed query, more ids or
results than allowed, a bad ref, is 400. Without a library on the server
the library calls answer 503.

The library's JSON is camelCase:

- `Item`: `{"id", "kind", "parentId", "title", "originalTitle", "year",
  "rating", "tmdbId", "seasonNo", "episodeNo", "poster", "addedAt",
  "meta", "aliases"}` (`meta`: the item's stored metadata object, left out
  when none; `aliases`: the item's other titles the library knows, its
  title and original title left out, in the order it stores them, `[]`
  when none: only on `GET /library/items/{id}`, `GET /library/tmdb/…` and
  `GET /library/search`, left out of the other answers).
- `File`: `{"id", "itemId", "path", "size", "mtime", "durationMs",
  "season", "episode", "probe"}` (`probe`: the stored ffprobe summary, left
  out when none; `season`, `episode` only in a series' file list, else 0).
- `Track`: an `Item` plus `{"albumId", "artistId", "fileId", "artist",
  "album", "disc", "no", "startMs", "endMs"}`.
- `SeriesAudio`: `{"files": [{"season", "episode", "titles"}], "profiles":
  [{"userId", "audioTitle", "updatedAt"}]}`: the audio track titles of each
  episode file, and the voice each profile last chose, the latest first.
  The profiles name user ids, kids' too: they need `users.read` as well.

**Paths.** A path the addon gives (`under`, `path`) is absolute and clean
(`filepath.Clean` leaves it as it is), and it lies inside one of
`libraryRoots` both as written and with every symlink resolved; else the
call is 400. A path that is not on disk is checked
through its deepest existing parent folder: one inside the roots is not
refused, it just has no files (`[]`). An entry that exists but cannot be
resolved (a dangling symlink) is refused. The writing calls
(`/library/import`, `/library/refs`, `/library/owners`) check every path
before doing anything, and take files and folders strictly below a
root, never a root itself (as written or once resolved): the first path
outside, or a root, answers 400
(`Путь вне медиатеки: <path>`) and nothing of the call is applied. They
never move, overwrite or delete a file on disk: they scan and record.

**Import.** Once its paths are checked, an import runs to its end even if
the addon hangs up, one import at a time on the server: the library is
scanned (all of it), the library files at `paths` (for a folder, every
file below it) get `ref` and the owner, the library's metadata pass is
woken and `library.updated` sent to the app. `ImportResult` is
`{"files": [{"path", "fileId", "itemId"}], "missing": [path], "taken":
[path]}`: `missing` are the paths not on disk or that are no library file
after the scan (not media), or folders with no library file below them,
each once; `taken` the files another addon references (with or without
`ref`), which keep that reference and are not claimed for the owner. A scan takes
as long as the library's size makes it: allow minutes.

**Metadata pass-through.** `source` is `tmdb` (`<path>` below
the TMDB v3 API, such as `/movie/550`), `musicbrainz` (below web service
v2, such as `/release-group/<mbid>`; add `fmt=json`), `openlibrary` or
`wikidata` (below their sites' roots, such as `/works/OL1W.json`,
`/w/api.php`): the server's own addresses of those services. The server sends the request
with its own key, User-Agent and rate limits and repeats it after the
provider's 429/503 as for its own lookups; the addon never sees the key
(it is not in the answer, its headers or the errors). Only the token is
read from the addon's request: its `Authorization`, cookies and any
`api_key` of the query are not forwarded, and no other header is. A redirect to another host is not followed: its
3xx is the answer. Addons
share a budget of their own per provider (two calls in flight at once),
so their traffic never holds back the library's own lookups; MusicBrainz,
Open Library and Wikidata keep one rate limit for the whole server, so
on those the addons' calls take turns with the library's. A source the
server has off (TMDB without a key) or does not know is 404 (`Источник
выключен`); a path that is empty, holds `..` or `.` as a segment in any
percent-encoding (`%2e%2e`, `%252e%252e`), a backslash (`\`, `%5c`) or an
empty segment (`//host`) is 400; a provider that cannot be reached, or an
answer over 8 MiB, is 502 (`Источник не ответил`). Other methods are
405. The source is checked before the path: `HEAD /metadata/{source}`
with no path is 404 for a source that is off and 400 for one that is on,
and asks the provider nothing, so an addon can tell which sources it has. TMDB's images (another host) are not proxied: put them in the image
cache with `POST /images`.

`Bound` is `{"bound", "missing", "taken"}`: how many files got the ref,
the paths with no library file, and those another addon references.

**Repeats.** In every writing call a path given twice counts once, and so
does a file reached twice (a folder and a file below it): it is in
`files`, `missing` or `taken` at most once and `bound` counts it once. In
`POST /library/refs` the last ref given for a path wins.

A ref in a path is percent-encoded as any path segment (`/` as `%2F`).

`/info`: `ffmpeg` and `ffprobe` are the paths of the server's tools;
`publicUrl` is the server's public address (`VAULT_PUBLIC_URL`, `""` when
unset); `libraryRoots` is `{"movies", "series", "music", "ebooks",
"audiobooks", "comics"}`, the library's absolute folders (`""` when unset).
`mediaRoot` is the server's media folder (`VAULT_MEDIA_ROOT`, absolute; `""`
when unset): an addon takes its own relative folders against it.
`fileRoots` is `[dir]`, the file browser's root folders, admin-only ones
included, **with their symlinks resolved** (`EvalSymlinks`), each once, in
the browser's order; a root not on disk is left out (it is resolved again on
every call). This is the form `PUT /files/holds` checks a hold in: it
resolves the hold's `root` and compares it with the browser's roots resolved
(below). So an addon that keeps only the holds the server accepts resolves
each hold's `root` the same way (`EvalSymlinks` of its deepest existing
folder, plus the rest) and keeps those at or inside one of `fileRoots`. The
server's own spelling of a stored hold (`Get`, the hold events: the root as
configured) may differ from `fileRoots` when a root is a symlink.
`missingRoots` is `[dir]`, the file browser's roots **not on disk** right now
(their symlinks do not resolve: a mount not up yet), as configured, each
once, in the browser's order. A hold whose `root` is at or inside one of
them is accepted as written (absolute, clean, its files inside it, none of
them the missing root itself) and kept: it protects the path once it is
there. Keep such holds in the set you push; never leave them out.

### File references and holds

The server keeps, per library file, at most one addon **reference**: a
string of the addon's choosing (a *ref*) that ties the file to the addon,
so the addon can find the files it brought in, and the server leaves them
alone where they share data with the addon (the server never re-encodes
such a file into another container). A ref, and a hold's key, is 1–128
printable ASCII characters (space to `~`).

- A file another addon references is **never re-bound**: binding it reports
  the path in `taken` and leaves it as it is. An addon may move its own
  file to another of its refs.
- A path with no library file is reported in `missing`.
- A reference goes with its library file.
- **A hold's key equals its ref:** the library files an addon imported from
  data it holds carry the hold's key as their ref.
- The server keeps each addon's last set of holds across its own restarts
  and while the addon is down. A saved hold that is no longer valid (its
  root left the file browser) is skipped and logged on load; the addon's
  other holds stay. A root that is only missing on disk (a mount not up
  yet) is not a reason: its holds are kept.
- A hold's `root` is absolute, clean and at or inside one of the file
  browser's roots (resolved: `/info`'s `fileRoots`); each of its `files` is absolute, clean, at or inside
  its `root` and strictly below a file-browser root (a listed path that is
  a root itself, as written or with its symlinks resolved, is refused). A
  hold whose `root` is at or inside one of `/info`'s `missingRoots` is
  accepted as written instead (nothing there to resolve yet). A
  listed path that is a **folder covers everything below it**: the server
  treats every file under it as held.
- **Listed paths are resolved when pushed:** the server resolves the symlinks
  of a hold's `root` and of each listed path (and of the file browser's
  roots) and stores each real path spelled below the root as the file
  browser names it (the root as configured, even a symlink); `Get` and the
  hold events report that form, not the pushed one. So the hold protects the
  real path however it was spelled: a path listed through a symlinked folder holds
  the real folder's file, and a move of the real folder is refused. A path
  that resolves outside the hold's root or the file browser is refused.
- Paths match **byte for byte**: no case folding, no Unicode
  normalization. Give a hold's paths exactly as the file system spells
  them.

A **hold** (`PUT /files/holds`) is data the addon still needs, outside the
library or inside it:

```json
{"key": "k1", "root": "/data/dl/Pack", "active": true, "itemId": 0, "busy": false,
 "files": [{"path": "/data/dl/Pack/a.mkv", "size": 1024, "selected": true}],
 "label": {"ru": "…", "en": "…"},
 "moveWarning": {"ru": "…", "en": "…"}, "keptWarning": {"ru": "…", "en": "…"}}
```

- At most 20 000 holds and 200 000 files in a set; keys unique in it.
  Every `root` and file path is absolute, clean and, its symlinks
  resolved, at or inside a root of the file browser (or, as written,
  under one of `missingRoots`); a path that is not on
  disk yet is accepted. Push the whole set again after every change, and on
  start.
- **Protected broadly.** A held path, any folder up to its `root`, and
  everything below a listed folder cannot be moved, renamed or trashed
  without the admin confirming (the warning is `moveWarning`, `{name}`
  replaced by the file's name; without one, a neutral text naming the
  addon), and then only once the addon agreed through its `files.*` hooks
  (below). An unselected file is protected too; an unlisted file under a
  `root` is not.
- **Trashed narrowly.** Deleting a library item takes along a hold whose
  `itemId` is the item (or one of its seasons, episodes, tracks) or whose
  `key` is the ref of a deleted file, once everything the library made from
  it goes: only the hold's **selected regular files** (not folders, not
  symlinks) go to the trash, never its `root`, never anything else under
  it. A hold with no selected file loses nothing; a **single-file hold**
  (`root` is the file itself) is never trashed by an item delete. `busy`
  refuses the delete (the data is being put into the library). When the
  item's delete leaves the hold's data, `keptWarning` is the note.
- The file browser shows `label` as a badge on held files and folders
  (`active` false: the addon holds the data but does not use it now).
  Kid profiles have no file browser. `label`, `moveWarning` and
  `keptWarning` are shown as refusal texts are: without control
  characters, trimmed, cut to 500 characters.
- With `VAULT_ADDONS=off` the server keeps no holds (it does not load
  them).

### Events

`POST /events` with `{"name": "ping", "data": {…}}` publishes the event
`addon.<id>.<name>` to the app's event stream, `data` as given.

- `name` matches `^[a-z0-9][a-z0-9.-]{0,39}$`; anything else is 400.
- The body is at most 1 MiB (a list of several hundred rows fits); a
  bigger one is 413 «Событие больше 1 МБ» and nothing is published; one
  that is not JSON is 400.
- A name starting with `admin.` (`addon.<id>.admin.<rest>`) reaches admins
  only; the other names reach admins and, when the addon's `access` is
  `adults`, adult profiles.
- **Kid profiles never get addon events.**

In the web part, list the short names in `registerAddon`'s `events`
(`events: ['ping']`); the app then subscribes to `addon.<id>.ping` (and, for
admins, `addon.<id>.admin.ping`), and `useEvents` delivers them.

### Notifications

The manifest's `notify` topics are mail topics of the server's
«Уведомления на почту». Each profile its `audience` admits (`admins`: admin
profiles; `adults`: every adult profile; **never a kid**) sees the topic,
named by its `title`, among its notification settings, and turns it on or
off there. Until a profile chooses, a topic takes the value of the
server's own topic of the same name, when there is one (`episodes`,
`available`, `requests`, `downloads`: a topic that moved into an addon
keeps the profiles' choice), and is off otherwise; so name a new topic
apart from those unless it carries one of them over. A topic is listed
while the addon is installed and on (a crashed addon keeps it) and has the
`notify` permission. `POST /notify` puts
one line in the next digest of each recipient that has an address and the
topic on:

```json
{"topic": "news", "key": "release-42",
 "subject": {"ru": "Готово", "en": "Done"}, "body": {"ru": "…", "en": "…"},
 "link": "/item/3", "poster": "https://image.tmdb.org/t/p/w185/x.jpg",
 "audience": "admins"}
```

- `topic` must be one of the manifest's (else 400 «Расширение не объявило
  эту тему уведомлений»).
- Recipients: at most one of `userIds: [id]` and `audience`. Without
  either, everyone the topic's audience admits. `audience` may only narrow
  the topic's (an `adults` topic may go to `admins`, not the other way);
  every profile in `userIds` must be one the audience admits. Anything else
  is 400 «Неверная аудитория уведомления»; a kid profile in `userIds` is
  400 «Детскому профилю это недоступно». Nothing is queued on a 400.
- `key` (1 to 200 characters) names the thing: a key the addon sent a
  profile before (waiting, or sent in the last 90 days) makes no second
  line. Keys are the addon's own, per topic: an equal key of another topic
  or another addon is another thing.
- `subject` (at most 200 characters per language, one language at least)
  is the line, `body` (optional, at most 2000) the detail under it; the
  letter takes the profile's language, else Russian, else English.
  Otherwise 400 «Неверный текст уведомления».
- `link` (optional) is a page of the app: it starts with `/` but not `//`
  (letters never link to another site; else 400 «Ссылка уведомления должна
  вести на страницу приложения»). `poster` (optional) is shown only when it
  is a TMDB image over https; any other is dropped.
- The answer `queued` counts the profiles the line was queued for (0 when
  nobody has the topic on, or every one already had the key).

A letter lists the addons' topics after the server's own, by addon id, then
in manifest order, each a section headed by its `title`, with its own
«Больше не присылать» link; the mail app's one-click unsubscribe turns
them off too. Removing the addon forgets the profiles' choices for its
topics and its lines not sent yet.

### Tracked titles

The server tracks the series and movies the profiles mark: for each title
it keeps an episode list, from TMDB, and the sources of each episode, one set per kind (`library`,
and the kinds of the addons' scanners). An addon with
`tracking` makes, finds and reads those titles; 503 «Отслеживание
недоступно» when the server tracks nothing.

- `POST /tracking/titles` with `{"kind": "series", "tmdbId": 126308,
  "name": "Сёгун", "poster": "/x.jpg"}` answers the title of that `kind`
  (`series` or `movie`, else 400 «Неизвестный раздел») and TMDB id (> 0,
  else 400 «Неверный id»), made when the server has none. `name` and
  `poster` (a TMDB image path) are optional and kept only when the title
  is made; a refresh fills the ones left empty. Making a title marks it
  for nobody.
- `GET /tracking/titles?kind&tmdbId` and `GET /tracking/titles/{id}` find
  a title; one the server does not have is 404 «Тайтл не отслеживается».
  A malformed `kind` or id is 400.
- `POST /tracking/titles/{id}/kick` asks for the title's refresh: its
  episode list and every kind's sources, in the background, soon (one
  refresh for several kicks). It answers 204 at once, without waiting for
  the refresh; an id the server does not have is 404 «Тайтл не
  отслеживается» (make it first with `POST /tracking/titles`).
- `GET /tracking/titles/{id}/episodes` and `/sources` read what the last
  refresh stored (`[]` when nothing); an unknown id is 404. `tracking`
  reads the sources of every kind, the library's included (their
  `itemId` and `fileId`), not only the addon's own.

The JSON is camelCase:

- `Title`: `{"id", "kind", "tmdbId", "imdbId", "name", "poster",
  "status", "cert"}`; `status` is `airing`, `ended`, `upcoming` or
  `unknown`, `cert` the TMDB age certification in its Russian form
  (`"16+"`, `""` when not known). The ids of the list services the server
  syncs marks with are not part of it.
- `Episode`: `{"titleId", "season", "number", "name", "airDate"}`
  (`airDate`: `YYYY-MM-DD` or `""`).
- `Source`: `{"titleId", "season", "episode", "kind", "ref", "provider",
  "quality", "labels", "meta", "firstSeen", "lastSeen"}`; `ref` is unique
  per title, kind, season and episode; `firstSeen` and `lastSeen` are when a
  refresh first and last found it. `meta` holds what applies, the rest
  left out: `itemId`, `fileId` (a `library` source: the episode's item
  and file), `sourceId` (the id the scanner gave it), `providerId` (the
  provider of the source, as the scanner names it), `voice`, `lang` (ISO 639-1), `audio`
  (`Dub`, `MVO`, `AVO`, `Original`), `studios`, `subs`, `fileIndex` (the
  episode's file within the source).

### Voice-over fitting

An external voice-over track (an `.mka`, `.ac3`… beside a video, see the
players' external audio) plays only once the server has fitted it: measured
against the video's track in the title's original language, the offset and
tempo it plays at found, or found to be of another cut. An addon with
`voiceover` that places such a track queues its fit and reads how it went;
503 «Подгонка озвучек недоступна» when the server fits nothing (it has no
ffmpeg).

- `POST /voiceover/fit` with `{"path": "/m/Show/S01E01.rus.mka", "fileId": 7,
  "origLang": "ja"}` queues the track at `path` (placed, or about to be:
  queue it just before linking it, the players leave it out until it is
  fitted) beside the library file `fileId`, and answers 202 at once; the
  fit runs in the background, soon, even while something plays. `path`
  must be absolute and clean (`filepath.Clean` leaves it as it is: no
  `..`), in the very folder of that file, with an audio extension the
  players know, and not the file itself; an unknown `fileId` (or one
  missing from disk) or any other `path` is 400 «Файл не найден в
  медиатеке». `origLang` (ISO 639, two or three letters, `""` when not
  known; anything else is 400) picks the track measured against; without it the server asks
  [`voiceover.origLang`](#voiceoveroriglang), then falls back to the
  first track that is not Russian, English first (a track that does not
  match that one is `unmeasured`, not `misfit`). Queueing a track again
  starts its fit again.
- `GET /voiceover/fit?path=` reads a queued track's fit: 404 «Озвучка не
  найдена» when it is not queued (or its library file left the library). A track or
  video that changes afterwards is fitted again (`pending` meanwhile).

`FitState`: `{"state", "offsetMs", "tempo"}`; `state` is `pending`
(queued, being fitted, or fitted again), `ok` (fitted: `offsetMs`,
positive when the track plays later on the video's timeline — so a track
whose sound starts 1.5 s after the video's fits at `-1500`, played 1.5 s
earlier — and `tempo`,
an atempo factor, `1` none), `misfit` (another cut: never played) or
`unmeasured` (its sync could not be checked: no track in the original
language, or the fit failed for good: never played). `offsetMs` and
`tempo` are `0` except for `ok`.

### Watch progress

An addon that plays titles of its own keeps a profile's place in the
library's movies and episodes where the apps keep it: a position it saves
is the one «Продолжить», the watched marks and the apps' players read, and
the one it reads back is where the apps would resume. It needs `progress`.

- `GET /progress?userId=2&items=7,8,9` answers `{"progress": {"7":
  {"positionMs": 600000, "durationMs": 8160000, "watched": false,
  "updatedAt": "2026-11-17T10:00:00Z"}}}`. `ProgressEntry`: `positionMs`
  and `durationMs` as saved, `watched` (the player's rule: from 92% of a
  known duration, or marked watched), `updatedAt` when the position was
  recorded (RFC 3339). An id the profile has no progress in is left out.
  So is one it may not see (over its rating limit) or that is no movie or
  episode: the answer says nothing of what was watched there, and is not
  an error. At most 500 ids per call.
- `PUT /progress` with `{"userId": 2, "itemId": 7, "positionMs": 600000,
  "durationMs": 8160000, "at": "2026-11-17T10:00:00Z", "played": false}`
  answers 204. `at` is when the position was reached (RFC 3339, optional:
  now); one more than 5 minutes ahead of the server's clock is 400 «Время
  позиции опережает часы сервера больше чем на 5 минут», one a little ahead
  is kept as now. A write older than the position the server keeps for the
  item (or than the last time the item was marked unwatched) changes nothing
  and is still 204, so a queue replayed later never overwrites fresher
  progress from another device. A negative position or duration, or an
  `itemId` or `at` that is not one, is 400. `played: true` says the
  position is a player's report: the time played since the previous report
  of the item also counts in the profile's «Статистика»; `false` is a
  position copied from elsewhere (it adds no played time).
- **Which profile.** `userId` is an adult profile (an admin included): a
  missing or unknown one is 400 «Нет такого профиля», a kid 400 «Детскому
  профилю это недоступно», on both calls.
- **Which item.** A movie or an episode the profile may see, by the rule
  the apps use (its rating limit, `maxRating` of `GET /users`: an episode
  takes its series' rating, a film without a rating is hidden from a limited
  profile). One over the limit, or that does not exist, is 404 «Элемент
  медиатеки не найден» (a hidden item reads as a missing one); a season, a
  series, an album, a book or anything else it may see is 400 «Это не фильм и
  не серия».
- **An episode's item.** `GET /library/series/{tmdbId}/episode-items?userId=2`
  (`library.read`) answers `{"episodes": [{"season": 1, "episode": 2,
  "itemId": 7}]}`: the id to read and save an episode's progress under, for
  each season and episode of the series that has a file, in that order. The
  two episodes of one file (`S01E01-E02`) have the same `itemId`. It lists
  what the profile may see, by the same rule: a series over its rating limit
  has none (an empty list, not an error). `userId` is as above.

### Imports

«Импорт медиатеки» is the server's: it keeps an import's jobs, matches
their songs on MusicBrainz, groups them by album (`ImportGroup`) and
rebuilds the playlists and stars. The albums the library lacks can be
fetched by an addon with `imports` that declares `import.services`: while
one is installed and on (running or not), the server leaves those albums
`waiting` for it; while none is, it settles them by the library alone
(`not_found` «Нет в медиатеке»; «Повторить» puts one back in the queue).
503 «Импорт недоступен» when the server runs no imports.

The import hooks have **no first-party answerer until a later stage; with none, the core degrades:**

- the Import page offers the file import alone (no addon's way in);
- the albums the library lacks are settled `not_found` «Нет в медиатеке»
  by the library alone;
- `GET /api/import/services` lists no service (`"services": []`,
  `downloads` and `catalog` false);
- `GET /api/import/search` is a 503 «Каталог недоступен — проверьте
  интернет», as without a catalog;
- `GET /api/import/oauth/<service>/callback` is a 404 «Сервис не подключён»
  for every service.

The Import page words its texts by what the server answers, not by what
else the addons offer: `GET /api/import/services` is `{"services",
"downloads", "catalog"}`. `downloads` is true by the same rule as the
queue (an addon with `imports` that declares `import.services` is
installed and on); while it is false the page promises no download, shows
no size or free space, says the albums the library lacks are «Нет в
медиатеке», and offers no «Повторить» (the route still answers: the
library settles the album again). `catalog` is true while an addon that
is on declares `import.search`; while it is false the review offers only
«Пропустить». An addons list the server cannot read answers both false.

**Operations, not column writes.** The addon's queue does not write an
album's columns: each of its moves is one operation of the server, one
transaction there. Several of them change an album and its songs
together (`start`, `requeue`, `finish`, `have`), and some read and
rewrite a list (`given-up` adds a download to the album's dead or slow
ones, each once; `requeue` counts the dead ones). Column writes would
leave the album and its songs out of step whenever a call failed half-way,
and two writers would lose each other's list entries.

- **Claims.** `move` and `start` are compare-and-sets: the first claimer
  of an album wins and every other gets 409 «Альбом уже взят». A queue
  moves an album from `waiting` (or `waiting_space`, `waiting_download`,
  `failed`) to `searching`, searches, and records its download with
  `start` (`searching` → `downloading`). When the server restarts, it puts
  every `searching` album back to `waiting`, so a `start` after that is
  409: the addon removes the download it began. `from` is one of
  `waiting`, `searching`, `waiting_space`, `waiting_download`, `failed`;
  `to` one of `waiting`, `searching`, `waiting_space` (else 400
  «Неверное состояние»).
- **Keys.** `key` names the download an album waits for or fetches,
  whatever the addon calls it; `skipKey` one it gave up waiting for
  (the album's own search leaves it out); `dead` and `slow` its own
  downloads given up for making no headway or for being too slow. The
  other writes are no-ops when the album is no longer in the state they
  expect (`clock`, `given-up` and `requeue` need it `downloading` that
  `key`; `give-up-wait` needs `waiting_download`; `wait` leaves an album
  that is searching, downloading or settled alone).
- **Songs.** `trackIds`, `downloading`, `missing`, `failed` and the keys of
  `done` and `have` (`{"<song id>": libraryTrackId}`) are songs of that
  album; another album's song is left alone. `album` is the library album
  (0: unknown); `have` makes it the importer's too.
- **Progress.** `progress` is kept in memory while the album downloads
  and dropped when it leaves `downloading`; the album list shows it as
  the bytes so far. Send it every 10 s or so; after a server restart the
  list shows 0 until the next one.
- **Reading jobs.** A sign-in service's library is read by the addon:
  `POST /imports/jobs` makes an empty job in `reading`, `PUT …/snapshot`
  fills it (→ `preview`, the server's own preview from then on) or
  `POST …/fail` fails it with the reason. A job cancelled or deleted
  meanwhile is no longer reading: 409. Every one of these tells the admins
  (`import.progress`), as the server's own steps do. The service's
  sign-in tokens stay with the addon: no call carries one.
  - **Only the addon's own jobs.** `snapshot`, `fail`, `fail-reading` and
    `GET /imports/reads` concern only the jobs the addon itself made with
    `POST /imports/jobs`. The server reads jobs of its own too (an
    uploaded Google Takeout whose videos are being named is `reading`),
    and in contract 1 it does not tell them apart yet: `fail-reading`
    fails every job still reading, `reads` lists them all, and `snapshot`
    or `fail` on the server's job replaces or fails it. So an addon keeps
    the ids it made, fails those it lost with `POST …/fail` one by one,
    and never calls `fail-reading` at its start. The server will mark who
    reads each job and refuse the others; until then this is the addon's
    to keep.
- **Errors.** An unknown job or album is 404 «Не найдено»; a malformed id
  400; another open job of the profile and service (`snapshot`), or a
  running job (`DELETE`), 409.

The JSON is camelCase:

- `ImportJob`: `{"id", "userId", "service", "account", "status",
  "favorites", "error", "report", "createdAt", "updatedAt", "startedAt",
  "finishedAt"}`; `status` is `reading`, `preview`, `matching`,
  `downloading`, `done`, `failed` or `cancelled`; `report` is
  `{"savedAlbumsMissing", "unresolved"}`; `startedAt` and `finishedAt` are
  left out until then.
- `ImportGroup`: `{"id", "jobId", "releaseGroup", "artist", "album", "year",
  "aliases", "albumItemId", "state", "key", "bytes", "attempts", "error",
  "updatedAt", "songs", "waitingFor", "skipKey", "anyFormat", "dead",
  "slow", "waitDone", "waitAt", "clock"}`; `state` is `waiting`,
  `searching`, `waiting_space`, `waiting_download`, `downloading`, `done`,
  `have`, `not_found`, `no_format` or `failed`; `releaseGroup` is the
  MusicBrainz release group; `waitingFor` names the download a
  `waiting_download` album waits for, `waitDone` and `waitAt` its bytes
  and when they last grew (left out when not); `clock` is
  `{"runSec", "quietSec", "base", "done"}`, its own download's running
  time, time since its bytes last grew, and its bytes when the clock
  started (-1: not yet) and since.
- `ImportTrack`: `{"id", "jobId", "key", "artist", "title", "album",
  "albumArtist", "isrc", "durationMs", "addedAt", "liked", "state",
  "reason", "mbRecording", "mbReleaseGroup", "mbTitle", "mbArtist",
  "mbAlbum", "mbYear", "groupId", "itemId"}`; `key` is the song's id
  within its snapshot.
- The snapshot: `{"service", "account", "tracks", "playlists", "albums"}`;
  a track `{"key", "artist", "title", "album", "albumArtist", "isrc",
  "durationMs", "addedAt", "liked"}`, a playlist `{"id", "name",
  "readable", "entries"}` (`entries`: track keys in order), an album
  `{"key", "artist", "title", "year", "addedAt"}`. At most 50 000 songs.

### Approving kids' requests

After an admin starts getting a title (a download the addon made for an
admin profile), `POST /requests/approve` approves the kids' requests of it,
pending and declined ones alike, and tells the kids as approving by hand
does («Просьбы» letters, `request.changed`):

```json
{"adminId": 1, "kind": "music", "tmdbId": 0, "extId": "mb-1",
 "title": "Группа крови", "artist": "Кино"}
```

- `kind` is `movie`, `series`, `music`, `book`, `audiobook` or `comic`
  (else 400 «Неизвестный раздел»). A movie or series is matched by
  `tmdbId` (> 0); the others by `extId`, the catalog id the request named,
  **or** by `artist` (the author; not for `comic`) and `title`, folded (case,
  «ё» and spacing ignored): a request without the catalog id, or a
  download without one, still matches. Without `tmdbId`, or without both
  `extId` and `title`: 400 «Не указано название».
- `adminId` is the admin who started it (the profile of the proxy call);
  a profile that is not an admin, or none: 400 «Одобрить может только
  администратор».
- `approved` counts the requests approved (0: none waiting).

### Playing an input

An addon with `play` registers an external video it serves on its own
socket: `POST /play/inputs` with an `InputRequest`

```json
{"userId": 7, "path": "/media/1", "name": "Show/Show.S01E01.mkv", "size": 1048576,
 "itemId": 0, "title": {"kind": "series", "tmdbId": 42, "season": 1, "episode": 1},
 "sidecars": {"audio": [{"path": "/media/1/a/0", "name": "Show/Rus/Show.S01E01.mka", "size": 100}],
              "subtitles": [{"path": "/media/1/s/0", "name": "Show/Subs/Show.S01E01.srt", "lang": "ru"}]},
 "slow": false, "meta": {}}
```

answers 201 `{"id", "expiresAt"}`. The `id` (32 hex characters, random)
names the input to the app; the server reads the main file and each
sidecar from the addon's socket at their `path`, with `GET` and `Range`,
so every `path` must answer ranges.

**Rules**, checked in this order; the first refusal answers:

1. Without `play`: 403, before the body is read. A server that keeps no
   input registry: 503 «Проигрывание недоступно», also before the body
   (and on `DELETE /play/inputs/{id}`).
2. A body over 64 KiB: 413; one that is not JSON: 400.
3. The request itself (400 «Неверное описание видео: …»):
   - `userId` and `size` (and each audio sidecar's `size`) positive;
   - at most 32 audio and 64 subtitle sidecars;
   - each `path` an absolute route of at most 1024 bytes on the addon's
     socket, with a query if wanted (printable ASCII, no spaces), written
     in **Go's canonical escaping**: ASCII only, every byte that needs escaping (non-ASCII, a
     space, …) written as uppercase `%XX`, and nothing else escaped (`%41`,
     `%2F` are refused). No host, no `..` or `.` segments (escaped ones
     neither), no `//`, backslashes or fragment, and never `/health` or
     `/hooks/…`;
   - each `name` a clean slash-separated relative path (the file's place in
     the addon's collection) of at most 512 bytes, no `..` or `.`
     segments, no control characters (below 0x20, or 0x7f), all names of
     an input different;
   - a subtitle's `name` ends in a subtitle extension the server reads
     (`.srt`, `.ass`, `.ssa`, `.vtt`) and its `lang` (optional) has at
     most 35 bytes and no control characters;
   - `meta` (optional) a JSON object of at most 4 KiB, kept for the addon;
   - `title` (optional) `kind` `movie` or `series`, `tmdbId` positive,
     `season`, `episode` not negative.
4. The profile (400): unknown «Профиль не найден»; a kid profile
   «Детскому профилю это недоступно» (**kids never play inputs**); a
   profile that is not an admin while the addon's `access` is not
   `adults` «Профилю это расширение недоступно».
5. `itemId` (optional, 400): a library item the profile can see (its age
   limit applies, as everywhere in the app) — else «Элемент медиатеки не
   найден», whether it is missing or hidden — and a movie or an episode,
   else «Это не фильм и не серия».
6. The addon must be running: while it starts 503 «Расширение
   запускается», otherwise 503 «Расширение не запущено».

**How the server reads an input.** Each read is a new connection to the
socket of the process that registered the input, `GET` or `HEAD` at the
part's `path` exactly as registered, with only these headers: `Range` (a
single range; a multi-range request is refused before the addon is
asked), `If-Range`, `X-Antresol-Input: <id>` and `X-Antresol-User` (the
input's profile, as for the proxy). No `Accept-Encoding` is sent: answer
with the plain bytes and a `Content-Length`. Only `200`, `206` and `416`
count; any other status, a redirect included (never followed), is a
failed read. Of the answer's headers only `Content-Length`,
`Content-Range`, `Accept-Ranges`, `Last-Modified` and `ETag` are used:
the type comes from the part's `name` (its video or audio extension,
else `application/octet-stream`), and nothing else the addon sends
(cookies, `Location`, CORS headers) reaches anyone.

**What the app sees.** The app plays an input at **`/play/input/<id>`**:
an addon's page or action sends the profile there (`useNavigate()` to
`/play/input/<id>` once its own API has registered the input) and the core player does the rest — it
starts at `startSec`, remembers track choices for `itemId` (none without one),
saves the position through the input's progress route and, while the video
loads, shows the addon's [`playerNotes`](#slots). A gone input shows «Видео
больше недоступно». The profile's player asks the server, never the
addon, and only the profile the input was registered for gets an answer:
another profile (an admin's too), an unknown id, an input whose process
is gone, and — for a profile that is not an admin — an addon whose current
`access` is no longer `adults` all answer the same 404 «Видео не найдено»
(the access is checked again on every request); a kid profile gets 403
«Недоступно в детском профиле».

- `GET /api/play/input/{id}` (`Cache-Control: no-store`):
  `{"id", "addonId", "name", "itemId", "title"?, "slow", "meta"?, "startSec"}`
  — the registered `name`, `itemId` (0 when none), `title`, `slow` and
  `meta` as sent, and `startSec`, the profile's saved position of
  `itemId` in seconds (0 without `itemId`, without a saved position, or
  when the item is watched). The addon's `path`s and socket are never
  shown. `slow: true` says the source is slow (a remote one that fills as
  it plays): the player then waits longer before it calls a slow start or
  a stall a failure, and blames the source rather than the device. `meta`
  is the addon's own: the server only hands it back, here and to the
  addon's `playerNotes`.
- `GET|HEAD /api/play/input/{id}/direct`: the main file as it is, read
  from the addon as above (the same headers reach the addon and the app;
  the answer always carries `X-Content-Type-Options: nosniff` and
  `Content-Security-Policy: sandbox`). A multi-range request is 416 with
  `Content-Range: bytes */<size>`. Read with a media key (the apps'
  players), at most 6 reads run at once per key: the 7th is 429
  «Слишком много одновременных потоков».
- `POST /api/play/input/{id}` with the body of `POST /api/play/{fileId}`
  (`caps`, `audioIndex`, `startSec`, `quality`, `replaceSession`): the
  play decision, the same answer shape as for a library file (`mode`,
  `reason`, `startSec`, `durationMs`, `audioIndex`, `audio`, `subtitles`,
  `quality`, `url`, and `hlsComplete` for HLS). The server probes the main
  file first, reading it from the addon as above: while that runs (on a
  slow source it may take minutes) the answer is 202 `{"pending": true}`
  after at most 25 s, and the player asks again; the probe goes on (at
  most 10 minutes) and a complete result is kept, so the next ask does
  not read the head again. Direct play answers
  `url: "/api/play/input/<id>/direct"`; otherwise the server transcodes
  or remuxes into an HLS session (`/api/hls/…`) that reads the input
  the same way. A server without its loopback relay answers 503
  «Перекодирование недоступно» before reading anything; a probe that
  fails (the addon answers an error, the file is not a video) is 502
  «Не удалось прочитать видео. Возможно, оно ещё не готово», and so is
  every ask for the next 2 minutes (a probe that fails after the 202 too):
  only then is the file probed again. At most 4 probes (main files and
  audio sidecars, of all addons and profiles) run at once; the others wait
  their turn, answering 202 meanwhile.

  **Audio sidecars** are listed in `audio` after the video's own tracks,
  in the order the request gave them: the i-th (from 0) as index
  `2000 + i` among those listed, `"external": true`, and `src`, a stable
  id of its `name` the player sends back as `audioSrc` with `audioIndex`
  to choose it. Each is probed once the main file is (through the
  socket, as above; at most 2 sidecars of one input at once, each bounded
  to 20 s) and listed once its probe has ended: one ask waits for them at
  most 4 s in all, and one still probing is listed by a later ask. A
  sidecar whose probe fails, which has no audio, or whose duration differs
  from the video's by more than 10 % is not listed (a failure is probed
  again after 2 minutes). Its language, title and studio come from its
  `name` as for the library's voice-over files beside a video (its
  folders, e.g. `Show/RUS Sound/Studio/…`), else from the file's own tags.
  A chosen sidecar still probing answers 202; one whose probe failed plays
  the video's default track. Without `audioIndex`, a sidecar plays when
  the profile's audio languages call for one the video lacks — among
  several of that language the voice the profile last chose in the series
  (the library series of `title` when it is a `series` the library
  knows, else of `itemId`'s episode), else the first — and when the video has no audio
  of its own. While only sidecars still probing have that language the
  answer is 202 for up to 20 s from their probe's start. Old app builds
  that do not announce external audio are never given sidecars.

  **Subtitle sidecars** are listed in `subtitles`, one per sidecar in the
  order the request gave them: the n-th (from 0) as index `1000 + n`,
  `"external": true`, `codec` from its `name`'s extension, and
  `url: "/api/play/input/<id>/subs/<n>.vtt"`. Its `lang` is the sidecar's
  `lang` when that is a language code or name (`"en"`, `"rus"`, `"pt-BR"`
  → `en`, `ru`, `pt`; any other text is ignored, never shown), else the
  language its `name` says, as for the library's subtitle files beside a
  video (`Show.S01E01.ru.srt`, `Show/Subs/…`), which also gives `title`
  and `forced`. Subtitle streams inside the main file are not listed.
- `GET /api/play/input/{id}/subs/{n}.vtt`: subtitle sidecar `n` as
  WebVTT (`nosniff`, `Content-Security-Policy: sandbox`). The server asks
  the addon `HEAD` at its `path` (as above, answered within 15 s), then
  `GET`: a sidecar over 8 MiB is 413 «Файл субтитров слишком большой». The
  file is decoded (UTF-8, UTF-16 with a BOM, else CP1251), converted and
  kept by input, `path`, `name`, `Content-Length` and `ETag` (another
  length or `ETag` is converted again). While the conversion runs (at most
  40 s per ask) the answer is 503 «Субтитры ещё готовятся» with
  `Retry-After: 3`; a sidecar that cannot be read or converted is 502
  «Не удалось прочитать субтитры», and so is every ask for the next
  2 minutes, without asking the addon. An `n` that names no subtitle
  sidecar is 404.
  The 413 is remembered as well: for 2 minutes every ask is 413 without
  asking the addon. Converted files unused for 6 hours are removed.
- `PUT /api/play/input/{id}/progress` `{"positionMs", "durationMs",
  "keepalive"}` → 204: the player's position. Both are milliseconds,
  at least 0, and `positionMs` at most `durationMs` + 5000, else 400
  «Неверная позиция». With an `itemId` the position is saved as the
  profile's progress in that library item (as `PUT /api/progress/{itemId}`
  does for a library file: `startSec` and the item's progress follow it).
  The server then calls the addon's `play.progress` hook, when the addon
  declares it (see [Hooks](#hooks-the-server--the-addon)); the save never
  waits for it.

**Limits and lifetime.** An input lives while it is used within 6 hours
(`expiresAt` is the time it lapses if nobody uses it; each use moves it on)
and while the process that registered it runs: an input is bound to
that process's socket, so a restart, a crash or stopping the addon ends
every input it registered — its id is then unknown on every route above
(404 «Видео не найдено», never a 5xx), the new process is never asked for
it, and reads in flight end when the old socket goes. Register the input
again from the new process (a new `id`). Each addon keeps at most 200 inputs; the
201st forgets its least recently used. `DELETE /play/inputs/{id}` forgets
one at once. Inputs are kept in memory only: a server restart forgets
them all.

### Trailers: `trailers.fetch`

An addon that declares the hook `trailers.fetch` plays the trailers of
movie and series pages inside the apps. It needs no permission: declaring
the hook is enough. The server keeps no trailer of its own: the addon
fetches the video, keeps the file and serves it on its socket; the server
decides who may play which trailer, asks the addon and relays the bytes.

| Hook | Body → answer | When |
| --- | --- | --- |
| `trailers.fetch` | `{"key", "url", "maxHeight"?}` (`TrailerFetch`) → 200 `{"path", "size", "durationMs", "contentType"}` (`TrailerFile`), 202 `{"retryAfterMs"}` (`TrailerPending`), 409 `{"error"}` or 404 | a profile plays a trailer; once for the first trailer of a list (the prefetch); before a read of a trailer the server has no answer for |

Called for the household (no `X-Antresol-User`), within **2 s** per call.

- **The body.** `key` is the trailer's id as TMDB lists it
  (`^[A-Za-z0-9_-]{11}$`); `url` is the trailer's page, the same `url` the
  apps get with it in `GET /api/items/{id}/trailers`: fetch that, nothing
  else, whatever site it names. `maxHeight` (optional, positive) is the
  tallest picture wanted; absent, the addon chooses (its own setting).
- **Only listed keys.** The server asks only for a key it listed itself
  (in a title's trailers within the last 30 days), and only for a profile
  that may see one of the titles that listed it (its age limit applies, as
  everywhere in the app). Any other key, a malformed one and a kid gated
  out answer 404 «Трейлер не найден» before an addon is asked.
- **Which addon.** The running addons (on, and not starting) that declare
  the hook are asked one at a time in addon-id order; the first answer that
  is not a 404 decides. A trailer the server can play has `play` in the
  list (`/api/trailers/<key>/play`) only while such an addon runs; without
  one the apps play it their own way, from its `url`.
- **200: ready.** `path` is where the server reads the file on the addon's
  socket. It is under `/media/` and written as an input's `path` is (see
  [Playing an input](#playing-an-input): an absolute route in Go's
  canonical escaping, no `..`, `.`, `//`, backslash or fragment, at most
  1024 bytes). `size` is positive (bytes), `durationMs` at least 0 (0:
  unknown), `contentType` is `video/mp4` (H.264 and AAC, what every app
  plays without a transcode). An answer breaking a rule is a failure.
- **202: still fetching.** The server asks the same addon again after
  `retryAfterMs` (taken between 250 and 2000 ms, 1000 when absent), for at
  most 15 s from the play; then the app gets 503 with `Retry-After: 2`
  and asks again later. Keep fetching in the background meanwhile.
- **409: a refusal** (the video cannot be fetched, the addon is set to
  fetch nothing): the app gets 502 with its text, cut as every refusal is.
- **404 (or 204): not this addon.** The next one is asked.
- **Down, failing or slow** (a timeout counts the addon as down, as for
  any hook): logged, and the next addon is asked. When no addon is left,
  the app gets 502 «Не удалось загрузить трейлер» if an addon failed, and
  404 if every one answered 404.
- **The prefetch.** When the server answers a title's trailer list, it
  calls `trailers.fetch` for the first trailer that can be played, without
  waiting for the answer, at most once per key per day (and only the first
  addon that does not answer 404). Answer it at once with 202 and fetch in
  the background: the play that follows is then quick.

**How the server reads the file.** As it reads an input's part: a new
connection to the socket of the process whose 200 named the `path`, `GET`
or `HEAD` at that `path` exactly, with only `Range` (a single range: a
multi-range request is 416 before the addon is asked, unless it carries
`If-Range`, when the whole file is read instead) and `If-Range`, and no
`Accept-Encoding`. Only `200`, `206` and `416` count; any other status, a
redirect included, is a failed read (502), except a 404 (the file is
gone), which is the app's 404. Of the answer only `Content-Length`,
`Content-Range`, `Accept-Ranges`, `Last-Modified` and `ETag` are used (of
a 416 only `Content-Range`); the type is always `video/mp4`. `http.ServeContent`
answers all of this. The server keeps the last 200 of each key while the
process that gave it runs; after the addon restarts, or with none kept, a
read first asks `trailers.fetch` once (no waiting for a 202) and is 404
unless it answers 200. The addon keeps its files as it likes (a cache with
its own limits), but a file it named in a 200 should stay readable for a
while: count a read as a use of it.

**What the app sees** (the same as servers that fetched trailers
themselves):

- `POST /api/trailers/{key}/play` (a session; a media key is refused) →
  200 `{"url", "durationMs"}`, where `url` is
  `/api/trailers/<key>.mp4`, with `?mk=<media key>` when the session can
  have one, playable without headers; 503 «Трейлер ещё готовится,
  попробуйте через пару секунд» with `Retry-After: 2`; 502 with the
  refusal's text, or «Не удалось загрузить трейлер»; 404 «Трейлер не
  найден» (see *Only listed keys* and *Which addon*). Never cached
  (`Cache-Control: no-store`).
- `GET|HEAD /api/trailers/{key}.mp4` (a session or a media key), with
  `Range`: the file, read from the addon as above, with
  `X-Content-Type-Options: nosniff`; the same 404 for a key it may not
  play.

## Hooks: the server → the addon

The server calls a hook the manifest declares in `hooks` as
`POST /hooks/<name>` on the addon's socket, with `Content-Type:
application/json`, `X-Antresol-Hook: <name>`, and `X-Antresol-User` (as in
the proxy) when the hook runs for a profile. The body is JSON (`{}` when the
hook has nothing to send).

- **Answers.** 200 with a JSON body; 204 = nothing to add; 202 with a
  JSON body only where the hook says so (`trailers.fetch`: still working),
  elsewhere a failure; 409
  `{"error": "…"}` = a refusal whose text the user is shown; 404 = not
  implemented (logged once, treated as 204), except for a declared
  `files.*` hook, where 404 is a failure. Redirects are not followed.
  Any other status (3xx included), a 409 without an `error` text, a body
  that is not JSON or over 32 MiB, or a timeout is a **failure**. An addon
  that is not running (stopped, starting, crashed) is **down**, and so is
  a running one the server cannot reach: a transport error (the socket
  refuses the connection, or it breaks before the whole answer is read) counts as
  down, not as a failure. A refusal text is shown without control
  characters and cut to 500 characters.
- **Without an answer.** What a failure, a refusal or a down addon does
  depends on the hook, and is written with it: the `files.*` hooks fail
  closed (see [File hooks](#file-hooks)); a hook called on several addons
  (below) simply leaves that addon out, logged, and the server goes on
  with the others' answers.
- **Undeclared hooks are never called.** Of the hooks a manifest may
  declare, this server calls the `files.*` ones, `play.progress`,
  `library.pending`, `disk.usage`, `requests.*`, `tracking.scan:<kind>`,
  `tracking.titles`, `tracking.watched`, `notify.interest`,
  `voiceover.origLang`, `trailers.fetch`, `import.services`, `import.stop`,
  `import.resolve`, `import.search`, `migrate.leftovers`,
  `library.afterScan`, `home.continue` and the four `channels.*`: every
  hook a manifest may declare.
- **One call per addon per operation.** Work for many keys comes in one body
  (`{"keys": [...]}`), not one call per key.
- **Deadlines.** Each hook has its own timeout (for example `files.plan`
  10 s, `library.pending` and `disk.usage` 2 s, `tracking.scan:<kind>`
  120 s, `library.afterScan` 60 s, `home.continue` 500 ms, `channels.ready`
  2 s, `channels.candidates` 30 s, `channels.resolve` 60 s,
  `channels.release` 5 s). The `files.*` hooks of one file operation share the operation's
  deadline. A hook called on several addons runs on all of them in parallel
  under one deadline; the 200 answers are taken in addon-id order, and
  every addon that gives no 200 (down, failed, refused, timed out, or 204
  and 404: nothing to add) is left out.
- **A hung addon.** When a hook call runs out of the hook's own deadline
  (not a shorter one of the caller, and not a caller that went away), that
  hook of that addon counts as down **by itself** for **60 s**: its calls
  fail at once, as for a down addon, and a hook called on several addons
  leaves it out without waiting. The addon's other hooks are still called,
  so a slow `disk.usage` never silences `tracking.scan` or the letters, and
  a slow `import.*` service list never blanks its other pages. The core
  logs this once per down period per hook. `files.*` hooks are still
  tried, since a file operation is refused on any failure. Any status
  change of the addon (a restart, a crash, a stop or a start) ends all its
  down periods at once.

The fixed hook names a manifest may declare (and `tracking.scan:<kind>`,
`<kind>` matching `[a-z][a-z0-9-]{0,19}`); the server's tests compare
this list with the code both ways:

```inventory-hooks
files.plan
files.pause
files.resume
files.stop
files.detach
files.attach
play.progress
library.pending
disk.usage
requests.open
requests.title
requests.search
tracking.titles
tracking.watched
notify.interest
voiceover.origLang
trailers.fetch
import.services
import.stop
import.resolve
import.search
migrate.leftovers
library.afterScan
home.continue
channels.ready
channels.candidates
channels.resolve
channels.release
```

### File hooks

A file operation on held data (above) asks each addon holding something it
touches. Keys are the addon's own (not prefixed). **An addon that holds
files must declare at least `files.plan` and `files.pause`** (else
`PUT /files/holds` with any hold is 400); declare `files.resume` and
`files.stop` too, and `files.detach` / `files.attach` to let holds follow
a move. A declared hook it does not answer (404) refuses the move.

| Hook | Body → answer | When |
| --- | --- | --- |
| `files.plan` | `{"moves": [{"from", "to"}], "keys": [key]}` → `{"follow": [key], "stop": [key]}` | before a move, rename or trash touching the addon's holds; `keys` are those touched; `to` is `""` for the trash. A trash of several paths sends them all in one call |
| `files.pause` | `{"keys": [key]}` → 204 | before the files move: stop using them for now |
| `files.resume` | `{"keys": [key]}` → 204 | the move failed or was given up: use them again |
| `files.stop` | `{"keys": [key]}` → 204 | the files moved away: stop for good (the data is kept) |
| `files.detach` | `{"from", "to", "keys": [key]}` → 204 | before a move the keys follow (`follow` of the plan) |
| `files.attach` | `{"from", "to", "keys": [key], "renamed": bool}` → 204 | after it: `renamed` false when the move did not happen |

- **Fail closed.** Only `200` and `204` are answers; anything else (404
  included), a timeout or a down addon is a failure. A touched key the
  plan does not name in `follow` (and not in `stop`) is stopped. A move
  away (`to` `""`) never follows. A `files.plan` that fails or refuses
  (409) **refuses the whole operation**: `files.pause` is not asked of
  that addon (another addon holding data in the same operation may already
  have been paused, addons are held in id order, and it then gets
  `files.resume`), nothing moves, and the admin sees the refusal text, or «the extension
  did not answer, the files stayed where they were». So does a
  `files.pause` or `files.detach` that fails or refuses. The keys of a
  refused operation get `files.resume` (also when they were never
  paused: resuming must be harmless); a retry plans afresh. A failed `files.resume` or `files.stop` is
  logged (reconcile at your next start); a failed `files.attach` after the
  move is reported to the admin, the move stands.
- **One call per hook per addon per operation**, with all its keys. The
  asking and holding (`files.plan`, `files.pause`, `files.detach`) share
  the operation's one deadline (at most 90 s; `files.plan` 10 s of it);
  settling after the move (`files.stop`, `files.resume`, `files.attach`)
  has a fresh deadline of its own.
  An operation touching no hold calls no hook. The one exception: deleting
  a library item trashes its downloaded files and its library files in two
  passes, so an addon holding data in both (a hold inside a library folder,
  or one spanning two file-browser roots) is asked once per pass.
- **Held paths only by their own path.** Holds are matched by path, as a
  file-browser root names it. A move, rename or trash of a path that goes
  through a symlinked folder inside the roots (`dl/link/a.mkv` with
  `dl/link → Pack`) is refused (409 «Эти данные используются, а путь к ним
  идёт через ссылку: переместите их по их собственному пути») when the data
  it really is, or anything inside it, is held; no hook is called. The
  check fails closed: a folder that cannot be resolved or walked counts as
  held. Renaming or trashing the link itself moves the link only and is
  allowed, and a root folder that is itself a symlink is not such a path.

### `play.progress`

| Hook | Body → answer | When |
| --- | --- | --- |
| `play.progress` | `PlayProgress` `{"inputId", "userId", "positionSec", "durationSec", "ended"}` → 204 | a profile's player saved its position in one of the addon's inputs |

- **Cadence.** At most once per 15 s per input while playing — about every
  20 s with the web player's 10 s saves — and at the end (`ended`: the
  position reached a known duration) or when the page goes away.
- Called for the input's profile (`X-Antresol-User`), within 5 s. The
  player's save never waits for it: a failure, a refusal, a timeout or a
  down addon changes nothing (logged once per input), and the call is not
  repeated.

### `library.pending`

| Hook | Body → answer | When |
| --- | --- | --- |
| `library.pending` | `{"scope": "mine" \| "all"}` → `{"cards": [PendingCard]}` | a library page of an adult profile asks `GET /api/library/pending` |

The titles on their way into the library: the library pages show them at
once, tinted, with their progress. Called for the profile
(`X-Antresol-User`, its `lang` the language of `hint`), on every addon
declaring it, within 2 s; an addon that does not answer in time, fails or
sends a body that is not `{"cards": [...]}` is left out (logged). `scope` is
`mine` for «Моя медиатека» (keep the titles this profile asked for) and
`all` otherwise.

```json
{"cards": [{
  "card": {"id": 0, "kind": "movie", "title": "…", "subtitle": "", "year": 2024,
           "poster": "<name from POST /images>", "tmdbKind": "movie", "tmdbId": 603},
  "pending": {"key": "q-17", "status": "active", "done": 1048576, "size": 4194304,
              "hint": "…", "href": "/addons/<id>/…"}}]}
```

- `card.kind` is one of `movie`, `series`, `album`, `book`, `audiobook`,
  `comic`, and `card.tmdbKind` is empty, `movie` or `series`; a card with
  another value is dropped.
- `card.id` is the title's library item when the library has it already
  (a new season: the page puts the progress on that card), else 0: a card
  of its own, opened by `href`, or by `tmdbKind`/`tmdbId`, or not at all.
- `pending.status` is `active`, `waiting` (for disk space), `paused`,
  `organizing` or `error`; a card with any other status is dropped.
  `error` cards reach admins only (nobody else can fix it), whatever the
  addon sends. `done` and `size` are bytes.
- `pending.key` is the addon's own; the app gets it as `<id>:<key>`. A
  card without a key is dropped.
- `pending.hint` (optional) is a line under the progress, already in the
  profile's language; control characters are dropped and it is cut to 200
  characters.
- `pending.href` (optional) is a page of the app, as a notification's
  `link`: it starts with `/` but not `//`; another value is dropped.
- `card.poster` is an image name `POST /images` gave; the app gets it as
  `/api/images/<name>`. Any other value (an outside URL included) is
  dropped.
- Kid and rating-limited profiles get no pending cards, and the hook is
  not called for them: a title has no rating until the library has it.
- The cards come in addon-id order, each addon's in its own order.

### `library.afterScan`

| Hook | Body → answer | When |
| --- | --- | --- |
| `library.afterScan` | `{}` → 204 | a library scan finished |

Called after each library scan and its fast metadata enrichers (a title the
scan found has its TMDB id by then), on every addon declaring it that is on
and running, in parallel, within 60 s. It is called in the
background: the scan's result never waits for an addon. There is no profile
(`X-Antresol-User` is absent), and the answer is 204, or any JSON, which the
server ignores. An addon that is not running is skipped quietly, one that is
slow, fails or refuses is logged and skipped, and nothing is repeated: the
next scan calls again. So what an addon does here must be safe to run
twice, and to miss once. A call may still be running when the next scan's
arrives, so an addon must not assume one at a time.

### `home.continue`

| Hook | Body → answer | When |
| --- | --- | --- |
| `home.continue` | `{}` → `{"cards": [ContinueCard]}` | Home of a profile that is not a kid, asked by a client that shows addon cards (`GET /api/home?addons=1`) |

The titles the profile was watching through the addon, for «Продолжить»
on Home. The body is `{}`: the profile is the `X-Antresol-User` header's
(its `maxRating` the limit the server applies). Called on every addon
declaring it that is on and running **and that the profile has access to**
(the same rule as the bundles, see [Who gets an addon](#who-gets-an-addon):
an addon with access `admins` is never asked for a non-admin), in parallel,
under a hard **500 ms**: an addon that is down, late, fails or sends a body
that is not `{"cards": [...]}` is left out (logged), and Home is drawn
without its cards. Never called for a kid. Answer from what the addon keeps
itself: no outside call fits in the deadline.

```json
{"cards": [{"key": "s-1399", "kind": "series", "tmdbId": 1399, "title": "…",
            "subtitle": "…", "year": 2011, "poster": "<name from POST /images>",
            "rating": "18+", "progress": 0.25, "resumeSec": 600, "season": 1,
            "episode": 2, "href": "/addons/<id>/…", "updatedAt": "2026-11-17T10:00:00Z"}]}
```

- `key` is the addon's own; the app gets it as `addon:<id>:<key>`, and the
  card's `addonId` as `<id>`. A card without a key is dropped.
- `kind` is `movie` or `series`, `tmdbId` the title's TMDB id; a card of
  another kind is dropped.
- **The server applies the rating itself.** For each card it takes its own
  item's rating when the library has the title (`kind` and `tmdbId`),
  else the card's `rating`; it drops a card above the profile's limit, and
  an unrated one (`""`) for a rating-limited profile. An addon still
  gates its own cards by `maxRating`.
- `poster` is an image name `POST /images` gave; the app gets it as
  `/api/images/<name>`. Any other value (an outside URL included) becomes
  `""`.
- `href` (optional) is the page of the app the card opens, as a
  notification's `link`: it starts with `/` but not `//`, and holds no
  backslash, space or control character; another value is dropped (the
  card stays).
- `progress` is 0..1, `resumeSec` where to resume, `season` and `episode`
  the episode of a series.
- `updatedAt` is when the profile last watched it: the server merges the
  cards with its own «Продолжить» by it, newest first, and cuts the shelf
  to its size. Of one addon's cards it looks at its 40 newest.

### «ТВ»: titles outside the library: `channels.*`

| Hook | Body → answer | When |
| --- | --- | --- |
| `channels.ready` | `{}` → `{"ready"}` (`ChannelReady`) | the channel editor asks whether to offer «titles outside the library» (`extrasAvailable` of `GET /api/channels/options`), and before candidates are asked for |
| `channels.candidates` | `{"rules": ChannelRules, "limit"}` (`ChannelCandidates`) → `{"titles": [ChannelTitle]}` (`ChannelCandidatesAnswer`) | a channel whose rule «titles outside the library» is on is scheduled (once a day; an hour after a failed ask) |
| `channels.resolve` | `{"channelId", "title": ChannelTitle}` (`ChannelResolve`) → `{"path", "headers"?}` (`ChannelStream`) | one of the addon's titles is about to air (about 2 minutes ahead) |
| `channels.release` | `{"token", "path"}` (`ChannelRelease`) → nothing (204) | the programme is over, or was resolved and not played |

A «ТВ» channel made of the library may also play titles of its genres
the library does not have, from an addon. An addon declares **all four
hooks or none** (the manifest is refused otherwise). The first addon in
id order that is on, running, declares them and is open to the channel's
profile answers for a channel; the others are not asked. The hooks run
for no profile (no `X-Antresol-User`); the bodies are JSON.

- **Who.** A shared channel is every profile's: only an addon with access
  `adults` answers for it. A profile's own channel is answered by an
  addon that profile gets by [Who gets an addon](#who-gets-an-addon) (an
  addon with access `admins` only for an admin's). A kid never gets an
  addon's title, on any channel, a shared one included: a kid's channel
  list, guide and IPTV guide leave those programmes out, a kid's live
  stream of the channel is refused (403 «Недоступно в детском профиле»)
  while the stream may hold one (from shortly before it starts, as the
  stream runs ahead of the clock, until its last kept segments are
  gone), a kid's own channel gets none, and the editor
  offers the rule to no kid.
- **`channels.ready`** says whether the addon can find and play titles
  now. Its answer is kept **60 s**; any status change of the addon (a
  restart, a crash, a stop or start) forgets it. A down, slow or failing
  addon is not ready.
- **`channels.candidates`.** The server asks only for rules with the
  rule on (not a hand-picked or ordered channel, and one that shows
  movies). `rules` is `{"genres", "yearFrom", "yearTo", "countries",
  "languages", "keywords", "maxRating"}`: titles of any of the genres
  (display names, either language; none: any), from `yearFrom` to `yearTo`
  (0: open), of any of the countries (ISO 3166-1), original languages (ISO
  639-1) and keywords (TMDB's, English, lower case), within `maxRating` (the
  channel's age limit, `""` none). Answer up to `limit` titles the library
  does not have, within 30 s; titles past `limit` are dropped.
- A `ChannelTitle` is `{"key", "kind", "tmdbId", "title", "year",
  "overview", "rating", "genres", "poster", "backdrop", "runtimeMs",
  "season", "episode"}`. `key` is the addon's stable identity of the title
  (at most 200 characters, no control character); the server keeps it as
  `addon:<id>:<key>` and sends it back bare in `channels.resolve`. `kind`
  is `movie` (the default) or `episode` (`tmdbId` then the series', with
  `season` and `episode`). `overview`, `rating` (the age rating, `""`
  unknown) and `genres` are in the server's metadata language; `poster`
  and `backdrop` are TMDB image paths (`/abc.jpg`; any other value shows no
  picture). `runtimeMs` is how long it lasts in the schedule. A title
  without a key, a title or a runtime, or of another kind, is dropped.
- **The server applies the rating itself.** It rates each title with its
  own item's rating when the library has it (`tmdbId`; an episode by its
  series), else with the title's `rating`, and drops a title above the
  channel's `maxRating`, and an unrated one (`""`) while a limit is set.
  If it cannot read the library's ratings it takes none of the answer and
  asks again in an hour. A profile's own limit still applies when it
  watches. An addon still keeps to `maxRating` itself.
- **`channels.resolve`** (60 s, within the server's own wait for the
  programme) gives a stream of the title for the channel: `path` is a
  route on the addon's socket, an HLS playlist (whose relative URIs stay
  under its folder) or a file, in the form of an input's path (see
  [Playing an input](#playing-an-input): starts with `/`, no `..`, not
  `/health` or `/hooks/…`). `headers` (optional, at most 16, no `Host`,
  `Range`, `If-Range`, hop-by-hop or `X-Antresol-*` name, no line break in
  a value) are sent with every request the server makes for this stream.
  A failure, a refusal, a bad path or bad headers: the title is left out
  of that channel for a day and the schedule made again without it.
- **The relay.** The server's own ffmpeg reads the stream through the
  core's loopback relay, never the addon's socket: `GET`/`HEAD` of
  `path`, and of the playlist's relative URIs under its folder, reach the
  addon with `Range` and `If-Range` passed on, the stream's `headers`, and
  `X-Antresol-Relay: <token>` (32 hex characters, one per resolved
  stream). The addon answers 200, 206 or 416; 404 and 410 end the stream,
  any other status is a failure. Only `Content-Length`, `Content-Range`,
  `Accept-Ranges` and a video, audio or HLS `Content-Type` of the answer
  pass on. The relay's URL is the server's alone: no answer to an app or
  a player holds it.
- **`channels.release`** (async, 5 s) tells the addon a stream it
  resolved is over (`token` as in `X-Antresol-Relay`, `path` as
  resolved); the relay forgets it at once. A stream is also forgotten
  after 6 h unused, and when the addon's process goes (its stream URLs
  then answer 404): resolve again rather than keeping state across a
  restart.

### `disk.usage`

| Hook | Body → answer | When |
| --- | --- | --- |
| `disk.usage` | `{}` → `{"segments": [{"id", "label": {"ru", "en"}, "dir"?, "bytes"?}]}` (`DiskUsage`) | the disk report of «Файлы» and «Статистика» is recomputed (at most every 10 minutes) |

- **What a segment is.** One part of the disk meter that is the addon's
  data, shown as `addon:<addonId>:<id>` beside the library categories and
  taken out of «Прочее». `id` is `^[a-z0-9][a-z0-9-]{0,31}$`, unique within
  the answer; `label` is its name, in at least one language (a missing one
  shows the other), at most 60 characters. At most 8 segments per addon;
  a bad segment is left out, the rest stay.
- **With `dir`** the server walks the folder itself, after the library
  categories and with their hardlinks: a file of the folder hardlinked
  into the library is counted once, in the library. `bytes` is then
  ignored. `dir` must be absolute and clean, and, its symlinks resolved,
  be at or inside `/info`'s `mediaRoot` or one of its `fileRoots`, and
  neither at, inside nor around a library folder or another segment's
  `dir` (so never `mediaRoot` itself); otherwise the segment is left out. A `dir` that does not resolve
  (under one of `missingRoots`, say) is left out of that report.
- **Without `dir`** `bytes` (≥ 0) is taken as it is: data the server
  cannot see. Do not report there what lies in the library as well, or it
  is counted twice.
- Called for the household (no `X-Antresol-User`), within 2 s; an addon
  that is down, slow or answers something else is left out of that
  report. `GET /api/stats/disk/addon:<addonId>:<id>` (admins) lists a
  segment with `dir` by its largest entries, as a library category.

### Kids' requests: `requests.open`, `requests.title`, `requests.search`

| Hook | Body → answer | When |
| --- | --- | --- |
| `requests.open` | `{}` → `{"open": bool}` (`RequestsOpenAnswer`) | «Попросить» and «Просьбы» are offered (`/api/me`, the kids' notification topics, every request) |
| `requests.title` | `{"kind", "extId"}` (`RequestTitleQuery`) → `{"title", "subtitle", "year", "poster", "explicit"}` (`RequestTitleAnswer`) | a kid asks for an album, book, audiobook or comic with a catalog id |
| `requests.search` | `{"kind", "q"}` (`RequestSearchQuery`) → `{"candidates": [RequestCandidate]}` | a kid searches that kind for something to ask for (`GET /api/requests/search`) |

All three are called for the household, **without** `X-Antresol-User`.

- **`requests.open`** (2 s): kids' requests are open while any running
  addon that declares the hook answers `{"open": true}`; with none they are
  closed (no «Попросить», no «Просьбы», every request 403). The answer is
  kept for 60 s; any addon's status change (installed, started, stopped,
  removed, crashed) asks again at once.
- **`requests.title`** (10 s): the addons that declare it are asked in id
  order; the first 200 names the request. Its `title` (required) and
  `subtitle` (the artist or author) replace what the app sent, and the
  request's keys come from them; `poster` is kept only when it is a
  catalog's cover (TMDB, Open Library, Cover Art Archive over https, or
  `/api/images/<name>`), else dropped; `explicit: true` refuses the
  request (403 «Недоступно для этого профиля»). 204 or 404 from every
  addon: the catalog is unknown to them and the app's text stays. A 409 is
  the kid's 400 with its text. An addon that is down or fails, when no
  other one answers, is the kid's 503 «Расширение «{name}» не ответило».
- **`requests.search`** (10 s) is called on every addon that declares it,
  for `kind` `music`, `book`, `audiobook` or `comic` (movies and series
  come from the server's TMDB). `RequestCandidate` is `{"kind", "tmdbId",
  "extId", "title", "subtitle", "year", "poster", "explicit", "aliases"}`;
  candidates of another `kind` or without a `title` are dropped, at most 40
  per addon are taken, texts are cut (`title`, `subtitle` 300 characters).
  The server drops `explicit` ones and every `poster` (kids get placeholder
  art for albums and books), adds each one's `kidRequest` and keeps the
  addons' order. An addon that is down, slow or answers something else is
  left out.

### Tracking scanners: `tracking.scan:<kind>`

| Hook | Body → answer | When |
| --- | --- | --- |
| `tracking.scan:<kind>` | `{"title": Title, "episodes": [Episode]}` (`ScanRequest`) → `{"sources": [Source]}` or `{"unchanged": true}` (`ScanAnswer`) | a tracked title is refreshed (see [Tracked titles](#tracked-titles)): when it is due (every 2 to 6 hours while it airs, less often otherwise) and after a kick |

Each kind an addon declares is a kind of sources of its own beside the
server's `library`: the refresh asks the addon for the
title's sources of that kind, and its answer replaces what is stored of
that kind only. Called for the household (no `X-Antresol-User`), within
120 s, on the addons that are on and running, after the server's own
scanners.

- **Kinds are the addon's alone.** A `kind` that is `library` or
  declared by another installed addon (on or off) is refused at
  install and at update (400 «Вид источника «{kind}» уже занят»); the
  refused version is not installed, a running one stays as it was.
- **`{"sources": [...]}`** replaces the kind's rows of the title: new
  ones are added, those no longer listed are removed: a 200 with
  `{"sources": []}`, or with `{}` (no `sources` and no `unchanged`),
  removes them all. Never answer a bare `{}` as a plain "done". Each source's `kind` is taken as the hook's whatever
  the addon wrote, `titleId`, `firstSeen` and `lastSeen` are the
  server's, and `meta.itemId` and `meta.fileId` are dropped (they name
  library rows). `ref` is the addon's id of the source, unique per
  episode.
- **`{"unchanged": true}`, 204 and 404** keep the kind's stored rows as
  they are (none removed, `lastSeen` kept), and are not logged as
  failures: answer so when the addon did not look this time.
- **Down, failed or slow** (an error status, a body that is not
  `ScanAnswer`, a timeout): logged, and the kind's rows stay; the
  server's other kinds go on. An addon that is stopped, crashed or turned
  off is not asked at all, and its rows stay listed (with no way to play
  them until it is back).
- **Who sees the rows.** The episode guide and a profile's source choice
  (`PUT /api/tracking/{kind}/{tmdbId}/choice`) take the kinds of the
  installed addons the profile has access to (`access`), on or off, on
  every server (a library-only one too); kids get the library's alone.
- **Kinds are opaque to the server.** It knows only `library`; every
  other kind, `online` included, is the name an installed addon declared,
  and the server gives it no meaning. A kind no installed addon declares (one of an
  addon since removed included) is not listed, and a choice of it is 400.

### More tracked titles and watch states: `tracking.titles`, `tracking.watched`

| Hook | Body → answer | When |
| --- | --- | --- |
| `tracking.titles` | `{}` → `{"titles": [{"kind", "tmdbId", "name", "poster"}]}` (`TitlesAnswer` of `ExtraTitle`) | the server lists the titles it tracks: every refresh round (about every 30 minutes) |
| `tracking.watched` | `{"userId", "kind", "tmdbId"}` (`WatchedRequest`) → `{"episodes": [{"season", "number", "watched", "positionSec"}]}` (`WatchedAnswer`) | a profile's episode guide or «Вышло новое» is shown: which of the title's episodes the profile watched |

Both are called for the household (no `X-Antresol-User`; `tracking.watched`
names the profile in `userId`), on every addon that declares them.

- **`tracking.titles`** (10 s): the titles the addon wants tracked beside
  the profiles' marks. Each one of `kind` `series` or `movie` with
  `tmdbId` > 0 is made a tracked title when the server has none (with
  `name` and `poster`, a TMDB image path, as in `POST /tracking/titles`)
  and is refreshed as a marked one; others are left out. An addon that is
  down, slow or answers something else adds no titles this time; they are
  back with its next answer. Titles the addon stops listing are no longer
  refreshed for it (the server keeps what it stored).
- **`tracking.watched`** (3 s): a movie is `season` 0, `number` 0. The
  answers are merged with the server's own progress (the library's): an
  episode is watched when any says so, and its position is the furthest
  one. An addon that is down, slow or fails is left out. Answer 204 when
  the addon knows nothing of that profile.

### `notify.interest`

| Hook | Body → answer | When |
| --- | --- | --- |
| `notify.interest` | `{"kind", "tmdbId"}` (`InterestRequest`; `kind` `movie` or `series`) → `{"userIds": [id]}` (`InterestAnswer`) | the server is about to tell profiles that new episodes of a series, or a movie, came into the library |

The profiles the addon knows care about the title (those that asked it to
follow the title, say), besides those the server already knows of (their
progress, «Хочу посмотреть»). Called for the household, on every addon
that declares it, within 5 s; the positive ids of all answers are merged,
once each. Name only current profiles (`GET /users`). Each profile is
still told only what its own settings and limits allow (its letters'
topics, a kid's or rating-limited profile's titles). An addon that is
down, slow or fails is left out, and the letters go out to the others.

### `voiceover.origLang`

| Hook | Body → answer | When |
| --- | --- | --- |
| `voiceover.origLang` | `{"fileId"}` (`OrigLangRequest`) → `{"lang"}` (`OrigLangAnswer`; ISO 639, `""` when not known) | the server is about to fit a voice-over track queued without `origLang` (see [Voice-over fitting](#voice-over-fitting)) |

The original language of the title of a library file. Called for the
household, on every addon that declares it, within 10 s; the first
answer (in addon-id order) with a language is taken, the language is kept
with the queued track. When no addon answers one (down, slow, failing, or
`""`), the fit runs without it.

### Playlist import: `import.services`, `import.stop`, `import.resolve`, `import.search`

| Hook | Body → answer | When |
| --- | --- | --- |
| `import.services` | `{}` → `{"services": [ImportService]}` (`ImportServicesAnswer`) | the admin's «Импорт» page lists its tiles (`GET /api/import/services`), and a sign-in comes back (below) |
| `import.stop` | `{"jobId"}` (`ImportStopRequest`) → 204 | an import job is cancelled or deleted: stop the addon's read of it, if one runs |
| `import.resolve` | `{"ids": [id]}` (`ImportResolveRequest`) → `{"tracks": [ImportSnapTrack]}` (`ImportResolveAnswer`) | an uploaded Google Takeout gives songs only by video id: name them |
| `import.search` | `{"kind": "music", "q"}` (`ImportSearchRequest`) → `{"candidates": [ImportCandidate]}` (`ImportSearchAnswer`) | the review looks for a song's album (a song's candidates, «Найти вручную») |

The playlist import is the server's ([Imports](#imports)); these hooks
add the music services an addon reads and the catalog it searches. All
four are called for the household (no `X-Antresol-User`), on every addon
that declares them. No hook carries a service's sign-in, code or token:
they stay with the addon.

These hooks have **no first-party answerer until a later stage; with none, the core degrades:** as [Imports](#imports) lists.

- **`import.services`** (2 s): `ImportService` is `{"id", "configured",
  "connect", "setup", "files"}`, a tile of the page: `id` one of the services
  the import knows (`playlistimport.Services` in the server's source:
  `apple`, `file` and the sign-in services); another is dropped;
  `connect` how it signs in (`redirect`, `paste`, `device`, `file`,
  `app`); `setup` the owner's steps while it is not `configured`; `files`
  what it can also be read from. The tiles of all addons are shown in
  addon-id order, and the first addon to list a service **owns** it. An
  addon that is down, slow or answers something else adds none; with no
  tile at all the page offers the file import alone.
- **`import.stop`** (5 s) is fired and not waited for: the cancel or
  delete is done whatever the addon answers. An addon that misses it finds
  the job gone when it writes the snapshot (`PUT …/snapshot` 404 or 409).
- **`import.resolve`** (30 s per call): the ids come in batches of at most
  25, one call per batch, until all are asked or the upload's 20-minute
  naming budget ends. Name the ids you can, each a track with `key` = the
  id and a `title`; leave out the ones you cannot (private videos,
  malformed ids). An id named by an earlier addon (in id order), one not
  asked for, or a track without a title is dropped. An addon that is down,
  slow or fails names nothing, and its ids stay unknown: the preview
  counts them (`report.unresolved`). A batch that times out cools
  `import.resolve` of that addon for 60 s (see [A hung addon](#hooks-the-server--the-addon)): its
  remaining ids stay unknown. The upload is a `reading` job of the
  server meanwhile.
- **`import.search`** (30 s): `ImportCandidate` is `{"tmdbId", "extId",
  "kind", "title", "subtitle", "year", "poster", "overview", "inLibrary",
  "libraryItemId", "aliases", "matchedTrack", "byArtist"}`; `extId` is the
  album's MusicBrainz release group (what the review's pick stores),
  `subtitle` its artist, `inLibrary`/`libraryItemId` the library's album
  of it, `matchedTrack` the song of the query the album holds,
  `byArtist` set on the music of the artist the query names. The
  candidates of all addons come in addon-id order. An addon that is down,
  slow or fails adds none (an empty list). When none gives a 200, a 409
  refusal is the admin's: its text `Каталог недоступен — проверьте
  интернет` is a 503 with that text (the catalog is offline), any other
  text a 400. With no enabled addon declaring the hook the search is a 503
  `Каталог недоступен — проверьте интернет`, as without a catalog.

**Sign-ins come back through the session.** A service's console has the
server's callback registered: `<publicUrl>/api/import/oauth/<service>/callback`
(build the redirect URI from `/info`'s `publicUrl` and that path; without
an https `publicUrl`, use the paste mode). The server answers that
address for a signed-in admin with **302** to
`/api/addons/<id>/x/admin/import/oauth/<service>/callback?<the same query>`,
where `<id>` is the addon whose `import.services` lists the service now;
the addon's route runs behind the proxy as any other (session, admin,
`X-Antresol-User`), so the addon checks `state` against the admin who
began the sign-in. The 302 carries `Cache-Control: no-store` and
`Referrer-Policy: no-referrer`, has no body, and the server logs no part
of its query. Without a session it is 401, for a profile that is not an
admin 403, and 404 «Сервис не подключён» when no addon lists the service.
An addon that listed it but is slow to answer or in its down period keeps
it (the 302 still goes to it); a stopped or failing one does not. There
is no route without a session.

«Повторить» of an album (`POST /api/import/groups/{id}/retry`, the
admin's own album only) is the server's, with or without an addon: the
album is back in `waiting`, for the addon or the library.

### «Переезд»: `migrate.leftovers`

| Hook | Body → answer | When |
| --- | --- | --- |
| `migrate.leftovers` | `{"lang"}` (`LeftoversRequest`) → `{"lines": [LeftoverLine]}` (`LeftoversAnswer`) | the admin's «Переезд» screen lists what does not move (`GET /api/migrate`) |

What the old services an addon moves from have that does not move (lists,
rules, watched folders): the screen shows these lines first, before the
server's own (Jellyfin's TV channels).

- Called for the household (no `X-Antresol-User`), on every addon declaring
  it, within 10 s. `lang` is the screen's language (`ru`, `en`); the lines
  come already written in it.
- A `LeftoverLine` is `{"source", "name", "status", "reason"}`: `source`
  the old service's id, `name` what stays, `reason` the line shown.
  `status` is `warning` (it stays behind) or `failed` (it could not be
  checked); any other value is taken as `warning`.
- The lines of all addons are shown in addon-id order. An addon that is
  not running, slow, failing or answers something else adds none (logged);
  204 and 404 add none.
- An answer every declaring addon gave is kept 60 s per language; an
  addon that stops meanwhile keeps its last lines on the screen until
  those 60 s end. An answer given while an addon is down is not kept:
  the next look asks again.
- Only the screen asks. A run's report lists the server's own leftovers
  alone; the addon's run keeps its own.

## The web part

The web archive is served at `/addons/<id>/ui/…` while the addon is on, to
the profiles the addon is for (403 for others, as the proxy). The list of
bundles, `GET /api/addons/web`, lists for each profile the addons it may
load (403 for kids). The app imports
**`/addons/<id>/ui/index.js?v=<version>`**, an ES module, for each addon
that is on, running and has a web part. When it runs, it calls
`registerAddon(id, contributions)`. The app reads the list when a profile
signs in, and again when an admin installs, switches, updates or removes
an addon in «Расширения» there; another open window (or a change made
through the API) shows it on its next load.

- **Only the entry is versioned.** Every other file (chunks, images) must
  change its name when its content changes: content-hashed names, as Vite
  makes by default. `index.css` (below) is versioned like the entry. Files
  are served with `Cache-Control: no-cache`.
- **Nothing imports `index.js`.** A chunk importing it would load it a second
  time under a URL without `?v=`, running the addon twice. The SDK's plugin
  makes `index.js` a one-line stub that imports the real entry,
  `chunks/main-<hash>.js`; the other chunks import that one.
- **Stylesheets.** A bundle may ship one `index.css` beside `index.js`,
  built from the addon's **CSS modules** only (`X.module.css`, whose classes
  the build hashes). `GET /api/addons/web` then lists it as `style`
  (`/addons/<id>/ui/index.css?v=<version>`), and the app adds it as one
  `<link rel="stylesheet">` beside the entry, for the same profiles (never a
  kid). A stylesheet that fails to load never holds up the script: the addon
  is only unstyled. The links of addons no longer listed are removed. The
  SDK's plugin parses the built `index.css` and refuses, at build time:
  - a plain (non-module) stylesheet, `:global()` and `@import`;
  - a selector, in any part of a selector list, whose first compound has
    none of the module's classes: `:root`, `html`, `body`, `*`, a bare
    element, attribute or id selector;
  - a compound after `+` or `~` without one of the module's classes
    (`.x ~ *` and `.x + div` reach the app's elements beside the addon's);
  - at-rules other than `@media`, `@supports` and `@container`;
    `@keyframes` (its CSS module hashes the name; a name prefixed `<id>-`
    also passes); and `@font-face` whose `font-family` is prefixed `<id>-`
    and whose `src` is relative `url()`s only. So `@property`, `@layer`,
    `@namespace`, `@page`, `@counter-style`, `@font-feature-values`,
    `@scope` and the rest are refused. The id is the plugin's `id` option,
    or the `id` of `antresol-addon.json` in the Vite root or the folder
    above it;
  - `!important`, in `@keyframes` too;
  - the app's page-frame tokens `var(--nav-height)`, `var(--nav-height-compact)`
    and `var(--page-top)` (the app draws an addon page's frame: «routes»);
  - a URL other than a relative file inside the bundle (no scheme, no
    leading `/`, no `..`, no `data:`), wherever it is: `url()` in any value
    or custom property, the options of `image-set()` (and
    `-webkit-image-set()`, `cross-fade()`), `@font-face` `src`; and
    `element()`. The plugin never inlines an asset, so a small image stays
    a file;
  - in `@font-face`, a descriptor the parser cannot read (a malformed
    `src`).
  The check is a guard for the app's look, not a sandbox: the addon's
  script runs with the page's full DOM.
  Style with the app's tokens (`var(--label)`, `var(--t-title-2)`…); the
  app's own components come from the SDK's `ui`.
- A bundle that fails or takes over 10 s to load is skipped; the app works
  on without it. An element that throws shows a notice in its place.
- `GET /api/me` says `"addons": true` when the server runs addons (not
  `VAULT_ADDONS=off`).

### Who gets an addon

| Profile | Manifest `access: "admins"` (default) | `access: "adults"` |
| --- | --- | --- |
| admin | everything | everything |
| adult (not an admin, not a kid) | nothing: not in its bundle list; the bundle and the proxy answer 403 | the bundle list, the bundle and the proxy, **except `x/admin/…`** (403) |
| kid | nothing (403 «Недоступно в детском профиле») | nothing |

The routes that manage addons (install, update, enable, the log, the list
in «Расширения») are for admins only, whatever the manifest says. The
addon still learns who asks from `X-Antresol-User` (`isAdmin`) and may
refuse more itself; what an addon keeps for admins belongs under its own
`/api/admin/…` (reached as `x/admin/…`), which the server guards.

Events follow the same table: an adult profile gets the events of an
addon with access `adults` but never its `addon.<id>.admin.*`, and none of
an addon with access `admins` (see Events).

So does `home.continue`: it is asked only of the addons the profile gets
by this table, and never for a kid. And so do the `channels.*` hooks, by
the channel's profile (see [«ТВ»](#тв-titles-outside-the-library-channels)).

### Shared modules

An addon does not bundle React, the router or the SDK: it imports the app's
own, through `globalThis.__antresol.shared`. The SDK's Vite plugin
(`@antresol/addon-sdk/vite-plugin`, `antresolAddon()`) maps the imports and
fails the build on anything else from these packages. Exactly these are
shared:

| Module | Names |
| --- | --- |
| `react` | React's stable public API only, as named exports and a default export holding the same: `Activity`, `Children`, `Component`, `Fragment`, `Profiler`, `PureComponent`, `StrictMode`, `Suspense`, `cloneElement`, `createContext`, `createElement`, `createRef`, `forwardRef`, `isValidElement`, `lazy`, `memo`, `startTransition`, `use`, `useActionState`, `useCallback`, `useContext`, `useDebugValue`, `useDeferredValue`, `useEffect`, `useEffectEvent`, `useId`, `useImperativeHandle`, `useInsertionEffect`, `useLayoutEffect`, `useMemo`, `useOptimistic`, `useReducer`, `useRef`, `useState`, `useSyncExternalStore`, `useTransition`, `version` (no `act`, `unstable_*`, dev-only or server-only names) |
| `react/jsx-runtime` | `jsx`, `jsxs`, `Fragment` (production builds only: no `jsxDEV`) |
| `react-dom` | `createPortal` |
| `react-router` | `Link`, `NavLink`, `Navigate`, `Outlet`, `useLocation`, `useNavigate`, `useNavigationType`, `useParams`, `useSearchParams` |
| `@antresol/addon-sdk` | `registerAddon`, `addonApi`, `ApiError`, `useAddonT`, `useUser`, `useEvents`, `ui`, `Icon`, `core`, `formatSize`, `formatRate`, `formatPercent`, `locale`, `useIsCompact`, `ViewMenu`, `ViewToolbar`, `useUrlView`, `useMediaSegments`, `MEDIA_SEGMENTS`, `mediaSegmentIcon`, `mediaSegmentOf`, `mediaKindIcon`, `DownloadButton`, `WatchButton`, `cardHref`, `titlePath`, `KIND_ICON`, `kindLabel`, `useDebounced`, `PendingOverlay`, `useWatchWay`, `useWatchWays`, `MigratePage`, `yieldSession`, `Chip`, `Chips`, `clock`, `langBadge`, `langCode`, `languageName` |

The same names, one module per line (`<module>: <names>`, a module may
take several lines), as the SDK's Vite plugin lists them (`SHARED`); the
server's tests compare the two both ways:

```inventory-shared
react: Activity, Children, Component, Fragment, Profiler, PureComponent, StrictMode, Suspense, cloneElement
react: createContext, createElement, createRef, forwardRef, isValidElement, lazy, memo, startTransition
react: use, useActionState, useCallback, useContext, useDebugValue, useDeferredValue, useEffect
react: useEffectEvent, useId, useImperativeHandle, useInsertionEffect, useLayoutEffect, useMemo, useOptimistic
react: useReducer, useRef, useState, useSyncExternalStore, useTransition, version
react/jsx-runtime: Fragment, jsx, jsxs
react-dom: createPortal
react-router: Link, NavLink, Navigate, Outlet, useLocation, useNavigate, useNavigationType, useParams, useSearchParams
@antresol/addon-sdk: ApiError, Chip, Chips, DownloadButton, Icon, KIND_ICON, MEDIA_SEGMENTS, MigratePage
@antresol/addon-sdk: PendingOverlay, ViewMenu, ViewToolbar, WatchButton, addonApi, cardHref, clock, core
@antresol/addon-sdk: formatPercent, formatRate, formatSize, kindLabel, langBadge, langCode, languageName, locale
@antresol/addon-sdk: mediaKindIcon, mediaSegmentIcon, mediaSegmentOf, registerAddon, titlePath, ui, useAddonT
@antresol/addon-sdk: useDebounced, useEvents, useIsCompact, useMediaSegments, useUrlView, useUser, useWatchWay
@antresol/addon-sdk: useWatchWays, yieldSession
```

The SDK (`@antresol/addon-sdk`):

- `registerAddon(id, contributions)`: the addon's slots (below); a second
  call replaces the first.
- `addonApi(id)`: `get<T>(path, signal?)`, `post<T>(path, body?)`,
  `put<T>(path, body?)`, `patch<T>(path, body?)` and `del<T>(path)` to the
  addon's own API through the proxy (every method but `get` sends a JSON
  body, `{}` without one). A failed call throws `ApiError` (`status`, and
  `message` = the answer's `error`). The path is relative to the addon's
  prefix (`admin/sources/3`, one trailing `?query` allowed); a leading `/`, a
  scheme, an empty segment, `#`, a backslash, a space or control character
  (raw or %-encoded) or a `.`/`..` segment in any spelling (`%2e%2e`) makes
  the call reject before anything is sent.
  `stream<T>(path, onLine, signal?)` is a `GET` whose answer is NDJSON:
  the body is read as it arrives, and each non-empty line, JSON-parsed,
  goes to `onLine` in order. It resolves at the end of the body and rejects
  with `ApiError` on a non-2xx answer (its `error`) or on a line that is not
  JSON; the same paths are refused, before anything is sent. An abort of
  `signal` stops the read.
- `useAddonT(id)`: `t(key, vars?)` over the addon's `strings`, in the
  interface language; `{name}` in a string takes `vars.name`; a missing key
  shows itself.
- `useUser()`: who is signed in, `{ id, name, isAdmin, isKid, lang, remote }`
  (`name`: the profile's name, as the addon's own answers name their
  authors; `lang`: the interface language; `remote`: this device is
  outside the home network, as `GET /api/me` says), or `null` before a
  sign-in.
- `useWatchWay()`: the «Смотреть ▾» way the signed-in profile chose last
  (an addon way's id `<addonId>:<label>`), read only and live:
  the component re-renders when the profile chooses another way, on this
  page or in another tab. `null` when none was chosen, before a sign-in, or
  for a kid. The core keeps the choice; an addon never stores it itself.
- `useWatchWays({ kind, tmdbId, title, poster? })`: the «Смотреть ▾» ways
  of **every** addon the profile has for that title (`kind` `movie` or
  `series`), as `WatchWay`s (`{ id, label, run }`, ids `<addonId>:<label>`,
  in addon id order), for an addon's own rows of titles (its search
  results' «Смотреть ▾», say). They are the `itemActions` entries with
  `watchWay: true`, with `visible` and `useAvailable` applied as on a
  title's page (the `item` they get is the title outside the library,
  `item.id` `0`); a way whose `useAvailable` still asks is not in the list
  yet and joins it when it answers `true`. The calling addon's own entries
  are among them: leave out those it draws itself. An element a way's `run`
  returns is drawn by the app until `close()`; text it says shows in the
  app's notice. `[]` for a kid and before a sign-in. A way's `useAvailable`
  runs in a component the app draws in its own shell, not inside the
  caller's component tree: it sees none of the caller's own React context
  (a provider of the addon, say), only the app's and what the addon's
  module reaches itself (its own stores, `addonApi`).
- `useEvents(handler, types?)`: the app's events, e.g.
  `['addon.<id>.ping']`.
- `ui`: the app's components (`ui.Button`, `ui.Notice`, `ui.Card`, …),
  **best effort** (see Versioning); `Icon` and its names.
  `ui.Badges({ items, className?, inline? })` draws badges as a list, or
  with `inline` as spans (inside a button or a label).
- `ui.Player`: the core's full-window video player, behind `React.lazy`:
  its code (and hls.js) loads on its first render, never with the app or
  with the addon's bundle, so render it inside a `Suspense` (a
  `fullWindow` route already is one). It plays one or more parts
  (`parts: MediaFile[]`, `startSec`, `title`, `subtitle?`, `artwork?`,
  `onClose`, `next?: UpNext`, `topActions?`, `overlay?`, `overlayOpen?`,
  `suspendKeys?`; `type PlayerProps`). For a source of the addon's own,
  pass `start(part, request)` and `stream` (`StreamPlayback`):
  - `start` gets a `PlayRequest` (`caps`, `startSec`, `quality`: the
    source quality, `replaceSession?`, `refresh?`: a re-play after a stale
    link, to resolve fresh) and answers a `PlayResponse` with `mode:
    'stream'`, `kind` (`hls` or `mp4`), `url` (the addon's proxy path,
    `/api/addons/<id>/x/…`), `session` (handed to `stop`), `startSec`,
    `audio`, `audioIndex`, `subtitles` and optionally `quality`
    (`StreamQualityInfo`: `{ options, current }`, the player's «Качество»
    menu). Whatever mode the answer names, the player plays it as a
    stream: the whole video from 0, every seek local, no heartbeat.
  - `stream` = `{ stop(session, keepalive?), initialQuality?,
    saveProgress(positionMs, durationMs, keepalive), onOtherSource(),
    onQuality?(q) }`. A `410` on the stream (or no first frame for 90 s, a
    stall) plays it again with `refresh` at the same place, at most twice
    in 5 minutes; then the player says «Источник не отвечает» and offers
    «Другой источник» (`onOtherSource`) and another quality.
  - The player does not stop the music: call `yieldSession()` when the
    page opens.
- `yieldSession()`: pauses the music and hands the system media controls
  back (a page that plays its own video).
- `Chip` and `Chips` (and `type ChipProps`): the app's pill choices, a
  radio group or toggles (**best effort**, like `ui`).
- The player's helpers: `clock(sec)` («1:02:03»), `langBadge(lang)` (a
  track's short badge, «RU»), `langCode(lang)` (`ru`; '' when unknown),
  `languageName(lang)` (in the interface language).
- `core`: read helpers of the core's own API — `core.getItem(id, signal?)`
  (a library item, as the app's item page reads it), `core.getTitle(kind,
  tmdbId, signal?)` (a movie's or series' page outside the library, as
  `TitleInfo`; it may answer 403 for a rating-limited adult profile: the
  title is not visible to this profile, which the addon shows as such, not
  as an error), `core.imageUrl(ref,
  width?)` (the URL of a cached image) and `core.getDisk()` (the media
  disk's total and free bytes).
- Formatting, in the interface language: `formatSize(bytes)`,
  `formatRate(bytesPerSecond)`, `formatPercent(n)`, `locale()`.
- `useIsCompact()`: true on a phone-sized window.
- List-page helpers, the same the app's pages use (**best effort**, like
  `ui`): `ViewMenu`, `ViewToolbar`, `useUrlView(spec)` (a view's options
  kept in the URL), `useMediaSegments`, `MEDIA_SEGMENTS`,
  `mediaSegmentIcon`, `mediaSegmentOf(kind)`, `mediaKindIcon(kind)`, and
  the types `MediaSegment`, `ViewSpec`.
- The core's own buttons and search helpers (**best effort**, like `ui`):
  `DownloadButton` (and `type DownloadState<Hint>`: an `active` state may
  carry `stalled?: string` and `hint?: Hint`, the addon's own words on why
  it makes no headway, which the button only carries), `WatchButton` (and `type
  WatchWay`), `cardHref(card)` (a card's page), `titlePath({kind, tmdbId})`
  (a title's page outside the library), `KIND_ICON` and `kindLabel(kind)`
  (a result kind's icon and name), `useDebounced(value, ms)` and `type
  Found<T>` (a search's state), `PendingOverlay({pending, corner})` (the
  ring a pending card draws, the same as the library's; `pending` is a
  `PendingState`).
- `MigratePage({ api?, full? })`: the core's «Переезд» page, for an
  addon's own migration page (a `routes` entry). `api` (`MigrateApi`) is
  where it reads and runs: `migrateStatus()`, `migrateState()`,
  `migrateRun(req)`, `migrateReports()`, `migrateReport(id)`, the same
  shapes as the core's `GET /api/migrate`, `GET /api/migrate/state`,
  `POST /api/migrate/run`, `GET /api/migrate/reports` and
  `GET /api/migrate/reports/{id}`, and `useProgress(handler)`, a hook the
  page calls on every render that hands `handler` each progress event's
  data (`{ reportId }`); keep the object the same for the page's life.
  `full` (`MigrateFullApi`) is the addon's part of its page: `source(id)`
  (a `SourceMeta`: icon and texts of a service the core does not know),
  `TopCards`, `HistoryAction` and `ConfirmItems` (components given a
  `MigrateCtx`, a report or the chosen sources), `progressNote(current)`,
  `subtitle`, `rollbackTitle?` (the title of a report that gave the
  items back, `rollbackOf`; without it the core's neutral one) and
  `SourceSetup?` (a component given the `MigrateCtx` and a service's
  status, drawn under that service's card outside its checkbox: another
  way to reach the service, e.g. its address and key; a core without it
  draws the cards alone). `subtitle`,
  `rollbackTitle` and `SourceMeta`'s `what` and `note` are a
  `MigrateText`: a key of the **core's** catalog (a `subtitle` key gets
  `{app}`), or a function `() => string` that returns the addon's own text
  in the interface language, which the page calls while it draws (so it
  follows a language change; `useAddonT` or the addon's own catalog).
  Without
  `api` the page is the core's own. The `full` part
  shows only where the status has `state`. An addon page with an `api`
  draws no `migrateSources` cards.
- Types: `AddonContributions`, `AddonRoute`, `AddonRouteProps`, `ManageTile`, `ItemAction`,
  `PlayerNote`, `NavBadge`, `SearchBlock`, `EmptyStateAction`,
  `RequestAction`, `StatsBlock`, `ItemPanel`, `EpisodeAction`,
  `PlayerTrackAction`, `TrackActionItem`, `ProfileMenuEntry`,
  `MigrateSourceEntry`, `ImportSource`, `ImportSourcesProps`, `ItemDetail`,
  `KidRequest`, `PendingState`, `PlayerInput`, `ServerEvent`, `TitleInfo`,
  `TitleEpisode`, `EpisodeSource`, `EpisodeCtx`, `EpisodePlay`,
  `MediaSegment`, `ViewSpec`, `MigrateApi`, `MigrateFullApi`,
  `MigrateCtx`, `MigrateProps`, `MigrateReport`, `MigrateText`, `SourceMeta`,
  `ImportJob`, `ImportService`, `PlayerProps`, `StreamPlayback`, `UpNext`,
  `MediaFile`, `PlayRequest`, `PlayResponse`, `StreamQualityInfo`.

### Slots

```ts
registerAddon('hello', {
  routes: [{ path: 'page', title: 'page.title', element: Page }],
  manageTiles: [{ label: 'tile', icon: 'puzzle', to: 'page' }],
  itemActions: [{ label: 'action.hello', icon: 'sparkles', visible: (item) => item.kind === 'movie', run: (item) => `…${item.title}` }],
  playerNotes: [{ element: ({ input }) => <span>Готовим {input.name}…</span> }],
  navBadges: [{ to: 'page', icon: 'puzzle', label: 'badge', useCount: useWaiting }],
  searchBlocks: [{ element: ({ q, scope, nothing }) => <Found q={q} scope={scope} first={nothing} /> }],
  emptyStateActions: [{ scope: 'music', label: 'empty.find', icon: 'search', to: '/search?dk=music' }],
  requestActions: [{ visible: (r) => r.kind === 'movie', element: ({ request, onClose, onApproved }) => <Pick request={request} onClose={onClose} onDone={onApproved} /> }],
  statsBlocks: [{ element: () => <HelloStats />, admin: true }],
  itemPanels: [{ element: ({ item, title }) => <HelloPanel tmdbId={title?.tmdbId ?? item.tmdbId} /> }],
  episodeActions: [{ kinds: ['hello'], use: useHelloEpisodes }],
  playerTrackActions: [{ label: 'track.hello', icon: 'sparkles', run: (it, close) => <HelloSheet id={it.id} onClose={close} /> }],
  profileMenu: [{ label: 'menu.hello', icon: 'puzzle', to: 'page', visible: (u) => !u.isAdmin }],
  migrateSources: [{ id: 'old', name: 'migrate.old', icon: 'arrowRight', to: 'migrate', admin: true }],
  importSources: [{ element: (p) => <HelloImport {...p} />, admin: true }],
  events: ['ping'],
  strings: {
    ru: { 'page.title': 'Привет', tile: 'Привет', 'action.hello': 'Привет от расширения', badge: 'Ждут: {count}', 'empty.find': 'Найти', … },
    en: { 'page.title': 'Hello', tile: 'Hello', 'action.hello': 'Hello from the addon', badge: 'Waiting: {count}', 'empty.find': 'Find', … },
  },
})
```

| Slot | What the app does with it |
| --- | --- |
| `routes` | a page inside the app's shell at `/addons/<id>/<path>`; `title` is the window's title. **The app draws the page's frame; the page draws none** (no room for the floating navigation, no gutters, no page end: start with the page's own title). A page a relative `manageTiles` or `migrateSources` `to` of the addon opens, and every page under that path (`sources` → `sources/add`, `sources/5/edit`), is drawn for an admin in «Управление», beside its section list, like the core's sections (on a phone with a back link to that list; a page under the tile's path brings its own back link, as the core's deeper sections do); any other page sits in the plain page frame. The SDK's plugin refuses the frame's tokens (`--nav-height`, `--nav-height-compact`, `--page-top`) in an addon's stylesheet. A path may hold `:name` segments (`sources/:sourceId/edit`): each takes one segment of the address, percent-decoded (an encoded `/` reads as `/` in the value), and the page gets the values as `useParams()` (the shared `react-router`; the app's own `addonId` and `*` are there too) and as the `params` prop of its element (`element({ params })`, `AddonRouteProps`, a plain object of strings), so a page written against react-router's `useParams()` needs no change. A path with no `:name` matches as it is, any number of slashes at either end of the path or the address aside; letter case counts (in the addon's id and its paths; only the app's own `/addons/` before them is case-insensitive); of several routes that fit an address the first registered wins. A route path is read as react-router's `matchPath` pattern: `*` at the end takes the rest of the address and `:name?` makes a segment optional (its value is then absent); an addon's own `*` is what its `useParams()` gives, over the app route's. An address that fits none shows the plain «not here» screen. With **`fullWindow: true`** the route covers the whole window like the app's own players, outside the shell (no navigation, no mini player): for a player page, rendering `ui.Player`. The app remembers on the device which paths of an addon are `fullWindow`, so a cold load of one (a reload mid-play) draws the player's black screen, not the shell, until the bundle registers. Kids get no route of any kind (they are sent to the home page). A path must not be `ui` or start with `ui/` (that is where the bundle is served): such a route is dropped with a console warning. |
| `manageTiles` | a tile in «Управление»; `to` is a route path of the addon (relative) or an app path starting with `/` |
| `itemActions` | a round button among the actions of an item's page (a film, a series). `visible(item)` hides it; `run(item, close)` runs on a press. A string `run` returns (or resolves to) shows in a notice under the actions; a throw shows as a failure there. An element `run` returns (not a promise of one) is drawn by the app — make it a sheet, `ui.Sheet` with `onCancel: close` — until the addon calls `close()`, or the page moves to another item; one at a time (a new one takes the last one's place, and an old `close` then closes nothing); an element that throws is removed and the failure shows under the actions. With **`watchWay: true`** the entry is no button: it is a way of the page's «Смотреть ▾» (beside the online ways; its way id, which the app remembers per profile as the chosen way, is `<id>:<label>`), and its `run` works the same. **`useAvailable(t)`** (optional, a watch way's) is a hook: whether the way is offered for the title `t` = `{ kind, tmdbId, itemId }` (`kind` `movie` or `series`; `itemId` `0` for a title outside the library): `true` offered, `false` not, `null` still asking, so not offered yet. The app calls it in a component of its own per entry, so it may use hooks (keep their order); while it asks, the page is drawn without the way and the «Смотреть ▾» menu gains it when it answers `true` (nothing else on the page moves). One that throws hides only its way. On another kind of item a way with `useAvailable` is not offered. A page opened with **`?way=<addonId>:<label>`** (a title's or an item's) runs that way once, as soon as it is offered (it waits for a `useAvailable` that still asks), and the parameter then leaves the URL; it does not change the remembered way. |
| `playerNotes` | a note under the player's spinner while one of **this addon's** inputs (`/play/input/<id>`) loads or rebuffers, in place of «Загружается…». `element({ input })` gets the input as `GET /api/play/input/{id}` answers it (`PlayerInput`: `id`, `addonId`, `name`, `itemId`, `title?`, `slow`, `meta?`, `startSec`); other addons' notes are not shown there. Several notes show one after another. A note that throws is replaced by nothing, except the addon's first note, whose place shows the plain «Загружается…»; the other notes still show. Two optional fields: `actions({ input })`, buttons the player shows at its top while the input plays; `useProgress(input)`, a hook the app calls (in a component of its own per note) that answers how far the source has got: the player keeps waiting while it grows. |
| `navBadges` | a count beside the app's navigation (the top bar's trailing group, the compact bar on phones): `{ to, icon, label, useCount }`. `useCount()` is a hook; at 0 the badge is hidden, above 99 it reads `99+`. `label` is a key whose string takes `{count}` (one wording for every count: «Ждут: {count}»). |
| `searchBlocks` | a block «Поиск» shows under the library's results: `element({ q, scope, text, nothing })` — the query as searched, the page's filter (`all`, `movies`, `series`, `music`, `books`, `audiobooks`, `comics`), the field's text as typed, and whether the library found nothing. A profile with at least one search block **can add** titles from outside the library: «Поиск» and the empty library pages then say so. |
| `emptyStateActions` | a button on an empty library page: `{ scope, label, icon?, to }`, `scope` one of `home`, `video`, `music`, `book`, `audiobook`, `comic` |
| `requestActions` | what «Одобрить» opens for a kid's request in «Управление → Просьбы»: `{ visible?(request), element({ request, onClose, onApproved }) }`, for a request of any kind (a movie, a series, an album, a book…). The first entry whose `visible` says yes (or that has none) is drawn as the sheet; `onApproved()` once it approved the request (the server took what the admin picked), `onClose()` to close it with nothing approved. With none, «Одобрить» approves at once; when the sheet throws, the request stays waiting and the page shows an error (nothing is approved). |
| `statsBlocks` | a block of «Управление → Статистика»: `element()` |
| `itemPanels` | a panel under the action row of an item's page and of a title's page (a movie or series outside the library): `element({ item, title? })`. On a title's page `item.id` is `0`, `item.tmdbId` is the title's and `title` is the page's `TitleInfo` (an `itemActions` entry gets the same `item` there). A panel that throws is drawn as nothing. With **`inRow: true`** the entry is not under the row but one of its round buttons, after the addon's `itemActions` (a toggle with a state of its own, «Следить»): its `element` draws one `ui.IconButton` with `tile` (a phone shows it as one of the row's tiles). |
| `episodeActions` | how the episode guide plays sources of the addon's own kinds: `{ kinds, labels?, order?, use(ctx) }`. `use` is a **hook**: the app calls it in a component of its own for each entry, once per guide, with `ctx` = `EpisodeCtx` (`kind`, `tmdbId`, `title`, `poster?`, `episodes`, `choice?`), and draws the answer's `node` (its sheets). The answer, an `EpisodePlay` (`play(episode, source)`, `other(episode)` for «Другие источники…», `download?(episode, source)`, `prefer?`, `node`) or `null` (nothing to offer now), serves the rows whose source `kind` is one of `kinds`. An entry registered after the guide is drawn joins it; one that throws drops out (its kinds play nothing), the guide stays. A title's page whose «Смотреть» plays the guide's next episode calls the same entries (its own `use` call) for a source of those kinds. The core's own kind `library` is never an entry's: the app drops it from `kinds` (with a console warning), and an entry left with none is dropped. Optional, per kind: `labels` (`{ [kind]: key }`, a key of the addon's `strings`: the kind's name in the guide's badges and the source sheet; without one the kind shows as itself) and `order` (`{ [kind]: number }`, a number **> 0**: where its sources sort, on a scale where the core's `library` is 0, so the library's own file always ranks first; an order of 0 or below is dropped with a console warning, and without one the kind sorts last: kinds without an `order` rank alike: the better quality first, then the order the server listed the sources in). The first entry that names a kind's label or order wins; a kid's guide has no entries, so it shows the raw kind (it plays the library only). |
| `playerTrackActions` | a row of the player's track menu while a library movie or episode plays (never while a stream plays): `{ label, icon?, visible?(item), run(item, close) }`, `item` = `TrackActionItem` (`id`, `title`, `kind`: the movie, or the series of an episode). An element `run` returns is drawn over the player until `close()`, as an item action's; a `ui.Sheet` in it mounts inside the player, so it shows in full screen too. |
| `profileMenu` | an entry of the profile menu: `{ label, icon, to, visible?(user) }`, `to` as `manageTiles`' (relative to `/addons/<id>/`, or an app path); `visible(user)` (the signed-in profile, as `GET /api/me` answers it) hides it; one that throws hides only its own entry. |
| `migrateSources` | a card of «Управление → Переезд» that opens the addon's own migration page: `{ id, name, icon, to, summary? }`; `to` is an app path starting with `/`, else relative to `/addons/<id>/`; `summary()` is drawn under the name inside the card's link, so it is **text only** (what waits there, say: no buttons or links), and one that throws is drawn as nothing. The cards stand after the core's own services. |
| `importSources` | the way in of «Управление → Импорт» while the server lists import services (`GET /api/import/services`, from `import.services`): `{ element(props) }`, props `ImportSourcesProps` = `{ services, onFiles(files), uploading, onJob(job), onStart(), onProblem(text) }`: `onFiles` uploads files as the file import does, `onJob` shows a job the element started (a sign-in's reading job), `onStart` clears the page's problem and notice, `onProblem` says one over the page. The first entry is drawn in place of the file import; while the app is still loading the profile's addons, the page waits for it. With no service listed, none registered, or when it throws, the file import stays. |
| `events` | the short names of the addon's events the app subscribes to |
| `strings` | `{ ru, en }` catalogs for `useAddonT` |

The keys `registerAddon` takes (the app's `AddonContributions`); the
server's tests compare this list with the app's type both ways:

```inventory-contributions
routes
manageTiles
itemActions
playerNotes
navBadges
searchBlocks
emptyStateActions
requestActions
statsBlocks
itemPanels
episodeActions
playerTrackActions
profileMenu
migrateSources
importSources
events
strings
```

Every entry of every slot (`routes`, `manageTiles`, `itemActions`,
`playerNotes`, `navBadges`, `searchBlocks`, `emptyStateActions`,
`requestActions`, `statsBlocks`, `itemPanels`, `episodeActions`,
`playerTrackActions`, `profileMenu`, `migrateSources`, `importSources`) may say
**`admin: true`**: the app then shows it to admins only (an admin's route
is simply not there for an adult: the page says the addon has no such
section). Use it for whatever calls `x/admin/…`; it only hides the entry,
the server's refusal of `x/admin/…` is what protects it. Note that
«Управление» itself is for admins, so `manageTiles` reach admins only.

A `to` (of `manageTiles`, `navBadges`, `emptyStateActions`, `profileMenu`, `migrateSources`) starting with
`/` is an app path; any other is relative to `/addons/<id>/`. An element
that throws is drawn as nothing (or a notice); the rest of the page stays.

Labels and titles are **keys of the addon's `strings`**: every `title` of
`routes` and `label` of `manageTiles`, `itemActions`, `navBadges`,
`emptyStateActions`, `playerTrackActions` and `profileMenu`, and every `name` of
`migrateSources`, is resolved by the app when it draws it, in the
interface language, the way `useAddonT` does (so it follows a language
switch). A key missing from `strings` shows as itself, so a plain-text
label still shows. Contributions hold no functions for labels: a label
built from runtime data is only the badge's `{count}`.

## Coming later in contract 1

Planned for later releases, none served by this server yet; an addon
must not rely on any of them until this document describes it in place,
and Versioning's rules hold for each when it lands:

- **File roots declared by addons**: a core API route `/files/roots`, the
  roots also in `/info`.

## Building an addon

The addon template (`antresol-addon-template`) has all of this wired: a Go
program with `/health`, a sample `/api/hello` and an admin-only
`/api/admin/hello`, on `addonkit` (below); a web part on the SDK's Vite
plugin; a `Makefile` (`make build` for the three platforms, the web archive
and the manifest with its checksums, `make local` for a development server)
and a release workflow for tags `v<version>`. The `hello` addon, made from
it, uses every part of this contract.

### `addonkit`: the Go library

`github.com/antresol-app/antresol/addonkit` (the core repository's
`addonkit/`, a module of its own) is the Go side of this contract, under the
**MIT License** and on the standard library only. Use it or speak the
contract yourself; it adds nothing the contract does not say.

- **Types.** The JSON shapes above (`Item`, `File`, `Track`, `User`, `Hold`,
  `Bound`, `ImportResult`, `SeriesAudio`, `Info`, `TrackingTitle`,
  `TrackingEpisode`, `TrackingSource`, `ScanRequest`, `ScanAnswer`,
  `TitlesAnswer`, `ExtraTitle`, `WatchedRequest`, `WatchedAnswer`,
  `InterestRequest`, `InterestAnswer`, `OrigLangRequest`,
  `OrigLangAnswer`, `TrailerFetch`, `TrailerFile`, `TrailerPending`,
  `FitState` with its states `FitPending`, `FitOK`,
  `FitMisfit`, `FitUnmeasured`, `ImportJob`, `ImportGroup`,
  `ImportRunClock`, `ImportTrack`, `ImportSnapshot`, `ImportService`,
  `ImportServicesAnswer`, `ImportStopRequest`, `ImportResolveRequest`,
  `ImportResolveAnswer`, `ImportSearchRequest`, `ImportCandidate`,
  `ImportSearchAnswer`, `LeftoversRequest`, `LeftoverLine`,
  `LeftoversAnswer`, `ProgressEntry`, `ProgressSave`, `EpisodeItem`,
  `ContinueAnswer`, `ContinueCard`, `ChannelReady`, `ChannelRules`,
  `ChannelCandidates`, `ChannelCandidatesAnswer`, `ChannelTitle`,
  `ChannelResolve`, `ChannelStream`, `ChannelRelease`, …), camelCase.
- **Environment.** `FromEnv()` reads the variables of [The
  process](#the-process) into `Env` (`ID`, `DataDir`, `Socket`, `CoreURL`,
  `Token`, `Locale`, `Contract`); it fails without `ANTRESOL_ADDON_SOCKET`
  or a numeric `ANTRESOL_CONTRACT`. `Env.Prefix()` is the addon's own
  `ANTRESOL_<ID>_`, `Env.Var(name)` one of its own variables.
- **Core API.** `NewClient(env)` calls the core API v1 with the token: one
  method per call of the table (`Info`, `Event`, `Item`, `ItemFiles`,
  `ItemByTMDB`, `ItemIDsByTMDB`, `FindTitle`, `FindAlbum`,
  `FindAlbumByMBID`, `FindBook`, `HaveEpisodes`, `EpisodeItems`, `SeriesFolder`,
  `SeriesAudio`, `AlbumDir`, `AlbumTracks`, `FilesUnder`, `FileByPath`,
  `FileByID`, `FilePaths`, `SearchTitles`, `RefFiles`, `TracksOfRef`,
  `Users`, `Watched`, `Progress`, `SaveProgress`, `Studios`, `Import`,
  `BindRef`, `UnbindRef`, `BindRefs`, `ClaimOwner`, `CacheImage`,
  `PutHolds`, `Holds`, `RegisterInput`, `DropInput`, `Notify`, `ApproveRequests`,
  `EnsureTitle`, `TitleByTMDB`, `TitleByID`, `KickTitle`, `TitleEpisodes`,
  `TitleSources`, `QueueFit`, `FitState`, `ImportActiveCount`,
  `ImportJobIDs`, `ImportJob`, `ImportCandidates`, `ImportGroups`,
  `ImportStillDownloading`, `ImportGroupTracks`, `MoveImportGroup`,
  `WaitImportGroup`, `GiveUpImportWait`, `StartImportGroup`,
  `SaveImportClock`, `ImportGivenUp`, `RequeueImportGroup`,
  `ImportNoFormat`, `FailImportGroup`, `FinishImportGroup`, `ImportHave`,
  `ImportNotFound`, `ImportProgress`, `RebuildImport`, `ImportOpen`,
  `CreateReadingJob`, `FillImportJob`, `FailImportJob`, `FailReadingJobs`,
  `DeleteImportJob`, `ImportReads`). An answer other than 2xx is an `*APIError` (`Status`, and
  `Message` from `{"error"}`); the lookups that return `ok` (`ItemByTMDB`,
  the `Find…`, `FileByPath`, `FileByID`, `TitleByTMDB`, `TitleByID`, `FitState`, `ImportJob`) take a 404 as `ok == false`,
  the import claims (`MoveImportGroup`, `StartImportGroup`, `FailImportJob`) a 409 as `ok == false`
  (`IsConflict` tells one), and `HaveEpisodes` and `EpisodeItems`
  of a series the library lacks are empty. `ItemIDsByTMDB` takes any number
  of ids (500 per call), `Watched` and `Progress` too (500 per list per call). A call times out after 30 s, `FilePaths` after
  5 minutes, `Import` after 30 (a scan takes minutes); a context with an
  earlier deadline wins. Redirects are not followed. For the metadata
  pass-through, `MetadataBase(source)` is the source's root and
  `MetadataHTTP()` an `*http.Client` that adds the token to requests to the
  core only.
- **Hooks.** `HookMux`, mounted at `/hooks/`: `Handle(name, fn)`, where
  `fn(ctx, user, body)` gets the profile (`nil` when the hook runs for
  none) and the JSON body (`{}` when empty). It answers 200 with the JSON of
  what `fn` returns, 204 for `nil, nil` (only an untyped `nil`: a nil
  slice, map or pointer is 200 with the body `null`), 409 `{"error"}` for a
  `*Refusal` (`Refuse(msg)`; an empty text becomes `DefaultRefusal`, since a
  409 without one is a failure), 500 for any other error, and 404 for a
  hook it has no handler for. An `*Answer` (`&Answer{Status, Body}`)
  answers with its own status: 202 or 200 with the JSON of `Body`, 204, or
  404 (`{"error"}`) for "not this addon" (`trailers.fetch`); a 200 or 202
  without a `Body`, and any other status, is a failure (500). A broken `X-Antresol-User` or a body that is
  not JSON is 400. Handle every hook the manifest declares: a declared `files.*`
  hook that answers 404 fails its file operation.
- **Who asks.** `UserFrom(r)` parses `X-Antresol-User`; `RequireAdmin(h)`
  answers 403 `{"error": "Недостаточно прав"}` unless the profile is an
  admin. The server already keeps `x/admin/…` to admins; wrapping the
  addon's own `/api/admin/…` in `RequireAdmin` is defence in depth.
- **Readiness.** `Ready.Health` is a `/health` handler answering
  `{"ok": true, "ready": false}` until `Ready.Set()`; call `Set` once the
  addon's own start is done (at once when it has none).
- `WriteJSON` and `WriteError` answer JSON and the contract's
  `{"error": "…"}`.
- **Tests.** `addonkit/addonkittest` (a package of its own, so an addon's
  binary never links `testing`) replays the core's recorded answers:
  `ReplayGolden(t, dir)` is an `httptest.Server` answering each file of
  `dir` (`{"method", "path", "status", "body"}`; `path` under
  `/api/addon-core/v1`, with its query, compared decoded and in any order)
  and 404 to anything else; point `NewClient(Env{CoreURL: srv.URL})` at it.
  `GoldenDir()` is `addonkit/testdata/golden`, one answer per call an
  adapter makes, recorded by the core's own test over a scanned library; in
  them the library's folder reads `{root}`, timestamps `{time}` (but for
  the `tracking-*` and `imports-*` answers, whose times are the recording's
  own), the scanner's signatures `{sig}`, a profile's uid `{uid}`, an
  input's id `{input}` and a cached image's name `{image}`. The replay
  answers a `"{time}"` string as `GoldenTime` (RFC 3339), so a client
  decodes it; the other placeholders are answered as written. Every
  `/api/addon-core/v1` route has a recorded answer, but for the metadata
  pass-through (`TestAddonCoreRouteCoverage`).

Until `addonkit` is published under a module version, the template's
`go.mod` requires it with a `replace`: every target of its `Makefile` first
points that `replace` at `$(ANTRESOL_CORE)/addonkit`, the same core checkout
the web part takes the SDK from (default `../..`; `make build
ANTRESOL_CORE=../antresol` in a repository of its own, as its release
workflow does with the core it checks out).
