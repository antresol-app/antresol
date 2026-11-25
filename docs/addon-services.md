# Extension helper services

Some extensions need a program of their own next to the server: a **helper service**. The Online
extension needs one that finds streams; the downloads extension needs one that gets past sites'
bot checks.

A helper service is one more container in your `compose.yml`. The extension finds it by itself,
by name.

## Add a helper service

1. Open the extension's settings page. While the service is missing, it says "not found" and
   shows a ready block for `compose.yml`.
2. Paste the block into `compose.yml` under `services:`, next to `antresol`, indented by two
   spaces like `antresol:`.
3. Run `docker compose up -d`.

Within half a minute the extension's page says the service was found. No address to enter and no
restart. Whether the services answer is also shown in **Settings → Server → Health → Addon
Services**.

The block looks like this (take the name, image and port from the extension's page or README):

```yaml
services:
  antresol:
    # ... as before

  service-name:
    image: <image from the extension's README>
    restart: unless-stopped
    # past these limits the kernel stops the service, not the server
    mem_limit: 1536m
    pids_limit: 4096
    cap_drop: [ALL]
    security_opt:
      - no-new-privileges:true
```

The block has no `ports:`: only the server reaches the service, nothing outside does. It has caps
on memory (`mem_limit`) and processes (`pids_limit`): a service that runs a browser can grow until
the machine is out of memory, and the caps make the system stop that service instead of the
server. The numbers above are the default; an extension may size them for its service (and add a
`cpus` cap), so the block on its page may differ. Raise `mem_limit` if the service is stopped for
lack of memory. The service also gets no extra Linux capabilities and cannot gain new
privileges; if an image refuses to start with that, remove the `cap_drop` and `security_opt`
lines.

## The block for each extension

- **Online**: the helper service section of
  [its README](https://github.com/antresol-app/antresol-addon-online#readme). The service's state:
  **Settings → Server → Online → Source Status**.
- **Downloads**: the downloads extension's README (in the
  [repository list](https://github.com/antresol-app)). The service's state:
  **Settings → Server → Download settings**.

## The service already runs elsewhere

If that program already runs on your network, give the extension its address, on its settings
page or in the `ANTRESOL_<ID>_…` variable its README names. An address you set wins: the extension
then does not look for the service next to the server.

Do the same when the server does not run in Docker: a compose service name works only inside the
compose project.

## How it works

Each extension declares its services in its manifest (`helpers`: name, image, port). The server
lists them in `GET /api/addons`. The extension reaches a service at `http://<service-name>:<port>`
on the compose project's own network.
