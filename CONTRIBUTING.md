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
snapshots. A tag also runs the Windows packaging workflow; do not call the Windows
package verified until that run succeeds. Downloadable workflow artifacts are
temporary (14 days), not durable GitHub Release assets.

Push the release commit and annotated tag together after local checks. Publish
GitHub Release assets only from a successful tag build, with the release notes;
mark previews appropriately. Creating a GitHub Release, changing repository
visibility, deploying a server and configuring branch protection are separate
operations. A maintenance branch is warranted only when supporting fixes for an
older release line independently of main; fix main too where applicable.
