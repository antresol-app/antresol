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
# third_party/ holds modules wired in by go.mod `replace` (sevenzip): go needs
# their go.mod to resolve the module graph, so they go in before the download.
COPY go.mod go.sum ./
COPY third_party/ ./third_party/
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
FROM debian:trixie-slim AS library
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ffmpeg vainfo ca-certificates tzdata curl \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -g 1000 vault \
 && useradd -u 1000 -g vault -G video -m -d /home/vault -s /usr/sbin/nologin vault \
 && mkdir -p /config && chown vault:vault /config
ENV VAULT_DATA_DIR=/config
VOLUME /config
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8420/api/health || exit 1
COPY --from=build-library /out/vault /usr/local/bin/vault
USER vault
EXPOSE 8420
ENTRYPOINT ["/usr/local/bin/vault"]
