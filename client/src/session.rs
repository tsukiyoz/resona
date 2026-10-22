//! An authenticated application session. Unknown command outcomes are never
//! replayed on a replacement connection.
use crate::{
    config::{Identity, ServerProfile},
    noise::{self, CancellationToken, Connection},
    wire::{self, pb},
};
use prost::Message;
use std::{
    collections::VecDeque,
    future::Future,
    pin::Pin,
    sync::{
        Arc, Mutex,
        atomic::{AtomicU16, Ordering},
    },
    time::Duration,
};
use tokio::{
    sync::{mpsc, oneshot, watch},
    task::JoinHandle,
    time::{Instant, sleep_until, timeout_at},
};
use zeroize::{Zeroize, Zeroizing};

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Error {
    Transport(noise::Error),
    InvalidState,
    Rejected(u32),
    Cancelled,
    Timeout,
    Busy,
    InvalidInput,
    AuthenticationRejected,
}
pub type Result<T> = std::result::Result<T, Error>;
impl From<noise::Error> for Error {
    fn from(e: noise::Error) -> Self {
        Self::Transport(e)
    }
}
impl Error {
    pub fn retryable(&self) -> bool {
        matches!(
            self,
            Self::Timeout
                | Self::Transport(
                    noise::Error::Timeout
                        | noise::Error::IdleTimeout
                        | noise::Error::Closed
                        | noise::Error::Io(_)
                        | noise::Error::RemoteClosed(0)
                        | noise::Error::KeyConfirmationTimeout
                        | noise::Error::KeyExhausted
                )
        )
    }
    pub fn message(&self) -> &'static str {
        match self {
            Self::Transport(noise::Error::RemoteClosed(3)) => {
                "你已被管理员移出服务器，可手动重新连接"
            }
            Self::Transport(noise::Error::RemoteClosed(4)) => {
                "你的身份已被服务器封禁，请联系管理员"
            }
            Self::Transport(noise::Error::RemoteClosed(5)) => "服务器人数已满，请稍后手动重试",
            Self::Transport(noise::Error::RemoteClosed(6)) => {
                "账号已在另一台设备登录，请手动重新连接"
            }
            Self::Transport(noise::Error::RemoteClosed(7)) => {
                "账号登录已过期或凭据已更改，请重新登录"
            }
            Self::AuthenticationRejected | Self::Transport(noise::Error::RemoteClosed(2)) => {
                "服务器拒绝连接，请检查服务器密码"
            }
            Self::Transport(noise::Error::Authentication) => "服务器公钥验证失败，请检查连接信息",
            Self::InvalidState => "服务器响应无效，请确认客户端与服务器版本一致",
            Self::Cancelled => "操作已取消",
            Self::Timeout | Self::Transport(noise::Error::Timeout) => "操作超时，结果未确认",
            Self::Busy => "操作正在进行，请稍后重试",
            Self::InvalidInput => "请求参数无效",
            Self::Rejected(2) => "频道已更改，请返回当前频道后重试",
            Self::Rejected(3) => "操作过于频繁，请稍后重试",
            Self::Rejected(4) => "没有执行此操作的权限",
            Self::Rejected(5) => "频道内仍有成员，不能删除",
            Self::Rejected(6) => "默认频道不能删除",
            Self::Rejected(7) => "服务器保存失败，请确认当前状态后重试",
            Self::Rejected(8) => "频道数量或 ID 已达上限",
            Self::Rejected(9) => "消息送达结果未确认，重发可能重复",
            Self::Rejected(10) => "账号验证失败，请检查输入或重新登录",
            Self::Rejected(11) => "账号名称已被使用",
            Self::Rejected(12) => "账号服务繁忙，请稍后重试",
            Self::Rejected(_) => "服务器拒绝此操作，请检查权限和频道状态",
            _ => "服务器连接已关闭，请检查网络后重试",
        }
    }
}

#[derive(Clone, Debug)]
pub struct MemberEvent {
    pub serial: u64,
    pub kind: &'static str,
    pub channel: u32,
    pub epoch: u32,
    pub timestamp: String,
}
#[derive(Clone, Debug)]
pub struct Snapshot {
    pub state: Arc<pb::State>,
    pub messages: Arc<VecDeque<pb::Message>>,
    pub chat_status: pb::ChatStatus,
    pub closed: Option<Error>,
    pub events: Arc<VecDeque<MemberEvent>>,
}
impl Snapshot {
    pub fn channel(&self) -> u32 {
        self.state
            .members
            .iter()
            .find(|m| m.id == self.state.self_)
            .map_or(0, |m| m.channel)
    }
    pub fn bitrate(&self) -> u32 {
        self.state
            .channels
            .iter()
            .find(|c| c.id == self.channel())
            .map_or(48000, |c| if c.bitrate == 0 { 48000 } else { c.bitrate })
    }
}

