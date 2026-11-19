# Troubleshooting

Look at the server's log first; the reason is almost always there:

```sh
docker compose logs --tail 100 antresol
```

## The server does not see the files

- The only host path in `.env` is `MEDIA_DIR`. `VAULT_*_DIR` variables are paths inside the
  container and start with `/media`. `stat ...: no such file or directory` or
  `mkdir ...: permission denied` usually means a host path in a `VAULT_*` variable.
- Check the folders: **Settings → Server → Library → Server folders**.
- New files show up within 15 minutes. To see them now: **Rescan library**.

## No write access

The container runs as uid 1000. If the files belong to another user:

- let uid 1000 write to the media folder, or
- add `user: "<uid>:<gid>"` of the files' owner to the `antresol` service in `compose.yml`.

**Settings → Server → Health → Folders** prints the exact command (and so does **Library → Server
folders → Check**).

## Apps do not find the server

Apps look for the server on the home network over mDNS (`antresol.local`) and UDP on port 8420.

- The phone and the server must be on the same network (not a guest Wi-Fi).
- The `antresol-mdns` service from `compose.yml` must be running: `docker compose ps`.
- **Docker Desktop (macOS, Windows), WSL 2, Podman machine:** containers run in a virtual machine
  there, and automatic discovery does not work. Enter `http://<computer's IP>:8420` in the app.
- Android before version 12 does not resolve `.local` names: use the IP.

The name the server took is shown in **Settings → My Settings → About This Server**.

## Two servers on one computer

Put the second stack in its own folder, with its own `container_name` and `HTTP_PORT=8421` in its
`.env`. It opens at `http://antresol.local:8421`. Apps find it over mDNS.

## Signing in without a password fails away from home

That is by design: a profile without a password signs in only from the home network. Give it a
password: **Settings → Server → Profiles → Edit**.

If it fails at home too and the server sits behind a proxy, see the next item.

## "does not list it" in the log

```
request from 172.18.0.1 carries X-Forwarded-For ... VAULT_TRUSTED_PROXIES does not list it
```

Requests come through a reverse proxy the server does not know about. Put that address in
`VAULT_TRUSTED_PROXIES` in `.env` and run `docker compose up -d`. Details:
[Public address](public-address.html).

## "The redirect address is built from the server's external address, which isn't set yet"

The external address is not set. Put it in **Settings → Server → Server Address**. See
[Public address](public-address.html).

## No franchises across collections, ratings, lyrics or intro skipping

These come from open services that may be off on your server. Check **Settings → Server →
External Services**: Wikidata merges franchises and gives Rotten Tomatoes scores, IMDb ratings
and song lyrics (LRCLIB) are off until you turn them on, TheIntroDB and IntroDB mark intros and
credits. A switch shown as set on the server is decided by a variable in `.env`: see
[External services](configuration.html#external-services). After Wikidata is turned on, the
franchises are worked out again within minutes.

## An extension says its service is not found

The extension needs a helper service in `compose.yml`. See [Helper services](addon-services.html).

## Video stutters or does not use the graphics card

Check **Settings → Server → Health → Hardware** or **Processing**: they show whether a graphics
card was found. How to give it to the container: [Hardware transcoding](hardware-transcoding.html).

## Still stuck

Open an [issue on GitHub](https://github.com/antresol-app/antresol/issues) with the last lines of
the log.
