use crate::{
    fixture, measure,
    processor::{Choice, Frame, N},
};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    fmt::Write as _,
    fs,
    path::{Path, PathBuf},
};

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Manifest {
    version: u32,
    seed: u32,
    warmup_ms: usize,
    duration_ms: usize,
    repeats: usize,
    scenes: Vec<Scene>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Scene {
    name: String,
    clean: PathBuf,
    noise: PathBuf,
    provenance: String,
    snr_db: f64,
    // Times refer to the measured portion, after warmup. Outside is known silence.
    speech_ms: Vec<[usize; 2]>,
}

#[derive(Serialize)]
struct Quality {
    aligned_si_sdr_db: Option<f64>,
    si_sdr_improvement_db: Option<f64>,
    speech_projection_gain_db: Option<f64>,
    silence_output_dbfs: Option<f64>,
    silence_reduction_db: Option<f64>,
    alignment_samples: usize,
}

#[derive(Serialize)]
struct Row {
    scene: String,
    case: String,
    model: &'static str,
    quality: Quality,
    clean_only_quality: Quality,
    performance: Vec<measure::ResultRow>,
    listening_file: String,
    listening_gain_db: f64,
}

fn power(x: &[f32]) -> f64 {
    x.iter().map(|x| (*x as f64).powi(2)).sum::<f64>() / x.len().max(1) as f64
}

fn hash(x: &[f32]) -> String {
    let mut h = Sha256::new();
    for v in x {
        h.update(v.to_le_bytes());
    }
    format!("{:x}", h.finalize())
}

fn mask(scene: &Scene, duration_ms: usize) -> Result<Vec<bool>, String> {
    let mut active = vec![false; duration_ms * 48];
    let mut end = 0;
    for &[a, b] in &scene.speech_ms {
        if a < end || a >= b || b > duration_ms {
            return Err(format!(
                "{}: speech_ms must be ordered, nonoverlapping ranges within duration",
                scene.name
            ));
        }
        active[a * 48..b * 48].fill(true);
        end = b;
    }
    if active.iter().filter(|x| **x).count() < 4800 {
        return Err(format!(
            "{}: need at least 100ms annotated speech",
            scene.name
        ));
    }
    Ok(active)
}

fn validate(m: &Manifest) -> Result<(), String> {
    if m.version != 1
        || !(100..=10000).contains(&m.warmup_ms)
        || !(1000..=60000).contains(&m.duration_ms)
        || m.warmup_ms % 10 != 0
        || m.duration_ms % 10 != 0
        || !(1..=10).contains(&m.repeats)
        || m.scenes.is_empty()
        || m.scenes.len() > 20
    {
        return Err("manifest v1 requires warmup 100..10000ms, duration 1000..60000ms (10ms multiples), repeats 1..10, scenes 1..20".into());
    }
    let mut names = std::collections::BTreeSet::new();
    for s in &m.scenes {
        if s.name.is_empty()
            || s.name.len() > 64
            || !s
                .name
                .bytes()
                .all(|x| x.is_ascii_alphanumeric() || x == b'-' || x == b'_')
            || !names.insert(&s.name)
            || s.provenance.trim().is_empty()
            || !s.snr_db.is_finite()
            || !(-10.0..=40.0).contains(&s.snr_db)
        {
            return Err("scenes need unique safe names, provenance and SNR -10..40dB".into());
        }
        mask(s, m.duration_ms)?;
    }
    Ok(())
}

fn mix(
    clean: &[f32],
    noise: &[f32],
    active: &[bool],
    warm: usize,
    snr: f64,
) -> Result<(Vec<f32>, Vec<f32>, f64), String> {
    let speech: Vec<_> = clean[warm..]
        .iter()
        .zip(active)
        .filter_map(|(x, a)| a.then_some(*x))
        .collect();
    let background: Vec<_> = noise[warm..]
        .iter()
        .zip(active)
        .filter_map(|(x, a)| a.then_some(*x))
        .collect();
    if power(&speech) < 1e-8 || power(&background) < 1e-10 {
        return Err("speech/noise energy too low for requested SNR".into());
    }
    let noise_gain = (power(&speech) / power(&background) / 10f64.powf(snr / 10.0)).sqrt();
    let mut mixed: Vec<_> = clean
        .iter()
        .zip(noise)
        .map(|(c, n)| *c + (*n as f64 * noise_gain) as f32)
        .collect();
    let peak = mixed
        .iter()
        .chain(clean)
        .fold(0.0f32, |a, x| a.max(x.abs()));
    let gain = (0.9 / peak).min(1.0);
    for x in &mut mixed {
        *x *= gain;
    }
    Ok((clean.iter().map(|x| x * gain).collect(), mixed, gain as f64))
}

// Bounded correlation alignment is only a reference-metric aid, not a latency measurement.
fn alignment(clean: &[f32], output: &[f32], active: &[bool]) -> usize {
    let score = |lag: usize| {
        let (mut xy, mut xx, mut yy) = (0.0, 0.0, 0.0);
        for i in (0..clean.len() - 1440).step_by(47) {
            if active[i] {
                let (x, y) = (clean[i] as f64, output[i + lag] as f64);
                xy += x * y;
                xx += x * x;
                yy += y * y;
            }
        }
        xy / (xx * yy).sqrt().max(1e-20)
    };
    (0..=1440)
        .max_by(|a, b| score(*a).total_cmp(&score(*b)))
        .unwrap()
}

fn sdr(clean: &[f32], output: &[f32], active: &[bool], lag: usize) -> (Option<f64>, Option<f64>) {
    let (mut xx, mut xy, mut yy) = (0.0, 0.0, 0.0);
    for i in 0..clean.len() - lag {
        if active[i] {
            let (x, y) = (clean[i] as f64, output[i + lag] as f64);
            xx += x * x;
            xy += x * y;
            yy += y * y;
        }
    }
    if xx < 1e-12 || yy < 1e-12 || xy <= 0.0 {
        return (None, None);
    }
    let projected = xy * xy / xx;
    (
        Some(measure::db(projected / (yy - projected).max(1e-12))),
        Some(20.0 * (xy / xx).log10()),
    )
}

fn metrics(clean: &[f32], input: &[f32], output: &[f32], active: &[bool]) -> Quality {
    let lag = alignment(clean, output, active);
    let (value, gain) = sdr(clean, output, active, lag);
    let (baseline, _) = sdr(clean, input, active, 0);
    let (mut input_noise, mut output_noise, mut count) = (0.0, 0.0, 0usize);
    // Exclude 50ms at both edges of each speech region (tails and alignment).
    let mut prefix = vec![0usize; active.len() + 1];
    for (i, a) in active.iter().enumerate() {
        prefix[i + 1] = prefix[i] + usize::from(*a);
    }
    for i in 2400..active.len().saturating_sub(2400 + lag) {
        if prefix[i + 2400] == prefix[i - 2400] {
            input_noise += (input[i] as f64).powi(2);
            output_noise += (output[i + lag] as f64).powi(2);
            count += 1;
        }
    }
    let silence = (count > 0).then(|| measure::db(output_noise / count as f64));
    Quality {
        aligned_si_sdr_db: value,
        si_sdr_improvement_db: value.zip(baseline).map(|(a, b)| a - b),
        speech_projection_gain_db: gain,
        silence_output_dbfs: silence,
        silence_reduction_db: silence.map(|v| measure::db(input_noise / count as f64) - v),
        alignment_samples: lag,
    }
}

fn frames(x: &[f32]) -> Vec<Frame> {
    x.chunks_exact(N).map(|v| v.try_into().unwrap()).collect()
}

pub fn execute(args: Vec<String>) -> Result<(), String> {
    execute_mode(args, false)
}

pub fn export_inputs(args: Vec<String>) -> Result<(), String> {
    execute_mode(args, true)
}

fn execute_mode(args: Vec<String>, export_only: bool) -> Result<(), String> {
    if args.len() != 2 {
        return Err("usage: resona-3a-bench quality MANIFEST.json NEW_OUTPUT_DIR".into());
    }
    let path = Path::new(&args[0]);
    let m: Manifest = serde_json::from_slice(&fs::read(path).map_err(|e| e.to_string())?)
        .map_err(|e| e.to_string())?;
    validate(&m)?;
    if export_only && (m.warmup_ms % 20 != 0 || m.duration_ms % 20 != 0) {
        return Err("codec chain requires warmup/duration in 20ms multiples".into());
    }
    let base = path.parent().unwrap_or(Path::new("."));
    let out = Path::new(&args[1]);
    let warm = m.warmup_ms * 48;
    let samples = warm + m.duration_ms * 48;
    let mut fixtures = Vec::new();
    let mut sources = Vec::new();
    // Read/validate every input before creating any output. No implicit looping/resampling.
    for scene in &m.scenes {
        let clean = fixture::read_wav(&base.join(&scene.clean), samples)
            .map_err(|e| format!("{} clean {}: {e}", scene.name, scene.clean.display()))?;
        let noise = fixture::read_wav(&base.join(&scene.noise), samples)
            .map_err(|e| format!("{} noise {}: {e}", scene.name, scene.noise.display()))?;
        let active = mask(scene, m.duration_ms)?;
        let silence: Vec<_> = clean[warm..]
            .iter()
            .zip(&active)
            .filter_map(|(x, a)| (!a).then_some(*x))
            .collect();
        if !silence.is_empty() && measure::db(power(&silence)) > -60.0 {
            return Err(format!(
                "{}: clean reference outside speech_ms is not silence (< -60dBFS required)",
                scene.name
            ));
        }
        let (clean_mix, capture, scale) = mix(&clean, &noise, &active, warm, scene.snr_db)?;
        sources.push(serde_json::json!({"scene":scene.name,"clean_pcm_sha256":hash(&clean),"noise_pcm_sha256":hash(&noise),"capture_pcm_sha256":hash(&capture),"common_mix_gain":scale}));
        fixtures.push((
            fixture::Fixture {
                name: "quality",
                capture: frames(&capture),
                render: vec![[0.0; N]; samples / N],
                clean: frames(&clean_mix),
                sha256: hash(&capture),
            },
            active,
        ));
    }
    if let Some(parent) = out.parent().filter(|p| !p.as_os_str().is_empty()) {
        fs::create_dir_all(parent).map_err(|e| e.to_string())?;
    }
    fs::create_dir(out).map_err(|e| format!("output must be a new directory: {e}"))?;
    if export_only {
        let mut scenes = Vec::new();
        for (scene, (fixture, _)) in m.scenes.iter().zip(&fixtures) {
            let mut names = Vec::new();
            let mut hashes = Vec::new();
            for (kind, frames) in [("clean", &fixture.clean), ("capture", &fixture.capture)] {
                let name = format!("{}-{kind}.f32", scene.name);
                let flat: Vec<_> = frames.iter().flatten().copied().collect();
                let bytes: Vec<_> = flat.iter().flat_map(|x| x.to_le_bytes()).collect();
                fs::write(out.join(&name), bytes).map_err(|e| e.to_string())?;
                names.push(name);
                hashes.push(hash(&flat));
            }
            scenes.push(serde_json::json!({"name":scene.name,"clean":names[0],"capture":names[1],"clean_sha256":hashes[0],"capture_sha256":hashes[1],"speech_ms":scene.speech_ms}));
        }
        let metadata = serde_json::json!({"version":1,"sample_rate":48000,"frame_samples":960,"warmup_ms":m.warmup_ms,"duration_ms":m.duration_ms,"repeats":m.repeats,"sources":sources,"source_manifest":m,"scenes":scenes});
        super::save(
            &out.join("manifest.json"),
            &serde_json::to_string_pretty(&metadata).unwrap(),
        )?;
        let attribution = base.join("ATTRIBUTION.md");
        if attribution.exists() {
            fs::copy(attribution, out.join("SOURCES.md")).map_err(|e| e.to_string())?;
        } else {
            super::save(
                &out.join("SOURCES.md"),
                &serde_json::to_string_pretty(&m).unwrap(),
            )?;
        }
        println!("{}", out.join("manifest.json").display());
        return Ok(());
    }
    fs::create_dir(out.join("raw")).map_err(|e| e.to_string())?;
    fs::create_dir(out.join("listening")).map_err(|e| e.to_string())?;
    let mut provenance = String::from("# Listening input provenance\n\n");
    for scene in &m.scenes {
        writeln!(provenance, "## {}\n\n{}\n", scene.name, scene.provenance).unwrap();
    }
    let attribution = base.join("ATTRIBUTION.md");
    if attribution.exists() {
        provenance.push_str(&fs::read_to_string(attribution).map_err(|e| e.to_string())?);
    }
    super::save(&out.join("listening/SOURCES.md"), &provenance)?;
    let model = if cfg!(feature = "rnnoise-little") {
        "little"
    } else {
        "regular"
    };
    let mut rows = Vec::new();
    let mut score = String::from(
        "scene,sample,speech_clarity_1_to_5,background_quietness_1_to_5,artifacts_1_to_5,notes\n",
    );
    for (scene, (f, active)) in m.scenes.iter().zip(&fixtures) {
        let clean: Vec<_> = f.clean[warm / N..].iter().flatten().copied().collect();
        let input: Vec<_> = f.capture[warm / N..].iter().flatten().copied().collect();
        fixture::write_wav(
            &out.join("raw").join(format!("{}-clean.wav", scene.name)),
            &f.clean[warm / N..],
        )?;
        let mut candidates = Vec::new();
        let mut order: Vec<_> = ["off", "agc2"]
            .into_iter()
            .flat_map(|agc| {
                ["off", "speex", "webrtc", "rnnoise", "nnnoiseless"]
                    .map(|ns| Choice::new("off", ns, agc))
            })
            .collect();
        let mut seed = m.seed;
        for i in (1..order.len()).rev() {
            seed = seed.wrapping_mul(1664525).wrapping_add(1013904223);
            order.swap(i, seed as usize % (i + 1));
        }
        for c in order {
            eprintln!("quality {} {} ({model})", scene.name, c.name());
            let mut perf = Vec::new();
            let mut first = Vec::new();
            for repeat in 1..=m.repeats {
                let (mut row, output) = measure::run(&c, f, warm / N, repeat)?;
                row.scene = scene.name.clone();
                perf.push(row);
                if repeat == 1 {
                    first = output;
                }
            }
            fixture::write_wav(
                &out.join("raw")
                    .join(format!("{}-{}.wav", scene.name, c.name())),
                &first,
            )?;
            let flat: Vec<_> = first.iter().flatten().copied().collect();
            let q = metrics(&clean, &input, &flat, active);
            let clean_fixture = fixture::Fixture {
                name: "clean-only",
                capture: f.clean.clone(),
                render: f.render.clone(),
                clean: Vec::new(),
                sha256: String::new(),
            };
            let (_, clean_output) = measure::run(&c, &clean_fixture, warm / N, 1)?;
            fixture::write_wav(
                &out.join("raw")
                    .join(format!("{}-{}-clean-only.wav", scene.name, c.name())),
                &clean_output,
            )?;
            let clean_flat: Vec<_> = clean_output.iter().flatten().copied().collect();
            let clean_only_quality = metrics(&clean, &clean, &clean_flat, active);
            let gain = q
                .speech_projection_gain_db
                .map(|v| (-v).clamp(-12.0, 12.0))
                .unwrap_or(0.0);
            let pass = if c.agc == "off" { "ans" } else { "agc2" };
            let file = format!("{}-{pass}-{:02}.wav", scene.name, candidates.len() + 1);
            rows.push(Row {
                scene: scene.name.clone(),
                case: c.name(),
                model,
                quality: q,
                clean_only_quality,
                performance: perf,
                listening_file: file.clone(),
                listening_gain_db: gain,
            });
            let g = 10f32.powf(gain as f32 / 20.0);
            candidates.push((file, flat.into_iter().map(|v| v * g).collect::<Vec<_>>()));
        }
        // Shared headroom across all matched candidates and the clean reference.
        let peak = candidates
            .iter()
            .flat_map(|(_, x)| x.iter())
            .chain(&clean)
            .fold(0.0f32, |a, x| a.max(x.abs()));
        let headroom = (0.95 / peak).min(1.0);
        for (file, mut x) in candidates {
            for v in &mut x {
                *v *= headroom;
            }
            fixture::write_wav(&out.join("listening").join(&file), &frames(&x))?;
            writeln!(score, "{},{file},,,,", scene.name).unwrap();
        }
        let reference: Vec<_> = clean.iter().map(|x| x * headroom).collect();
        fixture::write_wav(
            &out.join("listening")
                .join(format!("{}-reference.wav", scene.name)),
            &frames(&reference),
        )?;
        for r in rows.iter_mut().filter(|r| r.scene == scene.name) {
            r.listening_gain_db += 20.0 * (headroom as f64).log10();
        }
    }
    let metadata = serde_json::json!({"manifest":m,"inputs":sources,"os":std::env::consts::OS,"arch":std::env::consts::ARCH,"rnnoise_model":model,"speex":"1.2.1 -20dB","webrtc":"2.1.0 bundled APM, Moderate NS, AGC2 max18dB","nnnoiseless":"0.5.2 default model","rnnoise_commit":"70f1d256acd4b34a572f999a05c87bf00b67730d","alignment":"correlation estimate 0..30ms; metric aid, NOT algorithmic latency","listening":"projection speech gain matched within +/-12dB plus shared peak headroom; raw WAVs preserve AGC loudness"});
    super::save(
        &out.join("metadata.json"),
        &serde_json::to_string_pretty(&metadata).unwrap(),
    )?;
    super::save(
        &out.join("answer-key.json"),
        &serde_json::to_string_pretty(&rows).unwrap(),
    )?;
    super::save(&out.join("scores.csv"), &score)?;
    let mut csv = String::from("scene,case,model,si_sdr_db,si_sdr_improvement_db,speech_gain_db,silence_dbfs,silence_reduction_db,alignment_samples,mean_us,cpu_percent_one_core,p99_us,clean_only_si_sdr_db\n");
    let mut report = String::from("# Speech quality screening\n\nReference proxies are not MOS or a perceptual ranking. Listen separately for speech clarity, background quietness and artifacts. SI-SDR is gain-invariant; also inspect speech gain and raw WAVs to detect over-suppression. Correlation alignment (0..30ms) is not measured algorithmic delay. Null metrics mean insufficient signal/region, not a perfect score.\n\nANS-only (AGC off) is the primary denoiser comparison. AGC2 combinations are a separate listening check. Listening WAVs remove gain differences (bounded +/-12dB), while raw WAVs retain gain. Do not use matched WAVs to judge AGC loudness. Keep answer-key.json/report.md private during blind listening; use scores.csv. A higher artifacts score means fewer artifacts.\n\nCPU is normalized throughput cost for one real-time 48k mono/10ms stream, excluding construction, I/O and metrics. It is not game-frame impact, memory use, end-to-end or buffering latency. Settings are not equivalent in suppression strength.\n\n| Scene | NS/AGC | SI-SDR dB | Improvement dB | Speech gain dB | Silence reduction dB | Mean us | CPU % | p99 us |\n|---|---|---:|---:|---:|---:|---:|---:|---:|\n");
    let fmt = |v: Option<f64>| v.map(|x| format!("{x:.2}")).unwrap_or_else(|| "n/a".into());
    for r in &rows {
        let median = |get: fn(&measure::ResultRow) -> f64| {
            let mut v: Vec<_> = r.performance.iter().map(get).collect();
            v.sort_by(f64::total_cmp);
            measure::quantile(&v, 0.5)
        };
        let q = &r.quality;
        writeln!(
            csv,
            "{},{},{},{},{},{},{},{},{},{:.3},{:.4},{:.3},{}",
            r.scene,
            r.case,
            r.model,
            fmt(q.aligned_si_sdr_db),
            fmt(q.si_sdr_improvement_db),
            fmt(q.speech_projection_gain_db),
            fmt(q.silence_output_dbfs),
            fmt(q.silence_reduction_db),
            q.alignment_samples,
            median(|p| p.mean_us),
            median(|p| p.cpu_percent_one_core),
            median(|p| p.p99_us),
            fmt(r.clean_only_quality.aligned_si_sdr_db)
        )
        .unwrap();
        writeln!(
            report,
            "| {} | {} | {} | {} | {} | {} | {:.2} | {:.3} | {:.2} |",
            r.scene,
            r.case,
            fmt(q.aligned_si_sdr_db),
            fmt(q.si_sdr_improvement_db),
            fmt(q.speech_projection_gain_db),
            fmt(q.silence_reduction_db),
            median(|p| p.mean_us),
            median(|p| p.cpu_percent_one_core),
            median(|p| p.p99_us)
        )
        .unwrap();
    }
    super::save(&out.join("results.csv"), &csv)?;
    report.push_str("\n## Clean speech control\n\nThese processors receive the clean reference alone. Higher SI-SDR indicates less waveform distortion, not necessarily better perceptual quality. Inspect the clean-only raw WAVs, too.\n\n| Scene | NS/AGC | Clean-only SI-SDR dB | Speech gain dB |\n|---|---|---:|---:|\n");
    for r in &rows {
        writeln!(
            report,
            "| {} | {} | {} | {} |",
            r.scene,
            r.case,
            fmt(r.clean_only_quality.aligned_si_sdr_db),
            fmt(r.clean_only_quality.speech_projection_gain_db)
        )
        .unwrap();
    }
    super::save(&out.join("report.md"), &report)?;
    println!("{}", out.join("report.md").display());
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn alignment_and_gain_do_not_masquerade_as_quality() {
        let mut seed = 42u32;
        let clean: Vec<_> = (0..48000)
            .map(|_| {
                seed = seed.wrapping_mul(1664525).wrapping_add(1013904223);
                (seed as f64 / u32::MAX as f64 * 0.2 - 0.1) as f32
            })
            .collect();
        let active = vec![true; clean.len()];
        let mut output = vec![0.0; clean.len()];
        for i in 480..output.len() {
            output[i] = clean[i - 480] * 0.25;
        }
        let q = metrics(&clean, &clean, &output, &active);
        assert_eq!(q.alignment_samples, 480);
        assert!((q.speech_projection_gain_db.unwrap() + 12.0412).abs() < 0.01);
        assert!(q.aligned_si_sdr_db.unwrap() > 90.0);
        let muted = metrics(&clean, &clean, &vec![0.0; clean.len()], &active);
        assert!(muted.aligned_si_sdr_db.is_none());
        assert!(muted.silence_reduction_db.is_none());
    }
    #[test]
    fn mixing_preserves_snr_and_common_headroom() {
        let clean = vec![0.8; 48000];
        let noise = vec![0.5; 48000];
        let (reference, input, gain) = mix(&clean, &noise, &vec![true; 48000], 0, 6.0).unwrap();
        let residual: Vec<_> = input.iter().zip(&reference).map(|(x, c)| x - c).collect();
        assert!((measure::db(power(&reference) / power(&residual)) - 6.0).abs() < 0.001);
        assert!(gain < 1.0);
        assert!(input.iter().all(|x| x.abs() <= 0.901));
    }

    #[test]
    fn silence_regions_exclude_speech_tails() {
        let mut clean = vec![0.0; 48000];
        let mut active = vec![false; clean.len()];
        clean[0..24000].fill(0.1);
        active[0..24000].fill(true);
        let input: Vec<_> = clean.iter().map(|x| x + 0.02).collect();
        let output: Vec<_> = clean.iter().map(|x| x + 0.002).collect();
        let q = metrics(&clean, &input, &output, &active);
        assert!((q.silence_reduction_db.unwrap() - 20.0).abs() < 0.01);
    }

    #[test]
    fn rejects_invalid_manifests() {
        let mut m: Manifest =
            serde_json::from_str(include_str!("../quality-example.json")).unwrap();
        assert!(validate(&m).is_ok());
        m.scenes[0].speech_ms = vec![[0, 1000], [999, 1500]];
        assert!(validate(&m).is_err());
        m.scenes[0].speech_ms = vec![[0, 1000]];
        m.scenes[0].name = "../unsafe".into();
        assert!(validate(&m).is_err());
        m.version = 2;
        assert!(validate(&m).is_err());
    }

    #[test]
    fn quality_command_exports_all_cases_and_refuses_overwrite() {
        let unique = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let root =
            std::env::temp_dir().join(format!("resona-quality-{}-{unique}", std::process::id()));
        fs::create_dir(&root).unwrap();
        let mut clean = vec![0.0; 52800];
        let mut noise = vec![0.0; clean.len()];
        let mut seed = 19u32;
        for i in 0..clean.len() {
            if (4800..28800).contains(&i) {
                clean[i] = 0.1 * (i as f32 * 0.032).sin();
            }
            seed = seed.wrapping_mul(1664525).wrapping_add(1013904223);
            noise[i] = (seed as f64 / u32::MAX as f64 * 0.02 - 0.01) as f32;
        }
        fixture::write_wav(&root.join("clean.wav"), &frames(&clean)).unwrap();
        fixture::write_wav(&root.join("noise.wav"), &frames(&noise)).unwrap();
        super::super::save(&root.join("ATTRIBUTION.md"), "Test source attribution").unwrap();
        let manifest = serde_json::json!({"version":1,"seed":42,"warmup_ms":100,"duration_ms":1000,"repeats":1,"scenes":[{"name":"test","clean":"clean.wav","noise":"noise.wav","provenance":"synthetic test only","snr_db":5,"speech_ms":[[0,500]]}]});
        super::super::save(&root.join("manifest.json"), &manifest.to_string()).unwrap();
        let args = vec![
            root.join("manifest.json").to_str().unwrap().into(),
            root.join("out").to_str().unwrap().into(),
        ];
        execute(args.clone()).unwrap();
        let exported = root.join("chain-input");
        export_inputs(vec![args[0].clone(), exported.to_str().unwrap().into()]).unwrap();
        let chain_manifest: serde_json::Value =
            serde_json::from_slice(&fs::read(exported.join("manifest.json")).unwrap()).unwrap();
        assert_eq!(chain_manifest["frame_samples"], 960);
        assert_eq!(
            fs::read(exported.join("test-capture.f32")).unwrap().len(),
            52800 * 4
        );
        assert!(fs::read_to_string(exported.join("SOURCES.md"))
            .unwrap()
            .contains("Test source attribution"));
        assert!(execute(args.clone()).unwrap_err().contains("new directory"));
        let scores = fs::read_to_string(root.join("out/scores.csv")).unwrap();
        assert_eq!(scores.lines().count(), 11);
        assert!(scores.lines().all(|line| line.split(',').count() == 6));
        let rows: serde_json::Value =
            serde_json::from_slice(&fs::read(root.join("out/answer-key.json")).unwrap()).unwrap();
        assert_eq!(rows.as_array().unwrap().len(), 10);
        assert!(fs::read_to_string(root.join("out/listening/SOURCES.md"))
            .unwrap()
            .contains("Test source attribution"));
        for row in rows.as_array().unwrap() {
            let file = row["listening_file"].as_str().unwrap();
            let x = fixture::read_wav(&root.join("out/listening").join(file), 48000).unwrap();
            assert!(x.iter().all(|v| v.abs() <= 0.951));
        }
        fixture::write_wav(&root.join("noise.wav"), &frames(&vec![0.0; 52800])).unwrap();
        assert!(execute(vec![
            args[0].clone(),
            root.join("invalid").to_str().unwrap().into()
        ])
        .is_err());
        assert!(!root.join("invalid").exists());
        fs::remove_dir_all(root).unwrap();
    }
}