#[derive(Clone)]
pub enum Command {
    Account(AccountRequest),
    Move(u16),
    Chat {
        channel: u16,
        text: String,
        id: String,
    },
    ChatSync(u16),
    VoiceState {
        muted: bool,
        deafened: bool,
    },
    Watch {
        channels: bool,
        members: bool,
    },
    ClaimOwner(String),
    CreateChannel {
        name: String,
        description: String,
        bitrate: u32,
    },
    UpdateChannel {
        id: u16,
        name: String,
        description: String,
        bitrate: u32,
    },
    DeleteChannel(u16),
    ReorderChannel {
        id: u16,
        before: u16,
    },
}
#[derive(Clone, Default)]
pub struct AccountRequest {
    pub action: String,
    pub username: String,
    pub password: Zeroizing<String>,
    pub token: Zeroizing<String>,
    pub new_password: Zeroizing<String>,
    pub nickname: String,
}
impl AccountRequest {
    /// Form validation shared by the desktop and UI-independent command entry.
    /// The server remains authoritative for credentials and account admission.
    pub fn validate(&self) -> std::result::Result<(), &'static str> {
        if matches!(self.action.as_str(), "register" | "password")
            && (!(3..=32).contains(&self.username.len())
                || !self
                    .username
                    .bytes()
                    .all(|c| c.is_ascii_alphanumeric() || b"_.-".contains(&c)))
        {
            return Err("用户名需为 3–32 位英文字母、数字、下划线、点或短横线");
        }
        let password = match self.action.as_str() {
            "register" => Some(self.password.as_str()),
            "change_password" => Some(self.new_password.as_str()),
            _ => None,
        };
        if let Some(password) = password {
            if !(8..=256).contains(&password.len()) || password.contains('\0') {
                return Err("密码需为 8–256 字节（英文字母、数字和符号各占 1 字节）");
            }
        }
        if matches!(self.action.as_str(), "password" | "change_password")
            && (self.password.is_empty()
                || self.password.len() > 256
                || self.password.contains('\0'))
        {
            return Err("请输入有效的密码，最多 256 字节");
        }
        if self.action == "token" && (self.token.is_empty() || self.token.len() > 256) {
            return Err("请输入有效的登录 Token");
        }
        if (self.action == "update_profile"
            || (self.action == "register" && !self.nickname.is_empty()))
            && (self.nickname.trim().is_empty()
                || self.nickname.chars().count() > 30
                || self.nickname.chars().any(char::is_control))
        {
            return Err("昵称需为 1–30 个字符，不能包含换行等控制字符");
        }
        Ok(())
    }
}
impl Command {
    fn frame(&self, id: u32) -> Result<Vec<u8>> {
        use pb::Kind;
        let data = match self {
            Self::Account(a)
                if a.action.len() <= 32
                    && a.username.len() <= 32
                    && a.password.len() <= 256
                    && a.token.len() <= 256
                    && a.new_password.len() <= 256
                    && a.nickname.len() <= 120 =>
            {
                let mut command = pb::AccountCommand {
                    action: a.action.clone(),
                    username: a.username.clone(),
                    password: a.password.to_string(),
                    token: a.token.to_string(),
                    new_password: a.new_password.to_string(),
                    nickname: a.nickname.clone(),
                };
                let frame = wire::pack(Kind::Account, id, &command);
                command.password.zeroize();
                command.token.zeroize();
                command.new_password.zeroize();
                frame
            }
            Self::Move(channel) | Self::ChatSync(channel) if *channel != 0 => wire::pack(
                if matches!(self, Self::Move(_)) {
                    Kind::Move
                } else {
                    Kind::ChatSync
                },
                id,
                &pb::Command {
                    channel: (*channel).into(),
                    ..Default::default()
                },
            ),
            Self::Chat {
                channel,
                text,
                id: message_id,
            } if *channel != 0
                && !text.trim().is_empty()
                && text.len() <= 8192
                && !text.contains('\0')
                && message_id.len() <= 128
                && !message_id.is_empty() =>
            {
                wire::pack(
                    Kind::Chat,
                    id,
                    &pb::Command {
                        channel: (*channel).into(),
                        text: text.clone(),
                        message_id: message_id.clone(),
                        ..Default::default()
                    },
                )
            }
            Self::VoiceState { muted, deafened } => wire::pack(
                Kind::VoiceState,
                id,
                &pb::Command {
                    muted: *muted,
                    deafened: *deafened,
                    ..Default::default()
                },
            ),
            Self::Watch { channels, members } if !members || *channels => wire::pack(
                Kind::WatchResources,
                id,
                &pb::WatchResources {
                    all_channels: *channels,
                    all_members: *members,
                },
            ),
            Self::ClaimOwner(token)
                if token.len() == 64 && token.bytes().all(|b| b.is_ascii_hexdigit()) =>
            {
                wire::pack(
                    Kind::ClaimOwner,
                    id,
                    &pb::ClaimOwner {
                        token: token.clone(),
                    },
                )
            }
            Self::CreateChannel {
                name,
                description,
                bitrate,
            } if valid_channel(name, description, *bitrate) => wire::pack(
                Kind::CreateChannel,
                id,
                &pb::CreateChannel {
                    name: name.clone(),
                    description: description.clone(),
                    bitrate: *bitrate,
                },
            ),
            Self::UpdateChannel {
                id: channel,
                name,
                description,
                bitrate,
            } if *channel != 0 && valid_channel(name, description, *bitrate) => wire::pack(
                Kind::UpdateChannel,
                id,
                &pb::UpdateChannel {
                    id: (*channel).into(),
                    name: name.clone(),
                    description: description.clone(),
                    bitrate: *bitrate,
                },
            ),
            Self::DeleteChannel(channel) if *channel != 0 => wire::pack(
                Kind::DeleteChannel,
                id,
                &pb::DeleteChannel {
                    id: (*channel).into(),
                },
            ),
            Self::ReorderChannel {
                id: channel,
                before,
            } if *channel != 0 => wire::pack(
                Kind::ReorderChannel,
                id,
                &pb::ReorderChannel {
                    id: (*channel).into(),
                    before_id: (*before).into(),
                },
            ),
            _ => return Err(Error::InvalidInput),
        };
        data.map_err(Error::from)
    }
    fn soft(&self) -> bool {
        matches!(
            self,
            Self::Chat { .. } | Self::ChatSync(_) | Self::Watch { .. }
        )
    }
}
fn valid_channel(name: &str, description: &str, bitrate: u32) -> bool {
    !name.trim().is_empty()
        && name.chars().count() <= 100
        && !name.chars().any(char::is_control)
        && description.len() <= 1024
        && !description.contains('\0')
        && (bitrate == 0 || ((16000..=64000).contains(&bitrate) && bitrate.is_multiple_of(1000)))
}

