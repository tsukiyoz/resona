use std::{
    panic::{catch_unwind, AssertUnwindSafe},
    slice,
};
use webrtc_audio_processing::{config::*, Processor};

const FRAME: usize = 480;
const PACKET: usize = FRAME * 2;

struct Stage {
    processor: Processor,
    aec: bool,
    scratch: [f32; PACKET],
}

#[no_mangle]
pub extern "C" fn resona_apm_abi_version() -> u32 {
    1
}

#[no_mangle]
pub extern "C" fn resona_apm_new(
    kind: u32,
    level: i32,
    headroom_db: i32,
    max_gain_db: i32,
) -> *mut std::ffi::c_void {
    let build = || -> Option<Stage> {
        let (config, aec) = match kind {
            1 => (
                Config {
                    echo_canceller: Some(EchoCanceller::Full {
                        stream_delay_ms: None,
                    }),
                    high_pass_filter: Some(HighPassFilter::default()),
                    ..Default::default()
                },
                true,
            ),
            2 if (1..=4).contains(&level) => {
                let ns_level = match level {
                    1 => NoiseSuppressionLevel::Low,
                    2 => NoiseSuppressionLevel::Moderate,
                    3 => NoiseSuppressionLevel::High,
                    _ => NoiseSuppressionLevel::VeryHigh,
                };
                (
                    Config {
                        noise_suppression: Some(NoiseSuppression {
                            level: ns_level,
                            ..Default::default()
                        }),
                        ..Default::default()
                    },
                    false,
                )
            }
            3 if (1..=20).contains(&headroom_db) && (1..=24).contains(&max_gain_db) => (
                Config {
                    gain_controller: Some(GainController::GainController2(GainController2 {
                        input_volume_controller_enabled: false,
                        adaptive_digital: Some(AdaptiveDigital {
                            headroom_db: headroom_db as f32,
                            max_gain_db: max_gain_db as f32,
                            initial_gain_db: 0.0,
                            ..Default::default()
                        }),
                        fixed_digital: FixedDigital::default(),
                    })),
                    ..Default::default()
                },
                false,
            ),
            _ => return None,
        };
        let processor = Processor::new(48_000).ok()?;
        processor.set_config(config);
        Some(Stage {
            processor,
            aec,
            scratch: [0.0; PACKET],
        })
    };
    catch_unwind(AssertUnwindSafe(build))
        .ok()
        .flatten()
        .map(|stage| Box::into_raw(Box::new(stage)) as *mut std::ffi::c_void)
        .unwrap_or(std::ptr::null_mut())
}

#[no_mangle]
pub unsafe extern "C" fn resona_apm_process(
    handle: *mut std::ffi::c_void,
    samples: *mut f32,
    reference: *const f32,
) -> i32 {
    if handle.is_null() || samples.is_null() {
        return -1;
    }
    let stage = &mut *(handle as *mut Stage);
    if stage.aec && reference.is_null() {
        return -1;
    }
    let samples = slice::from_raw_parts_mut(samples, PACKET);
    stage.scratch.copy_from_slice(samples);
    let reference = if stage.aec {
        Some(slice::from_raw_parts(reference, PACKET))
    } else {
        None
    };
    let result = catch_unwind(AssertUnwindSafe(|| {
        for chunk in 0..2 {
            let start = chunk * FRAME;
            if let Some(reference) = reference {
                let mut render = [0.0; FRAME];
                render.copy_from_slice(&reference[start..start + FRAME]);
                stage
                    .processor
                    .process_render_frame([render.as_mut_slice()])
                    .map_err(|_| -2)?;
            }
            stage
                .processor
                .process_capture_frame([&mut stage.scratch[start..start + FRAME]])
                .map_err(|_| -3)?;
        }
        Ok::<(), i32>(())
    }));
    match result {
        Ok(Ok(())) => {
            samples.copy_from_slice(&stage.scratch);
            0
        }
        Ok(Err(code)) => code,
        Err(_) => -4,
    }
}

#[no_mangle]
pub unsafe extern "C" fn resona_apm_free(handle: *mut std::ffi::c_void) {
    if !handle.is_null() {
        drop(Box::from_raw(handle as *mut Stage));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_mode_processes_a_twenty_ms_frame_and_rejects_bad_parameters() {
        for (kind, level, headroom, max_gain) in [(1, 0, 0, 0), (2, 2, 0, 0), (3, 0, 5, 18)] {
            let handle = resona_apm_new(kind, level, headroom, max_gain);
            assert!(!handle.is_null());
            let mut samples = [0.02; PACKET];
            let reference = [0.01; PACKET];
            assert_eq!(
                unsafe { resona_apm_process(handle, samples.as_mut_ptr(), reference.as_ptr()) },
                0
            );
            assert!(samples.iter().all(|sample| sample.is_finite()));
            unsafe {
                resona_apm_free(handle);
            }
        }
        assert!(resona_apm_new(3, 0, 0, 18).is_null());
    }
}
