# All .env settings

A reference. The three required lines are usually enough; the rest is set in the interface.
After editing `.env`, run `docker compose up -d`.
`VAULT_*` paths are container paths: never put host paths into them.

## Required

| Variable | Example | What it does |
| --- | --- | --- |
| `MEDIA_DIR` | `/srv/media` | Media folder on the host; `compose.yml` mounts it at `/media` (read-write). |
| `VAULT_TMDB_KEY` | | TMDB API key for film and series metadata. |
| `TZ` | `Europe/Berlin` | Time zone of the server (TV guide, schedules). |

## Media folders

Folder names inside `MEDIA_DIR`, if yours differ from the defaults. The first-run
wizard and «Settings → Server → Library → Server folders» can pick them in the
web UI instead; folders picked there are used in place of these variables until
reset.

| Variable | Default |
| --- | --- |
| `MOVIES_DIR` | `movies` |
| `SERIES_DIR` | `series` |
| `MUSIC_DIR` | `music` |
| `BOOKS_DIR` | `books` |
| `EBOOKS_DIR` | `books/ebooks` |
| `AUDIOBOOKS_DIR` | `books/audiobooks` |
| `COMICS_DIR` | `books/manga` |

## Transcoding

| Variable | Default | What it does |
| --- | --- | --- |
| `VAULT_HWACCEL` | `auto` | `auto`, `qsv`, `vaapi`, `nvenc`, `videotoolbox` or `none`. `auto` tries the GPUs it sees with a short test encode at start. The container needs the GPU: uncomment the GPU lines in `compose.yml`. «Settings → Server → Processing» overrides it once changed there. |
| `VAULT_HWACCEL_DEVICE` | first node | Render node for `vaapi` when there are several, e.g. `/dev/dri/renderD128`. |
| `VAULT_TRANSCODE_DIR` | `/media/tmp/antresol-hls` | HLS segments, live TV and offline copies. Keep it off `/config`. To move it with compose, use a `compose.override.yml`. |
| `VAULT_TRANSCODE_MAX_BYTES` | `21474836480` (20 GB) | Total size limit of HLS segments. |
| `VAULT_REPACK_TS` | on | `off` keeps `.ts`/`.m2ts` films as they are instead of repacking into `.mkv`. |

## Network

| Variable | Default | What it does |
| --- | --- | --- |
| `VAULT_PUBLIC_URL` | | The server's outside address, such as `https://media.example.com`, no trailing `/`: links in e-mails and Telegram, Shikimori sign-in, the outside address for the apps. Usually set in «Settings → Server → Server Address»; the variable wins. See [Public address](public-address.md). |
| `VAULT_TRUSTED_PROXIES` | | Your reverse proxy's addresses, comma-separated: only they are trusted for `X-Forwarded-For` and `X-Forwarded-Proto`. |
| `VAULT_TRUSTED_NETS` | | Extra home networks, comma separated, e.g. Tailscale `100.64.0.0/10`. If you reach the server on such a network by a full name (Tailscale's `host.<tailnet>.ts.net`, a router's `.fritz.box`), list that name in `VAULT_TRUSTED_HOSTS` too, e.g. `*.ts.net`. |
| `VAULT_TRUSTED_HOSTS` | | Extra names, comma-separated, that people at home reach the server by, e.g. `media.lan,*.example.org`. The server's [public address](public-address.md), IP addresses, `localhost` and names like `antresol`, `*.local`, `*.lan` and `*.home.arpa` already count as home; a request that asks for any other name is treated as away from home, even from a home address. |
| `VAULT_SERVER_NAME` | `Antresol` | Name the apps show when they find the server. |
| `VAULT_DISCOVERY` | on | `off` turns off both mDNS and the UDP probe the apps use to find the server. |
| `VAULT_MDNS` | on | `off` turns off mDNS only, and with it the name `antresol.local` (`antresol-2.local`, … when a server on another computer holds it) a browser at home opens. |
| `VAULT_DISCOVERY_PORT` | `8420` | UDP probe port in the container; publish it as `8420:8420/udp` (`compose.yml` does, on the host port `HTTP_PORT`). |
| `VAULT_ANNOUNCE_DIR` | | Folder shared with the mDNS announcer (`antresol-mdns` in `compose.yml`, which sets it to `/announce` in both services): on a bridge network the server leaves `antresol.local` and the apps' search to it. See [Troubleshooting](troubleshooting.md#apps-do-not-find-the-server). |
| `VAULT_ANNOUNCE_PORT` | the server's | Announcer only: the HTTP port published for the server on the host. `compose.yml` sets it from `HTTP_PORT`. |
| `HTTP_PORT` | `8420` | `compose.yml` only: the host port of the web UI and the apps (TCP) and of the apps' UDP probe, for the server and the announcer together. A second stack on the same computer needs another one. |

