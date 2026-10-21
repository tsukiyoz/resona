.DEFAULT_GOAL := build
.PHONY: help dev build build-core build-opus build-apm build-desktop build-server deploy core clean clean-all test test-client test-native test-build test-deploy generate format lint

HOST_OS := $(shell uname -s)
GOEXE = $(shell go env GOEXE)
RUST_BUILD_ENV = RESONA_VERSION="$(VERSION)" RESONA_BUILD_TIME="$(BUILD_TIME)" PKG_CONFIG_PATH="$(CURDIR)/build/native/opus-install/lib/pkgconfig" OPUS_STATIC=1
GOFUMPT ?= go run mvdan.cc/gofumpt@v0.10.0
STATICCHECK ?= go run honnef.co/go/tools/cmd/staticcheck@v0.8.1
GO_MODULES := . test/controlcodec test/opuscodec test/audio3a/chain

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
	@echo 'make / make build   Build native desktop and server'
	@echo 'make build-core     Build the Rust client library into client/target/'
	@echo 'make build-apm      Build native WebRTC and RNNoise audio library into build/bin/'
	@echo 'make build-desktop  Build desktop with in-process core (macOS: desktop/dist/Resona.app)'
	@echo 'make build-server   Build the pure-Go server into build/bin/'
	@echo 'make deploy         Generate a Linux server package in build/deploy/ (no upload)'
	@echo 'make dev            Build native libraries and run the debug desktop'
	@echo 'make clean          Remove build/bin/, build/deploy/ and desktop/dist/'
	@echo 'make clean-all      Also remove desktop, client and native WebRTC Rust caches'
	@echo 'make test           Run Go and locked Rust tests'
	@echo 'make test-client    Test the Rust client foundation against a local Go server'
	@echo 'make test-build     Check cleanup boundaries in a temporary directory'
	@echo 'make test-deploy    Check deployment scripts without a real server'
	@echo 'make format         Format Go (gofumpt v0.10.0) and Rust sources'
	@echo 'make lint           Run go vet and Staticcheck for all Go modules'
	@echo 'VERSION=vX.Y.Z      Override build metadata (default: v + root VERSION file)'
	@echo 'Windows desktop: use build-windows.cmd (MSVC + UCRT64 setup)'

generate:
	go generate ./internal/nativewire/pb

format:
	$(GOFUMPT) -w cmd internal deploy tools test client/tests/peer
	cargo fmt --manifest-path desktop/Cargo.toml
	cargo fmt --manifest-path client/Cargo.toml
	cargo fmt --manifest-path native/webrtc-apm/Cargo.toml

lint:
	@for module in $(GO_MODULES); do \
		(cd "$$module" && go vet ./...) || exit $$?; \
	done

	@for module in $(GO_MODULES); do \
		(cd "$$module" && $(STATICCHECK) ./...) || exit $$?; \
	done

dev: build-opus build-apm
	$(RUST_BUILD_ENV) RESONA_WEBRTC_PLUGIN_DIR="$(CURDIR)/build/bin" cargo run --locked --manifest-path desktop/Cargo.toml

build: build-desktop build-server

build-opus:
	bash native/opus/build.sh

build-apm:
	mkdir -p build/bin
	bash -c 'source native/rnnoise/prepare.sh; PKG_CONFIG_LIBDIR=/nonexistent cargo build --locked --release --manifest-path native/webrtc-apm/Cargo.toml'
ifeq ($(HOST_OS),Darwin)
	cp native/webrtc-apm/target/release/libresona_webrtc_apm.dylib build/bin/
	install_name_tool -id @rpath/libresona_webrtc_apm.dylib build/bin/libresona_webrtc_apm.dylib
else ifeq ($(HOST_OS),Linux)
	cp native/webrtc-apm/target/release/libresona_webrtc_apm.so build/bin/
endif

build-core: build-opus build-apm
	$(RUST_BUILD_ENV) cargo build --locked --release --manifest-path client/Cargo.toml --features audio

core: build-core

build-server:
	mkdir -p build/bin
	CGO_ENABLED=0 go build -trimpath -ldflags '$(VERSION_LDFLAGS)' -o "build/bin/resona-server$(GOEXE)" ./cmd/resona-server

deploy:
	VERSION="$(VERSION)" BUILD_TIME="$(BUILD_TIME)" DEPLOY_ARCH="$(DEPLOY_ARCH)" sh deploy/package.sh

build-desktop:
ifeq ($(HOST_OS),Darwin)
	RESONA_VERSION="$(VERSION)" RESONA_BUILD_TIME="$(BUILD_TIME)" ./desktop/scripts/package-macos.sh
else ifeq ($(HOST_OS),Linux)
	$(MAKE) build-opus build-apm
	$(RUST_BUILD_ENV) cargo build --locked --release --manifest-path desktop/Cargo.toml
	cp desktop/target/release/resona-desktop build/bin/resona-desktop
	@echo 'Built: build/bin/resona-desktop (keep libresona_webrtc_apm.so beside it)'
else
	@echo 'For Windows desktop builds, run build-windows.cmd.' >&2
	@exit 1
endif

clean:
	rm -rf -- "$(CURDIR)/build/bin" "$(CURDIR)/build/deploy" "$(CURDIR)/desktop/dist"

clean-all: clean
	rm -rf -- "$(CURDIR)/desktop/target" "$(CURDIR)/client/target" "$(CURDIR)/native/webrtc-apm/target"

test-build:
	sh tools/test-build-toolchain.sh
	sh tools/test-macos-signing.sh

test-deploy:
	go test ./deploy

test-client: build-opus
	$(RUST_BUILD_ENV) cargo test --locked --manifest-path client/Cargo.toml --features audio

test: test-build test-deploy test-client
	go test ./internal/...
	$(RUST_BUILD_ENV) cargo test --locked --manifest-path desktop/Cargo.toml
	bash -c 'source native/rnnoise/prepare.sh; cargo test --locked --manifest-path native/webrtc-apm/Cargo.toml'

test-native:
	go test -race ./internal/protocol ./internal/protocol/native ./internal/nativewire ./internal/nativeidentity ./internal/noiseudp ./internal/server ./internal/audio ./internal/client
	$(MAKE) test-client
	$(RUST_BUILD_ENV) cargo test --locked --manifest-path desktop/Cargo.toml
