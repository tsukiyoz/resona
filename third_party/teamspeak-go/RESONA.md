# Resona Upstream Snapshot

- Source: https://github.com/HoneyBBQ/teamspeak-go
- Commit: a334def898f4d9c518a1434a9b9e9f889dcae954
- Imported: 2026-09-07, tracked files only, without Git metadata.
- License: MIT; original LICENSE and copyright notices retained.

Local changes: IncomingCommand / CommandObserver in types.go, constructor-only
WithCommandObserver in client.go, synchronous dispatch with one cloned Params
map per observer in commands.go, and observer_test.go. Existing routing remains
unchanged. A second guard in notifications.go prevents nickname-based guesses
from replacing an already-known authenticated client ID; the regression is
covered in observer_test.go. Observers receive already-unescaped parameters and must not
block waiting for network operations.

commands.go also adds ExecCommandContext for cancellable throttling and response
waiting, ExecCommandWithResponseContext for cancellable typed response rows, and
CommandError with a numeric ID and the existing error text/sentinel.
The duration-based command methods share the same implementation and preserve
the command-timeout sentinel; their deadline now includes throttling. Canceling
does not retract an already-sent command. Tracker cleanup removes collected rows,
and resolving a command retires its pending entry so late/duplicate replies cannot
block the packet reader. These cases are covered in command_test.go.
ExecCommand parses top-level parameters before attaching its own return_code;
message text containing the literal spelling cannot suppress tracking. Explicit
caller return_code parameters are rejected because the tracker owns response IDs.

helpers.go preserves shared movement context when splitting batched
notifycliententerview, notifyclientleftview, and notifyclientmoved commands.
Only cfid, ctid, reasonid, reasonmsg, invokerid, invokername, invokeruid, and
bantime inherit from the first row; explicit row values take precedence.
Member identities and attributes remain local to each row. The existing command
parser and escaping API preserve escaped values. Initial subscription snapshots,
batch movement, overrides, and unrelated commands are covered in
member_rows_test.go. An upstream TS3AudioBot connection trace demonstrates the
shared first-row context: https://github.com/Splamy/TS3AudioBot/issues/283.

transfer.go adds FileTransferInitDownloadContext for cancellable command and
file-transfer notification waits. The existing download initializer delegates
with a ten-second deadline. The transfer tracker retires the first notification
before delivery so duplicate notifications cannot block the packet reader.
Covered in transfer_test.go; used for bounded, read-only channel icon downloads.

The voice patch adds constructor-only VoiceObserver registration and independently
owned VoicePacket payloads. Incoming server voice parses the 16-bit voice sequence,
16-bit sender client ID, codec, encryption flag, and bounded payload before returning
promptly to the packet loop. Malformed and oversized payloads are dropped. Resona's
audio decode, jitter, mixing, and devices remain outside this module.
The five-byte server voice header with no following audio is preserved as an explicit
end-of-stream packet. A six-byte packet has one byte of Opus data and remains an audio
packet because one-byte Opus packets can be valid; Opus framing validation belongs to
the audio engine. This follows the TS3 protocol's empty-audio end marker and avoids
feeding an empty packet into the Opus decoder.

SendVoice and SendVoiceEncrypted accept only TS3 Opus Voice (4) and Opus Music (5)
payloads within the configured bound. The transport emits the proper encrypted or
unencrypted packet flag, applies the TS3 crypto path for encrypted voice, and tracks
separate receive generations for voice and whisper sequences, including uint16 wrap.
Initial client mute state is explicit through WithInitialMute so connecting never
briefly advertises a live microphone. The unit suite covers parsing and observer
ownership, encrypted wire decrypt, generation wrap, packet validation, and initial
mute. Resona additionally verifies codec 4 mono and codec 5 stereo encode/decode,
actual server-forwarded bidirectional packets, and device lifecycle separately.

Resona uses these interfaces for login snapshots, ordered membership updates,
channel text messages, custom channel icons, and bounded channel voice. It does not
claim whisper, legacy codec, or long-running compatibility.
See docs/adr/0005-read-only-ts3-session.md in the Resona root.

Run tests here separately: `go test -race ./...`. The parent module's tests do
not traverse this nested upstream module. Preserve this patch/provenance when
upgrading; remove the patch if upstream provides an equivalent interface.
