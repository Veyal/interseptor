.PHONY: build test race vet check run clean macos-app

# Mirrors the "Green before commit" gate in CONTRIBUTING.md:
#   go test ./... + go test -race ./... + go vet ./... + CGO_ENABLED=0 build
# Use `make check` before every commit.

# Local CLI builds identify themselves separately from published releases.
# Source archives have no Git tags, so use the declared fallback version there.
LOCAL_BASE_VERSION := $(shell if test -e .git; then git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null; fi)
ifeq ($(strip $(LOCAL_BASE_VERSION)),)
LOCAL_BASE_VERSION := $(shell sed -nE 's/^[[:space:]]*(const|var)[[:space:]]+Version[[:space:]]*=[[:space:]]*"([^"]*)".*$$/\2/p' internal/version/version.go)
endif
LOCAL_VERSION := $(patsubst v%,%,$(LOCAL_BASE_VERSION))-local
LOCAL_LDFLAGS := -X github.com/Veyal/interseptor/internal/version.Version=$(LOCAL_VERSION)

build:
	CGO_ENABLED=0 go build -ldflags "$(LOCAL_LDFLAGS)" ./cmd/interseptor

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

check: vet test race build

run:
	CGO_ENABLED=0 go run -ldflags "$(LOCAL_LDFLAGS)" ./cmd/interseptor

# Build dist/macos/Interseptor.app (macOS only). Signing and notarization are
# opt-in via CODESIGN_IDENTITY / NOTARY_PROFILE — see packaging/macos/README.md.
macos-app:
	./packaging/macos/build-app.sh

clean:
	go clean
	rm -rf dist