struct Request {
    command: Command,
    deadline: Instant,
    cancel: CancellationToken,
    reply: oneshot::Sender<Result<()>>,
}
struct Pending {
    id: u32,
    soft: bool,
    request: Request,
    code: Option<u32>,
    token: Zeroizing<String>,
}
#[derive(Clone)]
pub struct Handle {
    requests: mpsc::Sender<Request>,
    pub state: watch::Receiver<Snapshot>,
    credential: Arc<Mutex<Zeroizing<String>>>,
}
impl Handle {
    /// Explicit credential export for account UI and connection recovery. This
    /// secret is deliberately absent from every public state projection.
    pub fn account_token(&self) -> Zeroizing<String> {
        self.credential
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .clone()
    }
    pub async fn command(
        &self,
        command: Command,
        deadline: Instant,
        cancel: &CancellationToken,
    ) -> Result<()> {
        command.frame(1)?;
        let token = CancellationToken::new();
        let _guard = CancelOnDrop(token.clone());
        let (tx, rx) = oneshot::channel();
        tokio::select! {
            biased;
            _=cancel.cancelled()=> return Err(Error::Cancelled),
            r=timeout_at(deadline,self.requests.send(Request{command,deadline,cancel:token,reply:tx}))=>r.map_err(|_|Error::Timeout)?.map_err(|_|Error::Transport(noise::Error::Closed))?,
        }
        tokio::select! {
            biased;
            _=cancel.cancelled()=>Err(Error::Cancelled),
            r=timeout_at(deadline,rx)=>r.map_err(|_|Error::Timeout)?.map_err(|_|Error::Transport(noise::Error::Closed))?,
        }
    }
}
struct CancelOnDrop(CancellationToken);
impl Drop for CancelOnDrop {
    fn drop(&mut self) {
        self.0.cancel();
    }
}

