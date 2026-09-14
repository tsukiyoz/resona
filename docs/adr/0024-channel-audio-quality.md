# ADR-0024: Channel Audio Quality

Date: 2026-09-14. Status: Accepted (experimental).

## Decision

Native channels carry an authoritative target Opus bitrate. Presets are 20 kbps
(economy), 32 kbps (game voice), 48 kbps (high quality), or custom integer 16..64
kbps. These are initial product choices, not listening-test results. Opus VoIP,
mono, 48 kHz and 20 ms remain fixed. Music, stereo and codec complexity controls
are outside this slice. The server routes encoded audio without transcoding.

## Compatibility and Persistence

Add protobuf CreateChannel.bitrate field 3, UpdateChannel.bitrate field 4,
Channel.bitrate field 4 and State.can_configure_channel_audio field 10. Units are
bits/second. Positive values must be 16000..64000 and multiples of 1000. No voice
packet header or transport version changes. Zero means unspecified: create uses
32000, update preserves the old value, stored/state zero means legacy 48000.
Existing stores and startup channel seeds retain their old 48 kbps behavior.

Owner-only CRUD persists name, description and bitrate together and broadcasts
one authoritative snapshot. Invalid values change none of these fields. Clients
hide editable audio controls unless the separate capability is present; legacy
name/description CRUD remains usable. Old clients ignore new state fields and
continue encoding their legacy bitrate; the server cannot force them to adopt
the new encoder policy. This is a target setting, not a server-enforced bandwidth
limit or congestion control. Displayed kbps excludes encryption/network overhead
and VBR packet sizes vary.

## Encoder Ownership

The native adapter atomically publishes the current self channel's bitrate after
validating each ordered control snapshot. The audio engine optionally consumes a
ChannelBitrateSource, using atomic reads rather than a new control-plane mutex
per frame. Only the encoding goroutine calls SetBitrate after startup, immediately
before encoding an outgoing frame. Invalid source values fail closed. No device
reopen, mute command, activation change or queue rebuild occurs for quality-only
edits. Existing channel-change cleanup remains intact. Legacy TS3 voice/music
defaults stay 48/96 kbps; local monitoring bypasses network encoding.

The channel list and detail pane display confirmed server values. Form selection
is only a draft until Save; session and modal revision guards reject stale UI
actions. Tests cover persistence, legacy edits, both native transports, encoder
hot updates, PTT continuity, muted monitor privacy and input boundaries. Windows
device listening tests remain a separate acceptance step.
