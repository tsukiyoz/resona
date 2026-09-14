# Native server (experimental)

## Build version

```sh
resona-server --version
resona-core --version
docker exec resona-server /resona-server --version
```

The current binary names are `resona-server` and `resona-core`, not `resona`.
Version queries exit before initializing configuration, keys or network services.
Normal server startup also logs its version. Output contains version, full Git
commit, dirty state, UTC build time, Go runtime and target OS/architecture.
`dirty=true` means the binary includes uncommitted source; it is not a release tag.

`internal/version` reads embedded Go VCS metadata for ordinary builds. Version
defaults to `dev`; unavailable commit, dirty state or build time says `unknown`.
A commit timestamp is deliberately not presented as the build time.
Build overrides use `-ldflags -X` with `internal/version.Version`, `.Commit`,
`.BuildTime`, `.Dirty` under the full module path
`github.com/tsukiyoz/resona/internal/version`.

`make core VERSION=vX.Y.Z` sets the version and build time. Windows builds accept
`desktop/scripts/build-windows.ps1 -Version vX.Y.Z`. Docker builds accept build
args `VERSION`, `COMMIT`, `BUILD_TIME`, `DIRTY`; pass them explicitly because the
Docker context excludes Git metadata. These inputs label a build and do not
create commits or tags. Desktop's Rust executable retains its existing GUI
version; this slice adds queries to the Go server/core binaries.

The Go server relays compressed Opus. It does not decode, mix, encode, or depend
on devices, GPUI or TS3. Noise UDP is the default native experiment; QUIC remains
an explicit baseline. Deploy matching client/server builds. This is not yet a
production internet service.

When distributing server binaries, include [noise-license.txt](noise-license.txt)
alongside the applicable existing dependency notices. Desktop packaging includes
this newly introduced dependency's notice automatically.

## Build and run

Current protocol: Noise exp-4 / QUIC ALPN exp-3, mandatory native client identity.
The previously deployed exp-3 Noise server must be upgraded together with core.

### First owner

Provision offline, before starting the server (native binary):

```sh
./build/bin/resona-server --init-owner --access-dir /path/to/resona-access
./build/bin/resona-server --access-dir /path/to/resona-access
```

The first command prints a one-time 64-hex claim code, valid for 24 hours. Store
it privately; normal server startup does not print it. Only its digest is saved.
Existing provisioning and owner files are never overwritten. An unprovisioned
server permits member connections but has no claim action. After provisioning,
connect with the matching client, open the identity/permissions button in the
channel header, and submit the code. The same native client identity remains owner
after reconnect/server restart. Member IDs and nicknames do not confer ownership.

With Docker Compose, stop the server before provisioning and use the same data
volume and default access directory as normal startup:

```sh
docker compose stop server
docker compose run --rm --no-deps --entrypoint /usr/local/bin/resona-server server --init-owner
docker compose up -d server
```

The access directory must be writable by the server user, unlike a read-only
identity-key mount. Back it up along with the client native identity file. Do not
delete `owner.json` to routinely reset credentials. Lost-identity recovery and
ownership transfer are not yet implemented. To replace an expired or unused code,
stop the server and run:

```sh
resona-server --reset-owner-claim --access-dir /path/to/resona-access
```

This atomically replaces the unused code and prints a fresh code valid for 24
hours. It refuses an existing or corrupt owner record. No startup auto-reset and
no requirement to claim within 24 hours of server creation. Startup, init and
reset use the same OS process lock; do not remove `access.lock`. Old server builds
without this lock must also be stopped before renewal.

For the manually deployed Docker instance, use the installed image and the same
access mount. Run this on the remote host (replace IMAGE with the upgraded image):

```sh
docker stop resona-server
docker run --rm --log-driver none --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -v /opt/resona/access:/access IMAGE --reset-owner-claim --access-dir /access
docker start resona-server
```

## Channel management

An authenticated owner uses the desktop channel header Plus to create and the
channel context menu to edit/delete. The server enforces authorization. Ordinary
members cannot mutate. Default channel may be renamed but never deleted; occupied
channels cannot be deleted. Creation leaves users in their existing channels.

Channels are persisted in `access-dir/channels.json`. `--channels` seeds only the
first startup; once persisted, the file is authoritative. Back up the complete
access directory. At most 64 channels, names 1-100 Unicode code points without
control characters, descriptions at most 1024 UTF-8 bytes; combined text budget
16 KiB. IDs never reuse deleted values, including after restart. Nested channels,
passwords, admin delegation and recovery remain future work. See ADR-0023.

Owner/member recognition, claim and channel CRUD are implemented. Role delegation
remains subsequent work. Matching server management capability is required; an
older server leaves the native channel context menu disabled with an authorization
notice. The title-bar create shortcut is only shown when authorized.

### Docker (local Noise testing)

From the repository root with Docker Engine/Desktop or OrbStack and Compose:

```sh
docker compose up -d --build
docker compose logs server
```

Connect the desktop to `127.0.0.1:9988`, select **Resona (Noise UDP)** and enter
the public key printed in the logs. No password is required by default. Set
`RESONA_SERVER_PASSWORD` in the host environment before starting Compose to
require one; it is never baked into the image. Docker administrators can inspect
container environment variables.

