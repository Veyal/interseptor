.PHONY: build test race vet check run clean

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

clean:
	go clean
