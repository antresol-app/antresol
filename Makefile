VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null)
# The source offer (GET /api/about, AGPL §13): an official build passes the
# URL of its public tag; empty (the default) means the server shows no link.
SOURCE_URL ?=
LDFLAGS := -X github.com/antresol-app/antresol/internal/version.Version=$(VERSION) \
	-X github.com/antresol-app/antresol/internal/version.Commit=$(COMMIT) \
	-X github.com/antresol-app/antresol/internal/version.SourceURL=$(SOURCE_URL)

.PHONY: build web go test test-web e2e linux docker-check

build: go

web:
	@if [ ! -d web/node_modules ]; then npm --prefix web ci; fi
	npm --prefix web run build

go: web
	go build -ldflags "$(LDFLAGS)" -o bin/vault ./cmd/vault

test:
	go test ./...

test-web:
	npm --prefix web test

# Browser tests against a real bin/vault (built first).
e2e: build
	cd web && npx playwright test

linux: web
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/vault-linux-amd64 ./cmd/vault

# The core build (docs/build-tags.md): no acquisition code in the
# web bundle or the binary; check-library builds it and checks that.
# GOFLAGS is cleared: these are public builds whatever the caller's GOFLAGS.
.PHONY: library check-library
library:
	@if [ ! -d web/node_modules ]; then npm --prefix web ci; fi
	npm --prefix web run build
	GOFLAGS= CGO_ENABLED=0 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o bin/antresol-server ./cmd/vault

check-library:
	scripts/check-library-edition.sh

# Smoke check: the image builds from exactly what is committed (git archive
# HEAD), so a Dockerfile that misses a tracked path fails here, not on deploy.
# Needs a docker daemon; DOCKER_CHECK_TAG is removed afterwards. A checkout
# whose HEAD cannot build this image sets DOCKER_CHECK_REFUSE to say why and
# what to run instead, so the check stops at once, not after the web build.
DOCKER_CHECK_TAG ?= vault:docker-check
DOCKER_CHECK_REFUSE ?=
docker-check:
	@[ -z "$(DOCKER_CHECK_REFUSE)" ] || { echo "docker-check: $(DOCKER_CHECK_REFUSE)" >&2; exit 1; }
	git archive HEAD | docker build -q -t $(DOCKER_CHECK_TAG) -
	docker rmi $(DOCKER_CHECK_TAG)
