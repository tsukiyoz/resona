mod fixture;
mod measure;
mod processor;

use processor::Choice;
use std::{collections::BTreeMap, fmt::Write as _, fs, path::PathBuf};

fn main() {
    if let Err(e) = execute() {
        eprintln!("{e}");
        std::process::exit(1);
    }
}

fn execute() -> Result<(), String> {
    let mut seconds = 6usize;
    let mut warmup = 3usize;
    let mut repeats = 3usize;
    let mut out = PathBuf::from("results-3a");
    let mut near_path = None;
    let mut far_path = None;
    let mut selected = Choice::new("off", "off", "off");
    let mut custom = false;
    let mut wavs = false;
    let mut args = std::env::args().skip(1);
    while let Some(arg) = args.next() {
        if arg == "--help" || arg == "-h" {
            println!("resona-3a-bench [--seconds 6] [--warmup 3] [--repeats 3] [--out DIR] [--wavs]\n[--near speech-48k-mono.wav] [--far far-end-48k-mono.wav]\nDefault: all 13 presets. Custom: --aec off|speex|aec3 --ns off|speex|webrtc|rnnoise|nnnoiseless --agc off|speex|agc2\nInput WAVs must cover warmup + measured duration; no microphone/network is used.");
            return Ok(());
        }
        if arg == "--wavs" {
            wavs = true;
            continue;
        }
        let value = args
            .next()
            .ok_or_else(|| format!("missing value for {arg}"))?;
        match arg.as_str() {
            "--seconds" => seconds = value.parse().map_err(|_| "invalid seconds")?,
            "--warmup" => warmup = value.parse().map_err(|_| "invalid warmup")?,
            "--repeats" => repeats = value.parse().map_err(|_| "invalid repeats")?,
            "--out" => out = value.into(),
            "--near" => near_path = Some(PathBuf::from(value)),
            "--far" => far_path = Some(PathBuf::from(value)),
            "--aec" => {
                selected.aec = value;
                custom = true;
            }
            "--ns" => {
                selected.ns = value;
                custom = true;
            }
            "--agc" => {
                selected.agc = value;
                custom = true;
            }
            _ => return Err(format!("unknown argument {arg}")),
        }
    }
    if !(1..=60).contains(&seconds) || !(1..=30).contains(&warmup) || !(1..=10).contains(&repeats) {
        return Err("seconds 1..60, warmup 1..30, repeats 1..10 required".into());
    }
    let cases = if custom {
        selected.validate()?;
        vec![selected]
    } else {
        processor::presets()
    };
    let ticks = (seconds + warmup) * 100;
    let near = near_path
        .as_ref()
        .map(|p| fixture::read_wav(p, ticks * 480))
        .transpose()?;
    let far = far_path
        .as_ref()
        .map(|p| fixture::read_wav(p, ticks * 480))
        .transpose()?;
    let fixtures = fixture::make(ticks, warmup * 100, near.as_deref(), far.as_deref());
    fs::create_dir_all(&out).map_err(|e| e.to_string())?;
    if wavs {
        fs::create_dir_all(out.join("wav")).map_err(|e| e.to_string())?;
    }
    let hashes: BTreeMap<_, _> = fixtures.iter().map(|f| (f.name, &f.sha256)).collect();
    let metadata = serde_json::json!({"os": std::env::consts::OS, "arch": std::env::consts::ARCH,
        "sample_rate":48000,"frame_samples":480,"warmup_seconds":warmup,"measured_seconds":seconds,"repeats":repeats,
        "near_input":if near.is_some(){"external-wav"}else{"synthetic-harmonics"},"far_input":if far.is_some(){"external-wav"}else{"synthetic-harmonics"},
        "fixture_sha256":hashes,"cases":cases,"speex":"vendored 1.2.1",
        "webrtc_wrapper":"2.1.0 bundled C++ APM", "nnnoiseless":"0.5.2 default model",
        "rnnoise_commit":"70f1d256acd4b34a572f999a05c87bf00b67730d",
        "rnnoise_model_archive_sha256":"0a8755f8e2d834eff6a54714ecc7d75f9932e845df35f8b59bc52a7cfe6e8b37",
        "rnnoise_model":if cfg!(feature="rnnoise-little"){"little"}else{"regular"},
        "rnnoise_simd":"x86_64 runtime SSE4.1/AVX2 dispatch; aarch64 compiler NEON",
        "agc":"digital only, max gain 18dB; Speex target 8192, AGC2 headroom 5dB initial gain 0dB",
        "aec":"Speex 200ms tail plus residual suppression; AEC3 automatic delay and enforced HPF",
        "ns":"Speex -20dB; WebRTC Moderate; neural default model; levels not quality-equivalent"});
    save(
        &out.join("metadata.json"),
        &serde_json::to_string_pretty(&metadata).unwrap(),
    )?;
    let mut rows = Vec::new();
    let mut failures = Vec::new();
    for f in &fixtures {
        if wavs {
            for (name, frames) in [
                ("capture", &f.capture),
                ("render", &f.render),
                ("clean", &f.clean),
            ] {
                fixture::write_wav(
                    &out.join("wav").join(format!("{}-{name}.wav", f.name)),
                    &frames[warmup * 100..],
                )?;
            }
        }
        for repeat in 0..repeats {
            for j in 0..cases.len() {
                let c = &cases[(j + repeat) % cases.len()];
                eprintln!("{} {} repeat {}", f.name, c.name(), repeat + 1);
                let measured = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                    measure::run(c, f, warmup * 100, repeat + 1)
                }));
                match measured {
                    Ok(Ok((row, output))) => {
                        if wavs && repeat == 0 {
                            fixture::write_wav(
                                &out.join("wav").join(format!("{}-{}.wav", f.name, c.name())),
                                &output,
                            )?;
                        }
                        rows.push(row);
                    }
                    Ok(Err(e)) => failures.push(e),
                    Err(_) => failures.push(format!("panic in {} {}", c.name(), f.name)),
                }
            }
        }
    }
    save(
        &out.join("results.json"),
        &serde_json::to_string_pretty(&rows).unwrap(),
    )?;
    save(
        &out.join("failures.json"),
        &serde_json::to_string_pretty(&failures).unwrap(),
    )?;
    let mut csv = String::from("case,scene,repeat,frames,mean_us,cpu_percent_one_core,p50_us,p99_us,p999_us,max_us,input_rms_dbfs,output_rms_dbfs,attenuation_db,output_peak,clipped_samples,first_third_dbfs,middle_third_dbfs,last_third_dbfs\n");
    for r in &rows {
        writeln!(csv,"{},{},{},{},{:.4},{:.4},{:.4},{:.4},{:.4},{:.4},{:.4},{:.4},{:.4},{:.4},{},{:.4},{:.4},{:.4}",r.case,r.scene,r.repeat,r.frames,r.mean_us,r.cpu_percent_one_core,r.p50_us,r.p99_us,r.p999_us,r.max_us,r.input_rms_dbfs,r.output_rms_dbfs,r.attenuation_db,r.output_peak,r.clipped_samples,r.level_thirds_dbfs[0],r.level_thirds_dbfs[1],r.level_thirds_dbfs[2]).unwrap();
    }
    save(&out.join("results.csv"), &csv)?;
    let mut report = format!("# 3A benchmark\n\nFailures: {}. See failures.json.\n\n48kHz mono / 10ms. Values are medians across repeats. CPU is normalized to one real-time core, not sampled desktop CPU. P99 is a throughput-loop statistic, not audio end-to-end latency. Input is {}.\n\nAttenuation only describes output energy: muting everything would score well. Compare clean speech and double-talk WAVs before judging suppression. No perceptual quality ranking or calibrated algorithmic delay is claimed.\n\n| Scene | AEC-NS-AGC | Mean us | CPU % | p99 us | Attenuation dB | Output dBFS |\n|---|---|---:|---:|---:|---:|---:|\n",failures.len(),if near.is_some(){"external near speech; see metadata for far signal"}else{"synthetic, not real speech"});
    for f in &fixtures {
        for c in &cases {
            let group: Vec<_> = rows
                .iter()
                .filter(|r| r.scene == f.name && r.case == c.name())
                .collect();
            if group.is_empty() {
                continue;
            }
            let median = |get: fn(&measure::ResultRow) -> f64| {
                let mut v: Vec<_> = group.iter().map(|r| get(r)).collect();
                v.sort_by(f64::total_cmp);
                measure::quantile(&v, 0.5)
            };
            writeln!(
                report,
                "| {} | {} | {:.2} | {:.3} | {:.2} | {:.2} | {:.2} |",
                f.name,
                c.name(),
                median(|r| r.mean_us),
                median(|r| r.cpu_percent_one_core),
                median(|r| r.p99_us),
                median(|r| r.attenuation_db),
                median(|r| r.output_rms_dbfs)
            )
            .unwrap();
        }
    }
    save(&out.join("report.md"), &report)?;
    println!("{}", out.join("report.md").display());
    if failures.is_empty() {
        Ok(())
    } else {
        Err("3A processing checks failed; reports preserved".into())
    }
}

fn save(path: &std::path::Path, text: &str) -> Result<(), String> {
    fs::write(path, text).map_err(|e| e.to_string())
}
