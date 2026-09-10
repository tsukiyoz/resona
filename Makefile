.PHONY: dev build core test test-protocol

dev: core
	RESONA_CORE="$(CURDIR)/build/bin/resona-core" cargo run --manifest-path desktop/Cargo.toml

build: core
	cargo build --release --manifest-path desktop/Cargo.toml

core:
	CGO_ENABLED=1 go build -o build/bin/resona-core ./cmd/resona-core

test:
	go test ./internal/...
	cargo test --manifest-path desktop/Cargo.toml

test-protocol:
	cd third_party/teamspeak-go && go test -race ./...
