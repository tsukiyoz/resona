use std::{
    ffi::c_void,
    panic::{catch_unwind, AssertUnwindSafe},
    ptr, slice,
};

// Opaque, single-owner handles: no Rust references or retained Go buffers cross FFI.
enum Encoder {
    Original(Box<opus_rs::OpusEncoder>),
    Fork(Box<rusty_opus::OpusEncoder>),
}
enum Decoder {
    Original(Box<opus_rs::OpusDecoder>),
    Fork(Box<rusty_opus::OpusDecoder>),
}

#[no_mangle]
pub extern "C" fn bench_encoder_new(
    kind: i32,
    bitrate: i32,
    complexity: i32,
    cbr: bool,
) -> *mut c_void {
    catch_unwind(|| {
        let encoder = match kind {
            1 => {
                let mut e = opus_rs::OpusEncoder::new(48000, 1, opus_rs::Application::Voip).ok()?;
                e.bitrate_bps = bitrate;
                e.complexity = complexity;
                e.use_cbr = cbr;
                e.use_inband_fec = false;
                e.packet_loss_perc = 0;
                Encoder::Original(Box::new(e))
            }
            2 => {
                let mut e =
                    rusty_opus::OpusEncoder::new(48000, 1, rusty_opus::Application::Voip).ok()?;
                e.bitrate_bps = bitrate;
                e.complexity = complexity;
                e.use_cbr = cbr;
                e.use_inband_fec = false;
                e.use_dtx = false;
                e.packet_loss_perc = 0;
                Encoder::Fork(Box::new(e))
            }
            _ => return None,
        };
        Some(Box::into_raw(Box::new(encoder)).cast())
    })
    .ok()
    .flatten()
    .unwrap_or(ptr::null_mut())
}

#[no_mangle]
pub unsafe extern "C" fn bench_encode(
    handle: *mut c_void,
    pcm: *const f32,
    packet: *mut u8,
    capacity: usize,
) -> i32 {
    catch_unwind(AssertUnwindSafe(|| {
        let input = slice::from_raw_parts(pcm, 960);
        let output = slice::from_raw_parts_mut(packet, capacity);
        let result = match &mut *handle.cast::<Encoder>() {
            Encoder::Original(e) => e.encode(input, 960, output),
            Encoder::Fork(e) => e.encode(input, 960, output),
        };
        result.map(|n| n as i32).unwrap_or(-1)
    }))
    .unwrap_or(-100)
}

#[no_mangle]
pub extern "C" fn bench_decoder_new(kind: i32) -> *mut c_void {
    catch_unwind(|| {
        let decoder = match kind {
            1 => Decoder::Original(Box::new(opus_rs::OpusDecoder::new(48000, 1).ok()?)),
            2 => Decoder::Fork(Box::new(rusty_opus::OpusDecoder::new(48000, 1).ok()?)),
            _ => return None,
        };
        Some(Box::into_raw(Box::new(decoder)).cast())
    })
    .ok()
    .flatten()
    .unwrap_or(ptr::null_mut())
}

#[no_mangle]
pub unsafe extern "C" fn bench_decode(
    handle: *mut c_void,
    packet: *const u8,
    length: usize,
    pcm: *mut f32,
) -> i32 {
    catch_unwind(AssertUnwindSafe(|| {
        let input = slice::from_raw_parts(packet, length);
        let output = slice::from_raw_parts_mut(pcm, 960);
        let result = match &mut *handle.cast::<Decoder>() {
            Decoder::Original(d) => d.decode(input, 960, output),
            Decoder::Fork(d) => d.decode(input, 960, output),
        };
        result.map(|n| n as i32).unwrap_or(-1)
    }))
    .unwrap_or(-100)
}

#[no_mangle]
pub unsafe extern "C" fn bench_encoder_free(handle: *mut c_void) {
    drop(Box::from_raw(handle.cast::<Encoder>()));
}
#[no_mangle]
pub unsafe extern "C" fn bench_decoder_free(handle: *mut c_void) {
    drop(Box::from_raw(handle.cast::<Decoder>()));
}
