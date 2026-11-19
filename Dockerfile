# syntax=docker/dockerfile:1
#
# The public server image (Antresol Server, docs/build-tags.md): no
# acquisition code in the web UI or the binary, no extra listening port.
#   docker build .
# (`--target library` names the same, last stage.)

# 1. Фронтенд
# The web bundle and the static binary are built on the builder's own platform,
# so a multi-arch build emulates only the runtime stage.
FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web-library
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2. Бэкенд (статический бинарь, без CGO)
FROM --platform=$BUILDPLATFORM golang:1.27 AS go-src
WORKDIR /src
# third_party/ and addonkit/ hold modules wired in by go.mod `replace` (sevenzip,
# the addon contract types): go needs their go.mod to resolve the module graph,
# so they go in before the download.
COPY go.mod go.sum ./
COPY third_party/ ./third_party/
COPY addonkit/ ./addonkit/
RUN go mod download
COPY . .

FROM go-src AS build-library
ARG TARGETARCH
ARG VERSION=dev
# The commit, and the source offer of GET /api/about (AGPL §13): official
# images pass the URL of their public tag; empty means no link.
ARG COMMIT=
ARG SOURCE_URL=
COPY --from=web-library /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X github.com/antresol-app/antresol/internal/version.Version=${VERSION} \
      -X github.com/antresol-app/antresol/internal/version.Commit=${COMMIT} \
      -X github.com/antresol-app/antresol/internal/version.SourceURL=${SOURCE_URL}" \
    -o /out/vault ./cmd/vault \
 && scripts/check-library-edition.sh /out/vault

# 3. Рантайм
# GPU transcoding (README, "Hardware transcoding"): VA-API drivers for AMD
# (mesa) and, on amd64, Intel (iHD with its prebuilt kernels, from Debian's
# non-free component: MIT-licensed, non-free only because the kernels come
# without their source; LICENSING.md), i965 for Intel before Broadwell
# (libva picks iHD on newer ones) and the Intel Quick Sync runtime.
# NVIDIA's driver comes from the host through the NVIDIA Container Toolkit.
FROM debian:trixie-slim AS library
ARG TARGETARCH
RUN set -eu; intel=; \
    if [ "$TARGETARCH" = amd64 ]; then \
      sed -i 's/^Components: main$/Components: main non-free/' /etc/apt/sources.list.d/debian.sources; \
      intel="intel-media-va-driver-non-free libmfx-gen1.2 i965-va-driver"; \
    fi; \
    apt-get update \
 && apt-get install -y --no-install-recommends \
      ffmpeg mesa-va-drivers $intel vainfo ca-certificates tzdata curl \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -g 1000 vault \
 && useradd -u 1000 -g vault -G video -m -d /home/vault -s /usr/sbin/nologin vault \
 && mkdir -p /config /announce && chown vault:vault /config /announce
# /announce: the folder the server shares with its mDNS announcer
# (`vault mdns-announce`, compose.yml); a new volume mounted there takes
# this owner, so both run as vault and can write it.
# NVIDIA Container Toolkit: the driver libraries NVENC/NVDEC need
ENV VAULT_DATA_DIR=/config \
    NVIDIA_DRIVER_CAPABILITIES=compute,video,utility
VOLUME /config
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8420/api/health || exit 1
COPY --from=build-library /out/vault /usr/local/bin/vault
# The render node's group is the host's (render: a number that differs per
# distribution; video, which vault is in, only on some), so a fixed group in
# the image or a number copied into compose.yml misses on most hosts and the
# GPU fails its test encode ("No VA display found"). Started as root (the
# default), the entrypoint adds the group of each /dev/dri node (and any
# group_add) to vault's and runs the server as vault; group 0 is never added.
# Started as another user (compose user:), it runs the server as that user,
# and group_add is how that user gets the node.
COPY --chmod=0755 <<'EOF' /usr/local/bin/antresol-entrypoint
#!/bin/sh
set -eu
[ "$(id -u)" = 0 ] || exec /usr/local/bin/vault "$@"
groups=$(id -G vault | tr ' ' ',')
for g in $(id -G) $(stat -c %g /dev/dri/* 2>/dev/null); do
	case ",$groups," in *",$g,"*) ;; *) [ "$g" = 0 ] || groups="$groups,$g" ;; esac
done
export HOME=/home/vault USER=vault
setpriv --reuid=vault --regid=vault --groups="$groups" true 2>/dev/null || {
	echo "antresol: started as root but cannot switch to the vault user (CAP_SETUID/CAP_SETGID dropped?); run it as user: 1000:1000 with group_add for the GPU" >&2
	exit 1
}
exec setpriv --reuid=vault --regid=vault --groups="$groups" --inh-caps=-all /usr/local/bin/vault "$@"
EOF
EXPOSE 8420
ENTRYPOINT ["/usr/local/bin/antresol-entrypoint"]
