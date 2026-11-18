# Antresol

**A home media server for the films, series, music, audiobooks, books and
comics you already have.** Point it at your folders, open it in a browser,
and the whole family gets one library with profiles, progress that follows
you between devices, and a player that works at home and away.

[Русский](README.ru.md)

![The home screen: shelves of films and series](docs/images/home.png)

> **Honest statement.** Antresol is maintained by one person, with AI
> assistance. No content is provided: Antresol plays only the files you put
> in your own folders and fetches descriptions and artwork for them. Only add
> media you have the rights to. The iPhone, iPad and Mac app is closed source
> and talks to the server through the same public API as the web interface;
> the server and the web interface in this repository are open source (see
> [Licence](#licence)).

## What it does

- **Films and series** with posters, descriptions, cast and seasons. Plays
  in the browser: the original file when the browser can, otherwise a
  remux or a transcode, with hardware decoding where the host's ffmpeg has
  it (Intel Quick Sync, VideoToolbox). Audio tracks, subtitles, skip intro
  and credits, a quality limit for viewers away from home.
- **Music**: albums, artists, playlists, CUE images, synced lyrics,
  near-gapless playback, loudness levelling. Subsonic API for music apps.
- **Books, audiobooks, manga and comics**: an EPUB, FB2 and PDF reader with
  highlights and notes, an audiobook player with chapters and a sleep timer,
  a comic reader with right-to-left reading. OPDS catalogue for e-reader
  apps.
- **Profiles** for the household, with passwords, kid profiles that see only
  what is allowed for them, and screen time.
- **Offline**: save an album, a book or a volume to the device from the web
  app and use it without a connection; positions sync when you are back.
- **Metadata** from TMDB, MusicBrainz, the Cover Art Archive and Open Library.
  Each can be switched off.
- Interface in Russian and English.
- One self-contained Go binary with the web interface built in; a SQLite
  database; no other services needed.

<p>
  <img src="docs/images/title.png" alt="A film's page" width="49%">
  <img src="docs/images/music.png" alt="An album" width="49%">
</p>
<p>
  <img src="docs/images/books.png" alt="The books shelf" width="49%">
  <img src="docs/images/profiles.png" alt="Choosing a profile" width="49%">
</p>
<p><img src="docs/images/phone.png" alt="The home screen on a phone, 375 px wide" width="200"></p>

## Quick start

You need a Linux host with Docker and Compose and your media in one folder
(`movies/`, `series/`, `music/`, `books/`…; [layout](docs/install.md#media-folders)).

```sh
mkdir ~/antresol && cd ~/antresol
curl -fsSLO https://raw.githubusercontent.com/antresol-app/antresol/main/compose.yml
curl -fsSL https://raw.githubusercontent.com/antresol-app/antresol/main/.env.example -o .env
nano .env                  # MEDIA_DIR, VAULT_TMDB_KEY, TZ
docker compose up -d
```

Open `http://<host>:8420` and create the first administrator. For a GPU,
uncomment its lines in `compose.yml` ([hardware transcoding](docs/hardware-transcoding.md)).

More: [install details](docs/install.md) (paths, permissions, reverse proxy,
finding the server) · [all settings](docs/configuration.md) ·
[release notes](https://github.com/antresol-app/antresol/releases).


## Build from source

Requirements: Go 1.27, Node 22 (see `.nvmrc`), and `ffmpeg` and `ffprobe`
6.1 or newer on the `PATH`. ffmpeg 6.0 and older do not know
`-readrate_initial_burst`, which every remux and transcode passes, so on them
only direct play works. The Docker image already carries a newer ffmpeg.

```sh
make build                      # web bundle, then bin/vault with it embedded
VAULT_DATA_DIR=./data VAULT_MEDIA_ROOT=/srv/media bin/vault
```

`make test` runs the Go tests, `make test-web` the web unit tests and
`make e2e` the browser tests (Playwright). `docker build .` builds the
server image. `vault` is the code name: you will see it in
the binary, the `VAULT_*` variables and the database file.

## Apps

- **iPhone, iPad and Mac**: the Antresol app on the App Store. It is closed
  source for now and uses the server's public API. (Its book reader is built
  from the MPL-2.0 files of this repository; see [LICENSING.md](LICENSING.md).)
- **Android phone and Android TV**: one app, in closed testing on Google
  Play. Join the [antresol-test](https://groups.google.com/g/antresol-test)
  group, then [become a tester](https://play.google.com/apps/testing/app.antresol.mobile)
  and install it from Google Play. Source:
  [antresol-android](https://github.com/antresol-app/antresol-android).
- **Music**: any Subsonic client. Create an app password in the profile
  menu, then use the server address, your profile name and that password.
- **E-readers**: any OPDS reader, at `<server>/opds`, with the same app
  password.

## Contributing

Contributions are welcome, in English or Russian. Read
[CONTRIBUTING.md](CONTRIBUTING.md) first: sign-off (DCO), what is in scope,
the UI rules, and how AI-assisted work is handled. Please open an issue
before large or visual changes. Security problems: [SECURITY.md](SECURITY.md).
Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).
How decisions are made: [GOVERNANCE.md](GOVERNANCE.md).

## Licence

Antresol Server is free software under the
[GNU Affero General Public License v3.0 or later](LICENSE)
(`AGPL-3.0-or-later`). The native reader and the translations
(`web/reader-native.html`, nine files in `web/src/reader`, and
`web/src/i18n`) are under the [Mozilla Public License 2.0](web/src/reader/LICENSE)
(`MPL-2.0`). [LICENSING.md](LICENSING.md) lists the zones exactly,
[NOTICE](NOTICE) and [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) cover
third-party code.

Every server offers its source: the sign-in screens and the profile menu
(About this server) show a "Source code · AGPL" link, and `GET /api/about`
answers with the version, the commit, the licence and the link. Release builds
link to their tag; a build from source shows no link until you give it one.
If you let others use a modified server, the AGPL asks you to offer them your
source: set `VAULT_SOURCE_URL` to where it is (or build with
`make SOURCE_URL=<url>`).

The name "Antresol" and the logo are not covered by these licences; see
[TRADEMARKS.md](TRADEMARKS.md).

This product uses the TMDB API but is not endorsed or certified by TMDB.
