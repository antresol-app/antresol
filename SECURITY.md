# Security policy

## Reporting a vulnerability

Please do **not** open a public issue for a security problem.

Report it privately with the **Report a vulnerability** button on the
repository's Security tab (private vulnerability reporting).

Include what you found, how to reproduce it, the version (`/api/health`,
or the release tag) and what an attacker could do with it. You will get an
answer within seven days. Once a fix is released we credit you in the
release notes, unless you prefer not to be named.

## Supported versions

Only the latest release gets security fixes. Upgrade before reporting if you
can.

## Scope

In scope: the server (`cmd/`, `internal/`), its web interface (`web/`), the
published Docker image and the release binaries.

Things worth knowing when you assess a finding:

- A profile without a password can sign in only from the home network
  (`VAULT_TRUSTED_NETS`); the first-run setup is accepted only from there.
  Behind a reverse proxy only `VAULT_TRUSTED_PROXIES` is believed in
  `X-Forwarded-For`.
- Book and comic files are untrusted input: the reader sanitises book
  content and the server limits what archives may allocate.
- The server runs `ffmpeg` and `ffprobe` on media files.

Out of scope: the closed-source iPhone, iPad and Mac app (report app problems
the same way, but they are handled outside this repository), denial of service
by an authenticated administrator, and findings that need physical access to
the server.