pub struct VoicePacket {
    pub epoch: u32,
    pub sender_epoch: u32,
    pub sender: u16,
    pub instance: String,
    pub sequence: u16,
    pub end: bool,
    pub data: Vec<u8>,
    pub at: Instant,
}
#[derive(Clone)]
pub struct VoiceSender {
    sender: noise::VoiceSender,
    state: watch::Receiver<Snapshot>,
    sequence: Arc<AtomicU16>,
}
impl VoiceSender {
    pub fn send(&self, opus: &[u8]) -> noise::Result<()> {
        self.send_inner(None, opus)
    }
    /// A frame must retain the route for which it was captured and encoded.
    pub fn send_scoped(&self, epoch: u32, opus: &[u8]) -> noise::Result<()> {
        self.send_inner(Some(epoch), opus)
    }
    fn send_inner(&self, expected: Option<u32>, opus: &[u8]) -> noise::Result<()> {
        if opus.len() > 1024 {
            return Err(noise::Error::TooLarge);
        }
        let state = self.state.borrow();
        if state.closed.is_some() || expected.is_some_and(|epoch| epoch != state.state.epoch) {
            return Err(noise::Error::Closed);
        }
        let mut data = [0; 1031];
        data[0] = u8::from(opus.is_empty());
        data[1..5].copy_from_slice(&state.state.epoch.to_be_bytes());
        data[5..7].copy_from_slice(
            &self
                .sequence
                .fetch_add(1, Ordering::Relaxed)
                .wrapping_add(1)
                .to_be_bytes(),
        );
        data[7..7 + opus.len()].copy_from_slice(opus);
        self.sender.try_send(&data[..7 + opus.len()])
    }
}

pub struct Session {
    pub handle: Handle,
    pub voice_tx: VoiceSender,
    pub voice_rx: mpsc::Receiver<VoicePacket>,
    cancel: CancellationToken,
    task: Option<JoinHandle<()>>,
}
impl Drop for Session {
    fn drop(&mut self) {
        self.cancel.cancel();
    }
}
impl Session {
    pub async fn connect(
        profile: &ServerProfile,
        identity: &Identity,
        password: &str,
        cancel: &CancellationToken,
    ) -> Result<Self> {
        Self::connect_until(
            profile,
            identity,
            password,
            cancel,
            Instant::now() + Duration::from_secs(8),
        )
        .await
    }
    pub(crate) async fn connect_until(
        profile: &ServerProfile,
        identity: &Identity,
        password: &str,
        cancel: &CancellationToken,
        deadline: Instant,
    ) -> Result<Self> {
        profile.validate().map_err(|_| Error::InvalidInput)?;
        let endpoint = profile.endpoint().map_err(|_| Error::InvalidInput)?;
        let key = profile.public_key().map_err(|_| Error::InvalidInput)?;
        let mut addresses = tokio::select! {
            biased;
            _=cancel.cancelled()=>return Err(Error::Cancelled),
            r=timeout_at(deadline,tokio::net::lookup_host(endpoint))=>r.map_err(|_|Error::Timeout)?.map_err(|e|Error::from(noise::Error::Io(e.kind())))?,
        };
        let address = addresses.next().ok_or(Error::InvalidInput)?;
        let mut conn = timeout_at(deadline, Connection::dial(address, &key, cancel))
            .await
            .map_err(|_| Error::Timeout)??;
        let admitted = async {
            let hello = wire::sign_hello(
                identity.seed(),
                conn.channel_binding(),
                profile.nickname.clone(),
                password.into(),
            )?;
            wire::send(
                &conn.control_tx,
                pb::Kind::Hello,
                0,
                &hello,
                deadline,
                cancel,
            )
            .await?;
            let f = wire::FrameReader::default()
                .read(&mut conn.control_rx, deadline, cancel)
                .await?;
            if f.kind == pb::Kind::Reply as i32 {
                return Err(Error::AuthenticationRejected);
            }
            let state = pb::State::decode(f.body.as_slice()).map_err(|_| Error::InvalidState)?;
            if f.kind != pb::Kind::Welcome as i32
                || f.request != 0
                || !wire::valid_state(&state, &identity.uid())
            {
                return Err(Error::InvalidState);
            }
            if cancel.is_cancelled() {
                return Err(Error::Cancelled);
            }
            Ok(state)
        }
        .await;
        let state = match admitted {
            Ok(s) => s,
            Err(e) => {
                conn.close().await;
                return Err(e);
            }
        };
        let snapshot = Snapshot {
            events: Arc::new(VecDeque::new()),
            state: Arc::new(state),
            messages: Arc::new(VecDeque::new()),
            chat_status: pb::ChatStatus::default(),
            closed: None,
        };
        let (state_tx, state_rx) = watch::channel(snapshot.clone());
        let (request_tx, requests) = mpsc::channel(8);
        let (voice_out, voice_rx) = mpsc::channel(8);
        let cancel = CancellationToken::new();
        let worker_cancel = cancel.clone();
        let credential = Arc::new(Mutex::new(Zeroizing::new(String::new())));
        let driver_credential = credential.clone();
        let voice_tx = VoiceSender {
            sender: conn.voice_tx.clone(),
            state: state_rx.clone(),
            sequence: Arc::new(AtomicU16::new(0)),
        };
        let task = tokio::spawn(async move {
            let mut driver = Driver {
                snapshot,
                state_tx,
                requests,
                voice_out,
                pending: None,
                writer: None,
                next: 0,
                credential: driver_credential,
            };
            let result = driver.run(&mut conn, &worker_cancel).await;
            driver.writer.take();
            driver.snapshot.closed = Some(result.clone());
            driver.publish();
            if let Some(pending) = driver.pending.take() {
                let _ = pending.request.reply.send(Err(result));
            }
            conn.close().await;
        });
        Ok(Self {
            handle: Handle {
                requests: request_tx,
                state: state_rx,
                credential,
            },
            voice_tx,
            voice_rx,
            cancel,
            task: Some(task),
        })
    }
    pub async fn close(mut self) {
        self.cancel.cancel();
        if let Some(task) = self.task.take() {
            let _ = task.await;
        }
    }
}

