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

From the repository root, `make build` builds desktop, core and server;
`make clean` removes `build/bin/`, generated `build/deploy/` and `desktop/dist/` while keeping compiler
caches. `make clean-all` additionally removes `desktop/target/`. Keep cleanup
and build in separate invocations. On Linux, `make build-desktop` places both
executables in `build/bin/`; Windows keeps the dedicated build entry below.

The bundle includes a multi-resolution `Resona.icns` generated from
`build/appicon.png` with macOS `sips` and `iconutil`. `CFBundleIconFile` supplies
the Finder and Dock icon; launch the `.app`, not the bare Rust executable.

For Windows x64, run `build-windows.cmd` from the repository root after installing
the tools once, or download an artifact from the **Windows build** GitHub Action.
For prerequisites and detailed commands, see
[Windows build instructions](../docs/windows-build.md). The Go audio core uses
CGO/GCC; the Rust GUI uses the MSVC toolchain. Do not pass the Go GCC override
to the Rust build. Windows packaging is implemented but still requires native
build and runtime verification on Windows.
