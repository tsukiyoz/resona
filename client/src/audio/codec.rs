use super::{FRAME, RATE, Result};
use std::{
    ffi::{c_int, c_void},
    ptr::NonNull,
};

unsafe extern "C" {
    fn opus_encoder_create(
        rate: c_int,
        channels: c_int,
        application: c_int,
        error: *mut c_int,
    ) -> *mut c_void;
    fn opus_encoder_destroy(state: *mut c_void);
    fn opus_encoder_ctl(state: *mut c_void, request: c_int, ...) -> c_int;
    fn opus_encode_float(
        state: *mut c_void,
        pcm: *const f32,
        frames: c_int,
        data: *mut u8,
        capacity: c_int,
    ) -> c_int;
    fn opus_decoder_create(rate: c_int, channels: c_int, error: *mut c_int) -> *mut c_void;
    fn opus_decoder_destroy(state: *mut c_void);
    fn opus_decode_float(
        state: *mut c_void,
        data: *const u8,
        bytes: c_int,
        pcm: *mut f32,
        frames: c_int,
        fec: c_int,
    ) -> c_int;
    fn opus_packet_get_nb_samples(data: *const u8, bytes: c_int, rate: c_int) -> c_int;
}

pub struct Encoder {
    state: NonNull<c_void>,
    bitrate: u32,
}
// Each instance is moved to one audio worker and never shared.
unsafe impl Send for Encoder {}
impl Encoder {
    pub fn new(bitrate: u32) -> Result<Self> {
        let mut error = 0;
        let state = NonNull::new(unsafe { opus_encoder_create(RATE, 1, 2048, &mut error) })
            .ok_or_else(|| format!("Opus 编码器初始化失败：{error}"))?;
        let mut encoder = Self { state, bitrate: 0 };
        encoder.bitrate(bitrate)?;
        encoder.control(4010, 5)?;
        Ok(encoder)
    }
    fn control(&mut self, request: c_int, value: c_int) -> Result<()> {
        let code = unsafe { opus_encoder_ctl(self.state.as_ptr(), request, value) };
        if code != 0 {
            return Err(format!("Opus 编码参数无效：{code}"));
        }
        Ok(())
    }
    pub fn bitrate(&mut self, bitrate: u32) -> Result<()> {
        if !(16_000..=64_000).contains(&bitrate) || !bitrate.is_multiple_of(1000) {
            return Err("频道码率无效".into());
        }
        if self.bitrate != bitrate {
            self.control(4002, bitrate as c_int)?;
            self.bitrate = bitrate;
        }
        Ok(())
    }
    pub fn encode(&mut self, pcm: &[f32; FRAME], out: &mut [u8; 1024]) -> Result<usize> {
        if pcm.iter().any(|x| !x.is_finite()) {
            return Err("音频包含无效采样".into());
        }
        let n = unsafe {
            opus_encode_float(
                self.state.as_ptr(),
                pcm.as_ptr(),
                FRAME as c_int,
                out.as_mut_ptr(),
                out.len() as c_int,
            )
        };
        if n < 0 {
            return Err(format!("Opus 编码失败：{n}"));
        }
        Ok(n as usize)
    }
}
impl Drop for Encoder {
    fn drop(&mut self) {
        unsafe {
            opus_encoder_destroy(self.state.as_ptr());
        }
    }
}

pub struct Decoder {
    state: NonNull<c_void>,
}
unsafe impl Send for Decoder {}
impl Decoder {
    pub fn packet_samples(data: &[u8]) -> Option<usize> {
        if data.is_empty() || data.len() > 1024 {
            return None;
        }
        let n = unsafe { opus_packet_get_nb_samples(data.as_ptr(), data.len() as c_int, RATE) };
        if n > 0 && n as usize <= FRAME * 6 {
            Some(n as usize)
        } else {
            None
        }
    }
    pub fn new() -> Result<Self> {
        let mut error = 0;
        let state = NonNull::new(unsafe { opus_decoder_create(RATE, 1, &mut error) })
            .ok_or_else(|| format!("Opus 解码器初始化失败：{error}"))?;
        Ok(Self { state })
    }
    pub fn decode(&mut self, data: Option<&[u8]>, out: &mut [f32; FRAME * 6]) -> Result<usize> {
        let (ptr, len, frames) = match data {
            Some(data) if !data.is_empty() && data.len() <= 1024 => {
                if Self::packet_samples(data).is_none() {
                    return Err("Opus 数据包无效".into());
                }
                (data.as_ptr(), data.len() as c_int, out.len() as c_int)
            }
            None => (std::ptr::null(), 0, FRAME as c_int),
            _ => return Err("Opus 数据包长度无效".into()),
        };
        let n = unsafe {
            opus_decode_float(self.state.as_ptr(), ptr, len, out.as_mut_ptr(), frames, 0)
        };
        if n <= 0 {
            return Err(format!("Opus 解码失败：{n}"));
        }
        if out[..n as usize].iter().any(|x| !x.is_finite()) {
            return Err("Opus 解码结果无效".into());
        }
        Ok(n as usize)
    }
}
impl Drop for Decoder {
    fn drop(&mut self) {
        unsafe {
            opus_decoder_destroy(self.state.as_ptr());
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn real_opus_roundtrip_and_loss_concealment() {
        let mut encoder = Encoder::new(48_000).unwrap();
        let mut decoder = Decoder::new().unwrap();
        let mut packet = [0; 1024];
        let mut decoded = [0.0; FRAME * 6];
        let mut energy = 0.0;
        for frame in 0..10 {
            let pcm = std::array::from_fn(|i| {
                (2.0 * std::f32::consts::PI * 440.0 * (i + frame * FRAME) as f32 / RATE as f32)
                    .sin()
                    * 0.25
            });
            let n = encoder.encode(&pcm, &mut packet).unwrap();
            assert_eq!(
                decoder.decode(Some(&packet[..n]), &mut decoded).unwrap(),
                FRAME
            );
            energy += decoded[..FRAME].iter().map(|x| x * x).sum::<f32>();
        }
        assert!(energy > 10.0);
        assert_eq!(decoder.decode(None, &mut decoded).unwrap(), FRAME);
        assert!(decoder.decode(Some(&[255; 1024]), &mut decoded).is_err());
        assert!(decoder.decode(Some(&[]), &mut decoded).is_err());
    }
}
