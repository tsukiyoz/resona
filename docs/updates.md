# Desktop updates

## Distribution and interaction

One immutable version tag covers all distribution types. `resona-package.json`
records schema 1, product, platform, version and `distribution: portable|installer`.
Windows metadata lives beside resona-desktop.exe; macOS metadata lives in Contents/Resources.
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
actions. Their core rejects DownloadUpdate/InstallUpdate commands.
The former ZIP directory replacement helper and CLI entry were removed.
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

Windows installers use pinned Velopack 1.2.161 (commit
`92d6a1c91716729d449034df5c50307dcce39493`), installing for the current user.
The SDK runs installation hooks before GPUI/core/device initialization. Startup
auto-apply and file logging are disabled. No background update service or polling
is added. A matching Resona installation/manifest is required, in addition to the
installer marker. Renaming a portable ZIP does not enable OTA.

About offers Check -> Download -> Restart and update -> Confirm restart. Downloads
show bounded progress (at most four updates/second), can be cancelled, and survive
closing Settings. Cancellation discards the partial temporary file. Size, HTTPS,
signature and SHA-256 failures never enable installation. Downloads have a 30-minute
overall deadline and 30-second chunk wait. There is one task at a time; no resume
or delta in this first version. Temporary data is under the user's resona/updates
cache, outside the install directory, and removed on cancellation/normal exit.

Confirming restart rechecks the file and transfers its ownership outside the core.
Capture/PTT, microphone test, playback and network are stopped by normal core
cleanup. Only after core.close completes does the runtime stage the full nupkg
and launch Update.exe in wait-for-current-PID mode; GUI then quits. This avoids
consuming Velopack's 60-second helper deadline while closing audio devices.
The ordinary seven-second force-quit fallback is disabled for update exits;
application-level quit waits up to 60 seconds for cleanup/handoff before GPUI's
short quit-future deadline begins. Exceeding that bound or OS forced termination
can abort an update and is not a guaranteed graceful exit. Replacement starts
only after process exit. A failed launch restarts the current client runtime in
disconnected state, exposes the error in About and allows retry/reconnect. Replacement failures
use Velopack's native failure/recovery interaction. This is not automatic rollback
for an application regression. Ordinary quit does not apply a downloaded update.

Windows Authenticode is explicitly deferred for this iteration. Installers are
named `Setup-unsigned.exe`; Windows may display publisher/SmartScreen warnings.
Ed25519 authenticity remains mandatory for OTA, but is not Authenticode. Do not
describe these builds as trusted Windows publisher-signed packages. macOS Developer
ID/notarization and native Windows install/update acceptance remain separate.

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

Installed Windows builds replace the endpoint's final `/manifest.json` with
`/manifest-windows-installer.json`. That feed uses schema 2 and only installer
assets; portable schema 1 is unchanged for older released clients. The same trust
key signs both feeds. A full nupkg's product/platform/distribution metadata and
Resona nuspec version are validated before its digest is signed.

## Tag publication

`release.yml` validates an annotated tag on main, VERSION and release notes, then
calls Windows and both macOS architecture builds pinned to the event commit.
After portable and installer packages succeed, `scripts/publish-release.sh` signs both manifests,
creates a draft, uploads every package/manifest and makes the release public.
Assets use `Resona-vX.Y.Z-<platform>-portable.zip`,
`Resona-vX.Y.Z-win-x64-Setup-unsigned.exe` and `Resona-vX.Y.Z-win-x64-full.nupkg` names. Workflow artifacts remain
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

## Windows acceptance

CI builds the portable payload, installs pinned `vpk` with .NET 8 (build-only),
creates a separate installer stage and validates the full nupkg without release
secrets. Manual builds remain `-dev` and do not consume the stable update channel.
Download the `win-x64-installer-unsigned` artifact and run its Setup executable,
not the nupkg. Existing portable preferences and credentials retain their current
user-directory paths; uninstall never deliberately deletes those directories.

Before release, test two aligned formal versions on Windows: install A, publish B's
signed feed and full package to a test endpoint, then verify check/download/cancel,
closing/reopening Settings, ordinary quit without applying, active-call confirmed
restart, identity/bookmark/preference retention, corrupt/truncated packages, full
disk, missing/locked Update.exe and repeated failures. Use a dedicated signing key
and test server for fixtures, never publish a fake newer version to the stable feed.
Record native acceptance separately; macOS tests cannot prove Windows replacement.
