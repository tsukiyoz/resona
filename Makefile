.DEFAULT_GOAL := build
.PHONY: help dev build build-core build-apm build-desktop build-server deploy core clean clean-all test test-native test-build test-deploy generate

HOST_OS := $(shell uname -s)
GOEXE = $(shell go env GOEXE)
CORE_BINARY = $(CURDIR)/build/bin/resona-core$(GOEXE)

# Cleaning and building in the same invocation can race under make -j.
ifneq ($(filter clean clean-all,$(MAKECMDGOALS)),)
ifneq ($(filter-out clean clean-all help,$(MAKECMDGOALS)),)
$(error Run clean and build in separate make invocations)
endif
endif

VERSION ?= v$(shell cat VERSION)
DEPLOY_ARCH ?= amd64
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_LDFLAGS = -X github.com/tsukiyoz/resona/internal/version.Version=$(VERSION) -X github.com/tsukiyoz/resona/internal/version.BuildTime=$(BUILD_TIME)

help:
	@echo 'make / make build   Build core, native desktop and server'
	@echo 'make build-core     Build the CGO audio core into build/bin/'
	@echo 'make build-apm      Build optional WebRTC AEC3/NS/AGC2 library into build/bin/'
	@echo 'make build-desktop  Build core and desktop (macOS: desktop/dist/Resona.app)'
	@echo 'make build-server   Build the pure-Go server into build/bin/'
	@echo 'make deploy         Generate a Linux server package in build/deploy/ (no upload)'
	@echo 'make dev            Build core and run the debug desktop'
	@echo 'make clean          Remove build/bin/, build/deploy/ and desktop/dist/'
	@echo 'make clean-all      Also remove desktop/target/ and native/webrtc-apm/target/ (Rust caches)'
	@echo 'make test           Run Go and locked Rust tests'
	@echo 'make test-build     Check cleanup boundaries in a temporary directory'
	@echo 'make test-deploy    Check deployment scripts without a real server'
	@echo 'VERSION=vX.Y.Z      Override Go metadata (default: v + root VERSION file)'
	@echo 'Windows desktop: use build-windows.cmd (MSVC + UCRT64 setup)'

generate:
	go generate ./internal/nativewire/pb

dev: build-core
	RESONA_CORE="$(CORE_BINARY)" cargo run --locked --manifest-path desktop/Cargo.toml

build: build-desktop build-server

build-apm:
	mkdir -p build/bin
	PKG_CONFIG_LIBDIR=/nonexistent cargo build --locked --release --manifest-path native/webrtc-apm/Cargo.toml
ifeq ($(HOST_OS),Darwin)
	cp native/webrtc-apm/target/release/libresona_webrtc_apm.dylib build/bin/
	install_name_tool -id @rpath/libresona_webrtc_apm.dylib build/bin/libresona_webrtc_apm.dylib
else ifeq ($(HOST_OS),Linux)
	cp native/webrtc-apm/target/release/libresona_webrtc_apm.so build/bin/
endif

build-core: build-apm
	mkdir -p build/bin
	CGO_ENABLED=1 go build -trimpath -ldflags '$(VERSION_LDFLAGS)' -o "$(CORE_BINARY)" ./cmd/resona-core

core: build-core

build-server:
	mkdir -p build/bin
	CGO_ENABLED=0 go build -trimpath -ldflags '$(VERSION_LDFLAGS)' -o "build/bin/resona-server$(GOEXE)" ./cmd/resona-server

deploy:
	VERSION="$(VERSION)" BUILD_TIME="$(BUILD_TIME)" DEPLOY_ARCH="$(DEPLOY_ARCH)" sh deploy/package.sh

build-desktop: build-core
ifeq ($(HOST_OS),Darwin)
	RESONA_CORE_BINARY="$(CORE_BINARY)" ./desktop/scripts/package-macos.sh
else ifeq ($(HOST_OS),Linux)
	cargo build --locked --release --manifest-path desktop/Cargo.toml
	cp desktop/target/release/resona-desktop build/bin/resona-desktop
	@echo 'Built: build/bin/resona-desktop (keep resona-core beside it)'
else
	@echo 'For Windows desktop builds, run build-windows.cmd.' >&2
	@exit 1
endif

clean:
	rm -rf -- "$(CURDIR)/build/bin" "$(CURDIR)/build/deploy" "$(CURDIR)/desktop/dist"

clean-all: clean
	rm -rf -- "$(CURDIR)/desktop/target" "$(CURDIR)/native/webrtc-apm/target"

test-build:
	sh tools/test-build-toolchain.sh

test-deploy:
	go test ./deploy

test: test-build test-deploy
	go test ./internal/...
	cargo test --locked --manifest-path desktop/Cargo.toml
	cargo test --locked --manifest-path native/webrtc-apm/Cargo.toml

test-native:
	go test -race ./internal/protocol ./internal/protocol/native ./internal/nativewire ./internal/nativeidentity ./internal/noiseudp ./internal/server ./internal/audio ./internal/client ./internal/desktopipc ./cmd/resona-core
	cargo test --locked --manifest-path desktop/Cargo.toml
