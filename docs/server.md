# Native server (experimental)

The Go server relays compressed Opus. It does not decode, mix, encode, or depend
on devices, GPUI or TS3. Noise UDP is the default native experiment; QUIC remains
an explicit baseline. Deploy matching client/server builds. This is not yet a
production internet service.

When distributing server binaries, include [noise-license.txt](noise-license.txt)
alongside the applicable existing dependency notices. Desktop packaging includes
this newly introduced dependency's notice automatically.

## Build and run

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
control. Noise exp-3 uses Protobuf control messages and updates directional keys in the background at 12 hours or
2^23 packets without resetting membership or audio. Confirmation failure still
closes the session after 30 seconds; it is not automatic reconnect. Upgrade server
and client core together: older Noise experiments cannot handshake, though the existing
server identity file and public-key bookmarks remain valid. See ADR-0020/0021.
QUIC and Noise use the same default port and must
use different listen ports when running together.
