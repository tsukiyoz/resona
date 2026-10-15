# Desktop updates

## Current implementation

Settings -> About provides manual checking, download/cancel, and verified-package
status. Nothing contacts a release service at startup or on an idle timer.
Network, hashing and disk work run outside the voice workers and IPC command loop.
Only one update task runs at a time. Ordinary exit cancels work and cleans an
uncommitted staged download; it never installs an update.

Windows x64 portable packages support **Exit and update**. The running trusted
Core copies itself and its runtime DLLs into a separate transaction directory,
opens handles for the original Desktop/Core, and acknowledges preparation. The
parent sends commit and waits for the helper's armed acknowledgement before exit;
failed preparation writes abort and keeps the application running. Only an
acknowledged, committed transaction replaces the product
directory. The UI continues to use its existing bounded graceful shutdown.
The helper rechecks the signature, version, archive size/hash and ZIP paths.
Both the installed directory and new ZIP must have a regular `.resona-package`
marker. The new ZIP also requires `resona-package.json` with matching product,
platform and version. The Windows packager compares Desktop and Core versions
before producing this metadata; the manifest signer validates it before signing.
Desktop, Core, native DLLs, runtime DLLs and licenses switch as one directory.
User configuration, identities and credentials are outside this directory.

Replacement uses two same-volume directory renames. Ordinary replacement failure
restores the complete old directory. Failure to create the new desktop process
also restores and launches the old package. The old directory remains beside the
installation as `<install>.previous`; inspect the new version and move that backup
before another update. This first implementation does not automatically confirm
new-version readiness or remove the backup. New-process creation is not proof of
successful voice operation. Runtime crashes and power loss between renames are
not covered by automatic rollback; the retained previous directory allows manual
recovery. The updater never elevates privileges or kills other application instances.

macOS supports checking, verification, download and revealing the staged package.
Installation remains manual while Developer ID/notarization is unfinished. The
download is temporary and is cleaned on ordinary exit: copy the verified package
out of the shown cache directory before quitting for a manual installation.
An Ed25519 update signature does not replace Apple/Windows platform signing.

Windows process waiting, filesystem/antivirus behavior and the complete application
restart still require native CI and real-machine acceptance. Until the release
address and trust key are configured, the About page shows updates as unavailable.

## Trust and build configuration

Build-time `RESONA_UPDATE_URL` is a public HTTPS URL for `manifest.json`.
`RESONA_UPDATE_PUBLIC_KEY` is a base64 Ed25519 public key. Both are public values,
passed to Core with `-ldflags`; Windows/macOS Actions read repository Variables
with those names. A missing address/key disables checking. A dev version cannot
update, and Desktop/Core versions must match before installation.

The source repository can host releases if it is public. Private releases require
authentication, so preserving private source requires a public release repository
or public download service. No GitHub token is shipped in the client. This choice
does not change the update API or installer.

The signed payload declares `schema: 1`, `product: "resona"`, `channel: "stable"`,
the immutable `vMAJOR.MINOR.PATCH`, `expires` (RFC3339), notes, and an `assets` map
keyed by `windows/amd64`, `darwin/arm64`, `darwin/amd64`. Each asset has a public
HTTPS `url`, SHA-256 and exact byte size. Signature verification precedes schema
and package validation; only a strictly newer stable version is accepted.
Expiry limits stale-feed acceptance, but this is not a full TUF implementation.
Download URLs must be HTTPS, including redirects, and may not contain credentials.
Only an authorized signer can supply an asset URL.

## Producing a manifest

Keep the update Ed25519 signing key separate from server/client Noise identities.
Use an offline key or a restricted publishing Secret, never a repository file.
`RESONA_UPDATE_SIGNING_KEY` contains the base64 64-byte Ed25519 private key.
The signing utility never prints it. Public verification keys may be committed.
Do not move published tags or replace an existing version's application packages.

Prepare an asset-input JSON file (local package paths are not published):

```json
[
  {
    "platform": "windows/amd64",
    "path": "desktop/dist/Resona-win-x64.zip",
    "url": "https://github.com/OWNER/PUBLIC-REPO/releases/download/v0.2.0/Resona-win-x64.zip"
  }
]
```

Run in the restricted publishing environment with the signing Secret set:

```sh
go run ./tools/release-manifest -version v0.2.0 -assets build/release-assets.json \
  -expires 2027-10-14T00:00:00Z -out build/manifest.json
```

The tool validates Windows package identity/version, computes package sizes/hashes,
signs compact payload bytes and verifies
its own output. Publish the versioned package and `manifest.json` together, using
a latest-release download URL as the feed. Define an expiry long enough for the
support window and publish new metadata before it expires. Do not put the signing
key in Actions logs, source, artifacts or releases. Public release publication and
key provisioning are still pending; no remote repository was created by this change.
