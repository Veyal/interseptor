.PHONY: build test race vet check run clean macos-app

# Mirrors the "Green before commit" gate in CONTRIBUTING.md:
#   go test ./... + go test -race ./... + go vet ./... + CGO_ENABLED=0 build
# Use `make check` before every commit.

build:
	CGO_ENABLED=0 go build ./cmd/interseptor

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

check: vet test race build

run:
	go run ./cmd/interseptor

# Build dist/macos/Interseptor.app (macOS only). Signing and notarization are
# opt-in via CODESIGN_IDENTITY / NOTARY_PROFILE — see packaging/macos/README.md.
macos-app:
	./packaging/macos/build-app.sh

clean:
	go clean
	rm -rf dist
