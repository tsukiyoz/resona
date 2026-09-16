use std::{ffi::c_void, ptr::NonNull};
use webrtc_audio_processing::{config::*, Processor};

pub const N: usize = 480;
pub type Frame = [f32; N];

extern "C" {
    fn bench_speex_new(aec: i32, ns: i32, agc: i32) -> *mut c_void;
    fn bench_speex_process(p: *mut c_void, capture: *mut f32, render: *const f32);
    fn bench_speex_free(p: *mut c_void);
    fn rnnoise_create(model: *const c_void) -> *mut c_void;
    fn rnnoise_destroy(p: *mut c_void);
    fn rnnoise_process_frame(p: *mut c_void, output: *mut f32, input: *const f32) -> f32;
}

#[derive(Clone, Debug, serde::Serialize)]
pub struct Choice {
    pub aec: String,
    pub ns: String,
    pub agc: String,
}

impl Choice {
    pub fn new(aec: &str, ns: &str, agc: &str) -> Self {
        Self {
            aec: aec.into(),
            ns: ns.into(),
            agc: agc.into(),
        }
    }
    pub fn name(&self) -> String {
        format!("{}-{}-{}", self.aec, self.ns, self.agc)
    }
    pub fn validate(&self) -> Result<(), String> {
        if !["off", "speex", "aec3"].contains(&self.aec.as_str())
            || !["off", "speex", "webrtc", "rnnoise", "nnnoiseless"].contains(&self.ns.as_str())
            || !["off", "speex", "agc2"].contains(&self.agc.as_str())
        {
            return Err("invalid module: AEC off/speex/aec3; NS off/speex/webrtc/rnnoise/nnnoiseless; AGC off/speex/agc2".into());
        }
        Ok(())
    }
}

pub fn presets() -> Vec<Choice> {
    vec![
        Choice::new("off", "off", "off"),
        Choice::new("speex", "off", "off"),
        Choice::new("aec3", "off", "off"),
        Choice::new("off", "speex", "off"),
        Choice::new("off", "webrtc", "off"),
        Choice::new("off", "rnnoise", "off"),
        Choice::new("off", "nnnoiseless", "off"),
        Choice::new("off", "off", "speex"),
        Choice::new("off", "off", "agc2"),
        Choice::new("speex", "speex", "speex"),
        Choice::new("aec3", "webrtc", "agc2"),
        Choice::new("aec3", "rnnoise", "agc2"),
        Choice::new("aec3", "nnnoiseless", "agc2"),
    ]
}

