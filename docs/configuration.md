# Configuration

Antresol Server reads these environment variables (from `.env` next to `compose.yml`).
Most settings live in the web UI instead; set a variable only when you need it.
`VAULT_*` paths are container paths: never put host paths into them.

## Required

| Variable | Example | What it does |
| --- | --- | --- |
| `MEDIA_DIR` | `/srv/media` | Media folder on the host; `compose.yml` mounts it at `/media` (read-write). |
| `VAULT_TMDB_KEY` | | TMDB API key for film and series metadata. |
| `TZ` | `Europe/Berlin` | Time zone of the server (TV guide, schedules). |

## Media folders

Folder names inside `MEDIA_DIR`, if yours differ from the defaults. The first-run
wizard and «Settings → Server → Library & Languages → Server folders» can pick them in the
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
| `VAULT_HWACCEL` | `auto` | `auto`, `qsv`, `vaapi`, `nvenc`, `videotoolbox` or `none`. `auto` tries the GPUs it sees with a short test encode at start. The container needs the GPU: uncomment the GPU lines in `compose.yml`. «Settings → Server → Transcoding» overrides it once changed there. |
| `VAULT_HWACCEL_DEVICE` | first node | Render node for `vaapi` when there are several, e.g. `/dev/dri/renderD128`. |
| `VAULT_TRANSCODE_DIR` | `/media/tmp/antresol-hls` | HLS segments, live TV and offline copies. Keep it off `/config`. To move it with compose, use a `compose.override.yml`. |
| `VAULT_TRANSCODE_MAX_BYTES` | `21474836480` (20 GB) | Total size limit of HLS segments. |
| `VAULT_REPACK_TS` | on | `off` keeps `.ts`/`.m2ts` films as they are instead of repacking into `.mkv`. |

## Network

| Variable | Default | What it does |
| --- | --- | --- |
| `VAULT_TRUSTED_PROXIES` | | Address of your reverse proxy: only it is trusted for `X-Forwarded-For`. |
| `VAULT_TRUSTED_NETS` | | Extra home networks, comma separated, e.g. Tailscale `100.64.0.0/10`. |
| `VAULT_SERVER_NAME` | `Antresol` | Name the apps show when they find the server. |
| `VAULT_DISCOVERY` | on | `off` turns off both mDNS and the UDP probe the apps use to find the server. |
| `VAULT_MDNS` | on | `off` turns off mDNS only, and with it the name `antresol.local` (`antresol-2.local`, … when another server holds it) a browser at home opens. |
| `VAULT_DISCOVERY_PORT` | `8420` | UDP probe port; publish it as `8420:8420/udp`. Broadcast and mDNS (and so `antresol.local`) need host networking: see [install.md](install.md). |

## Subtitles

| Variable | What it does |
| --- | --- |
| `VAULT_OPENSUBTITLES_KEY` | OpenSubtitles API key for automatic subtitles. |
| `VAULT_OPENSUBTITLES_USER` | OpenSubtitles account. |
| `VAULT_OPENSUBTITLES_PASSWORD` | OpenSubtitles password. |

## Email

Mail is set in the web UI, not in `.env`: «Settings → Server → Mail and notifications → Sending mailbox».
It sends email notifications and books to Kindle. Any SMTP account works:

| Service | Server | Port | Login | Password |
| --- | --- | --- | --- | --- |
| Gmail | `smtp.gmail.com` | 587 | your Gmail address | an [app password](https://myaccount.google.com/apppasswords) (needs 2-Step Verification) |
| Resend | `smtp.resend.com` | 465 or 587 | `resend` | an API key; the sender address must be on a domain verified in Resend |
| Other mail | from its help pages | 587 or 465 | usually your address | usually an app password |

Then each person turns on what they want in «Settings → Notifications» and confirms
their address with the test email. Everything is off until turned on.

## Telegram

Notifications can also go to Telegram, next to email, or instead of it: the same events,
as plain text, with a link to the title when `VAULT_PUBLIC_URL` is set. Telegram is set up
once, then each person needs two taps.

**The administrator**, once: in Telegram open [@BotFather](https://t.me/BotFather), send
`/newbot` and pick a name. Paste the token it sends into «Settings → Server → Mail and
notifications → Telegram bot» and press «Check»: the bot's @name appears. The server keeps the
token like the SMTP password and never shows it again; it is never written to the log.
«Turn Bot Off» forgets it.

**Each person** (adult profiles), in «Settings → Notifications»: «Connect Telegram» shows a
link. Open it in Telegram and press Start; the page notices by itself, and the bot confirms in
the profile's language. «Disconnect» in the same place, or blocking the bot in Telegram, ends
it. Kid profiles cannot connect Telegram.

How it behaves:

- No public address, webhook or open port is needed. The server asks Telegram for new messages
  (long polling), so it only needs to reach `api.telegram.org` over HTTPS, as any home server
  can. One poller runs per server, only while a token is set.
- A profile chooses its channels: email, Telegram or both (a switch for each appears once both
  are set up). Topics and quiet hours (22:00–08:00, server time) apply to both. Each channel
  sends at most one digest an hour per profile.
- Telegram's limits are respected: messages are spaced a second apart, and on a «too many
  requests» answer nothing is sent until Telegram's `retry_after` has passed. A chat that
  blocked the bot or was deleted is disconnected.
- A bot token can be polled by one program only. If the screen says another program takes the
  bot's messages, or it has a webhook, use a bot of its own for the server.
- Changing the token to another bot disconnects the chats linked with the old one; pasting
  the same bot again keeps them.

## Addons

| Variable | Default | What it does |
| --- | --- | --- |
| `VAULT_ADDONS` | on | `off` disables addons. |
| `VAULT_ADDONS_DEV` | | Local addons for development, one folder per addon (relative: under the data dir). |
| `VAULT_ADDONS_PIN` | | Addons pinned to releases in `VAULT_ADDONS_DEV` (`id@version`, comma separated), installed at boot and locked in the admin UI. |

An addon's own settings are variables named `ANTRESOL_<ID>_*` (its README
lists them). An addon may need a helper service beside the server: it then
finds it by its compose service name, and an address set in its variable or
its settings page wins ([install.md](install.md#addons-that-need-a-helper-service)).

## Advanced

| Variable | Default | What it does |
| --- | --- | --- |
| `VAULT_DB_MMAP_BYTES` | `0` (off) | How much of the database SQLite maps into memory. Reads get faster, but a read error on the disk holding `/config` then stops the whole server: use it only on a reliable disk. |
| `VAULT_SOURCE_URL` | this repository | Link of «Source code · AGPL» (AGPL §13). Set it when you run a modified server for others; set but empty hides the link. |
