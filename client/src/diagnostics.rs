//! Structured, bounded memory only. No timer or automatic file flush.
use crate::workspace::{now, timestamp};
use serde::Serialize;
use std::{
    collections::BTreeMap,
    io::Write,
    path::{Path, PathBuf},
    sync::{
        Mutex,
        atomic::{AtomicU64, Ordering},
    },
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

const CAPACITY: usize = 512;
const WINDOW: Duration = Duration::from_secs(15 * 60);
#[derive(Clone, Copy, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Kind {
    Started,
    ConnectionRequested,
    ConnectionEstablished,
    ConnectionFailed,
    ConnectionLost,
    ConnectionCancelled,
    VoiceApplied,
    VoiceFailed,
    VoiceCounters,
    CoreStopped,
}
#[derive(Clone, Copy, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Field {
    Operation,
    Attempt,
    Generation,
    ElapsedMs,
    Retryable,
    Muted,
    Deafened,
    Received,
    Dropped,
    Decoded,
    DecodeErrors,
    Sent,
    SendErrors,
    CaptureDrops,
}
#[derive(Clone, Copy)]
struct Event {
    at: Instant,
    time: i64,
    kind: Kind,
    fields: [Option<(Field, i64)>; 6],
}
struct Ring {
    entries: Box<[Option<Event>]>,
    next: usize,
    count: usize,
    overwritten: u64,
}
pub struct Recorder {
    ring: Mutex<Ring>,
    dropped: AtomicU64,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct Export {
    version: String,
    platform: String,
    exported_at: String,
    window_seconds: u64,
    capacity: usize,
    overwritten: u64,
    contention_drops: u64,
    events: Vec<ExportEvent>,
}
#[derive(Serialize)]
struct ExportEvent {
    time: String,
    level: &'static str,
    message: Kind,
    fields: BTreeMap<String, i64>,
}
impl Default for Recorder {
    fn default() -> Self {
        Self {
            ring: Mutex::new(Ring {
                entries: vec![None; CAPACITY].into_boxed_slice(),
                next: 0,
                count: 0,
                overwritten: 0,
            }),
            dropped: AtomicU64::new(0),
        }
    }
}
impl Recorder {
    pub fn record(&self, kind: Kind, fields: &[(Field, i64)]) {
        let at = Instant::now();
        let time = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_nanos()
            .min(i64::MAX as u128) as i64;
        let event = Event {
            at,
            time,
            kind,
            fields: std::array::from_fn(|i| fields.get(i).copied()),
        };
        let Ok(mut ring) = self.ring.try_lock() else {
            self.dropped.fetch_add(1, Ordering::Relaxed);
            return;
        };
        while ring.count > 0
            && ring.entries[(ring.next + CAPACITY - ring.count) % CAPACITY]
                .is_some_and(|e| at.saturating_duration_since(e.at) > WINDOW)
        {
            ring.count -= 1;
        }
        let next = ring.next;
        ring.entries[next] = Some(event);
        ring.next = (next + 1) % CAPACITY;
        if ring.count == CAPACITY {
            ring.overwritten += 1;
        } else {
            ring.count += 1;
        }
    }
    pub fn export(&self, directory: &Path, version: &str) -> Result<PathBuf, String> {
        let at = Instant::now();
        let mut raw = Vec::with_capacity(CAPACITY);
        let (overwritten, dropped) = {
            let ring = self.ring.lock().map_err(|_| "诊断记录不可用")?;
            for i in 0..ring.count {
                if let Some(event) =
                    ring.entries[(ring.next + CAPACITY - ring.count + i) % CAPACITY]
                    && at.saturating_duration_since(event.at) <= WINDOW
                {
                    raw.push(event);
                }
            }
            (ring.overwritten, self.dropped.load(Ordering::Relaxed))
        };
        let events = raw
            .into_iter()
            .map(|event| ExportEvent {
                time: timestamp(event.time),
                level: "INFO",
                message: event.kind,
                fields: event
                    .fields
                    .into_iter()
                    .flatten()
                    .map(|(key, value)| {
                        (
                            serde_json::to_value(key).unwrap().as_str().unwrap().into(),
                            value,
                        )
                    })
                    .collect(),
            })
            .collect();
        let export = Export {
            version: version.into(),
            platform: format!("{}/{}", std::env::consts::OS, std::env::consts::ARCH),
            exported_at: now(),
            window_seconds: WINDOW.as_secs(),
            capacity: CAPACITY,
            overwritten,
            contention_drops: dropped,
            events,
        };
        std::fs::create_dir_all(directory).map_err(|_| "无法创建诊断导出目录")?;
        if std::fs::symlink_metadata(directory)
            .map_err(|_| "无法检查诊断目录")?
            .file_type()
            .is_symlink()
        {
            return Err("诊断目录不能是符号链接".into());
        }
        let mut file = tempfile::Builder::new()
            .prefix("resona-diagnostics-")
            .suffix(".json")
            .tempfile_in(directory)
            .map_err(|_| "无法创建诊断文件")?;
        serde_json::to_writer_pretty(&mut file, &export).map_err(|_| "无法写入诊断文件")?;
        file.flush().map_err(|_| "无法完成诊断文件")?;
        file.keep()
            .map(|(_, path)| path)
            .map_err(|_| "无法保留诊断文件".into())
    }
    pub fn export_default(&self, version: &str) -> Result<PathBuf, String> {
        let root = dirs::cache_dir().ok_or("无法定位诊断导出目录")?;
        self.export(&root.join("resona/diagnostics"), version)
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn bounded_order_contention_and_only_explicit_export_writes() {
        let directory = tempfile::tempdir().unwrap();
        let recorder = Recorder::default();
        for n in 0..600 {
            recorder.record(Kind::VoiceApplied, &[(Field::Operation, n)]);
        }
        assert_eq!(std::fs::read_dir(directory.path()).unwrap().count(), 0);
        {
            let _guard = recorder.ring.lock().unwrap();
            recorder.record(Kind::Started, &[]);
        }
        let path = recorder.export(directory.path(), "test").unwrap();
        let data: serde_json::Value =
            serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
        assert_eq!(data["events"].as_array().unwrap().len(), 512);
        assert_eq!(data["overwritten"], 88);
        assert_eq!(data["contentionDrops"], 1);
        assert_eq!(data["events"][0]["fields"]["operation"], 88);
    }
}
