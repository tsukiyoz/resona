# Local listening comparison

Loopback-only Noise UDP server with six real voice clients. Channels compare
the same synthesized speech and seeded pink noise: raw, SpeexDSP ANS level 2,
and WebRTC NS level 2. All use 32 kbps mono Opus with complexity 5. Processing
and encoding happen before playback; a single 20 ms timer sends synchronized
packets, avoiding repeated DSP work while gaming. A loop boundary may contain a
brief codec discontinuity. No microphone or real server is used.

On macOS, generate the fixture (requires `ffmpeg` and the Tingting system voice):

```sh
mkdir -p build/listening
say -v Tingting -r 190 -f tools/listening-bot/speech.txt -o build/listening/speech.aiff
ffmpeg -y -i build/listening/speech.aiff -f lavfi -i 'anoisesrc=color=pink:amplitude=0.045:sample_rate=48000:seed=42' -filter_complex '[0:a]aresample=48000,aformat=channel_layouts=mono,volume=0.6[s];[s][1:a]amix=inputs=2:duration=first:normalize=0,alimiter=limit=0.9:level=false[a]' -map '[a]' -ar 48000 -ac 1 -f f32le build/listening/noisy.f32
make build-apm
say -v Tingting -r 240 -o build/listening/agc.aiff '这是一段用于比较收听音量的语音，请注意每次播放的响度变化，其他内容保持完全相同。'
ffmpeg -y -i build/listening/agc.aiff -ar 48000 -ac 1 -f f32le build/listening/agc.f32
go build -o build/listening/listening-bot ./tools/listening-bot
RESONA_WEBRTC_PLUGIN_DIR="$PWD/build/bin" build/listening/listening-bot
```

Add the printed address and public key in Resona, with no server password.
Join each comparison channel. Start with receive AGC off, keep global and member
volume fixed, then compare SpeexDSP and WebRTC receive AGC. Changing your own
AEC/ANS does not change the bot's audio: use the channels for that comparison.
Keep your microphone muted. Stop with Ctrl-C; default automatic stop is two hours.
Each invocation gets a new ephemeral server identity and port.

Channel 04 contains three independent members using the exact same clean phrase.
The shared input is normalized to whole-clip RMS -34/-26/-18 dBFS (including pauses),
then Opus-encoded without sender DSP. Each speaks for at most ten seconds with
a 0.5-second gap; inactive bots send no frames. Reset each member's gain to 0 dB
and hold global volume constant. Compare receive AGC off/SpeexDSP/WebRTC after
confirming each change and listening for two complete rounds. This isolates gain
convergence from noise suppression; it is not LUFS normalization or equal perceived
loudness. Supply an alternative clean clip through `-agc-pcm` when system voices
are unavailable; all three members always reuse that clip.

The source is synthetic speech plus stationary noise, not a representative
human-room recording or AEC test. The fixture cannot establish real-world
keyboard-noise or reverberation quality. Replace `-pcm` with 48 kHz mono f32le
audio (20 ms to 120 seconds) for other locally authorized recordings.

Fixtures and binaries in `build/listening` are ignored by Git. Integration test:

```sh
RESONA_WEBRTC_PLUGIN_DIR="$PWD/build/bin" go test -race ./tools/listening-bot -v
```
