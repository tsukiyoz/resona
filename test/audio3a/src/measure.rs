use crate::{
    fixture::Fixture,
    processor::{Chain, Choice, Frame},
};
use cpu_time::ProcessTime;
use serde::Serialize;
use std::time::Instant;

#[derive(Serialize)]
pub struct ResultRow {
    pub case: String,
    pub scene: String,
    pub repeat: usize,
    pub frames: usize,
    pub mean_us: f64,
    pub cpu_percent_one_core: f64,
    pub p50_us: f64,
    pub p99_us: f64,
    pub p999_us: f64,
    pub max_us: f64,
    pub input_rms_dbfs: f64,
    pub output_rms_dbfs: f64,
    pub attenuation_db: f64,
    pub output_peak: f64,
    pub clipped_samples: usize,
    pub level_thirds_dbfs: [f64; 3],
    pub checksum: f64,
}

fn energy(x: &[Frame]) -> f64 {
    x.iter().flatten().map(|v| (*v as f64).powi(2)).sum::<f64>() / (x.len() * 480) as f64
}
pub fn db(energy: f64) -> f64 {
    10.0 * energy.max(1e-12).log10()
}
pub fn quantile(x: &[f64], q: f64) -> f64 {
    x[((x.len() as f64 * q).ceil() as usize)
        .saturating_sub(1)
        .min(x.len() - 1)]
}

pub fn run(
    choice: &Choice,
    fixture: &Fixture,
    warm: usize,
    repeat: usize,
) -> Result<(ResultRow, Vec<Frame>), String> {
    let mut chain = Chain::new(choice)?;
    for i in 0..warm {
        let mut frame = fixture.capture[i];
        chain.process(&mut frame, &fixture.render[i])?;
    }
    let n = fixture.capture.len() - warm;
    let mut output = vec![[0.0; 480]; n];
    let mut times = vec![0.0; n];
    let cpu = ProcessTime::now();
    let wall = Instant::now();
    for (i, frame) in output.iter_mut().enumerate() {
        let start = Instant::now();
        *frame = fixture.capture[i + warm];
        chain.process(frame, &fixture.render[i + warm])?;
        times[i] = start.elapsed().as_secs_f64() * 1e6;
    }
    let elapsed = wall.elapsed().as_secs_f64();
    let cpu_seconds = cpu.elapsed().as_secs_f64();
    // Correctness/quality diagnostics and disk I/O are outside the measured loop.
    if output
        .iter()
        .flatten()
        .any(|v| !v.is_finite() || v.abs() > 8.0)
    {
        return Err(format!(
            "{} {} produced nonfinite/excessive samples",
            choice.name(),
            fixture.name
        ));
    }
    times.sort_by(f64::total_cmp);
    let input_db = db(energy(&fixture.capture[warm..]));
    let output_db = db(energy(&output));
    let row = ResultRow {
        case: choice.name(),
        scene: fixture.name.into(),
        repeat,
        frames: n,
        mean_us: elapsed * 1e6 / n as f64,
        cpu_percent_one_core: cpu_seconds / (n as f64 * 0.01) * 100.0,
        p50_us: quantile(&times, 0.5),
        p99_us: quantile(&times, 0.99),
        p999_us: quantile(&times, 0.999),
        max_us: times[n - 1],
        input_rms_dbfs: input_db,
        output_rms_dbfs: output_db,
        attenuation_db: input_db - output_db,
        output_peak: output
            .iter()
            .flatten()
            .fold(0.0_f64, |a, v| a.max(v.abs() as f64)),
        clipped_samples: output.iter().flatten().filter(|v| v.abs() >= 1.0).count(),
        level_thirds_dbfs: std::array::from_fn(|i| db(energy(&output[i * n / 3..(i + 1) * n / 3]))),
        checksum: output.iter().flatten().map(|v| *v as f64).sum(),
    };
    Ok((row, output))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn metrics_have_known_scale() {
        assert!((db(0.01) + 20.0).abs() < 1e-9);
        assert_eq!(quantile(&[1.0, 2.0, 3.0, 4.0], 0.99), 4.0);
        let f = crate::fixture::make(20, 5, None, None);
        let (r, _) = run(&Choice::new("off", "off", "off"), &f[2], 5, 0).unwrap();
        assert!(r.attenuation_db.abs() < 1e-9);
        assert_eq!(r.clipped_samples, 0);
    }
}
