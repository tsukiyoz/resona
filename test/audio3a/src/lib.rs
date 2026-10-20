pub mod processor;

use processor::{Chain, Choice, N};
use std::{
    ffi::c_void,
    panic::{catch_unwind, AssertUnwindSafe},
    ptr::NonNull,
    slice,
};

extern "C" {
    fn bench_speex_new_frame(aec: i32, ns: i32, agc: i32, frame: i32) -> *mut c_void;
    fn bench_speex_process(p: *mut c_void, capture: *mut f32, render: *const f32);
    fn bench_speex_free(p: *mut c_void);
}

enum PacketProcessor {
    Split(Chain),
    Speex(NonNull<c_void>),
}

#[no_mangle]
pub extern "C" fn bench_packet_model() -> u32 {
    u32::from(cfg!(feature = "rnnoise-little"))
}

impl Drop for PacketProcessor {
    fn drop(&mut self) {
        if let Self::Speex(p) = self {
            unsafe { bench_speex_free(p.as_ptr()) };
        }
    }
}

// Each handle belongs to one sender or receiver and one benchmark worker.
#[no_mangle]
pub extern "C" fn bench_packet_new(kind: u32) -> *mut c_void {
    catch_unwind(|| {
        let stage = match kind {
            0 => PacketProcessor::Split(Chain::new(&Choice::new("off", "off", "off")).ok()?),
            1 => PacketProcessor::Split(Chain::new(&Choice::new("off", "rnnoise", "off")).ok()?),
            2 => PacketProcessor::Speex(NonNull::new(unsafe {
                bench_speex_new_frame(0, 1, 0, 960)
            })?),
            3 => PacketProcessor::Split(Chain::new(&Choice::new("off", "off", "agc2")).ok()?),
            _ => return None,
        };
        Some(Box::into_raw(Box::new(stage)) as *mut c_void)
    })
    .ok()
    .flatten()
    .unwrap_or(std::ptr::null_mut())
}

/// # Safety
/// Handle must be live and exclusively owned; samples must hold `len` floats.
#[no_mangle]
pub unsafe extern "C" fn bench_packet_process(
    handle: *mut c_void,
    samples: *mut f32,
    len: usize,
) -> i32 {
    if handle.is_null() || samples.is_null() || len != 960 {
        return -1;
    }
    let samples = slice::from_raw_parts_mut(samples, len);
    if samples.iter().any(|x| !x.is_finite()) {
        return -1;
    }
    match catch_unwind(AssertUnwindSafe(|| -> Result<(), String> {
        match &mut *(handle as *mut PacketProcessor) {
            PacketProcessor::Speex(p) => {
                bench_speex_process(p.as_ptr(), samples.as_mut_ptr(), [0.0; 960].as_ptr())
            }
            PacketProcessor::Split(chain) => {
                for chunk in samples.chunks_exact_mut(N) {
                    chain.process(chunk.try_into().unwrap(), &[0.0; N])?;
                }
            }
        }
        Ok(())
    })) {
        Ok(Ok(())) => 0,
        _ => -2,
    }
}

/// # Safety
/// Handle must be null or a live exclusively owned handle; release exactly once.
#[no_mangle]
pub unsafe extern "C" fn bench_packet_free(handle: *mut c_void) {
    if !handle.is_null() {
        drop(Box::from_raw(handle as *mut PacketProcessor));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn packet_stages_are_bounded_and_have_independent_state() {
        assert!(bench_packet_new(99).is_null());
        for kind in 0..=3 {
            let a = bench_packet_new(kind);
            let b = bench_packet_new(kind);
            assert!(!a.is_null() && !b.is_null());
            unsafe {
                let mut x = [0.03; 960];
                assert_eq!(bench_packet_process(a, x.as_mut_ptr(), 480), -1);
                assert_eq!(
                    bench_packet_process(std::ptr::null_mut(), x.as_mut_ptr(), 960),
                    -1
                );
                for _ in 0..10 {
                    let mut x = std::array::from_fn::<_, 960, _>(|i| (i as f32 * 0.04).sin() * 0.1);
                    let mut y = x;
                    assert_eq!(bench_packet_process(a, x.as_mut_ptr(), 960), 0);
                    assert_eq!(bench_packet_process(b, y.as_mut_ptr(), 960), 0);
                    assert_eq!(x, y);
                    assert!(x.iter().all(|x| x.is_finite() && x.abs() <= 1.0));
                }
                bench_packet_free(a);
                bench_packet_free(b);
            }
        }
    }
}
