//! A single owner serializes connection replacement and recovery. Presentation
//! observes snapshots; voice packets use a separate bounded handoff.
use crate::{
    config::{self, Identity, ServerProfile},
    noise::CancellationToken,
    session::{self, Command, Session},
};
use std::{sync::Arc, time::Duration};
use tokio::{
    sync::{Notify, mpsc, watch},
    task::JoinHandle,
    time::{Instant, sleep},
};
use zeroize::Zeroizing;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Mode {
    Offline,
    Connecting,
    Connected,
    Reconnecting,
    Failed,
}
impl Mode {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Offline => "offline",
            Self::Connecting => "connecting",
            Self::Connected => "connected",
            Self::Reconnecting => "reconnecting",
            Self::Failed => "failed",
        }
    }
}
#[derive(Clone)]
pub struct State {
    pub generation: u64,
    pub session_id: String,
    pub mode: Mode,
    pub profile: Option<ServerProfile>,
    pub attempt: u32,
    pub waiting: bool,
    pub slow: bool,
    pub recovered: bool,
    pub error: String,
    pub connection: Option<session::Handle>,
    pub voice: Option<session::VoiceSender>,
}
impl Default for State {
    fn default() -> Self {
        Self {
            generation: 0,
            session_id: String::new(),
            mode: Mode::Offline,
            profile: None,
            attempt: 0,
            waiting: false,
            slow: false,
            recovered: false,
            error: String::new(),
            connection: None,
            voice: None,
        }
    }
}
pub struct VoicePacket {
    pub generation: u64,
    pub packet: session::VoicePacket,
}
enum Control {
    Connect(ServerProfile, Zeroizing<String>),
    Disconnect,
    Retry(String),
}
#[derive(Clone)]
pub struct Handle {
    control: mpsc::Sender<Control>,
    pub state: watch::Receiver<State>,
}
impl Handle {
    pub async fn connect(
        &self,
        profile: ServerProfile,
        password: Zeroizing<String>,
    ) -> config::Result<()> {
        profile.validate()?;
        self.control
            .send(Control::Connect(profile, password))
            .await
            .map_err(|_| "客户端已关闭")
    }
    pub async fn disconnect(&self) -> config::Result<()> {
        self.control
            .send(Control::Disconnect)
            .await
            .map_err(|_| "客户端已关闭")
    }
    pub async fn retry(&self, id: String) -> config::Result<()> {
        self.control
            .send(Control::Retry(id))
            .await
            .map_err(|_| "客户端已关闭")
    }
    pub fn session(&self, id: &str) -> session::Result<session::Handle> {
        let state = self.state.borrow();
        if state.mode != Mode::Connected || state.session_id != id {
            return Err(session::Error::Cancelled);
        }
        state.connection.clone().ok_or(session::Error::Cancelled)
    }
}

pub struct Client {
    pub handle: Handle,
    pub voice_rx: mpsc::Receiver<VoicePacket>,
    cancel: CancellationToken,
    task: Option<JoinHandle<()>>,
}
impl Drop for Client {
    fn drop(&mut self) {
        self.cancel.cancel();
    }
}
impl Client {
    pub fn start(identity: Arc<Identity>) -> Self {
        let cancel = CancellationToken::new();
        let token = cancel.clone();
        let (control_tx, control) = mpsc::channel(8);
        let (state_tx, state) = watch::channel(State::default());
        let (voice_out, voice_rx) = mpsc::channel(8);
        let task = tokio::spawn(async move {
            let (progress_tx, progress) = mpsc::channel(8);
            Runner {
                identity,
                control,
                state_tx,
                state: State::default(),
                voice_out,
                session: None,
                observed: None,
                job: None,
                job_cancel: CancellationToken::new(),
                retry: Arc::new(Notify::new()),
                profile: None,
                password: Zeroizing::new(String::new()),
                progress_tx,
                progress,
            }
            .run(&token)
            .await;
        });
        Self {
            handle: Handle {
                control: control_tx,
                state,
            },
            voice_rx,
            cancel,
            task: Some(task),
        }
    }
    pub async fn close(mut self) {
        self.cancel.cancel();
        if let Some(task) = self.task.take() {
            let _ = task.await;
        }
    }
}

