.PHONY: dev build core test test-protocol generate

VERSION ?= dev
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_LDFLAGS = -X github.com/tsukiyoz/resona/internal/version.Version=$(VERSION) -X github.com/tsukiyoz/resona/internal/version.BuildTime=$(BUILD_TIME)

generate:
	go generate ./internal/nativewire/pb

dev: core
	RESONA_CORE="$(CURDIR)/build/bin/resona-core" cargo run --manifest-path desktop/Cargo.toml

build: core
	cargo build --release --manifest-path desktop/Cargo.toml

core:
	CGO_ENABLED=1 go build -ldflags '$(VERSION_LDFLAGS)' -o build/bin/resona-core ./cmd/resona-core

test:
	go test ./internal/...
	cargo test --manifest-path desktop/Cargo.toml

test-protocol:
	cd third_party/teamspeak-go && go test -race ./...
