# Quality smoke fixture attribution

Speech: SRI International / Lab41, **Voices Obscured in Complex Environmental
Settings (VOiCES)**, source file
`Lab41-SRI-VOiCES-src-sp0307-ch127535-sg0042.wav` (LibriSpeech-derived human
English reading). Dataset: <https://iqtlabs.github.io/voices/>;
license: [Creative Commons Attribution 4.0](https://creativecommons.org/licenses/by/4.0/).
Source description and license: <https://iqtlabs.github.io/voices/Lab41-SRI-VOiCES_README/>.

Small-file mirror used by the official PyTorch audio I/O tutorial:
<https://download.pytorch.org/torchaudio/tutorial-assets/Lab41-SRI-VOiCES-src-sp0307-ch127535-sg0042.wav>.
Original WAV SHA-256:
`c65fcd726d6b08c82c1e5dc7558f863cd8d483e3ed2f4a7bcf271dc1865ada14`.

Changes: explicitly resampled from 16kHz mono to 48kHz mono float32, placed twice
in a ten-second buffer with digital silence between utterances. Resampling does
not make this full-band speech. Pink noise is synthetic, made by FFmpeg
`anoisesrc` with a fixed seed. Mixing/SNR and any shared peak attenuation are
recorded by the evaluation tool. Processed derivatives must retain this attribution.

This short, single-speaker English fixture checks the evaluation pipeline and
offers preliminary candidate screening. It cannot establish Mandarin quality,
keyboard/fan/room/game-noise performance, shouting/whispers, model generalization,
full-band behavior, or a product default. Do not describe it as a real gaming
recording or claim it was unseen during model training. Add consented, locally
held recordings using the same manifest interface before product selection.

## Recorded-noise round 2

`quality-round2.py` prepares THCHS-30 Mandarin readings and MS-SNSD recorded
noises. Its `ATTRIBUTION` constant lists authors, licenses, source URLs, selected
members and transformations; `prepare` copies that text into `ATTRIBUTION.md`,
and listening exports retain it in `SOURCES.md`. Source hashes are pinned in the
script and the generated `sources.json`. Keep attribution with shared audio.

THCHS-30: <https://www.openslr.org/18/> (Apache License 2.0 as declared there).
MS-SNSD: <https://github.com/microsoft/MS-SNSD#dataset-licenses>, pinned revision
`fe61c4ba0d9ac8dd7e23d719cc79f8947e1dc742`; its README identifies the selected
Freesound noise collection as CC0. No MS-SNSD speech is used. The small verified
THCHS archive prefix contains the four complete selected speech WAVs; extraction
stops after those members, without extracting arbitrary paths or links.

The four speech clips are C31_736, B8_404, A23_150 and C6_639. C21_517 was rejected
after a source check found strong infrasonic energy (40Hz high-pass changed
five-second RMS by 4.10dB, versus 0.11dB for C31_736). Its removal avoids treating
low-frequency filtering as severe speech suppression in waveform metrics and
gain matching. The selected references are not high-pass filtered. They may
still contain original recording noise; these are not studio stems, full-band
gaming recordings, or evidence of training-data exclusion.
