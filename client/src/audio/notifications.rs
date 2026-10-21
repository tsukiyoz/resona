use super::{Device, FRAME, Result};
use std::{
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, AtomicU64, Ordering},
    },
    thread::{self, Thread},
    time::{Duration, Instant},
};
use tokio::sync::oneshot;

struct Cue {
    pcm: Vec<f32>,
    volume: f32,
    version: u64,
    deadline: Instant,
    done: Option<oneshot::Sender<Result<()>>>,
}
struct Shared {
    pending: Mutex<Option<Cue>>,
    version: AtomicU64,
    closed: AtomicBool,
}
struct WaitCue {
    shared: Arc<Shared>,
    version: u64,
    wake: Thread,
}
impl Drop for WaitCue {
    fn drop(&mut self) {
        if self
            .shared
            .version
            .compare_exchange(
                self.version,
                self.version.wrapping_add(1),
                Ordering::AcqRel,
                Ordering::Acquire,
            )
            .is_ok()
        {
            self.wake.unpark();
        }
    }
}
#[derive(Clone)]
pub struct SoundHandle {
    shared: Arc<Shared>,
    wake: Thread,
}
pub struct Sounds {
    pub handle: SoundHandle,
    done: Option<oneshot::Receiver<()>>,
}
impl SoundHandle {
    fn enqueue(
        &self,
        kind: &str,
        volume: i32,
        done: Option<oneshot::Sender<Result<()>>>,
    ) -> Result<u64> {
        if self.shared.closed.load(Ordering::Acquire) {
            return Err("提示音已关闭".into());
        }
        if !(0..=100).contains(&volume) {
            return Err("提示音音量无效".into());
        }
        let bytes = match kind {
            "connected" => {
                include_bytes!("../../../internal/audio/sounds/connected.wav").as_slice()
            }
            "disconnected" => {
                include_bytes!("../../../internal/audio/sounds/disconnected.wav").as_slice()
            }
            "member_joined" => {
                include_bytes!("../../../internal/audio/sounds/member-joined.wav").as_slice()
            }
            "member_left" => {
                include_bytes!("../../../internal/audio/sounds/member-left.wav").as_slice()
            }
            _ => return Err("提示音类型无效".into()),
        };
        let pcm = decode(bytes)?;
        let mut pending = self.shared.pending.lock().map_err(|_| "提示音状态不可用")?;
        if self.shared.closed.load(Ordering::Acquire) {
            return Err("提示音已关闭".into());
        }
        let version = self.shared.version.fetch_add(1, Ordering::AcqRel) + 1;
        *pending = Some(Cue {
            pcm,
            volume: volume as f32 / 100.0,
            version,
            deadline: Instant::now() + Duration::from_secs(3),
            done,
        });
        drop(pending);
        self.wake.unpark();
        Ok(version)
    }
    pub fn play(&self, kind: &str, volume: i32) -> Result<()> {
        self.enqueue(kind, volume, None).map(|_| ())
    }
    pub async fn play_and_wait(&self, kind: &str, volume: i32) -> Result<()> {
        let (done, rx) = oneshot::channel();
        let version = self.enqueue(kind, volume, Some(done))?;
        let _guard = WaitCue {
            shared: self.shared.clone(),
            version,
            wake: self.wake.clone(),
        };
        tokio::time::timeout(Duration::from_secs(3), rx)
            .await
            .map_err(|_| "提示音退出等待超时")?
            .map_err(|_| "提示音已取消")?
    }
    pub fn stop(&self) {
        if let Ok(mut pending) = self.shared.pending.lock() {
            self.shared.version.fetch_add(1, Ordering::AcqRel);
            *pending = None;
        }
        self.wake.unpark();
    }
}
impl Sounds {
    pub fn start() -> Result<Self> {
        let shared = Arc::new(Shared {
            pending: Mutex::new(None),
            version: AtomicU64::new(0),
            closed: AtomicBool::new(false),
        });
        let owner = shared.clone();
        let (done_tx, done) = oneshot::channel();
        let worker = thread::Builder::new()
            .name("resona-sounds".into())
            .spawn(move || {
                while !owner.closed.load(Ordering::Acquire) {
                    let cue = owner.pending.lock().ok().and_then(|mut p| p.take());
                    if let Some(mut cue) = cue {
                        let result = play(&owner, &cue);
                        if let Some(done) = cue.done.take() {
                            let _ = done.send(result);
                        }
                        continue;
                    }
                    if owner.closed.load(Ordering::Acquire) {
                        break;
                    }
                    thread::park();
                }
                let _ = done_tx.send(());
            })
            .map_err(|_| "无法启动提示音线程")?;
        let wake = worker.thread().clone();
        drop(worker);
        Ok(Self {
            handle: SoundHandle { shared, wake },
            done: Some(done),
        })
    }
    pub async fn close(mut self) -> Result<()> {
        self.stop();
        if let Some(done) = self.done.take() {
            tokio::time::timeout(Duration::from_secs(3), done)
                .await
                .map_err(|_| "提示音设备仍在退出")?
                .map_err(|_| "提示音线程异常退出")?;
        }
        Ok(())
    }
    fn stop(&self) {
        self.handle.shared.closed.store(true, Ordering::Release);
        self.handle.stop();
    }
}
impl Drop for Sounds {
    fn drop(&mut self) {
        self.stop();
    }
}
fn current(shared: &Shared, cue: &Cue) -> bool {
    !shared.closed.load(Ordering::Acquire)
        && shared.version.load(Ordering::Acquire) == cue.version
        && Instant::now() < cue.deadline
}
fn play(shared: &Shared, cue: &Cue) -> Result<()> {
    if !current(shared, cue) {
        return Err("提示音已取消".into());
    }
    if cue.volume == 0.0 {
        return Ok(());
    }
    let mut device = Device::open(false, true, "", "")?;
    if !current(shared, cue) {
        return Err("提示音已取消".into());
    }
    let deadline = Instant::now() + Duration::from_secs_f64(cue.pcm.len() as f64 / 48_000.0 + 2.0);
    let mut pos = 0;
    let mut drain = None;
    while Instant::now() < deadline {
        if !current(shared, cue) {
            return Err("提示音已取消".into());
        }
        if device.failed() {
            return Err("提示音设备已断开".into());
        }
        if pos < cue.pcm.len() {
            let mut out = [0.0; FRAME * 2];
            for (i, frame) in out.chunks_exact_mut(2).enumerate() {
                let sample = cue.pcm.get(pos + i).copied().unwrap_or(0.0) * cue.volume;
                frame.fill(sample);
            }
            let _gate = shared.pending.lock().map_err(|_| "提示音状态不可用")?;
            if !current(shared, cue) {
                return Err("提示音已取消".into());
            }
            pos += device.write(&out, false)?.min(FRAME);
        } else if device.pending() == 0 {
            let until = *drain.get_or_insert_with(|| Instant::now() + Duration::from_millis(100));
            if Instant::now() >= until {
                return Ok(());
            }
        }
        thread::park_timeout(Duration::from_millis(20));
    }
    Err("提示音输出未完成".into())
}
fn decode(bytes: &[u8]) -> Result<Vec<f32>> {
    if bytes.len() < 12 || &bytes[..4] != b"RIFF" || &bytes[8..12] != b"WAVE" {
        return Err("提示音格式无效".into());
    }
    let mut offset = 12;
    let mut format = None;
    let mut pcm = None;
    while offset + 8 <= bytes.len() {
        let size = u32::from_le_bytes(bytes[offset + 4..offset + 8].try_into().unwrap()) as usize;
        let start = offset + 8;
        let end = start
            .checked_add(size)
            .filter(|&end| end <= bytes.len())
            .ok_or("提示音数据损坏")?;
        match &bytes[offset..offset + 4] {
            b"fmt " => format = Some(&bytes[start..end]),
            b"data" => pcm = Some(&bytes[start..end]),
            _ => {}
        }
        offset = end + size % 2;
    }
    let format = format.filter(|f| f.len() >= 16).ok_or("提示音格式缺失")?;
    if u16::from_le_bytes(format[..2].try_into().unwrap()) != 1
        || u16::from_le_bytes(format[2..4].try_into().unwrap()) != 1
        || u32::from_le_bytes(format[4..8].try_into().unwrap()) != 48_000
        || u16::from_le_bytes(format[14..16].try_into().unwrap()) != 16
    {
        return Err("提示音须为 48kHz 单声道 PCM16".into());
    }
    let pcm = pcm
        .filter(|p| !p.is_empty() && p.len() % 2 == 0 && p.len() <= 48_000 * 2 * 10)
        .ok_or("提示音长度无效")?;
    Ok(pcm
        .chunks_exact(2)
        .map(|p| f32::from(i16::from_le_bytes(p.try_into().unwrap())) / 32768.0)
        .collect())
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn bundled_cues_are_finite_bounded_pcm_and_invalid_chunks_fail() {
        for bytes in [
            include_bytes!("../../../internal/audio/sounds/connected.wav").as_slice(),
            include_bytes!("../../../internal/audio/sounds/disconnected.wav").as_slice(),
            include_bytes!("../../../internal/audio/sounds/member-joined.wav").as_slice(),
            include_bytes!("../../../internal/audio/sounds/member-left.wav").as_slice(),
        ] {
            let pcm = decode(bytes).unwrap();
            assert!(!pcm.is_empty());
            assert!(pcm.iter().all(|x| x.is_finite() && x.abs() <= 1.0));
            let mut damaged = bytes.to_vec();
            damaged[16..20].fill(255);
            assert!(decode(&damaged).is_err());
        }
    }
    #[tokio::test]
    async fn idle_stop_close_never_opens_a_device() {
        let player = Sounds::start().unwrap();
        player.handle.stop();
        player.close().await.unwrap();
    }
}
