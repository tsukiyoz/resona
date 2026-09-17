# Contributing and Releases

## Branches

`main` is the integration and release baseline. Start short-lived branches from
the latest main using `feature/tsukiyo/{module}-{YYYYMMDD}_{task}` or
`fix/tsukiyo/{module}-{YYYYMMDD}_{task}` as specified in AGENTS.md. Keep each branch
focused on a reviewable change. Do not maintain a permanent develop or release
branch while only one release line is supported.

Use pull requests targeting main for contributions. Describe behavior, tests and
compatibility changes; obtain review and passing relevant checks before merging.
Maintainers may merge directly when explicitly authorized, with equivalent local
checks and recorded review evidence. Preserve a merge commit for a feature made
of several coherent commits. Delete completed topic branches when no longer
needed; never force-push shared main.

## Checks

- Formatting: `make format` uses pinned `gofumpt` v0.10.0 for Go and `cargo fmt`
  for both Rust applications. The first run may download the Go formatter; an
  installed matching version can be used with `make format GOFUMPT=gofumpt`.
  Generated and vendored Go files are skipped by gofumpt's directory traversal.

- Core: `go test ./internal/...`; use `go test -race` for affected concurrency and
  lifecycle packages.
- Desktop: `cargo test --locked --manifest-path desktop/Cargo.toml`, native build
  and relevant GUI flows. Windows Actions validate Windows-specific code.
- Protocol: regenerate protobuf via `go generate ./internal/nativewire/pb`, test
  the Noise transport and document compatibility/migration.
- Keep credentials, identities, local test data and build artifacts out of Git.
  Review staged changes and run `git diff --check` before committing.

## Releases

Use annotated, immutable `vMAJOR.MINOR.PATCH` tags on main. Never delete or move a
published tag to repair a release; make a new version. The current 0.x series is
an experimental preview sequence, not a stable protocol/API compatibility promise.
Document incompatible protocol changes and coordinated client/server upgrades in
every affected release. Use minor versions for substantial milestones or breaking
protocol changes and patch versions for compatible fixes. Adopt 1.0 only after
the supported workflows and protocol compatibility commitments are established.

The root `VERSION` file is the base version source, without the `v` prefix
(for example `0.1.1`). Before tagging, update it, synchronize desktop
Cargo.toml/Cargo.lock and write `docs/releases/vX.Y.Z.md`. Cargo requires a literal
manifest version; desktop builds reject a mismatch with VERSION. macOS bundle
versions are read automatically. Make, deployment packaging, Windows local builds
and Docker builds default to `v` plus VERSION; explicit build overrides do not
edit the file or create a release. Raw `go build` without build flags still reports
`dev`. CI requires the exact tag to match VERSION and the desktop version.
Main-branch CI builds append `-dev` and are development
snapshots. A tag also runs the Windows and macOS packaging workflows; do not call
either platform package verified until its run succeeds. macOS builds separate
Apple Silicon and Intel bundles. PR builds are ad-hoc signed test packages;
trusted push builds and manual main builds require the signing Secrets below.
Certificate signing alone does not mean Apple notarization. Downloadable workflow artifacts are
temporary (14 days), not durable GitHub Release assets.

Push the release commit and annotated tag together after local checks. Publish
GitHub Release assets only from a successful tag build, with the release notes;
mark previews appropriately. Creating a GitHub Release, changing repository
visibility, deploying a server and configuring branch protection are separate
operations. A maintenance branch is warranted only when supporting fixes for an
older release line independently of main; fix main too where applicable.

## macOS CI Signing

Configure repository Actions Secrets `MACOS_CERTIFICATE_P12_BASE64` (Base64 of an
encrypted PKCS#12 export containing the selected certificate and private key),
`MACOS_CERTIFICATE_PASSWORD` (export password), and `MACOS_SIGNING_IDENTITY`
(40-character certificate SHA-1 fingerprint). Never commit the export or password,
or put them in ordinary Actions Variables. Export only the intended identity.

Trusted builds import the identity into a temporary runner keychain, pass it to
`RESONA_CODESIGN_IDENTITY`, verify the bundle, and delete the temporary keychain
with an `always()` cleanup step. Missing/expired signing credentials fail trusted
builds rather than silently producing ad-hoc packages. PRs and manual non-main
builds do not receive signing Secrets. Keep main/tag publishing permissions
restricted to trusted maintainers; a workflow with Secrets can use the private key.

The current identity is Apple Development, for development testing. Public macOS
distribution should use Developer ID Application and a separate notarization
workflow. Keep local and CI signing identities consistent; mixing ad-hoc builds
with certificate-signed builds may prompt for old Keychain items again. A recreated
password item was verified locally across two builds of the same development
identity; this is not a guarantee that arbitrary old ACLs migrate without prompts.