struct Driver {
    snapshot: Snapshot,
    state_tx: watch::Sender<Snapshot>,
    requests: mpsc::Receiver<Request>,
    voice_out: mpsc::Sender<VoicePacket>,
    pending: Option<Pending>,
    writer: Option<Pin<Box<dyn Future<Output = noise::Result<()>> + Send>>>,
    next: u32,
    credential: Arc<Mutex<Zeroizing<String>>>,
}
impl Driver {
    fn publish(&self) {
        self.state_tx.send_replace(self.snapshot.clone());
    }
    async fn run(&mut self, conn: &mut Connection, cancel: &CancellationToken) -> Error {
        let mut reader = wire::FrameReader::default();
        loop {
            if cancel.is_cancelled() {
                return Error::Cancelled;
            }
            if self.pending.as_ref().is_some_and(|p| {
                p.request.cancel.is_cancelled() || p.request.deadline <= Instant::now()
            }) {
                let p = self.pending.take().unwrap();
                let error = if p.request.cancel.is_cancelled() {
                    Error::Cancelled
                } else {
                    Error::Timeout
                };
                let _ = p.request.reply.send(Err(error.clone()));
                if !p.soft || self.writer.is_some() {
                    return error;
                }
            }
            let deadline = self.pending.as_ref().map(|p| p.request.deadline);
            let request_cancel = self
                .pending
                .as_ref()
                .map(|p| p.request.cancel.clone())
                .unwrap_or_default();
            tokio::select! {
                _=cancel.cancelled()=>return Error::Cancelled,
                _=request_cancel.cancelled(),if self.pending.is_some()=>{}
                _=async {if let Some(d)=deadline {sleep_until(d).await;}else{std::future::pending::<()>().await;} }=>{}
                result=async {match self.writer.as_mut(){Some(w)=>w.await,None=>std::future::pending().await}}=>{
                    self.writer.take();
                    if let Err(e)=result{return e.into();}
                    self.finish_reply();
                }
                frame=reader.read(&mut conn.control_rx,Instant::now()+Duration::from_secs(86400*365),cancel)=>{
                    let f=match frame{Ok(f)=>f,Err(e)=>return e.into()};
                    if let Err(e)=self.apply(f){return e;}
                }
                voice=conn.voice_rx.recv(cancel)=>{
                    match voice{Ok(data)=>self.voice(data),Err(e)=>return e.into()}
                }
                request=self.requests.recv(),if self.pending.is_none() && self.writer.is_none()=>{
                    let Some(request)=request else {return Error::Cancelled;};
                    if request.cancel.is_cancelled() || request.deadline<=Instant::now(){let _=request.reply.send(Err(Error::Cancelled));continue;}
                    let Some(id)=self.next.checked_add(1) else {return Error::InvalidState;}; self.next=id;
                    let data=match request.command.frame(id){Ok(d)=>d,Err(e)=>{let _=request.reply.send(Err(e));continue;}};
                    let sender=conn.control_tx.clone();let deadline=request.deadline;let token=request.cancel.clone();
                    // Poll retransmission alongside receive work, so control ACK
                    // loss cannot stop draining incoming voice and resource events.
                    self.writer=Some(Box::pin(async move {sender.write(&data,deadline,&token).await}));
                    self.pending=Some(Pending{id,soft:request.command.soft(),request,code:None,token:Zeroizing::new(String::new())});
                }
            }
        }
    }
    fn finish_reply(&mut self) {
        if self.writer.is_none() && self.pending.as_ref().is_some_and(|p| p.code.is_some()) {
            let p = self.pending.take().unwrap();
            let code = p.code.unwrap();
            if code == 0
                && let Command::Account(a) = &p.request.command
            {
                let mut credential = self.credential.lock().unwrap_or_else(|e| e.into_inner());
                if a.action == "logout" {
                    credential.zeroize();
                } else if !p.token.is_empty() {
                    *credential = p.token;
                }
            }
            let _ = p.request.reply.send(if code == 0 {
                Ok(())
            } else {
                Err(Error::Rejected(code))
            });
        }
    }
    fn apply(&mut self, f: pb::Frame) -> Result<()> {
        match pb::Kind::try_from(f.kind).map_err(|_| Error::InvalidState)? {
            pb::Kind::State => {
                let s = pb::State::decode(f.body.as_slice()).map_err(|_| Error::InvalidState)?;
                let old = &self.snapshot.state;
                if !wire::valid_state(&s, &old.identity_uid)
                    || s.self_ != old.self_
                    || s.epoch < old.epoch
                    || (s.can_watch_resources && s.revision <= old.revision)
                {
                    return Err(Error::InvalidState);
                }
                if s.epoch != old.epoch {
                    self.snapshot.messages = Arc::new(VecDeque::new());
                    self.snapshot.chat_status = pb::ChatStatus::default();
                } else if s.all_members == old.all_members && s.all_channels == old.all_channels {
                    let channel = self.snapshot.channel();
                    let events = Arc::make_mut(&mut self.snapshot.events);
                    for (before, after, kind) in [
                        (&old.members, &s.members, "member_left"),
                        (&s.members, &old.members, "member_joined"),
                    ] {
                        for member in before
                            .iter()
                            .filter(|m| m.channel == channel && m.id != s.self_)
                        {
                            if !after
                                .iter()
                                .any(|m| m.instance == member.instance && m.channel == channel)
                            {
                                let serial = events.back().map_or(1, |e| e.serial + 1);
                                if events.len() == 64 {
                                    events.pop_front();
                                }
                                events.push_back(MemberEvent {
                                    serial,
                                    kind,
                                    channel,
                                    epoch: s.epoch,
                                    timestamp: crate::workspace::now(),
                                });
                            }
                        }
                    }
                }
                self.snapshot.state = Arc::new(s);
                self.publish();
            }
            pb::Kind::Reply => {
                let mut r =
                    pb::Reply::decode(f.body.as_slice()).map_err(|_| Error::InvalidState)?;
                if r.code > 255 || r.account_token.len() > 256 {
                    return Err(Error::InvalidState);
                }
                if self.pending.as_ref().is_some_and(|p| p.id == f.request) {
                    self.pending.as_mut().unwrap().code = Some(r.code);
                    self.pending.as_mut().unwrap().token =
                        Zeroizing::new(std::mem::take(&mut r.account_token));
                    self.finish_reply();
                }
            }
            pb::Kind::Message => {
                let m = pb::Message::decode(f.body.as_slice()).map_err(|_| Error::InvalidState)?;
                if m.epoch != self.snapshot.state.epoch {
                    return Ok(());
                }
                if m.channel != self.snapshot.channel()
                    || m.sequence == 0
                    || m.timestamp <= 0
                    || m.author_uid.len() != 64
                    || !m.author_uid.bytes().all(|b| b.is_ascii_hexdigit())
                    || m.text.len() > 8192
                    || m.nickname.len() > 120
                    || m.client_id.len() > 128
                {
                    return Err(Error::InvalidState);
                }
                let messages = Arc::make_mut(&mut self.snapshot.messages);
                if messages.iter().any(|old| old.sequence == m.sequence) {
                    return Ok(());
                }
                let index = messages.partition_point(|old| old.sequence < m.sequence);
                messages.insert(index, m);
                let mut size: usize = messages
                    .iter()
                    .map(|m| m.text.len() + m.nickname.len())
                    .sum();
                while messages.len() > 500 || size > 512 * 1024 {
                    if let Some(m) = messages.pop_front() {
                        size -= m.text.len() + m.nickname.len();
                    }
                }
                self.publish();
            }
            pb::Kind::ChatStatus => {
                let status =
                    pb::ChatStatus::decode(f.body.as_slice()).map_err(|_| Error::InvalidState)?;
                if status.status > 2 {
                    return Err(Error::InvalidState);
                }
                if status.epoch == self.snapshot.state.epoch
                    && status.channel == self.snapshot.channel()
                    && status.generation >= self.snapshot.chat_status.generation
                {
                    self.snapshot.chat_status = status;
                    self.publish();
                }
            }
            _ => return Err(Error::InvalidState),
        }
        Ok(())
    }
    fn voice(&self, data: Vec<u8>) {
        if data.len() < 13 || data.len() > 1037 || data[0] > 1 {
            return;
        }
        let epoch = u32::from_be_bytes(data[1..5].try_into().unwrap());
        let sequence = u16::from_be_bytes(data[5..7].try_into().unwrap());
        let sender = u16::from_be_bytes(data[7..9].try_into().unwrap());
        let sender_epoch = u32::from_be_bytes(data[9..13].try_into().unwrap());
        let end = data[0] == 1;
        if epoch != self.snapshot.state.epoch
            || sender == 0
            || sender as u32 == self.snapshot.state.self_
            || end != (data.len() == 13)
        {
            return;
        }
        let Some(member) = self.snapshot.state.members.iter().find(|m| {
            m.id == sender as u32 && m.epoch == sender_epoch && m.channel == self.snapshot.channel()
        }) else {
            return;
        };
        if let Ok(slot) = self.voice_out.try_reserve() {
            slot.send(VoicePacket {
                epoch,
                sender_epoch,
                sender,
                instance: member.instance.clone(),
                sequence,
                end,
                data: data[13..].to_vec(),
                at: Instant::now(),
            });
        }
    }
}

