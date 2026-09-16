use crate::processor::{Frame, N};
use sha2::{Digest, Sha256};
use std::{f32::consts::TAU, path::Path};

pub struct Fixture {
    pub name: &'static str,
    pub capture: Vec<Frame>,
    pub render: Vec<Frame>,
    pub clean: Vec<Frame>,
    pub sha256: String,
}

pub fn read_wav(path: &Path, samples: usize) -> Result<Vec<f32>, String> {
    let mut r = hound::WavReader::open(path).map_err(|e| e.to_string())?;
    let spec = r.spec();
    if spec.channels != 1 || spec.sample_rate != 48000 {
        return Err("WAV must be 48 kHz mono; convert before benchmarking".into());
    }
    if (r.len() as usize) < samples {
        return Err("WAV shorter than warmup + measured duration".into());
    }
    let values: Vec<f32> = match (spec.sample_format, spec.bits_per_sample) {
        (hound::SampleFormat::Float, 32) => r
            .samples::<f32>()
            .take(samples)
            .collect::<Result<_, _>>()
            .map_err(|e| e.to_string())?,
        (hound::SampleFormat::Int, 16) => r
            .samples::<i16>()
            .take(samples)
            .map(|v| v.map(|x| x as f32 / 32768.0))
            .collect::<Result<_, _>>()
            .map_err(|e| e.to_string())?,
        _ => return Err("WAV must use PCM16 or float32".into()),
    };
    if values.iter().any(|v| !v.is_finite() || v.abs() > 1.0) {
        return Err("invalid/non-normalized WAV samples".into());
    }
    Ok(values)
}

pub fn write_wav(path: &Path, frames: &[Frame]) -> Result<(), String> {
    let spec = hound::WavSpec {
        channels: 1,
        sample_rate: 48000,
        bits_per_sample: 32,
        sample_format: hound::SampleFormat::Float,
    };
    let mut w = hound::WavWriter::create(path, spec).map_err(|e| e.to_string())?;
    for v in frames.iter().flatten() {
        w.write_sample(*v).map_err(|e| e.to_string())?;
    }
    w.finalize().map_err(|e| e.to_string())
}

fn signal(i: usize, frequency: f32) -> f32 {
    let t = i as f32 / 48000.0;
    let envelope = (0.5 + 0.5 * (TAU * 2.7 * t).sin()).powi(2);
    envelope
        * (0.13 * (TAU * frequency * t).sin()
            + 0.06 * (TAU * frequency * 2.03 * t).sin()
            + 0.03 * (TAU * frequency * 4.1 * t).sin())
}

pub fn make(ticks: usize, warm: usize, near: Option<&[f32]>, far: Option<&[f32]>) -> Vec<Fixture> {
    [
        "silence",
        "echo-only",
        "noise-only",
        "speech-noise",
        "double-talk",
        "level-steps",
        "echo-path-change",
        "speech-only",
    ]
    .into_iter()
    .map(|name| {
        let mut capture = vec![[0.0; N]; ticks];
        let mut render = vec![[0.0; N]; ticks];
        let mut clean = vec![[0.0; N]; ticks];
        let mut rng = 0x85a9_5bcdu32;
        for i in 0..ticks * N {
            let tick = i / N;
            let position = i % N;
            let near_sample = near.map_or_else(|| signal(i, 147.0), |x| x[i]);
            let far_sample = |j| far.map_or_else(|| signal(j, 211.0), |x| x[j]);
            rng = rng.wrapping_mul(1664525).wrapping_add(1013904223);
            let white = (rng as f64 / u32::MAX as f64 * 2.0 - 1.0) as f32;
            let transient = if i % 24000 < 160 { white * 0.1 } else { 0.0 };
            let noise = 0.018 * white + 0.01 * (TAU * 60.0 * i as f32 / 48000.0).sin() + transient;
            let echo_active = matches!(name, "echo-only" | "double-talk" | "echo-path-change");
            let delay = if name == "echo-path-change" && tick >= warm + (ticks - warm) / 2 {
                3840
            } else {
                1920
            };
            let echo = if echo_active && i >= delay + 600 {
                0.55 * far_sample(i - delay) + 0.18 * far_sample(i - delay - 600)
            } else {
                0.0
            };
            let gain = if name == "level-steps" {
                let progress = tick.saturating_sub(warm) * 3 / (ticks - warm);
                [0.08, 0.6, 2.0][progress.min(2)]
            } else {
                1.0
            };
            let s = if matches!(
                name,
                "speech-noise" | "double-talk" | "level-steps" | "speech-only"
            ) {
                near_sample * gain
            } else {
                0.0
            };
            let n = if matches!(name, "noise-only" | "speech-noise" | "double-talk") {
                noise
            } else {
                0.0
            };
            capture[tick][position] = (s + echo + n).clamp(-1.0, 1.0);
            clean[tick][position] = s;
            if echo_active {
                render[tick][position] = far_sample(i);
            }
        }
        let mut hash = Sha256::new();
        for frames in [&capture, &render, &clean] {
            for v in frames.iter().flatten() {
                hash.update(v.to_le_bytes());
            }
        }
        Fixture {
            name,
            capture,
            render,
            clean,
            sha256: format!("{:x}", hash.finalize()),
        }
    })
    .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn fixtures_are_repeatable_and_echo_has_no_near_speech() {
        let a = make(20, 5, None, None);
        let b = make(20, 5, None, None);
        assert_eq!(a[1].sha256, b[1].sha256);
        assert!(a[1].clean.iter().flatten().all(|v| *v == 0.0));
        assert!(a[1].capture.iter().flatten().any(|v| v.abs() > 0.01));
        assert!(a[0].capture.iter().flatten().all(|v| *v == 0.0));
    }
}
