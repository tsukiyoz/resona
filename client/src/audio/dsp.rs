use super::{FRAME, RATE, Result};
use libloading::Library;
use serde::{Deserialize, Serialize};
use std::{
    ffi::{c_int, c_void},
    path::{Path, PathBuf},
    ptr::NonNull,
    sync::Arc,
};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default, deny_unknown_fields)]
pub struct Params {
    pub level: i32,
    #[serde(rename = "tailMs")]
    pub tail_ms: i32,
    pub residual: bool,
    pub target: i32,
    pub max_gain_db: i32,
    pub headroom_db: i32,
}
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Spec {
    pub name: String,
    pub backend: String,
    pub params: Params,
}
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Processing {
    pub preprocess: [Spec; 2],
    pub postprocess: [Spec; 1],
}
#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OptionInfo {
    pub phase: &'static str,
    pub name: &'static str,
    pub id: &'static str,
    pub display_name: &'static str,
    pub description: &'static str,
}
unsafe extern "C" {
    fn speex_preprocess_state_init(frame: c_int, rate: c_int) -> *mut c_void;
    fn speex_preprocess_state_destroy(state: *mut c_void);
    fn speex_preprocess_ctl(state: *mut c_void, request: c_int, ptr: *mut c_void) -> c_int;
    fn speex_preprocess_run(state: *mut c_void, pcm: *mut i16) -> c_int;
    fn speex_echo_state_init(frame: c_int, tail: c_int) -> *mut c_void;
    fn speex_echo_state_destroy(state: *mut c_void);
    fn speex_echo_ctl(state: *mut c_void, request: c_int, ptr: *mut c_void) -> c_int;
    fn speex_echo_cancellation(
        state: *mut c_void,
        input: *const i16,
        reference: *const i16,
        output: *mut i16,
    );
}
type NewFn = unsafe extern "C" fn(u32, i32, i32, i32) -> *mut c_void;
type ProcessFn = unsafe extern "C" fn(*mut c_void, *mut f32, *const f32) -> i32;
type FreeFn = unsafe extern "C" fn(*mut c_void);
struct External {
    _library: Library,
    version: u32,
    new: NewFn,
    process: ProcessFn,
    free: FreeFn,
}
impl External {
    fn open(path: &Path) -> Result<Self> {
        // Only the explicit product library is loaded; no OS search by bare name.
        unsafe {
            let library = Library::new(path).map_err(|_| "原生音频库不可用".to_string())?;
            let version = library
                .get::<unsafe extern "C" fn() -> u32>(b"resona_apm_abi_version\0")
                .map_err(|_| "原生音频库版本不匹配".to_string())?();
            if !(1..=2).contains(&version) {
                return Err("原生音频库版本不匹配".into());
            }
            let new = *library
                .get::<NewFn>(b"resona_apm_new\0")
                .map_err(|_| "原生音频库接口不完整".to_string())?;
            let process = *library
                .get::<ProcessFn>(b"resona_apm_process\0")
                .map_err(|_| "原生音频库接口不完整".to_string())?;
            let free = *library
                .get::<FreeFn>(b"resona_apm_free\0")
                .map_err(|_| "原生音频库接口不完整".to_string())?;
            Ok(Self {
                _library: library,
                version,
                new,
                process,
                free,
            })
        }
    }
}
pub fn library_path() -> PathBuf {
    let name = if cfg!(target_os = "macos") {
        "libresona_webrtc_apm.dylib"
    } else if cfg!(windows) {
        "resona_webrtc_apm.dll"
    } else {
        "libresona_webrtc_apm.so"
    };
    if let Some(dir) = std::env::var_os("RESONA_WEBRTC_PLUGIN_DIR") {
        return PathBuf::from(dir).join(name);
    }
    let exe = std::env::current_exe().unwrap_or_default();
    let dir = exe.parent().unwrap_or(Path::new("/"));
    if cfg!(target_os = "macos") && dir.file_name().is_some_and(|n| n == "MacOS") {
        return dir.parent().unwrap().join("Frameworks").join(name);
    }
    dir.join(name)
}
#[derive(Clone)]
pub struct Factory {
    path: PathBuf,
    version: Option<u32>,
}
impl Default for Factory {
    fn default() -> Self {
        Self::new(&library_path())
    }
}
impl Factory {
    pub fn new(path: &Path) -> Self {
        Self {
            path: path.into(),
            version: External::open(path).ok().map(|library| library.version),
        }
    }
    pub fn options(&self) -> Vec<OptionInfo> {
        let mut options = Vec::with_capacity(10);
        for (phase, name, speex, webrtc, sd, wd) in [
            (
                "preprocess",
                "aec",
                "SpeexDSP",
                "WebRTC AEC3",
                "轻量回声消除，适合日常语音。",
                "更复杂的回声消除，资源开销通常较高。",
            ),
            (
                "preprocess",
                "ans",
                "SpeexDSP",
                "WebRTC NS",
                "轻量降噪，适合风扇、空调等稳定噪声；可能保留键盘声。",
                "传统降噪，可调强度；较强时可能影响音色。",
            ),
            (
                "postprocess",
                "agc",
                "SpeexDSP",
                "WebRTC AGC2",
                "对每位成员独立均衡收听响度。",
                "对每位成员独立均衡收听响度，可调目标余量。",
            ),
        ] {
            options.push(OptionInfo {
                phase,
                name,
                id: "none",
                display_name: "关闭",
                description: "不进行此项处理。",
            });
            options.push(OptionInfo {
                phase,
                name,
                id: "speex",
                display_name: speex,
                description: sd,
            });
            if self.version.is_some() {
                options.push(OptionInfo {
                    phase,
                    name,
                    id: "webrtc",
                    display_name: webrtc,
                    description: wd,
                });
            }
            if name == "ans" && self.version.is_some_and(|version| version >= 2) {
                options.push(OptionInfo{phase,name,id:"rnnoise",display_name:"RNNoise（标准模型）",description:"神经网络降噪，适合键盘等复杂噪声；比 Speex 更耗 CPU，可能改变部分音色。"});
            }
        }
        options
    }
    pub fn build(&self, specs: &[Spec], names: &[&str]) -> Result<Chain> {
        if specs.len() != names.len() {
            return Err("处理链长度无效".into());
        }
        let mut processors = Vec::with_capacity(specs.len());
        let mut external: Option<Arc<External>> = None;
        for (spec, expected) in specs.iter().zip(names) {
            validate(spec, expected)?;
            if spec.backend.is_empty() || spec.backend == "none" {
                continue;
            }
            let stage = match spec.backend.as_str() {
                "speex" => Stage::Speex(Box::new(Speex::new(spec)?)),
                "webrtc" | "rnnoise" => {
                    let library = match &external {
                        Some(library) => library.clone(),
                        None => {
                            let library = Arc::new(External::open(&self.path)?);
                            external = Some(library.clone());
                            library
                        }
                    };
                    let kind = if spec.backend == "rnnoise" {
                        4
                    } else {
                        match spec.name.as_str() {
                            "aec" => 1,
                            "ans" => 2,
                            "agc" => 3,
                            _ => return Err("处理项无效".into()),
                        }
                    };
                    if kind == 4 && library.version < 2 {
                        return Err("RNNoise 不可用，请安装匹配的音频库".into());
                    }
                    let p = spec.params;
                    let level = if kind == 2 { defaulted(p.level, 2) } else { 0 };
                    let headroom = if kind == 3 {
                        defaulted(p.headroom_db, 5)
                    } else {
                        0
                    };
                    let gain = if kind == 3 {
                        defaulted(p.max_gain_db, 18)
                    } else {
                        0
                    };
                    let handle =
                        NonNull::new(unsafe { (library.new)(kind, level, headroom, gain) })
                            .ok_or("原生音频处理器初始化失败")?;
                    Stage::External { handle, library }
                }
                _ => return Err("未知音频处理器".into()),
            };
            processors.push(stage);
        }
        Ok(Chain { processors })
    }
}
fn defaulted(value: i32, fallback: i32) -> i32 {
    if value == 0 { fallback } else { value }
}
fn validate(s: &Spec, expected: &str) -> Result<()> {
    if s == &Spec::default() {
        return Ok(());
    }
    if s.name != expected {
        return Err(format!("处理链顺序无效：期望 {expected}"));
    }
    let p = s.params;
    let none = Params::default();
    let valid = match (s.backend.as_str(), expected) {
        ("none", _) => p == none,
        ("rnnoise", "ans") => p == none,
        ("webrtc", "aec") => p == none,
        ("webrtc", "ans") => {
            p == Params {
                level: p.level,
                ..none
            } && (0..=4).contains(&p.level)
        }
        ("webrtc", "agc") => {
            p == Params {
                headroom_db: p.headroom_db,
                max_gain_db: p.max_gain_db,
                ..none
            } && (0..=20).contains(&p.headroom_db)
                && (0..=24).contains(&p.max_gain_db)
        }
        ("speex", "aec") => {
            p == Params {
                tail_ms: p.tail_ms,
                residual: p.residual,
                ..none
            } && (p.tail_ms == 0 || ((40..=500).contains(&p.tail_ms) && p.tail_ms % 20 == 0))
        }
        ("speex", "ans") => {
            p == Params {
                level: p.level,
                ..none
            } && (0..=3).contains(&p.level)
        }
        ("speex", "agc") => {
            p == Params {
                target: p.target,
                max_gain_db: p.max_gain_db,
                ..none
            } && (p.target == 0 || (2048..=16384).contains(&p.target))
                && (0..=24).contains(&p.max_gain_db)
        }
        _ => false,
    };
    if valid {
        Ok(())
    } else {
        Err(format!("{}/{} 参数无效", s.name, s.backend))
    }
}

