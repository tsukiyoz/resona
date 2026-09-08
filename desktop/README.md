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
CGO_ENABLED=1 go build -o ../build/bin/resona-core ../cmd/resona-core
./scripts/package-macos.sh
```

For Windows x64, run `build-windows.cmd` from the repository root after installing
the tools once, or download an artifact from the **Windows build** GitHub Action.
For prerequisites and detailed commands, see
[Windows build instructions](../docs/windows-build.md). The Go audio core uses
CGO/GCC; the Rust GUI uses the MSVC toolchain. Do not pass the Go GCC override
to the Rust build. Windows packaging is implemented but still requires native
build and runtime verification on Windows.