struct Progress {
    generation: u64,
    attempt: u32,
    waiting: bool,
    slow: bool,
}
struct Runner {
    identity: Arc<Identity>,
    control: mpsc::Receiver<Control>,
    state_tx: watch::Sender<State>,
    state: State,
    voice_out: mpsc::Sender<VoicePacket>,
    session: Option<Session>,
    observed: Option<watch::Receiver<session::Snapshot>>,
    job: Option<JoinHandle<session::Result<Established>>>,
    job_cancel: CancellationToken,
    retry: Arc<Notify>,
    profile: Option<ServerProfile>,
    password: Zeroizing<String>,
    progress_tx: mpsc::Sender<Progress>,
    progress: mpsc::Receiver<Progress>,
}
impl Runner {
    fn publish(&self) {
        self.state_tx.send_replace(self.state.clone());
    }
    async fn retire(&mut self) {
        self.state.connection = None;
        self.state.voice = None;
        self.observed = None;
        self.job_cancel.cancel();
        if let Some(job) = self.job.take()
            && let Ok(Ok(established)) = job.await
        {
            established.session.close().await;
        }
        if let Some(session) = self.session.take() {
            session.close().await;
        }
    }
    fn begin(&mut self, recover: bool, restore: Option<(u16, bool)>) {
        self.state.generation += 1;
        self.state.connection = None;
        self.state.voice = None;
        self.state.recovered = recover;
        self.state.mode = if recover {
            Mode::Reconnecting
        } else {
            Mode::Connecting
        };
        self.state.attempt = 0;
        self.state.waiting = false;
        self.state.slow = false;
        self.state.error = if recover {
            "连接中断，正在重连；恢复后麦克风保持静音".into()
        } else {
            String::new()
        };
        self.job_cancel = CancellationToken::new();
        self.retry = Arc::new(Notify::new());
        let plan = Plan {
            identity: self.identity.clone(),
            profile: self.profile.clone().unwrap(),
            password: self.password.clone(),
            cancel: self.job_cancel.clone(),
            retry: self.retry.clone(),
            progress: self.progress_tx.clone(),
            generation: self.state.generation,
            recover,
            restore,
        };
        self.job = Some(tokio::spawn(plan.run()));
        self.publish();
    }
    async fn run(mut self, cancel: &CancellationToken) {
        loop {
            tokio::select! {
                _=cancel.cancelled()=>break,
                control=self.control.recv()=>{
                    match control {
                        Some(Control::Connect(profile,password))=>{
                            self.retire().await;self.profile=Some(profile.clone());self.password=password;
                            self.state.profile=Some(profile);
                            match config::random_id(){Ok(id)=>{self.state.session_id=id;self.begin(false,None);},Err(_)=>self.fail("无法生成连接标识，请重新连接")}
                        }
                        Some(Control::Disconnect)=>{
                            self.state.generation+=1;self.state.mode=Mode::Offline;self.state.profile=None;self.state.session_id.clear();self.state.error.clear();self.state.waiting=false;self.state.slow=false;self.state.attempt=0;
                            self.state.connection=None;self.state.voice=None;self.publish();self.retire().await;self.profile=None;self.password=Zeroizing::new(String::new());
                        }
                        Some(Control::Retry(id)) if id==self.state.session_id=>{
                            if self.job.is_some() && self.state.mode==Mode::Reconnecting && self.state.waiting{self.retry.notify_waiters();}
                        }
                        Some(Control::Retry(_))=>{}
                        None=>break,
                    }
                }
                result=async {match self.job.as_mut(){Some(job)=>job.await,None=>std::future::pending().await}}=>{
                    self.job.take();
                    match result {
                        Ok(Ok(established))=>{
                            let session=established.session;
                            let id=match config::random_id(){Ok(id)=>id,Err(_)=>{session.close().await;self.fail("无法生成连接标识，请重新连接");continue;}};
                            self.observed=Some(session.handle.state.clone());self.state.connection=Some(session.handle.clone());self.state.voice=Some(session.voice_tx.clone());self.state.mode=Mode::Connected;
                            self.state.session_id=id;self.state.attempt=0;self.state.waiting=false;self.state.slow=false;
                            self.state.error=if self.state.recovered{if established.restored{"连接已恢复，麦克风保持静音，请确认后开启"}else{"连接已恢复，但原频道不可用；已留在当前频道并保持麦克风静音"}.into()}else{String::new()};self.session=Some(session);self.publish();
                        }
                        Ok(Err(error))=>self.fail(error.message()),
                        Err(_)=>self.fail("连接任务意外结束，请重新连接"),
                    }
                }
                Some(p)=self.progress.recv()=>{
                    if p.generation==self.state.generation && self.job.is_some(){self.state.attempt=p.attempt;self.state.waiting=p.waiting;self.state.slow=p.slow;
                        if p.slow{self.state.error="连接恢复失败，正在后台继续重试；可立即重试或退出服务器".into();}
                        self.publish();}
                }
                changed=async {match self.observed.as_mut(){Some(state)=>{state.changed().await.ok()?;Some(state.borrow().clone())},None=>std::future::pending().await}}=>{
                    if let Some(snapshot)=changed {
                        if let Some(error)=&snapshot.closed {
                            let retry=error.retryable();let channel=snapshot.channel() as u16;
                            let deafened=snapshot.state.members.iter().find(|m|m.id==snapshot.state.self_).is_some_and(|m|m.deafened);
                            self.retire().await;
                            if retry && !cancel.is_cancelled(){self.begin(true,Some((channel,deafened)));}
                            else{self.fail(error.message());}
                        } else {self.publish();}
                    }else{self.retire().await;self.begin(true,None);}
                }
                packet=async{match self.session.as_mut(){Some(session)=>session.voice_rx.recv().await,None=>std::future::pending().await}}=>{
                    if let Some(packet)=packet && let Ok(slot)=self.voice_out.try_reserve(){slot.send(VoicePacket{generation:self.state.generation,packet});}
                }
            }
        }
        self.retire().await;
    }
    fn fail(&mut self, message: &str) {
        self.password = Zeroizing::new(String::new());
        self.state.connection = None;
        self.state.voice = None;
        self.state.mode = Mode::Failed;
        self.state.error = message.into();
        self.state.waiting = false;
        self.state.slow = false;
        self.publish();
    }
}

