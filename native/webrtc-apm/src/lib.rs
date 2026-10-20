use std::{
    ffi::c_void,
    panic::{catch_unwind, AssertUnwindSafe},
    ptr::NonNull,
    slice,
};
use webrtc_audio_processing::{config::*, Processor};

const FRAME: usize = 480;
const PACKET: usize = FRAME * 2;

struct WebRTCStage {
    processor: Processor,
    aec: bool,
    scratch: [f32; PACKET],
}

extern "C" {
    fn rnnoise_create(model: *const c_void) -> *mut c_void;
    fn rnnoise_destroy(state: *mut c_void);
    fn rnnoise_process_frame(state: *mut c_void, output: *mut f32, input: *const f32) -> f32;
}

enum Stage {
    WebRTC(WebRTCStage),
    RNNoise(NonNull<c_void>),
}

impl Drop for Stage {
    fn drop(&mut self) {
        if let Self::RNNoise(state) = self {
            unsafe { rnnoise_destroy(state.as_ptr()) };
        }
    }
}

#[no_mangle]
pub extern "C" fn resona_apm_abi_version() -> u32 {
    2
}

#[no_mangle]
pub extern "C" fn resona_apm_new(
    kind: u32,
    level: i32,
    headroom_db: i32,
    max_gain_db: i32,
) -> *mut std::ffi::c_void {
    let build = || -> Option<Stage> {
        if kind == 4 {
            if level != 0 || headroom_db != 0 || max_gain_db != 0 {
                return None;
            }
            return NonNull::new(unsafe { rnnoise_create(std::ptr::null()) }).map(Stage::RNNoise);
        }
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
        Some(Stage::WebRTC(WebRTCStage {
            processor,
            aec,
            scratch: [0.0; PACKET],
        }))
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
    let samples = slice::from_raw_parts_mut(samples, PACKET);
    if samples.iter().any(|sample| !sample.is_finite()) {
        return -1;
    }
    if let Stage::RNNoise(state) = stage {
        // RNNoise expects PCM16-scale floats and retains its own streaming state.
        let mut output = [0.0; PACKET];
        for (input, output) in samples
            .chunks_exact(FRAME)
            .zip(output.chunks_exact_mut(FRAME))
        {
            let input: [f32; FRAME] = std::array::from_fn(|i| input[i] * 32768.0);
            rnnoise_process_frame(state.as_ptr(), output.as_mut_ptr(), input.as_ptr());
        }
        if output.iter().any(|sample| !sample.is_finite()) {
            return -3;
        }
        for (sample, output) in samples.iter_mut().zip(output) {
            *sample = (output / 32768.0).clamp(-1.0, 1.0);
        }
        return 0;
    }
    let Stage::WebRTC(stage) = stage else {
        unreachable!()
    };
    if stage.aec && reference.is_null() {
        return -1;
    }
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
        for (kind, level, headroom, max_gain) in
            [(1, 0, 0, 0), (2, 2, 0, 0), (3, 0, 5, 18), (4, 0, 0, 0)]
        {
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
        assert!(resona_apm_new(4, 1, 0, 0).is_null());
    }

    #[test]
    fn rnnoise_streams_have_independent_state_and_preserve_pcm_scale() {
        let a = resona_apm_new(4, 0, 0, 0);
        let b = resona_apm_new(4, 0, 0, 0);
        assert!(!a.is_null() && !b.is_null());
        let mut energy = 0.0;
        for tick in 0..100 {
            let mut x = std::array::from_fn::<_, PACKET, _>(|i| {
                ((tick * PACKET + i) as f32 * 0.04).sin() * 0.1
            });
            let mut y = x;
            unsafe {
                assert_eq!(resona_apm_process(a, x.as_mut_ptr(), std::ptr::null()), 0);
                assert_eq!(resona_apm_process(b, y.as_mut_ptr(), std::ptr::null()), 0);
            }
            assert_eq!(x, y);
            assert!(x
                .iter()
                .all(|sample| sample.is_finite() && sample.abs() <= 1.0));
            energy += x.iter().map(|x| x * x).sum::<f32>();
        }
        assert!(energy > 0.001 && energy < 1000.0);
        unsafe {
            let mut invalid = [f32::NAN; PACKET];
            assert_eq!(
                resona_apm_process(a, invalid.as_mut_ptr(), std::ptr::null()),
                -1
            );
            resona_apm_free(a);
            resona_apm_free(b);
        }
    }
}