The first start creates `/data/noise.key` in the named `server-data` volume.
Ordinary restart, rebuild, force-recreate and `docker compose down` preserve it.
Do not use `docker compose down -v` unless intentionally deleting the identity;
new keys require updating client bookmarks. Back up the volume privately.
Existing corrupt keys cause startup failure instead of silent replacement.

```sh
docker compose stop
docker compose start
docker compose down
```

Only loopback UDP is published by default. For another local port, set
`RESONA_PORT=19988`. To deliberately allow LAN clients, set
`RESONA_BIND_IP=0.0.0.0` before starting and allow UDP through the host firewall.
For custom static channels, mount a JSON file read-only and override the Compose
service command with `--listen 0.0.0.0:9988 --channels /path/channels.json`.
The container is non-root with a read-only root filesystem; only the data volume
is writable. This packages the experimental Noise server, not a production claim.

### Native binary

From the repository root, using the Go version in go.mod:

```sh
CGO_ENABLED=0 go build -o build/bin/resona-server ./cmd/resona-server
./build/bin/resona-server --init-key
./build/bin/resona-server
```

Windows PowerShell:

```powershell
$env:CGO_ENABLED = '0'
go build -o build/bin/resona-server.exe ./cmd/resona-server
./build/bin/resona-server.exe --init-key
./build/bin/resona-server.exe
```

Noise identity defaults to `os.UserConfigDir()/resona-server/noise.key` (32-byte
private key), or `--noise-key /path/noise.key`. `--init-key` never overwrites it;
startup prints only its public key. Keep the private file private and stable.
Create a bookmark with **Resona (Noise UDP)** and the printed 64-hex X25519 public
key in the required server public key field. Empty/unknown keys are rejected.
Key replacement requires manually updating the bookmark and clears old credentials.

For the QUIC baseline, initialize with `--init-cert` and run with
`--transport quic`. Certificates default to `os.UserConfigDir()/resona-server/cert.pem` and `key.pem`
(macOS: `~/Library/Application Support/resona-server/`; Windows: under
`%AppData%`). Initialization never overwrites existing files. The self-signed
certificate lasts one year. Keep the private key private and the certificate
stable across restarts; replacement requires updating client pins. Custom paths
use `--cert /path/cert.pem --key /path/key.pem` for both initialization and serving.
A trusted-issuer certificate can be supplied instead.

The default listener is `127.0.0.1:9988` over UDP. For another computer, explicitly
use `--listen 0.0.0.0:9988` and permit the UDP port through the firewall. TCP-only
forwarding will not work. `RESONA_SERVER_PASSWORD` in the server environment sets
an optional shared password; unset/empty permits guests. Passwords are not CLI
arguments and are not printed. Ctrl+C stops the listener and connected clients.

## Desktop

For the QUIC baseline, create a bookmark, select Resona (QUIC/TLS), and enter address and nickname.
For a self-signed certificate, enter the SHA-256 fingerprint printed by the
server; obtain it from the operator through a trusted channel. The hash covers
the leaf certificate DER bytes. An empty fingerprint uses system CA and hostname
verification, not disabled verification. Connect using the normal bookmark and
password flow. Old bookmarks default to TS3; there is no automatic fallback.
Changing protocol, address or pin clears previous saved credentials; failure to
clear them prevents changing the destination.

Default channels are Lobby (1) and Gaming (2). Supply `--channels channels.json`
to load a static array:

```json
[
  {"ID": 1, "Name": "Lobby", "Description": "Arrival channel"},
  {"ID": 2, "Name": "Gaming", "Description": "Game voice chat"}
]
```

Limits: 1-64 channels; unique nonzero uint16 IDs; names of at most 100 Unicode
code points; descriptions of at most 1024 UTF-8 bytes each; combined channel
names/descriptions at most 16 KiB. `--max-clients` is 1-64 (default 64), including
accepted connections still authenticating. First channel is the arrival channel.
Guests may move to any configured channel.

## Scope

Implemented: guest nicknames, optional shared password, membership snapshots,
static channels/descriptions, movement, channel text and independent speaker
Opus relay. Microphone starts muted. Existing client controls own devices, DSP,
codec, mixing, and per-user local playback volume.

Pending: persistent accounts, roles/ACLs, channel passwords, dynamic channel
editing, private messages, persistent history, automatic reconnect, native member
notification sounds, and server-side media processing. Guest IDs are not accounts;
IDs are not reused within a server lifetime, requiring restart after 65535
admissions. Loopback tests do not establish Windows device quality, WAN loss
tolerance, scalability or DDoS resistance.

See [wire specification](native-protocol.md) and [ADR-0018](adr/0018-native-quic.md).

Noise framing and limitations are in [noise-protocol.md](noise-protocol.md) and
[ADR-0019](adr/0019-noise-udp.md). Noise control currently uses stop-and-wait
delivery. It has bounded queues and rate limits, but no adaptive voice congestion
control. Noise exp-4 uses signed-identity Protobuf control messages and updates directional keys in the background at 12 hours or
2^23 packets without resetting membership or audio. Confirmation failure still
closes the session after 30 seconds; it is not automatic reconnect. Upgrade server
and client core together: older Noise experiments cannot handshake, though the existing
server identity file and public-key bookmarks remain valid. See ADR-0020/0021.
QUIC and Noise use the same default port and must
use different listen ports when running together.
