//! Device-independent voice processing. Native objects belong to their worker;
//! no codec or DSP runs in a device callback or on the UI executor.
#[allow(unsafe_code)]
mod codec;
#[allow(unsafe_code)]
mod device;
#[allow(unsafe_code)]
mod dsp;
mod engine;
mod notifications;
mod pipeline;

pub use codec::{Decoder, Encoder};
pub use device::{Captured, Device, DeviceInfo};
pub use dsp::{Chain, Factory, OptionInfo, Params, Processing, Spec, library_path};
pub use engine::{Engine, Handle, State};
pub use notifications::{SoundHandle, Sounds};
pub use pipeline::{Config, Counters, Peer, Pipeline, Route};
pub const FRAME: usize = 960;
pub const RATE: i32 = 48_000;
pub type Result<T> = std::result::Result<T, String>;

pub fn level_db(samples: &[f32]) -> i32 {
    if samples.is_empty() {
        return -60;
    }
    let energy = samples.iter().map(|x| f64::from(*x).powi(2)).sum::<f64>() / samples.len() as f64;
    (10.0 * energy.log10()).clamp(-60.0, 0.0) as i32
}
pub fn active(samples: &[f32]) -> bool {
    !samples.is_empty()
        && samples.iter().map(|x| f64::from(*x).powi(2)).sum::<f64>() / samples.len() as f64
            > 0.000001
}

#[derive(Default)]
pub struct Limiter {
    gain: f32,
}
impl Limiter {
    pub fn apply(&mut self, samples: &mut [f32], volume: f32) {
        let peak = samples
            .iter()
            .map(|s| (s * volume).abs())
            .fold(0.0_f32, f32::max);
        let target = if peak > 0.98 { 0.98 / peak } else { 1.0 };
        if self.gain == 0.0 || target < self.gain {
            self.gain = target;
        } else {
            self.gain += (target - self.gain) * 0.1;
        }
        for sample in samples {
            *sample *= volume * self.gain;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn limiter_preserves_ratios_and_meter_matches_product() {
        assert_eq!(level_db(&[0.0; FRAME]), -60);
        assert_eq!(level_db(&[0.5; FRAME]), -6);
        let mut data = [2.0, -1.0, 0.5];
        Limiter::default().apply(&mut data, 1.0);
        assert!((data[0] - 0.98).abs() < 0.0001);
        assert_eq!(data[0], -2.0 * data[1]);
    }
}