pub struct Chain {
    processors: Vec<Stage>,
}
impl Chain {
    #[cfg(test)]
    pub(crate) fn failing() -> Self {
        Self {
            processors: vec![Stage::Failure],
        }
    }
    pub fn process(
        &mut self,
        samples: &mut [f32; FRAME],
        reference: Option<&[f32; FRAME]>,
    ) -> Result<()> {
        if samples.iter().any(|x| !x.is_finite())
            || reference.is_some_and(|r| r.iter().any(|x| !x.is_finite()))
        {
            return Err("音频处理输入无效".into());
        }
        for p in &mut self.processors {
            match p {
                #[cfg(test)]
                Stage::Failure => {
                    samples[0] = f32::NAN;
                    return Err("test processor failure".into());
                }
                Stage::Speex(p) => p.process(samples, reference),
                Stage::External { handle, library } => {
                    let code = unsafe {
                        (library.process)(
                            handle.as_ptr(),
                            samples.as_mut_ptr(),
                            reference.map_or(std::ptr::null(), |r| r.as_ptr()),
                        )
                    };
                    if code != 0 {
                        return Err(format!("原生音频处理失败：{code}"));
                    }
                }
            }
            if samples.iter().any(|x| !x.is_finite()) {
                return Err("音频处理结果包含无效采样".into());
            }
        }
        Ok(())
    }
}
enum Stage {
    #[cfg(test)]
    Failure,
    Speex(Box<Speex>),
    External {
        handle: NonNull<c_void>,
        library: Arc<External>,
    },
}
unsafe impl Send for Stage {}
impl Drop for Stage {
    fn drop(&mut self) {
        if let Self::External { handle, library } = self {
            unsafe { (library.free)(handle.as_ptr()) };
        }
    }
}
struct Speex {
    pre: Option<NonNull<c_void>>,
    echo: Option<NonNull<c_void>>,
    input: [i16; FRAME],
    reference: [i16; FRAME],
    output: [i16; FRAME],
}
impl Speex {
    fn new(s: &Spec) -> Result<Self> {
        let mut p = Self {
            pre: None,
            echo: None,
            input: [0; FRAME],
            reference: [0; FRAME],
            output: [0; FRAME],
        };
        if s.name == "aec" {
            p.echo = Some(
                NonNull::new(unsafe {
                    speex_echo_state_init(
                        FRAME as c_int,
                        RATE * defaulted(s.params.tail_ms, 200) / 1000,
                    )
                })
                .ok_or("Speex 回声消除初始化失败")?,
            );
            let mut rate = RATE;
            if unsafe {
                speex_echo_ctl(
                    p.echo.unwrap().as_ptr(),
                    24,
                    (&mut rate as *mut c_int).cast(),
                )
            } != 0
            {
                return Err("Speex 采样率设置失败".into());
            }
            if !s.params.residual {
                return Ok(p);
            }
        }
        p.pre = Some(
            NonNull::new(unsafe { speex_preprocess_state_init(FRAME as c_int, RATE) })
                .ok_or("Speex 处理器初始化失败")?,
        );
        p.set(0, i32::from(s.name == "ans"))?;
        if s.name == "ans" {
            p.set(18, -10 * defaulted(s.params.level, 2))?;
        }
        if s.name == "agc" {
            for (k, v) in [
                (2, 1),
                (46, defaulted(s.params.target, 8192)),
                (30, defaulted(s.params.max_gain_db, 18)),
                (26, 6),
                (28, 40),
            ] {
                p.set(k, v)?;
            }
        }
        if let Some(echo) = p.echo {
            if unsafe { speex_preprocess_ctl(p.pre.unwrap().as_ptr(), 24, echo.as_ptr()) } != 0 {
                return Err("Speex 残余回声配置失败".into());
            }
            p.set(20, -40)?;
            p.set(22, -15)?;
        }
        Ok(p)
    }
    fn set(&mut self, option: c_int, mut value: c_int) -> Result<()> {
        if unsafe {
            speex_preprocess_ctl(
                self.pre.unwrap().as_ptr(),
                option,
                (&mut value as *mut c_int).cast(),
            )
        } != 0
        {
            return Err("Speex 参数设置失败".into());
        }
        Ok(())
    }
    fn process(&mut self, samples: &mut [f32; FRAME], reference: Option<&[f32; FRAME]>) {
        for (dst, sample) in self.input.iter_mut().zip(samples.iter()) {
            *dst = (sample * 32768.0).clamp(-32768.0, 32767.0) as i16;
        }
        let ptr = if let Some(echo) = self.echo {
            self.reference.fill(0);
            if let Some(reference) = reference {
                for (dst, sample) in self.reference.iter_mut().zip(reference) {
                    *dst = (sample * 32768.0).clamp(-32768.0, 32767.0) as i16;
                }
            }
            unsafe {
                speex_echo_cancellation(
                    echo.as_ptr(),
                    self.input.as_ptr(),
                    self.reference.as_ptr(),
                    self.output.as_mut_ptr(),
                )
            };
            self.output.as_mut_ptr()
        } else {
            self.input.as_mut_ptr()
        };
        if let Some(pre) = self.pre {
            unsafe { speex_preprocess_run(pre.as_ptr(), ptr) };
        }
        let output = if self.echo.is_some() {
            &self.output
        } else {
            &self.input
        };
        for (dst, sample) in samples.iter_mut().zip(output) {
            *dst = f32::from(*sample) / 32768.0;
        }
    }
}
impl Drop for Speex {
    fn drop(&mut self) {
        unsafe {
            if let Some(pre) = self.pre {
                speex_preprocess_state_destroy(pre.as_ptr());
            }
            if let Some(echo) = self.echo {
                speex_echo_state_destroy(echo.as_ptr());
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn spec(name: &str, backend: &str) -> Spec {
        Spec {
            name: name.into(),
            backend: backend.into(),
            ..Default::default()
        }
    }
    #[test]
    fn strict_order_parameters_and_unavailable_backends() {
        let f = Factory::new(Path::new("/no-such-resona-audio-library"));
        assert!(f.build(&[spec("agc", "speex")], &["ans"]).is_err());
        assert!(f.build(&[spec("ans", "rnnoise")], &["ans"]).is_err());
        let mut invalid = spec("aec", "speex");
        invalid.params.tail_ms = 21;
        assert!(f.build(&[invalid], &["aec"]).is_err());
        assert_eq!(f.options().len(), 6);
    }
    #[test]
    fn speex_three_independent_slots_use_finite_fixed_frames() {
        let f = Factory::new(Path::new("/no-such-resona-audio-library"));
        let mut pre = f
            .build(
                &[spec("aec", "speex"), spec("ans", "speex")],
                &["aec", "ans"],
            )
            .unwrap();
        let mut post = f.build(&[spec("agc", "speex")], &["agc"]).unwrap();
        for _ in 0..10 {
            let mut frame = [0.05; FRAME];
            pre.process(&mut frame, Some(&[0.0; FRAME])).unwrap();
            post.process(&mut frame, None).unwrap();
            assert!(frame.iter().all(|s| s.is_finite() && s.abs() <= 1.0));
        }
        assert!(pre.process(&mut [f32::NAN; FRAME], None).is_err());
    }
    #[test]
    fn product_external_library_runs_all_stages_when_requested() {
        if std::env::var_os("RESONA_TEST_NATIVE_AUDIO").is_none() {
            return;
        }
        let f = Factory::default();
        assert_eq!(
            f.options().len(),
            10,
            "requested native audio library is missing"
        );
        for (name, backend) in [
            ("aec", "webrtc"),
            ("ans", "webrtc"),
            ("agc", "webrtc"),
            ("ans", "rnnoise"),
        ] {
            let mut chain = f.build(&[spec(name, backend)], &[name]).unwrap();
            for _ in 0..10 {
                let mut pcm = [0.01; FRAME];
                chain.process(&mut pcm, Some(&[0.0; FRAME])).unwrap();
                assert!(pcm.iter().all(|x| x.is_finite()));
            }
        }
    }
}
