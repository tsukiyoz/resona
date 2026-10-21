//! One worker owns devices, codecs and processing chains. Device callbacks only
//! copy bounded PCM buffers and wake this worker.
use super::{Captured, Config, Counters, Device, FRAME, Factory, Pipeline, Result, Route};
use crate::{lifecycle, session};
use serde::Serialize;
use std::{
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, AtomicU64, Ordering},
        mpsc,
    },
    thread::{self, Thread},
    time::Duration,
};
use tokio::{
    sync::{oneshot, watch},
    time::Instant,
};

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct State {
    #[serde(flatten)]
    pub config: Config,
    pub operation: u64,
    pub applied_operation: u64,
    pub generation: u64,
    pub active: bool,
    pub busy: bool,
    pub error: String,
    pub local_speaking: bool,
    #[serde(rename = "speakingClientIDs")]
    pub speaking_client_ids: Vec<String>,
    pub push_to_talk_pressed: bool,
    #[serde(rename = "inputLevelDB")]
    pub input_level_db: i32,
    pub counters: Counters,
}
impl Default for State {
    fn default() -> Self {
        Self {
            config: Config::default(),
            operation: 0,
            applied_operation: 0,
            generation: 0,
            active: false,
            busy: false,
            error: String::new(),
            local_speaking: false,
            speaking_client_ids: Vec::new(),
            push_to_talk_pressed: false,
            input_level_db: -60,
            counters: Counters::default(),
        }
    }
}
trait Stream: Send {
    fn failed(&self) -> bool;
    fn epoch(&mut self, epoch: u32);
    fn capture(&mut self, out: &mut Captured) -> bool;
    fn reference(&mut self, out: &mut [f32; FRAME * 2]);
    fn write(&mut self, samples: &[f32; FRAME * 2], monitor: bool) -> Result<()>;
}
impl Stream for Device {
    fn failed(&self) -> bool {
        Device::failed(self)
    }
    fn epoch(&mut self, epoch: u32) {
        Device::epoch(self, epoch)
    }
    fn capture(&mut self, out: &mut Captured) -> bool {
        Device::capture(self, out)
    }
    fn reference(&mut self, out: &mut [f32; FRAME * 2]) {
        Device::reference(self, out);
    }
    fn write(&mut self, samples: &[f32; FRAME * 2], monitor: bool) -> Result<()> {
        Device::write(self, samples, monitor).map(|_| ())
    }
}
type Open = Box<dyn FnMut(&Config, bool) -> Result<Box<dyn Stream>> + Send>;
struct Gate {
    epoch: u32,
    pressed: bool,
    enabled: bool,
    route: Route,
    sender: Option<session::VoiceSender>,
    revision: u64,
}
struct Shared {
    gate: Mutex<Gate>,
    cancelled: AtomicBool,
    operation: AtomicU64,
    visible: AtomicBool,
}
struct Apply {
    operation: u64,
    scope: (u64, u32),
    config: Config,
    monitor: bool,
    deadline: Instant,
    cancelled: Arc<AtomicBool>,
    reply: oneshot::Sender<Result<State>>,
}
struct CancelApply(Arc<AtomicBool>);
impl Drop for CancelApply {
    fn drop(&mut self) {
        self.0.store(true, Ordering::Release);
    }
}
#[derive(Clone)]
pub struct Handle {
    shared: Arc<Shared>,
    commands: tokio::sync::mpsc::Sender<Apply>,
    packets: mpsc::SyncSender<lifecycle::VoicePacket>,
    wake: Thread,
    pub state: watch::Receiver<State>,
}
impl Handle {
    pub async fn configure(&self, config: Config, monitor: bool) -> Result<State> {
        config.validate()?;
        let permit = self
            .commands
            .try_reserve()
            .map_err(|_| "音频设置队列不可用")?;
        let operation = self.shared.operation.fetch_add(1, Ordering::AcqRel) + 1;
        let cancelled = Arc::new(AtomicBool::new(false));
        let guard = CancelApply(cancelled.clone());
        let (reply, rx) = oneshot::channel();
        let scope = {
            let mut gate = self.shared.gate.lock().map_err(|_| "音频状态不可用")?;
            gate.enabled = false;
            gate.epoch = gate.epoch.wrapping_add(1);
            if !config.enabled
                || config.muted
                || config.deafened
                || config.activation_mode != self.state.borrow().config.activation_mode
            {
                gate.pressed = false;
            }
            (gate.route.generation, gate.route.epoch)
        };
        permit.send(Apply {
            operation,
            scope,
            config,
            monitor,
            deadline: Instant::now() + Duration::from_secs(15),
            cancelled,
            reply,
        });
        self.wake.unpark();
        let result = tokio::time::timeout(Duration::from_secs(15), rx)
            .await
            .map_err(|_| "音频设备响应超时")?
            .map_err(|_| "音频工作线程已关闭")?;
        drop(guard);
        result
    }
    pub fn route(&self, route: Route, sender: Option<session::VoiceSender>) -> Result<()> {
        let mut gate = self.shared.gate.lock().map_err(|_| "音频状态不可用")?;
        if !gate.route.same_scope(&route) || gate.sender.is_some() != sender.is_some() {
            gate.enabled = false;
            gate.epoch = gate.epoch.wrapping_add(1);
            gate.pressed = false;
        }
        gate.route = route;
        gate.sender = sender;
        gate.revision = gate.revision.wrapping_add(1);
        drop(gate);
        self.wake.unpark();
        Ok(())
    }
    pub fn push(&self, packet: lifecycle::VoicePacket) -> bool {
        if self.packets.try_send(packet).is_ok() {
            self.wake.unpark();
            true
        } else {
            false
        }
    }
    pub fn ptt(&self, pressed: bool) -> Result<()> {
        let mut gate = self.shared.gate.lock().map_err(|_| "音频状态不可用")?;
        if gate.pressed != pressed {
            gate.pressed = pressed;
            gate.epoch = gate.epoch.wrapping_add(1);
        }
        drop(gate);
        self.wake.unpark();
        Ok(())
    }
    pub fn visible(&self, visible: bool) {
        self.shared.visible.store(visible, Ordering::Release);
        self.wake.unpark();
    }
}
pub struct Engine {
    pub handle: Handle,
    done: Option<oneshot::Receiver<()>>,
}
impl Engine {
    #[cfg(test)]
    pub(crate) fn start_silent(factory: Factory) -> Result<Self> {
        struct Silent;
        impl Stream for Silent {
            fn failed(&self) -> bool {
                false
            }
            fn epoch(&mut self, _: u32) {}
            fn capture(&mut self, _: &mut Captured) -> bool {
                false
            }
            fn reference(&mut self, out: &mut [f32; FRAME * 2]) {
                out.fill(0.0);
            }
            fn write(&mut self, _: &[f32; FRAME * 2], _: bool) -> Result<()> {
                Ok(())
            }
        }
        Self::start_with(factory, Box::new(|_, _| Ok(Box::new(Silent))))
    }
    pub fn start(factory: Factory) -> Result<Self> {
        Self::start_with(
            factory,
            Box::new(|config, monitor| {
                Device::open(
                    !config.muted || monitor,
                    true,
                    &config.input_device_id,
                    &config.output_device_id,
                )
                .map(|device| Box::new(device) as Box<dyn Stream>)
            }),
        )
    }
    fn start_with(factory: Factory, open: Open) -> Result<Self> {
        let shared = Arc::new(Shared {
            gate: Mutex::new(Gate {
                epoch: 1,
                pressed: false,
                enabled: false,
                route: Route::default(),
                sender: None,
                revision: 0,
            }),
            cancelled: AtomicBool::new(false),
            operation: AtomicU64::new(0),
            visible: AtomicBool::new(true),
        });
        let (tx, commands) = tokio::sync::mpsc::channel(8);
        let (packet_tx, packets) = mpsc::sync_channel(128);
        let (state_tx, state) = watch::channel(State::default());
        let (done_tx, done) = oneshot::channel();
        let worker_shared = shared.clone();
        let worker = thread::Builder::new()
            .name("resona-audio".into())
            .spawn(move || {
                Worker {
                    shared: worker_shared,
                    commands,
                    packets,
                    state_tx,
                    state: State::default(),
                    factory,
                    open,
                    pipeline: None,
                    device: None,
                    monitor: false,
                    route_revision: u64::MAX,
                    capture_epoch: 0,
                    transmitting: false,
                    next_mix: Instant::now(),
                    next_publish: Instant::now(),
                }
                .run();
                let _ = done_tx.send(());
            })
            .map_err(|_| "无法启动音频工作线程")?;
        let wake = worker.thread().clone();
        // The worker retains its own resources until native callbacks have stopped.
        // Shutdown waits a bounded interval; it never frees a blocked driver's data.
        drop(worker);
        Ok(Self {
            handle: Handle {
                shared,
                commands: tx,
                packets: packet_tx,
                wake,
                state,
            },
            done: Some(done),
        })
    }
    pub async fn close(mut self) -> Result<()> {
        self.stop();
        if let Some(done) = self.done.take() {
            tokio::time::timeout(Duration::from_secs(3), done)
                .await
                .map_err(|_| "音频驱动仍在退出，清理将在后台继续")?
                .map_err(|_| "音频线程异常退出")?;
        }
        Ok(())
    }
    fn stop(&self) {
        self.handle.shared.cancelled.store(true, Ordering::Release);
        if let Ok(mut gate) = self.handle.shared.gate.lock() {
            gate.enabled = false;
            gate.epoch = gate.epoch.wrapping_add(1);
        }
        self.handle.wake.unpark();
    }
}
impl Drop for Engine {
    fn drop(&mut self) {
        self.stop();
    }
}
struct Worker {
    shared: Arc<Shared>,
    commands: tokio::sync::mpsc::Receiver<Apply>,
    packets: mpsc::Receiver<lifecycle::VoicePacket>,
    state_tx: watch::Sender<State>,
    state: State,
    factory: Factory,
    open: Open,
    pipeline: Option<Pipeline>,
    device: Option<Box<dyn Stream>>,
    monitor: bool,
    route_revision: u64,
    capture_epoch: u32,
    transmitting: bool,
    next_mix: Instant,
    next_publish: Instant,
}
impl Worker {
    fn current(&self, apply: &Apply) -> bool {
        !self.shared.cancelled.load(Ordering::Acquire)
            && !apply.cancelled.load(Ordering::Acquire)
            && self.shared.operation.load(Ordering::Acquire) == apply.operation
            && Instant::now() < apply.deadline
            && self
                .shared
                .gate
                .lock()
                .is_ok_and(|gate| (gate.route.generation, gate.route.epoch) == apply.scope)
    }
    fn publish(&mut self) {
        self.state_tx.send_replace(self.state.clone());
        self.next_publish = Instant::now() + Duration::from_millis(100);
    }
    fn fail(&mut self, error: String) {
        self.end();
        self.device = None;
        self.state.active = false;
        self.state.busy = false;
        self.state.error = error;
        self.state.local_speaking = false;
        self.state.speaking_client_ids.clear();
        self.state.input_level_db = -60;
        if let Ok(mut gate) = self.shared.gate.lock() {
            gate.enabled = false;
        }
        self.publish();
    }
    fn end(&mut self) {
        if self.transmitting {
            if let Ok(gate) = self.shared.gate.lock()
                && let Some(sender) = &gate.sender
                && let Some(pipeline) = &self.pipeline
            {
                let _ = sender.send_scoped(pipeline.route.epoch, &[]);
            }
            self.transmitting = false;
        }
    }
    fn apply(&mut self, apply: Apply) {
        if !self.current(&apply) {
            let _ = apply.reply.send(Err("音频设置已取消".into()));
            return;
        }
        self.state.operation = apply.operation;
        self.state.generation = apply.scope.0;
        self.state.busy = true;
        self.publish();
        let mut retired = false;
        let result = self.configure(&apply, &mut retired);
        if !self.current(&apply) {
            self.fail("音频设置已取消或超时".into());
            let _ = apply.reply.send(Err(self.state.error.clone()));
            return;
        }
        self.state.busy = false;
        match result {
            Ok(()) => {
                self.state.applied_operation = apply.operation;
                self.state.config = apply.config;
                self.state.error.clear();
                self.publish();
                let _ = apply.reply.send(Ok(self.state.clone()));
            }
            Err(error) => {
                if retired {
                    self.fail(error.clone());
                } else {
                    self.state.error = error.clone();
                    if let Err(error) = self.update_route() {
                        self.fail(error);
                    } else {
                        self.publish();
                    }
                }
                let _ = apply.reply.send(Err(error));
            }
        }
    }
    fn configure(&mut self, apply: &Apply, retired: &mut bool) -> Result<()> {
        let route = self
            .shared
            .gate
            .lock()
            .map_err(|_| "音频状态不可用")?
            .route
            .clone();
        let needed =
            apply.config.enabled && !apply.config.deafened && (route.channel != 0 || apply.monitor);
        let reopen = self
            .pipeline
            .as_ref()
            .is_none_or(|p| !p.config.same_devices(&apply.config))
            || self.monitor != apply.monitor
            || self.device.is_some() != needed;
        if reopen {
            let pipeline =
                Pipeline::new(apply.config.clone(), route.clone(), self.factory.clone())?;
            *retired = true;
            self.end();
            self.device = None;
            let device = if needed {
                Some((self.open)(&apply.config, apply.monitor)?)
            } else {
                None
            };
            if !self.current(apply) {
                drop(device);
                return Err("音频设置已取消".into());
            }
            self.device = device;
            self.pipeline = Some(pipeline);
        } else if let Some(pipeline) = &mut self.pipeline {
            pipeline.configure(apply.config.clone())?;
        }
        self.monitor = apply.monitor;
        self.state.active = apply.config.enabled && (route.channel != 0 || apply.monitor);
        if self.device.is_none() {
            self.state.local_speaking = false;
            self.state.speaking_client_ids.clear();
            self.state.input_level_db = -60;
            self.state.push_to_talk_pressed = false;
        }
        self.route_revision = u64::MAX;
        self.next_mix = Instant::now();
        self.update_route()?;
        Ok(())
    }
    fn update_route(&mut self) -> Result<()> {
        let (route, revision, epoch, pressed) = {
            let gate = self.shared.gate.lock().map_err(|_| "音频状态不可用")?;
            (
                (gate.revision != self.route_revision).then(|| gate.route.clone()),
                gate.revision,
                gate.epoch,
                gate.pressed,
            )
        };
        if let Some(route) = route {
            let mut changed = false;
            if let Some(pipeline) = &mut self.pipeline {
                if pipeline.route.generation != route.generation {
                    pipeline.config.muted = true;
                    self.state.config.muted = true;
                }
                self.state.generation = route.generation;
                changed = pipeline.route(route)?;
                if changed {
                    if self.monitor {
                        self.monitor = false;
                        pipeline.config.muted = true;
                        self.state.config.muted = true;
                    }
                    // Closing the old SPSC rings prevents buffered old-channel PCM
                    // from reaching a new route. Only this worker touches them.
                    self.device = None;
                    self.transmitting = false;
                    if pipeline.config.enabled
                        && !pipeline.config.deafened
                        && (pipeline.route.channel != 0 || self.monitor)
                    {
                        let device = (self.open)(&pipeline.config, self.monitor)?;
                        if self.shared.cancelled.load(Ordering::Acquire)
                            || self
                                .shared
                                .gate
                                .lock()
                                .map_err(|_| "音频状态不可用")?
                                .revision
                                != revision
                        {
                            drop(device);
                        } else {
                            self.device = Some(device);
                        }
                    }
                }
                self.state.active =
                    pipeline.config.enabled && (pipeline.route.channel != 0 || self.monitor);
            }
            self.route_revision = revision;
            if changed {
                self.state.local_speaking = false;
                self.state.speaking_client_ids.clear();
                self.state.input_level_db = -60;
                self.state.push_to_talk_pressed = false;
                self.publish();
            }
        }
        if self.capture_epoch != epoch {
            self.capture_epoch = epoch;
            if let Some(device) = &mut self.device {
                device.epoch(epoch);
            }
            if !pressed {
                if let Some(pipeline) = &mut self.pipeline {
                    pipeline.release_ptt();
                }
                self.end();
            }
        }
        if let Some(pipeline) = &self.pipeline {
            let mut gate = self.shared.gate.lock().map_err(|_| "音频状态不可用")?;
            if gate.revision == self.route_revision
                && !self.state.busy
                && !self.shared.cancelled.load(Ordering::Acquire)
                && self.shared.operation.load(Ordering::Acquire) == self.state.operation
            {
                gate.enabled = self.device.is_some()
                    && !self.monitor
                    && pipeline.config.enabled
                    && !pipeline.config.muted
                    && !pipeline.config.deafened;
            }
        }
        Ok(())
    }
    fn tick(&mut self) -> Result<()> {
        if self.shared.cancelled.load(Ordering::Acquire) {
            return Ok(());
        }
        let Some(device) = &mut self.device else {
            return Ok(());
        };
        if device.failed() {
            return Err("音频设备已断开，请重新选择设备".into());
        }
        let Some(pipeline) = &mut self.pipeline else {
            return Ok(());
        };
        let now = Instant::now();
        for _ in 0..128 {
            match self.packets.try_recv() {
                Ok(packet) => pipeline.push(packet, now),
                Err(_) => break,
            }
        }
        let mut captured = Captured::default();
        let mut reference = [0.0; FRAME * 2];
        let mut mono = [0.0; FRAME];
        let mut encoded = [0; 1024];
        for _ in 0..8 {
            if !device.capture(&mut captured) {
                break;
            }
            if captured.epoch != self.capture_epoch
                || !captured.age.is_finite()
                || captured.age > 0.1
                || captured.age < 0.0
            {
                pipeline.counters.capture_drops += 1;
                continue;
            }
            device.reference(&mut reference);
            for (i, frame) in reference.chunks_exact(2).enumerate() {
                mono[i] = (frame[0] + frame[1]) * 0.5;
            }
            let (epoch, pressed) = {
                let gate = self.shared.gate.lock().map_err(|_| "音频状态不可用")?;
                (gate.epoch, gate.pressed)
            };
            if epoch != captured.epoch {
                pipeline.counters.capture_drops += 1;
                continue;
            }
            let (packet, level) = pipeline.capture(
                &mut captured.samples,
                &mono,
                pressed,
                self.monitor,
                now,
                &mut encoded,
            )?;
            self.state.input_level_db = level;
            if self.monitor {
                let _gate = self.shared.gate.lock().map_err(|_| "音频状态不可用")?;
                if self.shared.cancelled.load(Ordering::Acquire) {
                    return Ok(());
                }
                let stereo = std::array::from_fn(|i| captured.samples[i / 2]);
                device.write(&stereo, true)?;
                continue;
            }
            let gate = self.shared.gate.lock().map_err(|_| "音频状态不可用")?;
            if gate.enabled
                && gate.epoch == epoch
                && gate.revision == self.route_revision
                && !self.shared.cancelled.load(Ordering::Acquire)
            {
                if let Some(n) = packet
                    && let Some(sender) = &gate.sender
                {
                    match sender.send_scoped(pipeline.route.epoch, &encoded[..n]) {
                        Ok(()) => {
                            pipeline.sent(&captured.samples, now);
                            self.transmitting = true;
                        }
                        Err(_) => pipeline.counters.send_errors += 1,
                    }
                } else if self.transmitting {
                    if let Some(sender) = &gate.sender {
                        let _ = sender.send_scoped(pipeline.route.epoch, &[]);
                    }
                    self.transmitting = false;
                }
            }
        }
        if now >= self.next_mix {
            self.next_mix = now + Duration::from_millis(20);
            let mut out = [0.0; FRAME * 2];
            pipeline.render(now, &mut out)?;
            let _gate = self.shared.gate.lock().map_err(|_| "音频状态不可用")?;
            if !self.shared.cancelled.load(Ordering::Acquire) {
                device.write(&out, false)?;
            }
        }
        if now >= self.next_publish && self.shared.visible.load(Ordering::Acquire) {
            let (local, peers) = pipeline.speaking(now);
            self.state.local_speaking = local;
            self.state.speaking_client_ids = peers.into_iter().map(|id| id.to_string()).collect();
            self.state.counters = pipeline.counters.clone();
            self.state.error = pipeline.error();
            self.state.push_to_talk_pressed = self
                .shared
                .gate
                .lock()
                .map_err(|_| "音频状态不可用")?
                .pressed;
            self.publish();
        }
        Ok(())
    }
    fn run(mut self) {
        while !self.shared.cancelled.load(Ordering::Acquire) {
            while let Ok(command) = self.commands.try_recv() {
                self.apply(command);
            }
            if self.shared.cancelled.load(Ordering::Acquire) {
                break;
            }
            if let Err(error) = self.update_route().and_then(|_| self.tick()) {
                self.fail(error);
            }
            // Native calls and other blocking primitives can consume an unpark
            // token. Recheck persistent work before the indefinite idle wait.
            if self.shared.cancelled.load(Ordering::Acquire) {
                break;
            }
            if !self.commands.is_empty() {
                continue;
            }
            if let Ok(gate) = self.shared.gate.lock()
                && (gate.revision != self.route_revision || gate.epoch != self.capture_epoch)
            {
                continue;
            }
            if self.device.is_some() {
                thread::park_timeout(self.next_mix.saturating_duration_since(Instant::now()));
            } else {
                while self.packets.try_recv().is_ok() {}
                thread::park();
            }
        }
        self.end();
        self.device = None;
        self.state.active = false;
        self.state.busy = false;
        self.state.local_speaking = false;
        self.state.speaking_client_ids.clear();
        self.state.input_level_db = -60;
        self.state.push_to_talk_pressed = false;
        self.publish();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{path::Path, sync::atomic::AtomicUsize};
    struct Fake(Arc<AtomicUsize>);
    impl Drop for Fake {
        fn drop(&mut self) {
            self.0.fetch_add(1, Ordering::Relaxed);
        }
    }
    impl Stream for Fake {
        fn failed(&self) -> bool {
            false
        }
        fn epoch(&mut self, _: u32) {}
        fn capture(&mut self, _: &mut Captured) -> bool {
            false
        }
        fn reference(&mut self, out: &mut [f32; FRAME * 2]) {
            out.fill(0.0);
        }
        fn write(&mut self, _: &[f32; FRAME * 2], _: bool) -> Result<()> {
            Ok(())
        }
    }
    fn factory() -> Factory {
        Factory::new(Path::new("/no-native-dsp"))
    }
    #[tokio::test]
    async fn offline_preferences_open_no_hardware_and_monitor_closes() {
        let opened = Arc::new(AtomicUsize::new(0));
        let closed = Arc::new(AtomicUsize::new(0));
        let a = opened.clone();
        let b = closed.clone();
        let engine = Engine::start_with(
            factory(),
            Box::new(move |_, _| {
                a.fetch_add(1, Ordering::Relaxed);
                Ok(Box::new(Fake(b.clone())))
            }),
        )
        .unwrap();
        let config = Config {
            enabled: true,
            ..Config::default()
        };
        engine
            .handle
            .configure(config.clone(), false)
            .await
            .unwrap();
        assert_eq!(opened.load(Ordering::Relaxed), 0);
        engine.handle.configure(config.clone(), true).await.unwrap();
        assert_eq!(opened.load(Ordering::Relaxed), 1);
        engine.handle.configure(config, false).await.unwrap();
        assert_eq!(closed.load(Ordering::Relaxed), 1);
        engine.close().await.unwrap();
    }
    #[tokio::test]
    async fn cancelled_native_open_never_publishes_late_success() {
        let (entered, ready) = oneshot::channel();
        let mut entered = Some(entered);
        let (release, wait) = mpsc::channel();
        let closed = Arc::new(AtomicUsize::new(0));
        let b = closed.clone();
        let engine = Engine::start_with(
            factory(),
            Box::new(move |_, _| {
                if let Some(tx) = entered.take() {
                    let _ = tx.send(());
                }
                wait.recv().unwrap();
                Ok(Box::new(Fake(b.clone())))
            }),
        )
        .unwrap();
        let handle = engine.handle.clone();
        let job = tokio::spawn(async move {
            handle
                .configure(
                    Config {
                        enabled: true,
                        ..Config::default()
                    },
                    true,
                )
                .await
        });
        ready.await.unwrap();
        job.abort();
        let _ = job.await;
        release.send(()).unwrap();
        let state = engine.handle.state.clone();
        engine.close().await.unwrap();
        assert_eq!(closed.load(Ordering::Relaxed), 1);
        assert!(!state.borrow().active);
    }
    #[tokio::test]
    async fn rejected_queue_does_not_invalidate_last_accepted_operation() {
        let (entered, ready) = oneshot::channel();
        let mut entered = Some(entered);
        let (release, wait) = mpsc::channel();
        let mut wait = Some(wait);
        let engine = Engine::start_with(
            factory(),
            Box::new(move |_, _| {
                if let Some(tx) = entered.take() {
                    let _ = tx.send(());
                }
                if let Some(wait) = wait.take() {
                    wait.recv().unwrap();
                }
                Ok(Box::new(Fake(Arc::new(AtomicUsize::new(0)))))
            }),
        )
        .unwrap();
        let h = engine.handle.clone();
        let first = tokio::spawn(async move {
            h.configure(
                Config {
                    enabled: true,
                    ..Config::default()
                },
                true,
            )
            .await
        });
        ready.await.unwrap();
        let mut jobs = Vec::new();
        for n in 0..8 {
            let h = engine.handle.clone();
            jobs.push(tokio::spawn(async move {
                h.configure(
                    Config {
                        enabled: true,
                        volume: 101 + n,
                        ..Config::default()
                    },
                    true,
                )
                .await
            }));
            while engine.handle.commands.capacity() != 7 - n as usize {
                tokio::task::yield_now().await;
            }
        }
        let operation = engine.handle.shared.operation.load(Ordering::Acquire);
        assert!(
            engine
                .handle
                .configure(Config::default(), false)
                .await
                .is_err()
        );
        assert_eq!(
            engine.handle.shared.operation.load(Ordering::Acquire),
            operation
        );
        release.send(()).unwrap();
        assert!(first.await.unwrap().is_err());
        for (i, job) in jobs.into_iter().enumerate() {
            assert_eq!(job.await.unwrap().is_ok(), i == 7);
        }
        assert_eq!(engine.handle.state.borrow().config.volume, 108);
        engine.close().await.unwrap();
    }
    #[tokio::test]
    async fn failed_candidate_preserves_audio_and_deafen_clears_ptt() {
        let closed = Arc::new(AtomicUsize::new(0));
        let b = closed.clone();
        let engine = Engine::start_with(
            factory(),
            Box::new(move |_, _| Ok(Box::new(Fake(b.clone())))),
        )
        .unwrap();
        engine
            .handle
            .route(
                Route {
                    generation: 1,
                    epoch: 1,
                    channel: 1,
                    ..Default::default()
                },
                None,
            )
            .unwrap();
        let config = Config {
            enabled: true,
            muted: false,
            activation_mode: "ptt".into(),
            ..Default::default()
        };
        let state = engine
            .handle
            .configure(config.clone(), false)
            .await
            .unwrap();
        assert!(state.active);
        let mut bad = config.clone();
        bad.processing.preprocess[0] = super::super::Spec {
            name: "aec".into(),
            backend: "unknown".into(),
            ..Default::default()
        };
        assert!(engine.handle.configure(bad, false).await.is_err());
        assert_eq!(closed.load(Ordering::Relaxed), 0);
        assert_eq!(engine.handle.state.borrow().config, config);
        assert!(engine.handle.state.borrow().active);
        engine.handle.ptt(true).unwrap();
        let mut deaf = config.clone();
        deaf.deafened = true;
        engine.handle.configure(deaf, false).await.unwrap();
        assert!(!engine.handle.shared.gate.lock().unwrap().pressed);
        engine.handle.configure(config, false).await.unwrap();
        assert!(!engine.handle.shared.gate.lock().unwrap().pressed);
        engine.handle.ptt(true).unwrap();
        engine
            .handle
            .route(
                Route {
                    generation: 2,
                    epoch: 2,
                    channel: 1,
                    ..Default::default()
                },
                None,
            )
            .unwrap();
        assert!(!engine.handle.shared.gate.lock().unwrap().pressed);
        let mut observed = engine.handle.state.clone();
        engine
            .handle
            .route(
                Route {
                    generation: 3,
                    ..Default::default()
                },
                None,
            )
            .unwrap();
        tokio::time::timeout(Duration::from_secs(1), async {
            while observed.borrow().generation != 3 {
                observed.changed().await.unwrap();
            }
        })
        .await
        .unwrap();
        assert!(!observed.borrow().active);
        assert!(observed.borrow().speaking_client_ids.is_empty());
        assert_eq!(observed.borrow().input_level_db, -60);
        engine.close().await.unwrap();
    }
    struct Counting {
        writes: Arc<AtomicUsize>,
    }
    impl Stream for Counting {
        fn failed(&self) -> bool {
            false
        }
        fn epoch(&mut self, _: u32) {}
        fn capture(&mut self, _: &mut Captured) -> bool {
            false
        }
        fn reference(&mut self, out: &mut [f32; FRAME * 2]) {
            out.fill(0.0);
        }
        fn write(&mut self, _: &[f32; FRAME * 2], _: bool) -> Result<()> {
            self.writes.fetch_add(1, Ordering::SeqCst);
            Ok(())
        }
    }
    #[tokio::test]
    async fn stop_during_route_open_never_writes_late_audio() {
        let (entered, ready) = oneshot::channel();
        let mut entered = Some(entered);
        let (release, wait) = mpsc::channel();
        let mut opens = 0;
        let writes = Arc::new(AtomicUsize::new(0));
        let w = writes.clone();
        let engine = Engine::start_with(
            factory(),
            Box::new(move |_, _| {
                opens += 1;
                if opens == 2 {
                    let _ = entered.take().unwrap().send(());
                    wait.recv().unwrap();
                    thread::park_timeout(Duration::from_millis(1));
                }
                Ok(Box::new(Counting { writes: w.clone() }))
            }),
        )
        .unwrap();
        engine
            .handle
            .route(
                Route {
                    generation: 1,
                    epoch: 1,
                    channel: 1,
                    ..Default::default()
                },
                None,
            )
            .unwrap();
        engine
            .handle
            .configure(
                Config {
                    enabled: true,
                    ..Default::default()
                },
                false,
            )
            .await
            .unwrap();
        engine
            .handle
            .route(
                Route {
                    generation: 1,
                    epoch: 2,
                    channel: 2,
                    ..Default::default()
                },
                None,
            )
            .unwrap();
        ready.await.unwrap();
        engine.stop();
        let before = writes.load(Ordering::SeqCst);
        release.send(()).unwrap();
        engine.close().await.unwrap();
        assert_eq!(writes.load(Ordering::SeqCst), before);
    }
    #[tokio::test]
    async fn session_replacement_rejects_pending_device_open() {
        let (entered, ready) = oneshot::channel();
        let mut entered = Some(entered);
        let (release, wait) = mpsc::channel();
        let closed = Arc::new(AtomicUsize::new(0));
        let b = closed.clone();
        let engine = Engine::start_with(
            factory(),
            Box::new(move |_, _| {
                if let Some(tx) = entered.take() {
                    let _ = tx.send(());
                }
                wait.recv().unwrap();
                Ok(Box::new(Fake(b.clone())))
            }),
        )
        .unwrap();
        engine
            .handle
            .route(
                Route {
                    generation: 1,
                    epoch: 1,
                    channel: 1,
                    ..Default::default()
                },
                None,
            )
            .unwrap();
        let h = engine.handle.clone();
        let job = tokio::spawn(async move {
            h.configure(
                Config {
                    enabled: true,
                    muted: false,
                    ..Config::default()
                },
                false,
            )
            .await
        });
        ready.await.unwrap();
        engine
            .handle
            .route(
                Route {
                    generation: 2,
                    epoch: 2,
                    channel: 1,
                    ..Default::default()
                },
                None,
            )
            .unwrap();
        release.send(()).unwrap();
        assert!(job.await.unwrap().is_err());
        assert_eq!(closed.load(Ordering::Relaxed), 1);
        assert_eq!(engine.handle.state.borrow().applied_operation, 0);
        engine.close().await.unwrap();
    }
}
