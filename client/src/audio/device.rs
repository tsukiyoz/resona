use super::{FRAME, Result};
use serde::Serialize;
use std::{
    ffi::{CStr, CString, c_char, c_int, c_void},
    ptr::NonNull,
    thread::{self, Thread},
};

#[repr(C)]
pub struct Captured {
    pub epoch: u32,
    pub age: f64,
    pub samples: [f32; FRAME],
}
impl Default for Captured {
    fn default() -> Self {
        Self {
            epoch: 0,
            age: 0.0,
            samples: [0.0; FRAME],
        }
    }
}
#[derive(Clone, Debug, Serialize)]
pub struct DeviceInfo {
    pub id: String,
    pub name: String,
    pub kind: &'static str,
    pub default: bool,
}
unsafe extern "C" {
    fn ra_device_list(
        callback: extern "C" fn(*mut c_void, *const c_char, *const c_char, c_int, c_int),
        user: *mut c_void,
    ) -> c_int;
    fn ra_device_open(
        input: c_int,
        output: c_int,
        input_id: *const c_char,
        output_id: *const c_char,
        wake: extern "C" fn(*mut c_void),
        user: *mut c_void,
        error: *mut c_int,
    ) -> *mut c_void;
    fn ra_device_close(device: *mut c_void);
    fn ra_device_failed(device: *mut c_void) -> c_int;
    fn ra_device_pending(device: *mut c_void) -> u32;
    fn ra_device_epoch(device: *mut c_void, epoch: u32);
    fn ra_device_discard(device: *mut c_void);
    fn ra_device_capture(device: *mut c_void, out: *mut Captured) -> c_int;
    fn ra_device_reference(device: *mut c_void, out: *mut f32) -> u32;
    fn ra_device_write(device: *mut c_void, samples: *const f32, monitor: c_int) -> u32;
}
extern "C" fn wake(user: *mut c_void) {
    // The stable Box outlives all callbacks; native close joins them before Drop.
    unsafe {
        (&*user.cast::<Thread>()).unpark();
    }
}
extern "C" fn listed(
    user: *mut c_void,
    id: *const c_char,
    name: *const c_char,
    input: c_int,
    default: c_int,
) {
    unsafe {
        let list = &mut *user.cast::<Vec<DeviceInfo>>();
        list.push(DeviceInfo {
            id: CStr::from_ptr(id).to_string_lossy().into_owned(),
            name: CStr::from_ptr(name).to_string_lossy().into_owned(),
            kind: if input != 0 { "input" } else { "output" },
            default: default != 0,
        });
    }
}
pub struct Device {
    handle: NonNull<c_void>,
    _wake: Box<Thread>,
}
// Only the audio worker accesses the handle; native callbacks own the other
// side of each SPSC ring. Device is deliberately not Sync.
unsafe impl Send for Device {}
impl Device {
    pub fn list() -> Result<Vec<DeviceInfo>> {
        let mut list = Vec::new();
        let code = unsafe { ra_device_list(listed, (&mut list as *mut Vec<DeviceInfo>).cast()) };
        if code != 0 {
            return Err(format!("无法读取音频设备：{code}"));
        }
        Ok(list)
    }
    pub fn open(input: bool, output: bool, input_id: &str, output_id: &str) -> Result<Self> {
        if !input && !output {
            return Err("至少需要一个音频设备".into());
        }
        let input_id = CString::new(input_id).map_err(|_| "输入设备 ID 无效")?;
        let output_id = CString::new(output_id).map_err(|_| "输出设备 ID 无效")?;
        let mut wake = Box::new(thread::current());
        let mut error = 0;
        let handle = NonNull::new(unsafe {
            ra_device_open(
                input.into(),
                output.into(),
                input_id.as_ptr(),
                output_id.as_ptr(),
                self::wake,
                (&mut *wake as *mut Thread).cast(),
                &mut error,
            )
        })
        .ok_or_else(|| {
            if input && cfg!(target_os = "macos") {
                format!("无法打开音频设备，请确认麦克风权限：{error}")
            } else {
                format!("无法打开音频设备：{error}")
            }
        })?;
        Ok(Self {
            handle,
            _wake: wake,
        })
    }
    pub fn failed(&self) -> bool {
        unsafe { ra_device_failed(self.handle.as_ptr()) != 0 }
    }
    pub fn pending(&self) -> usize {
        unsafe { ra_device_pending(self.handle.as_ptr()) as usize }
    }
    pub fn epoch(&mut self, epoch: u32) {
        unsafe { ra_device_epoch(self.handle.as_ptr(), epoch) };
    }
    pub fn discard(&mut self) {
        unsafe { ra_device_discard(self.handle.as_ptr()) };
    }
    pub fn capture(&mut self, out: &mut Captured) -> bool {
        unsafe { ra_device_capture(self.handle.as_ptr(), out) != 0 }
    }
    pub fn reference(&mut self, out: &mut [f32; FRAME * 2]) -> usize {
        out.fill(0.0);
        unsafe { ra_device_reference(self.handle.as_ptr(), out.as_mut_ptr()) as usize }
    }
    pub fn write(&mut self, samples: &[f32; FRAME * 2], monitor: bool) -> Result<usize> {
        if samples.iter().any(|x| !x.is_finite() || x.abs() > 1.0) {
            return Err("输出音频采样无效".into());
        }
        Ok(unsafe {
            ra_device_write(self.handle.as_ptr(), samples.as_ptr(), monitor.into()) as usize
        })
    }
}
impl Drop for Device {
    fn drop(&mut self) {
        unsafe { ra_device_close(self.handle.as_ptr()) };
    }
}
