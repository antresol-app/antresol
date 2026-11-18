# Installing Antresol Server

The short version is in the [README](../README.md#quick-start). This page has the details.

## Media folders

You need a Linux host with Docker and Compose, and your media in one folder:

```
/srv/media/
  movies/      Film Title (2019)/Film Title (2019).mkv
  series/      Series Title/Season 01/Series Title S01E01.mkv
  music/       Artist/2019 - Album/01 - Track.flac
  books/
    ebooks/      Author - Title.epub
    audiobooks/  Author - Title/*.mp3  (or one .m4b)
    manga/       Series/Volume 01.cbz
```

Your folders may have other names: pick them in the first-run wizard (below)
or set them in `.env` ([configuration.md](configuration.md#media-folders)).

## First setup

After you create the administrator, a short wizard walks through what the
server needs. Every step can be skipped, and the same checks stay in
Manage → Settings (Library → Server folders, Transcoding), so nothing has to
be done in one sitting:

1. **Media folders.** What the server sees under `/media`, with the number of
   entries in each folder, and the status of each library folder: found,
   missing («Create» makes it) or not writable. When your folders are named
   differently from the defaults, say which one is which: an empty default
   folder beside a folder with files in it is shown as empty, with the folder
   that has the files offered. A pick is stored in the server's settings and
   used instead of the `*_DIR` variables (a reset goes back to them); the
   server restarts for a few seconds to apply it. A folder must be inside the
   media folder (a link that leads out of it is refused) and not inside
   another library's folder. While the library has files but the media disk
   looks unmounted, «Create» is not offered: a folder made on the empty mount
   point would make it look like the real disk, and the next scan would hide
   the library.
2. **Write access.** The server makes and removes a test file in the media
   folder, each library folder and the transcoding folder. When it cannot,
   the page says which user the server runs as and gives the fix to copy: a
   `chown` command, or `user: "<uid>:<gid>"` for the compose file.
3. **Graphics card.** The hardware status line, with what to uncomment in
   `compose.yml` when no GPU was passed to the container.

«Done» starts the first library scan.

## Notes

- **Paths.** `MEDIA_DIR` is the only host path. The server runs inside the
  container and sees your media at `/media`; the `*_DIR` names are relative
  to it. Do not set `VAULT_MEDIA_ROOT`, `VAULT_MOVIES_DIR` or
  `VAULT_TRANSCODE_DIR` to host paths such as `/tank/media/...`: the
  container has no such folder (`stat ...: no such file or directory`,
  `mkdir ...: permission denied`). Missing folders inside it are created
  empty: all of them on a new install, and music and book folders later too.
- The container runs as uid/gid 1000 and needs to write to the media folder
  (subtitles it fetches, temporary transcode files in `tmp/antresol-hls`).
  If your files belong to another user, give uid 1000 write access or add
  `user: "<uid>:<gid>"` to the service (the volume `antresol-config` then needs
  the same owner). The first-run wizard and Manage → Settings → Library test
  this for real and print the exact command. Settings and the database live in
  the `antresol-config` volume.
- **Temporary files on another disk.** Playback segments, live TV and
  offline copies go to `tmp/antresol-hls` inside the media folder. To keep
  them elsewhere, for example on a separate disk mounted at `/scratch`, put
  a `compose.override.yml` next to `compose.yml`; `docker compose up -d`
  reads both:

  ```yaml
  services:
    antresol:
      environment:
        VAULT_TRANSCODE_DIR: /transcode
      volumes:
        - /scratch/antresol-hls:/transcode
  ```

  Create the folder first and give uid 1000 write access to it: a folder
  Docker creates by itself belongs to root, and the server cannot write
  there. Setting `VAULT_TRANSCODE_DIR` in `.env` alone does not work with
  this `compose.yml`, which sets it itself and mounts nothing else.
- Without a GPU passed to the container, it transcodes on the CPU; see
  [Hardware transcoding](hardware-transcoding.md).
- Outside your home network, put Antresol behind a reverse proxy with HTTPS
  and set `VAULT_TRUSTED_PROXIES` to the proxy's address. The log line
  `request from <ip> carries X-Forwarded-For ... does not list it` shows the
  address to put there. A profile without a password can sign in only from
  the home network.
- **Finding the server.** The apps look for servers on the home network
  by themselves: by mDNS / Bonjour (`_antresol._tcp`)
  and by a UDP probe on port 8420 (`VAULT_DISCOVERY_PORT`). With Docker's
  default bridge network multicast and broadcast stay outside the
  container: the published `8420/udp` answers apps that ask the host's
  address, and for discovery that needs nothing typed run the container with
  `network_mode: host` (then drop `ports:`) or the binary on the host.
  `VAULT_SERVER_NAME` is the name the apps list (default `Antresol`);
  `VAULT_DISCOVERY=off` turns both off, `VAULT_MDNS=off` mDNS alone. On a
  TV, sign in by scanning its QR code with the phone app or the phone's
  camera (the page `/pair`).
- Every setting is an environment variable and can go in `.env`: see
  [configuration.md](configuration.md); [build-tags.md](build-tags.md)
  explains the build.

Upgrade notes for each version are in its
[release notes](https://github.com/antresol-app/antresol/releases).