enum Stage {
    Speex(NonNull<c_void>),
    Webrtc(Processor, bool),
    Rnnoise(NonNull<c_void>),
    Nnnoiseless(Box<nnnoiseless::DenoiseState<'static>>),
}

impl Drop for Stage {
    fn drop(&mut self) {
        // Native state is owned by this stage and only accessed on the benchmark thread.
        unsafe {
            match self {
                Self::Speex(p) => bench_speex_free(p.as_ptr()),
                Self::Rnnoise(p) => rnnoise_destroy(p.as_ptr()),
                _ => {}
            }
        }
    }
}

fn speex(aec: bool, ns: bool, agc: bool) -> Result<Stage, String> {
    let p = unsafe { bench_speex_new(aec.into(), ns.into(), agc.into()) };
    Ok(Stage::Speex(
        NonNull::new(p).ok_or("Speex allocation failed")?,
    ))
}

fn webrtc(aec: bool, ns: bool, agc: bool) -> Result<Stage, String> {
    let p = Processor::new(48000).map_err(|e| format!("WebRTC init: {e:?}"))?;
    let config = Config {
        echo_canceller: aec.then_some(EchoCanceller::Full {
            stream_delay_ms: None,
        }),
        noise_suppression: ns.then_some(NoiseSuppression::default()),
        gain_controller: agc.then_some(GainController::GainController2(GainController2 {
            input_volume_controller_enabled: false,
            adaptive_digital: Some(AdaptiveDigital {
                max_gain_db: 18.0,
                initial_gain_db: 0.0,
                ..Default::default()
            }),
            fixed_digital: FixedDigital::default(),
        })),
        ..Default::default()
    };
    p.set_config(config);
    Ok(Stage::Webrtc(p, aec))
}

pub struct Chain {
    stages: Vec<Stage>,
}
impl Chain {
    pub fn new(c: &Choice) -> Result<Self, String> {
        c.validate()?;
        let mut stages = Vec::new();
        if c.aec == "speex" && c.ns == "speex" && c.agc == "speex" {
            stages.push(speex(true, true, true)?);
        } else if c.aec == "aec3" && c.ns == "webrtc" && c.agc == "agc2" {
            stages.push(webrtc(true, true, true)?);
        } else {
            match c.aec.as_str() {
                "speex" => stages.push(speex(true, false, false)?),
                "aec3" => stages.push(webrtc(true, false, false)?),
                _ => {}
            }
            match c.ns.as_str() {
                "speex" => stages.push(speex(false, true, false)?),
                "webrtc" => stages.push(webrtc(false, true, false)?),
                "rnnoise" => stages.push(Stage::Rnnoise(
                    NonNull::new(unsafe { rnnoise_create(std::ptr::null()) })
                        .ok_or("RNNoise allocation failed")?,
                )),
                "nnnoiseless" => stages.push(Stage::Nnnoiseless(nnnoiseless::DenoiseState::new())),
                _ => {}
            }
            match c.agc.as_str() {
                "speex" => stages.push(speex(false, false, true)?),
                "agc2" => stages.push(webrtc(false, false, true)?),
                _ => {}
            }
        }
        Ok(Self { stages })
    }

    pub fn process(&mut self, capture: &mut Frame, render: &Frame) -> Result<(), String> {
        for stage in &mut self.stages {
            match stage {
                Stage::Speex(p) => unsafe {
                    bench_speex_process(p.as_ptr(), capture.as_mut_ptr(), render.as_ptr())
                },
                Stage::Webrtc(p, aec) => {
                    if *aec {
                        let mut reference = *render;
                        p.process_render_frame([reference.as_mut_slice()])
                            .map_err(|e| format!("render: {e:?}"))?;
                    }
                    p.process_capture_frame([capture.as_mut_slice()])
                        .map_err(|e| format!("capture: {e:?}"))?;
                }
                Stage::Rnnoise(p) => {
                    let input = capture.map(|v| v * 32768.0);
                    unsafe {
                        rnnoise_process_frame(p.as_ptr(), capture.as_mut_ptr(), input.as_ptr());
                    }
                    for v in capture.iter_mut() {
                        *v /= 32768.0;
                    }
                }
                Stage::Nnnoiseless(p) => {
                    let input = capture.map(|v| v * 32768.0);
                    p.process_frame(capture, &input);
                    for v in capture.iter_mut() {
                        *v /= 32768.0;
                    }
                }
            }
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn all_presets_execute_finite_frames() {
        for c in presets() {
            let mut p = Chain::new(&c).unwrap();
            for tick in 0..12 {
                let mut capture =
                    std::array::from_fn(|i| ((tick * N + i) as f32 * 0.07).sin() * 0.1);
                let render = capture;
                p.process(&mut capture, &render).unwrap();
                assert!(
                    capture.iter().all(|v| v.is_finite() && v.abs() < 4.0),
                    "{}",
                    c.name()
                );
            }
        }
    }
    #[test]
    fn bypass_preserves_samples_and_invalid_choice_fails() {
        let mut p = Chain::new(&Choice::new("off", "off", "off")).unwrap();
        let mut frame = [0.25; N];
        p.process(&mut frame, &[0.0; N]).unwrap();
        assert_eq!(frame, [0.25; N]);
        assert!(Chain::new(&Choice::new("unknown", "off", "off")).is_err());
    }
}
