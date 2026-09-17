# Resona native desktop

This directory contains the GPUI desktop client. It uses `gpui` 0.2.2 and
`gpui-component` 0.5.1 from crates.io. The UI starts a private `resona-core`
child process beside the executable, or from `RESONA_CORE` during development,
and communicates over inherited stdin/stdout NDJSON.

Run against a development core without connecting to a server:

```sh
RESONA_CORE=../build/bin/resona-core cargo run
```

Build the macOS app bundle:

```sh
make -C .. build-desktop
open dist/Resona.app
```

Running `./scripts/package-macos.sh` directly also builds the core via
`make build-core`. Both entries build the Go core (with CGO) and the Rust GUI from the current
checkout. It does not reuse an old `build/bin/resona-core` by default. Set
`RESONA_CORE_BINARY` only to deliberately package a supplied core, for example an
isolated test harness; the caller is responsible for matching its IPC/protocol.
Quit any running Resona before replacing and reopening its bundle.

For stable macOS Keychain identity across builds, install a valid Apple code-signing
certificate and its private key, then select it explicitly (name or certificate hash):

```sh
security find-identity -v -p codesigning
RESONA_CODESIGN_IDENTITY='Apple Development: Your Name (TEAMID)' make -C .. build-desktop
```

Packaging signs the core as `dev.resona.core`, then the app as `dev.resona.client`,
and verifies both. Use the same signing team and identifiers for subsequent builds.
Signing failure fails the build; there is no silent ad-hoc fallback when an identity
is supplied. Without an identity, local builds remain available but their
ad-hoc signatures do not provide stable identity across binary changes. Existing
passwords saved by older ad-hoc builds may still require initial authorization.
This does not rewrite Keychain ACLs or grant unrestricted access. Certificate setup
and old-item migration must be verified on the target Mac. Developer ID distribution
also requires a separate hardened-runtime/notarization workflow; this signing option
alone is not a notarized release pipeline.

From the repository root, `make build` builds desktop, core and server;
`make clean` removes `build/bin/`, generated `build/deploy/` and `desktop/dist/` while keeping compiler
caches. `make clean-all` additionally removes `desktop/target/`. Keep cleanup
and build in separate invocations. On Linux, `make build-desktop` places both
executables in `build/bin/`; Windows keeps the dedicated build entry below.

The bundle includes a multi-resolution `Resona.icns` generated from
`build/appicon.png` with macOS `sips` and `iconutil`. `CFBundleIconFile` supplies
the fixed white Finder icon and initial Dock icon; launch the `.app`, not the bare
Rust executable. While running, the in-app logo and macOS Dock icon follow the
effective theme: white with dark bars for light mode, dark with light bars for
dark mode. Windows sets both native window icon sizes; the taskbar may retain a
cached pinned-shortcut icon. EXE and shortcut defaults remain white. Theme preview
and cancellation update runtime icons without rewriting the signed package.

From the repository root, `go run ./tools/appicon` regenerates both runtime PNGs,
both Windows ICO resources and the white `build/appicon.png` from the original
`desktop/assets/resona-source.png`. Runtime PNGs are 256px and cached; icon updates
only run when the theme changes, without an idle timer.

The **macOS build** GitHub Action builds separate Apple Silicon (`macos-arm64`)
and Intel (`macos-x64`) packages on main pushes, matching pull requests and version
tags; it can also be started manually. Download the appropriate artifact, extract
its inner ZIP and move `Resona.app` to Applications. `ditto` preserves bundle
metadata and executable permissions. CI packages currently use ad-hoc signatures,
not Developer ID or Apple notarization; Gatekeeper may block downloaded builds.
Formal signing and notarization remain pending certificate setup.

Both desktop workflows name artifacts `Resona-<version>-<platform>-<commit SHA>`.
Branch builds use `v<VERSION>-dev`; exact matching version tags omit `-dev`.
The suffix identifies the source commit, not additional application dependencies.
Artifacts expire after 14 days and are not permanent GitHub Release assets.

For Windows x64, run `build-windows.cmd` from the repository root after installing
the tools once, or download an artifact from the **Windows build** GitHub Action.
For prerequisites and detailed commands, see
[Windows build instructions](../docs/windows-build.md). The Go audio core uses
CGO/GCC; the Rust GUI uses the MSVC toolchain. Do not pass the Go GCC override
to the Rust build. Windows packaging is implemented but still requires native
build and runtime verification on Windows.