## External services {#external-services}

Open services the server asks about titles already in the library. They bring nothing into the
library. Each has a switch in **Settings → Server → External Services**, and the first-run
wizard lists them all, turned on. A switch applies at once, without a restart. A variable below
wins over the switch, and the switch then shows it as set on the server.

| Service | Default | Sends | Gives | Variable |
| --- | --- | --- | --- | --- |
| Wikidata | on | TMDB and IMDb ids of films and series, book titles | Franchises merged across TMDB collections, Rotten Tomatoes scores, book titles in other languages | `VAULT_WIKIDATA_BASEURL`: `off`, `on` or your own address |
| IMDb ratings | off | nothing about the library: the server downloads IMDb's shared ratings file once a day | IMDb ratings on title pages. The dataset is for personal, non-commercial use | `VAULT_IMDB_RATINGS_URL`: `off`, `on` or a mirror's address |
| Song lyrics (LRCLIB) | off | Artist, title, album and length of a track | Lyrics, synced ones too | `VAULT_LRCLIB_URL`: `off`, `on` or your own LRCLIB address |
| Intros and credits (TheIntroDB, IntroDB) | on | TMDB/IMDb id of a film or series, season and episode | Intro and credits marks before the server's own analysis finds them | `VAULT_SEGMENTS_ONLINE`: `on` or `off` |

IMDb ratings and song lyrics are off until you turn them on, because you accept their terms: the
IMDb dataset is licensed for personal, non-commercial use, and lyrics belong to their authors.
A server that never chose shows a one-time notice about this section on **Health**.

## Subtitles

| Variable | What it does |
| --- | --- |
| `VAULT_OPENSUBTITLES_KEY` | OpenSubtitles API key for automatic subtitles. |
| `VAULT_OPENSUBTITLES_USER` | OpenSubtitles account. |
| `VAULT_OPENSUBTITLES_PASSWORD` | OpenSubtitles password. |

## Mail and Telegram

Set up in the interface, not in `.env`: see [Mail, Kindle and Telegram](mail.md).

## TV channels

| Variable | Default | What it does |
| --- | --- | --- |
| `VAULT_IPTV` | off | `on` turns on IPTV links (M3U and XMLTV) for players such as TiviMate. See [TV channels and IPTV](tv.md). |
| `VAULT_CHANNELS_MAX` | `3` | How many channels may stream at once; `0` turns channel streaming off. |

## Migration

| Variable | Default | What it does |
| --- | --- | --- |
| `VAULT_IMPORT_DIR` | `/import` | Where to look for the Jellyfin, Kavita and Audiobookshelf databases. See [Migration](migrate.md). |

## Addons

| Variable | Default | What it does |
| --- | --- | --- |
| `VAULT_ADDONS` | on | `off` disables addons. |
| `VAULT_ADDONS_DEV` | | Local addons for development, one folder per addon (relative: under the data dir). |
| `VAULT_ADDONS_PIN` | | Addons pinned to releases in `VAULT_ADDONS_DEV` (`id@version`, comma separated), installed at boot and locked in the admin UI. |
| `VAULT_ADDONS_ACCEPT_UNSIGNED` | off | The server signs the profile header it sends to an addon, and an addon built on a current addonkit refuses one that is not signed. `on` lets such an addon take an unsigned header (an escape hatch for requests made by hand). Addons built on an older addonkit do not check. |

An addon's own settings are variables named `ANTRESOL_<ID>_*` (its README
lists them). An addon may need a helper service beside the server: it then
finds it by its compose service name, and an address set in its variable or
its settings page wins ([Helper services](addon-services.md)).

## Advanced

| Variable | Default | What it does |
| --- | --- | --- |
| `VAULT_DB_MMAP_BYTES` | `0` (off) | How much of the database SQLite maps into memory. Reads get faster, but a read error on the disk holding `/config` then stops the whole server: use it only on a reliable disk. |
| `VAULT_SOURCE_URL` | this repository | Link of «Source code · AGPL» (AGPL §13). Set it when you run a modified server for others; set but empty hides the link. |
