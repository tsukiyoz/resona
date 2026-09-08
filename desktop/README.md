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

On Windows, build the Go core as `build/bin/resona-core.exe`, then run
`scripts/package-windows.ps1`. Build it from a Developer PowerShell with a C
compiler available; voice support requires CGO and the package script rejects
an unset `CGO_ENABLED`:

```powershell
$env:CGO_ENABLED = "1"
go build -o build/bin/resona-core.exe ./cmd/resona-core
desktop/scripts/package-windows.ps1
```

Windows packaging is implemented but requires runtime verification on Windows.