#[cfg(test)]
mod moderation_tests {
    use super::*;

    #[test]
    fn account_forms_report_input_rules_before_admission() {
        let mut request = AccountRequest {
            action: "register".into(),
            username: "tsukiyo".into(),
            nickname: "中文昵称".into(),
            password: "short-7".to_string().into(),
            ..Default::default()
        };
        assert!(request.validate().unwrap_err().contains("8–256 字节"));
        request.password = "exactly8".to_string().into();
        assert_eq!(request.validate(), Ok(()));
        request.username = "中文用户名".into();
        assert!(request.validate().unwrap_err().contains("用户名"));
        request.username = "Tsukiyo_1".into();
        request.nickname = "\n".into();
        assert!(request.validate().unwrap_err().contains("昵称"));
        request.nickname = "🙂".repeat(30);
        assert_eq!(request.validate(), Ok(()));
        request.nickname.push('🙂');
        assert!(request.validate().is_err());
        request.action = "password".into();
        request.password = "wrong".to_string().into();
        assert_eq!(request.validate(), Ok(()));
        request.action = "change_password".into();
        assert!(request.validate().unwrap_err().contains("8–256 字节"));
        request.new_password = "中文ab".to_string().into();
        assert_eq!(request.validate(), Ok(()));
        request.action = "token".into();
        assert!(request.validate().unwrap_err().contains("Token"));
    }

    #[test]
    fn explicit_membership_denials_never_trigger_reconnect() {
        for (code, message) in [(3, "移出"), (4, "封禁"), (5, "人数已满")] {
            let error = Error::Transport(noise::Error::RemoteClosed(code));
            assert!(!error.retryable());
            assert!(error.message().contains(message));
        }
        assert!(Error::Transport(noise::Error::RemoteClosed(0)).retryable());
    }
}
