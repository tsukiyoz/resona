# Desktop updates

## Distribution and interaction

One immutable version tag covers all distribution types. `resona-package.json`
records schema 1, product, platform, version and `distribution: portable|installer`.
Windows metadata lives beside Core; macOS metadata lives in Contents/Resources.
Core validates it once at startup and returns interpreted capabilities/reasons.
Missing, invalid or mismatched metadata never enables updates. See ADR-0030.

Current Windows ZIP and macOS .app ZIP packages are **portable**. Updates are
available only in Settings -> About; the main window has no update button or
extra toolbar. Opening About reads local status without starting a network check.
Users explicitly check, then open a verified newer version's GitHub Release page
and download/replace the application themselves. Desktop consumes Core's `canOpenRelease` and
verified URL; it does not infer actions from platform or distribution strings.
The system browser is requested to open the page; this is not reported as a
successful download or installation. Core URL errors remain in the panel.

Portable packages have no in-app download, package reveal, install or update-exit
actions. Core also rejects legacy DownloadUpdate/GetUpdatePackage/InstallUpdate
commands. The former ZIP directory replacement helper and CLI entry were removed.
No packages are downloaded or cached. Configuration, identities and credentials
remain in their existing user directories outside the application package.

Only one check runs at a time with a 10-second overall deadline. Closing settings or changing pages leaves it running;
explicit cancellation or ordinary app exit cancels it. Failure/cancellation can
be retried. Starting a new check clears the old release action. Startup only reads
local update status; there is no startup network check, idle timer, animation or
result toast. Checks never run in voice workers or the IPC command loop.

Only actual cancellation uses the cancelled state. Deadline, network, HTTP and
manifest verification errors use failed, with "检查更新失败：" followed by a safe
reason. HTTP 404 identifies an unpublished manifest; DNS, TLS, connection and
timeout errors remain distinct. Raw request URLs and transport errors are not
shown. No automatic retry extends the deadline.

**Installer packages are not implemented yet.** Their metadata type is recognized,
but reports "installer update backend unavailable" and exposes no update actions.
Future installers must handle their own upgrade, failure recovery and restart;
changing a ZIP marker to installer does not implement those behaviors. Signed
assets will need a distribution dimension before both types can be served.
macOS Developer ID/notarization and Windows native acceptance remain separate.

## Trust and build configuration

Build-time `RESONA_UPDATE_URL` points to
`https://github.com/tsukiyoz/resona/releases/latest/download/manifest.json`.
`RESONA_UPDATE_PUBLIC_KEY` is the base64 Ed25519 public key. Both are public Actions
Variables injected into Core. Missing configuration and dev snapshots disable
checking with an explicit reason. No GitHub token is shipped to clients.

The signed payload has schema 1, product resona, channel stable, an immutable
vMAJOR.MINOR.PATCH, expiry, notes, `releaseURL`, and an assets map keyed by platform
(windows/amd64, darwin/arm64, darwin/amd64). Current assets declare
`distribution: portable`, HTTPS URL, SHA-256 and exact size. Signature verification
precedes interpretation. The Release page must exactly match
`https://github.com/tsukiyoz/resona/releases/tag/<signed version>`.
Only a strictly newer formal version with a matching platform asset enables the
open-page action. Expired, invalid or forged manifests never expose that action.
This is not a complete TUF implementation. Ed25519 signing is independent of
Apple/Windows platform signing; manual downloads are handled by the browser.

## Tag publication

`release.yml` validates an annotated tag on main, VERSION and release notes, then
calls Windows and both macOS architecture builds pinned to the event commit.
After all three packages succeed, `scripts/publish-release.sh` signs the manifest,
creates a draft, uploads every package/manifest and makes the release public.
Assets use `Resona-vX.Y.Z-<platform>-portable.zip` names. Workflow artifacts remain
temporary (14 days); Release assets provide the public downloads.

Existing releases, including drafts, are never overwritten. Review/delete an
incomplete draft before rerunning the same release. Never move a published tag.
Publication is serialized; older versions cannot take over latest. The remote
annotated tag is checked against the event commit before draft creation and
publication. Maintainers must still protect tags: Git ref mutation and Release
API operations are not atomic. Manual workflow dispatch must select a release tag.

## Signing and recovery of publishing configuration

The publishing-only Secret `RESONA_UPDATE_SIGNING_KEY` contains the base64 64-byte
Ed25519 private key. It is separate from Noise identities and never passed to
platform builds or PRs. Signer output is checked against the public key embedded
in clients. All ZIP product/version/platform/distribution metadata is validated before
signing. Manifests expire after 365 days; publish before expiry.

On 2026-10-15, the Secret and two Variables were provisioned in tsukiyoz/resona.
The private backup is a local Keychain item **Resona release update signing key**,
service `io.github.tsukiyoz.resona.release-signing`, account `github-releases`.
No private file was added to Git/artifacts and no Release was published then.
Back it up securely; changing the trust key breaks already installed clients.

Example asset-input JSON for the signing tool:

```json
[{"platform":"windows/amd64","path":"desktop/dist/Resona-win-x64.zip","url":"https://github.com/tsukiyoz/resona/releases/download/v0.2.0/Resona-v0.2.0-win-x64-portable.zip"}]
```

```sh
go run ./tools/release-manifest -version v0.2.0 -assets build/release-assets.json \
  -expires 2027-10-14T00:00:00Z -public-key "$RESONA_UPDATE_PUBLIC_KEY" -out build/manifest.json
```

The interaction was evaluated against DBX commit
`12139b55df9b4bf0ae89d4fab459bf1871e8a783`, notably
[AppToolbar.vue](https://github.com/t8y2/dbx/blob/12139b55df9b4bf0ae89d4fab459bf1871e8a783/apps/desktop/src/components/layout/AppToolbar.vue)
and UpdateDialog/useAppUpdater. Resona does not adopt hourly checks or plugin updates.