struct Established {
    session: Session,
    restored: bool,
}
struct Plan {
    identity: Arc<Identity>,
    profile: ServerProfile,
    password: Zeroizing<String>,
    cancel: CancellationToken,
    retry: Arc<Notify>,
    progress: mpsc::Sender<Progress>,
    generation: u64,
    recover: bool,
    restore: Option<(u16, bool)>,
}
impl Plan {
    async fn progress(&self, attempt: u32, waiting: bool) -> session::Result<()> {
        tokio::select! {
            biased;
            _=self.cancel.cancelled()=>Err(session::Error::Cancelled),
            r=self.progress.send(Progress{generation:self.generation,attempt,waiting,slow:self.recover && attempt>3})=>r.map_err(|_|session::Error::Cancelled),
        }
    }
    async fn run(self) -> session::Result<Established> {
        for attempt in 1.. {
            if self.recover || attempt > 1 {
                let wake = self.retry.notified();
                tokio::pin!(wake);
                wake.as_mut().enable();
                self.progress(attempt, true).await?;
                let delay = if self.recover {
                    backoff(attempt)
                } else {
                    Duration::from_millis(300 * (attempt as u64 - 1))
                };
                tokio::select! {biased;_=self.cancel.cancelled()=>return Err(session::Error::Cancelled),_=&mut wake=>{},_=sleep(delay)=>{}}
            }
            self.progress(attempt, false).await?;
            let deadline = Instant::now() + Duration::from_secs(8);
            let result = Session::connect_until(
                &self.profile,
                &self.identity,
                &self.password,
                &self.cancel,
                deadline,
            )
            .await;
            match result {
                Ok(session) => {
                    let mut restored = true;
                    let current_channel = session.handle.state.borrow().channel();
                    if let Some((channel, _)) = self.restore
                        && channel as u32 != current_channel
                    {
                        let result = session
                            .handle
                            .command(Command::Move(channel), deadline, &self.cancel)
                            .await;
                        if let Err(error) = result {
                            if matches!(error, session::Error::Rejected(_)) {
                                restored = false;
                            } else {
                                session.close().await;
                                if !error.retryable() {
                                    return Err(error);
                                }
                                continue;
                            }
                        }
                    }
                    if let Some((_, true)) = self.restore
                        && let Err(error) = session
                            .handle
                            .command(
                                Command::VoiceState {
                                    muted: true,
                                    deafened: true,
                                },
                                deadline,
                                &self.cancel,
                            )
                            .await
                    {
                        session.close().await;
                        if !error.retryable() {
                            return Err(error);
                        }
                        continue;
                    }
                    if self.cancel.is_cancelled() {
                        session.close().await;
                        return Err(session::Error::Cancelled);
                    }
                    return Ok(Established { session, restored });
                }
                Err(error) if !error.retryable() || (!self.recover && attempt >= 3) => {
                    return Err(error);
                }
                Err(_) => {}
            }
        }
        unreachable!()
    }
}
fn backoff(attempt: u32) -> Duration {
    let seconds = match attempt {
        1 => 1,
        2 => 2,
        3 => 4,
        _ => 25,
    };
    let mut jitter = [100];
    let _ = getrandom::fill(&mut jitter);
    Duration::from_millis(seconds * 1000 * (100 + jitter[0] as u64 % 21) / 100)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn recovery_backoff_stays_in_the_product_bounds() {
        for attempt in 1..=20 {
            let base = match attempt {
                1 => 1,
                2 => 2,
                3 => 4,
                _ => 25,
            };
            for _ in 0..100 {
                let delay = backoff(attempt);
                assert!(delay >= Duration::from_secs(base));
                assert!(delay <= Duration::from_millis(base * 1200));
            }
        }
    }
    #[tokio::test]
    async fn retries_cannot_leave_a_permit_for_the_next_wait() {
        let retry = Notify::new();
        let waiting = retry.notified();
        tokio::pin!(waiting);
        waiting.as_mut().enable();
        retry.notify_waiters();
        retry.notify_waiters();
        waiting.await;
        retry.notify_waiters();
        assert!(
            tokio::time::timeout(Duration::from_millis(10), retry.notified())
                .await
                .is_err()
        );
    }
}
