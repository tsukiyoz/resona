//! In-process product API. Blocking OS calls never run on the UI or transport.
use crate::{
    audio, build_info::BuildInfo, config, credentials, diagnostics, lifecycle, model,
    noise::CancellationToken, resources, session, updates, workspace::Projection,
};
use serde_json::{Value, json};
use std::{
    path::PathBuf,
    sync::{
        Arc, Mutex,
        atomic::{AtomicU64, Ordering},
    },
    time::Duration,
};
use tokio::{
    sync::{Mutex as AsyncMutex, Semaphore, watch},
    task::JoinHandle,
    time::Instant,
};
use zeroize::Zeroizing;

pub type Result<T> = std::result::Result<T, String>;
#[derive(Clone)]
pub struct View {
    pub workspace: Arc<model::Workspace>,
    pub voice: Value,
    pub microphone_test: Value,
    pub resources: Value,
    pub update: Value,
    pub notifications: Value,
}
pub struct Options {
    #[cfg(test)]
    silent_devices: bool,
    pub directory: PathBuf,
    pub processor_library: PathBuf,
    pub info: BuildInfo,
    pub update: updates::Settings,
    pub credentials: Option<Box<dyn credentials::Backend + Send + Sync>>,
}
impl Options {
    pub fn local() -> Result<Self> {
        let info = BuildInfo::default();
        let distribution = std::env::current_exe()
            .ok()
            .map(|p| updates::distribution(&p, &info.version))
            .unwrap_or_default();
        Ok(Self {
            #[cfg(test)]
            silent_devices: false,
            directory: config::directory().map_err(str::to_owned)?,
            processor_library: audio::library_path(),
            credentials: None,
            update: updates::Settings {
                endpoint: env!("RESONA_UPDATE_URL").into(),
                public_key: env!("RESONA_UPDATE_PUBLIC_KEY").into(),
                current: info.version.clone(),
                distribution,
            },
            info,
        })
    }
}
struct Local {
    projection: Projection,
    preferences: audio::Config,
    monitor_restore: Option<audio::Config>,
    ready_generation: Option<u64>,
    closing: bool,
}
struct App {
    local: Mutex<Local>,
    credentials: Arc<Mutex<credentials::Store<Box<dyn credentials::Backend + Send>>>>,
    secure_storage: bool,
    credential_operation: AsyncMutex<()>,
    connection_operation: AsyncMutex<()>,
    connection_revision: AtomicU64,
    voice_operation: AsyncMutex<()>,
    life: lifecycle::Handle,
    audio: audio::Handle,
    sounds: audio::SoundHandle,
    interest: watch::Sender<resources::Interest>,
    updates: AsyncMutex<updates::Manager>,
    options: Options,
    processors: Vec<audio::OptionInfo>,
    recorder: Arc<diagnostics::Recorder>,
    publish: watch::Sender<View>,
    cancel: CancellationToken,
    slots: Arc<Semaphore>,
}
#[derive(Clone)]
pub struct Handle {
    app: Arc<App>,
    pub view: watch::Receiver<View>,
}
impl Handle {
    pub async fn request(&self, method: &str, params: Value) -> Result<Value> {
        let urgent = matches!(
            method,
            "DisconnectServer" | "PrepareShutdown" | "Shutdown" | "StopNotifications"
        ) || (method == "SetPushToTalk" && params["pressed"] == false);
        let _slot = if urgent {
            None
        } else {
            Some(
                self.app
                    .slots
                    .clone()
                    .try_acquire_owned()
                    .map_err(|_| "操作繁忙，请稍后重试")?,
            )
        };
        tokio::select! { biased;
            _=self.app.cancel.cancelled()=>Err("客户端已关闭".into()),
            result=self.app.dispatch(method,params)=>result,
        }
    }
}
pub struct Core {
    pub handle: Handle,
    cancel: CancellationToken,
    task: Option<JoinHandle<()>>,
}
impl Drop for Core {
    fn drop(&mut self) {
        self.cancel.cancel();
    }
}
impl Core {
    pub async fn start(mut options: Options) -> Result<Self> {
        let directory = options.directory.clone();
        let library = options.processor_library.clone();
        let (profiles, identity, factory) = tokio::task::spawn_blocking(move || {
            let profiles = config::ProfileStore::new(directory.join("servers.json"))
                .load()
                .map_err(str::to_owned)?;
            let identity = config::Identity::load_or_create(&directory.join("native-identity.key"))
                .map_err(str::to_owned)?;
            Ok::<_, String>((profiles, identity, audio::Factory::new(&library)))
        })
        .await
        .map_err(|_| "无法初始化本地配置")??;
        let mut client = lifecycle::Client::start(Arc::new(identity));
        let packets = client.take_voice_rx();
        let observer = resources::Observer::start(client.handle.state.clone());
        #[cfg(test)]
        let engine = if options.silent_devices {
            audio::Engine::start_silent(factory.clone())?
        } else {
            audio::Engine::start(factory.clone())?
        };
        #[cfg(not(test))]
        let engine = audio::Engine::start(factory.clone())?;
        let sounds = audio::Sounds::start()?;
        let updater = updates::Manager::new(options.update.clone());
        let update_status = updater.status.clone();
        let projection = Projection::new(profiles);
        let (publish, view) = watch::channel(View {
            workspace: Arc::new(projection.workspace.clone()),
            voice: json!(model::VoiceState::default()),
            microphone_test: json!(model::VoiceState::default()),
            resources: json!({"state":"idle","generation":0,"sessionID":"","error":""}),
            update: json!(*update_status.borrow()),
            notifications: json!({}),
        });
        let cancel = CancellationToken::new();
        let secure_storage =
            options.credentials.is_some() || credentials::Store::<credentials::Native>::available();
        let app = Arc::new(App {
            secure_storage,
            local: Mutex::new(Local {
                projection,
                preferences: audio::Config {
                    activation_mode: "continuous".into(),
                    vad_threshold_db: -40,
                    ..Default::default()
                },
                monitor_restore: None,
                ready_generation: None,
                closing: false,
            }),
            credentials: Arc::new(Mutex::new(credentials::Store::new(
                options
                    .credentials
                    .take()
                    .unwrap_or_else(|| Box::new(credentials::Native)),
            ))),
            credential_operation: AsyncMutex::new(()),
            connection_operation: AsyncMutex::new(()),
            connection_revision: AtomicU64::new(0),
            voice_operation: AsyncMutex::new(()),
            life: client.handle.clone(),
            audio: engine.handle.clone(),
            sounds: sounds.handle.clone(),
            interest: observer.interest_handle(),
            updates: AsyncMutex::new(updater),
            options,
            processors: factory.options(),
            recorder: Arc::new(diagnostics::Recorder::default()),
            publish,
            cancel: cancel.clone(),
            slots: Arc::new(Semaphore::new(8)),
        });
        let handle = Handle {
            app: app.clone(),
            view,
        };
        let task = tokio::spawn(run(
            app,
            client,
            observer,
            engine,
            sounds,
            packets,
            update_status,
        ));
        Ok(Self {
            handle,
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
async fn run(
    app: Arc<App>,
    client: lifecycle::Client,
    observer: resources::Observer,
    engine: audio::Engine,
    sounds: audio::Sounds,
    mut packets: tokio::sync::mpsc::Receiver<lifecycle::VoicePacket>,
    mut update: watch::Receiver<updates::Status>,
) {
    let mut life = app.life.state.clone();
    let mut audio = app.audio.state.clone();
    let mut resource = observer.status.clone();
    let mut observed: Option<watch::Receiver<session::Snapshot>> = None;
    let mut generation = u64::MAX;
    let mut started_generation = None;
    let mut previous_mode = lifecycle::Mode::Offline;
    let mut monitor_scope = (0, 0);
    let mut jobs = tokio::task::JoinSet::new();
    app.recorder.record(diagnostics::Kind::Started, &[]);
    loop {
        tokio::select! { biased;
            _=app.cancel.cancelled()=>break,
            result=life.changed()=>{if result.is_err(){break;} let current=life.borrow_and_update().clone();
                if current.mode!=previous_mode {
                    let kind=match current.mode {lifecycle::Mode::Connected=>diagnostics::Kind::ConnectionEstablished,lifecycle::Mode::Reconnecting=>diagnostics::Kind::ConnectionLost,lifecycle::Mode::Failed=>diagnostics::Kind::ConnectionFailed,lifecycle::Mode::Offline=>diagnostics::Kind::ConnectionCancelled,_=>diagnostics::Kind::ConnectionRequested};
                    app.recorder.record(kind,&[(diagnostics::Field::Generation,current.generation as i64),(diagnostics::Field::Attempt,current.attempt.into())]);previous_mode=current.mode;
                }
                if generation!=current.generation { generation=current.generation; observed=current.connection.as_ref().map(|c|c.state.clone());
                    monitor_scope=(generation,current.connection.as_ref().map_or(0,|c|c.state.borrow().state.epoch));
                    app.lock().ready_generation=None; }
                if current.mode==lifecycle::Mode::Connected && started_generation!=Some(current.generation){started_generation=Some(current.generation);observed=current.connection.as_ref().map(|c|c.state.clone());
                    let worker=app.clone();let expected=current.generation;jobs.spawn(async move{worker.start_connected(expected).await;}); }
                app.refresh(true);
            },
            result=async{match &mut observed{Some(rx)=>rx.changed().await,None=>std::future::pending().await}}=>{
                if result.is_err(){observed=None;}
                let scope=app.snapshot();let scope=(scope.0.generation,scope.1.as_ref().map_or(0,|s|s.state.epoch));
                if scope!=monitor_scope && app.lock().monitor_restore.is_some(){let worker=app.clone();jobs.spawn(async move{worker.stop_monitor_after_move(scope.0).await;});}
                monitor_scope=scope;app.refresh(true);
            },
            result=audio.changed()=>{if result.is_err(){break;} app.refresh(false);},
            result=resource.changed()=>{if result.is_err(){break;} let status=resource.borrow_and_update().clone();
                app.refresh(true);
                app.publish.send_modify(|v|{if !status.session_id.is_empty()&&(v.workspace.session.id!=status.session_id||!v.workspace.connected()){return;}let mut value=json!(status); if status.state=="ready"&&app.interest.borrow().active{value["workspace"]=json!(*v.workspace);}v.resources=value;}); },
            result=update.changed()=>{if result.is_err(){break;}app.publish.send_modify(|v|v.update=json!(*update.borrow_and_update()));},
            Some(packet)=packets.recv()=>{app.audio.push(packet);},
            Some(_)=jobs.join_next(),if !jobs.is_empty()=>{},
        }
    }
    {
        let mut local = app.lock();
        local.closing = true;
        local.ready_generation = None;
    }
    app.audio.ptt(false).ok();
    app.audio.route(audio::Route::default(), None).ok();
    app.sounds.stop();
    jobs.abort_all();
    while jobs.join_next().await.is_some() {}
    let _ = tokio::join!(
        observer.close(),
        client.close(),
        engine.close(),
        sounds.close()
    );
    app.updates.lock().await.stop().await;
    app.recorder.record(diagnostics::Kind::CoreStopped, &[]);
}
impl App {
    fn lock(&self) -> std::sync::MutexGuard<'_, Local> {
        self.local
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }
    fn snapshot(&self) -> (lifecycle::State, Option<session::Snapshot>) {
        let life = self.life.state.borrow().clone();
        let snapshot = life.connection.as_ref().map(|c| c.state.borrow().clone());
        (life, snapshot)
    }
    fn workspace(&self) -> Value {
        json!(*self.publish.borrow().workspace)
    }
    fn refresh(&self, workspace: bool) {
        let mut local = self.lock();
        let (life, snapshot) = self.snapshot();
        let state = self.audio.state.borrow().clone();
        let present = self.interest.borrow().active;
        let critical = local.projection.workspace.session.id != life.session_id
            || local.projection.workspace.session.mode != life.mode.as_str();
        if workspace {
            if present || critical {
                local.projection.apply(&life, snapshot.as_ref());
            } else if let Some(snapshot) = &snapshot {
                local.projection.observe_events(&life, snapshot);
            }
            let mut route = snapshot
                .as_ref()
                .filter(|_| life.mode == lifecycle::Mode::Connected)
                .map(|s| audio::Route::from_snapshot(life.generation, s))
                .unwrap_or_default();
            for (id, peer) in &mut route.peers {
                let _ = id;
                let (volume, muted) = local.projection.playback_values(&peer.instance);
                peer.volume = volume.into();
                peer.muted = muted;
            }
            let sender = if local.ready_generation == Some(life.generation)
                && local.monitor_restore.is_none()
            {
                life.voice.clone()
            } else {
                None
            };
            self.audio.route(route, sender).ok();
        }
        let mut voice = json!(state);
        voice["channelCodec"] = json!(4);
        let monitor = if local.monitor_restore.is_some() {
            voice.clone()
        } else {
            json!(model::VoiceState {
                generation: state.generation,
                operation: state.operation,
                applied_operation: state.applied_operation,
                ..Default::default()
            })
        };
        if local.monitor_restore.is_some() {
            voice["muted"] = json!(true);
            voice["active"] = json!(false);
        }
        self.publish.send_modify(|v| {
            if workspace && (present||critical) {
                local.projection.workspace.revision=local.projection.workspace.revision.wrapping_add(1);
                v.workspace = Arc::new(local.projection.workspace.clone());
            }
            v.voice = voice;
            v.microphone_test = monitor;
            v.notifications=json!({"session":local.projection.workspace.session,"notifications":local.projection.workspace.notifications});
        });
    }
    async fn start_connected(self: &Arc<Self>, generation: u64) {
        let config = {
            let mut local = self.lock();
            if self.life.state.borrow().generation != generation {
                return;
            }
            local.monitor_restore = None;
            let mut config = local.preferences.clone();
            config.enabled = true;
            config.muted = !config.auto_unmute_on_connect || self.life.state.borrow().recovered;
            config
        };
        if let Err(error) = self
            .configure(config.clone(), false, Some(generation))
            .await
            && !config.muted
            && self.life.state.borrow().generation == generation
        {
            let mut fallback = config;
            fallback.muted = true;
            if self
                .configure(fallback, false, Some(generation))
                .await
                .is_ok()
            {
                self.publish.send_modify(|v| {
                    v.voice["error"] = json!(format!("麦克风启动失败，已静音收听：{error}"))
                });
            }
        }
    }
    async fn configure(
        &self,
        config: audio::Config,
        monitor: bool,
        expected: Option<u64>,
    ) -> Result<Value> {
        let _operation = self.voice_operation.lock().await;
        if self.lock().monitor_restore.is_some() {
            return Err("请先停止麦克风试听".into());
        }
        self.configure_locked(config, monitor, expected).await
    }
    async fn configure_locked(
        &self,
        config: audio::Config,
        monitor: bool,
        expected: Option<u64>,
    ) -> Result<Value> {
        config.validate()?;
        let (life, _) = self.snapshot();
        if expected.is_some_and(|g| g != life.generation) || self.lock().closing {
            return Err("语音操作已取消".into());
        }
        let connected = life.mode == lifecycle::Mode::Connected;
        let session = life.connection.clone();
        let previous_ready = self.lock().ready_generation.take();
        self.refresh(true);
        let previous = self.audio.state.borrow().config.clone();
        if let Some(session) = &session
            && let Err(error) = session
                .command(
                    session::Command::VoiceState {
                        muted: monitor || config.muted || config.deafened || !config.enabled,
                        deafened: config.deafened,
                    },
                    Instant::now() + Duration::from_secs(3),
                    &self.cancel,
                )
                .await
        {
            if self.life.state.borrow().generation == life.generation {
                if matches!(
                    error,
                    session::Error::Busy
                        | session::Error::Rejected(_)
                        | session::Error::InvalidInput
                ) {
                    self.lock().ready_generation = previous_ready;
                } else {
                    let mut safe = previous.clone();
                    safe.muted = true;
                    let _ = self.audio.configure(safe, false).await;
                }
                self.refresh(true);
                self.publish
                    .send_modify(|v| v.voice["error"] = json!(error.message()));
            }
            return Err(error.message().into());
        }
        if self.life.state.borrow().generation != life.generation {
            return Err("连接已改变，语音设置未应用".into());
        }
        let result = self.audio.configure(config.clone(), monitor).await;
        if self.life.state.borrow().generation != life.generation {
            return Err("连接已改变，语音设置未应用".into());
        }
        match result {
            Ok(state) => {
                self.recorder.record(
                    diagnostics::Kind::VoiceApplied,
                    &[
                        (diagnostics::Field::Operation, state.operation as i64),
                        (diagnostics::Field::Generation, state.generation as i64),
                        (diagnostics::Field::Muted, state.config.muted as i64),
                        (diagnostics::Field::Deafened, state.config.deafened as i64),
                    ],
                );
                let mut local = self.lock();
                if !monitor {
                    local.preferences = config;
                }
                local.ready_generation = connected.then_some(life.generation);
                drop(local);
                self.refresh(true);
                Ok(json!(state))
            }
            Err(error) => {
                self.recorder.record(
                    diagnostics::Kind::VoiceFailed,
                    &[(diagnostics::Field::Generation, life.generation as i64)],
                );
                if let Some(session) = session
                    && session
                        .command(
                            session::Command::VoiceState {
                                muted: previous.muted || previous.deafened || !previous.enabled,
                                deafened: previous.deafened,
                            },
                            Instant::now() + Duration::from_secs(3),
                            &self.cancel,
                        )
                        .await
                        .is_ok()
                {
                    self.lock().ready_generation = connected.then_some(life.generation);
                }
                self.refresh(true);
                Err(error)
            }
        }
    }
    async fn command(&self, id: &str, command: session::Command) -> Result<()> {
        let handle = self.life.session(id).map_err(|e| e.message())?;
        let ordering = matches!(command, session::Command::ReorderChannel { .. });
        handle
            .command(
                command,
                Instant::now() + Duration::from_secs(5),
                &self.cancel,
            )
            .await
            .map_err(|e| {
                if ordering
                    && !matches!(
                        e,
                        session::Error::Rejected(_)
                            | session::Error::InvalidInput
                            | session::Error::Busy
                    )
                {
                    format!("排序结果尚未确认：{}", e.message())
                } else {
                    e.message().into()
                }
            })
    }
    async fn stop_monitor_after_move(&self, generation: u64) {
        let _operation = self.voice_operation.lock().await;
        let restore = self.lock().monitor_restore.clone();
        if let Some(config) = restore
            && self.life.state.borrow().generation == generation
            && self
                .configure_locked(config, false, Some(generation))
                .await
                .is_ok()
        {
            self.lock().monitor_restore = None;
            self.refresh(true);
        }
    }
    async fn stop_voice_and_disconnect(&self) -> Result<()> {
        {
            let mut local = self.lock();
            local.ready_generation = None;
            local.monitor_restore = None;
        }
        self.audio.ptt(false)?;
        self.audio.route(audio::Route::default(), None)?;
        self.sounds.stop();
        let mut life = self.life.state.clone();
        let generation = life.borrow().generation;
        self.life.disconnect().await.map_err(str::to_owned)?;
        tokio::time::timeout(Duration::from_secs(2), async {
            while life.borrow().generation <= generation {
                life.changed().await.map_err(|_| "客户端已关闭")?;
            }
            Ok::<_, &str>(())
        })
        .await
        .map_err(|_| "断开请求未确认")?
        .map_err(str::to_owned)?;
        let _voice = self.voice_operation.lock().await;
        let mut config = self.audio.state.borrow().config.clone();
        config.enabled = false;
        config.muted = true;
        self.audio.configure(config, false).await?;
        self.refresh(true);
        Ok(())
    }
    async fn dispatch(self: &Arc<Self>, method: &str, mut params: Value) -> Result<Value> {
        if self.lock().closing
            && !matches!(
                method,
                "PrepareShutdown"
                    | "Shutdown"
                    | "StopNotifications"
                    | "GetVoiceState"
                    | "GetWorkspace"
                    | "GetCapabilities"
                    | "GetCoreInfo"
                    | "GetMicrophoneTestState"
            )
        {
            return Err("客户端正在退出".into());
        }
        let string = |key: &str| {
            params
                .get(key)
                .and_then(Value::as_str)
                .unwrap_or("")
                .to_owned()
        };
        if matches!(method, "OpenPreview" | "LeavePreview" | "SendMessage")
            || method == "SelectChannel"
                && self.lock().projection.workspace.session.mode == "preview"
        {
            if !cfg!(debug_assertions) {
                return Err("本地预览仅用于调试构建".into());
            }
            if !matches!(
                self.life.state.borrow().mode,
                lifecycle::Mode::Offline | lifecycle::Mode::Failed
            ) {
                return Err("请先断开服务器".into());
            }
            self.lock()
                .projection
                .preview(method, &string("id"), &string("text"))?;
            self.refresh(true);
            return Ok(self.workspace());
        }
        match method {
            "GetCapabilities" => Ok(
                json!({"protocolVersion":1,"platform":std::env::consts::OS,"securePasswordStorage":self.secure_storage,"voice":true,"audioProcessors":self.processors}),
            ),
            "GetCoreInfo" => Ok(json!(self.options.info)),
            "GetWorkspace" => Ok(self.workspace()),
            "GetVoiceState" => Ok(self.publish.borrow().voice.clone()),
            "GetMicrophoneTestState" => Ok(self.publish.borrow().microphone_test.clone()),
            "GetAudioDevices" => tokio::task::spawn_blocking(audio::Device::list)
                .await
                .map_err(|_| "无法枚举音频设备")?
                .map(|v| json!(v)),
            "SetResourceInterest" => {
                let interest: resources::Interest =
                    serde_json::from_value(params).map_err(|_| "资源范围无效")?;
                if interest.all_members && !interest.all_channels {
                    return Err("成员关注需要频道范围".into());
                }
                self.audio.visible(interest.active);
                self.interest.send_replace(interest);
                if interest.active {
                    self.refresh(true);
                }
                Ok(json!(true))
            }
            "SaveServer"
            | "DeleteServer"
            | "ForgetServerPassword"
            | "GetServerCredentialStatus" => self.profile_command(method, params).await,
            "ConnectSavedServer" | "ConnectServerWithPassword" => {
                let id = string("id");
                let saved = method == "ConnectSavedServer";
                let password = Zeroizing::new(
                    params
                        .get_mut("password")
                        .and_then(|v| v.take().as_str().map(str::to_owned))
                        .unwrap_or_default(),
                );
                let remember = params["remember"].as_bool().unwrap_or(false);
                self.connect(id, password, remember, saved).await
            }
            "DisconnectServer" => {
                self.connection_revision.fetch_add(1, Ordering::AcqRel);
                let _lock = self.connection_operation.lock().await;
                self.stop_voice_and_disconnect().await?;
                Ok(self.workspace())
            }
            "RetryConnection" => {
                self.life
                    .retry(string("sessionID"))
                    .await
                    .map_err(str::to_owned)?;
                Ok(self.workspace())
            }
            "SelectChannel" => {
                let id = channel(&string("id"))?;
                let session = {
                    let mut local = self.lock();
                    if !local
                        .projection
                        .workspace
                        .session
                        .switching_channel_id
                        .is_empty()
                    {
                        return Err("频道切换中".into());
                    }
                    local.projection.workspace.session.switching_channel_id = id.to_string();
                    local.projection.workspace.session.id.clone()
                };
                self.refresh(true);
                let result = self.command(&session, session::Command::Move(id)).await;
                self.lock()
                    .projection
                    .workspace
                    .session
                    .switching_channel_id
                    .clear();
                self.refresh(true);
                result?;
                Ok(self.workspace())
            }
            "ClaimServerOwner" => {
                self.command(
                    &string("sessionID"),
                    session::Command::ClaimOwner(string("token")),
                )
                .await?;
                self.refresh(true);
                Ok(self.workspace())
            }
            "CreateChannel" | "UpdateChannel" | "DeleteChannel" | "ReorderChannel" => {
                let command = match method {
                    "CreateChannel" => session::Command::CreateChannel {
                        name: string("name"),
                        description: string("description"),
                        bitrate: params["bitrate"]
                            .as_u64()
                            .unwrap_or(48000)
                            .try_into()
                            .map_err(|_| "码率无效")?,
                    },
                    "UpdateChannel" => session::Command::UpdateChannel {
                        id: channel(&string("channelID"))?,
                        name: string("name"),
                        description: string("description"),
                        bitrate: params["bitrate"]
                            .as_u64()
                            .unwrap_or(48000)
                            .try_into()
                            .map_err(|_| "码率无效")?,
                    },
                    "ReorderChannel" => session::Command::ReorderChannel {
                        id: channel(&string("channelID"))?,
                        before: if string("beforeID").is_empty() {
                            0
                        } else {
                            channel(&string("beforeID"))?
                        },
                    },
                    _ => session::Command::DeleteChannel(channel(&string("channelID"))?),
                };
                self.command(&string("sessionID"), command).await?;
                Ok(json!({}))
            }
            "SendChannelMessage" | "RetryMessage" => {
                let message = {
                    let mut local = self.lock();
                    if method == "RetryMessage" {
                        local.projection.retry(
                            &string("id"),
                            params["allowDuplicate"].as_bool().unwrap_or(false),
                        )?
                    } else {
                        local.projection.enqueue(
                            &string("sessionID"),
                            &string("channelID"),
                            string("text"),
                        )?
                    }
                };
                let (life, snapshot) = self.snapshot();
                let scope = (
                    life.generation,
                    snapshot.as_ref().map_or(0, |s| s.state.epoch),
                );
                self.refresh(true);
                let result = match life.connection {
                    Some(handle) => {
                        handle
                            .command(
                                session::Command::Chat {
                                    channel: channel(&message.channel_id)?,
                                    text: message.text.clone(),
                                    id: message.id.clone(),
                                },
                                Instant::now() + Duration::from_secs(5),
                                &self.cancel,
                            )
                            .await
                    }
                    None => Err(session::Error::Cancelled),
                };
                self.lock().projection.delivered(scope, &message.id, result);
                self.refresh(true);
                Ok(self.workspace())
            }
            "RetryChannelChat" => {
                self.command(
                    &string("sessionID"),
                    session::Command::ChatSync(channel(&string("channelID"))?),
                )
                .await?;
                Ok(json!({}))
            }
            "SetUserPlayback" => {
                let id = string("userID");
                let instance = string("instance");
                let volume = params["volume"]
                    .as_i64()
                    .ok_or("缺少收听增益")?
                    .clamp(0, 794) as i32;
                let muted = params["muted"].as_bool().ok_or("缺少静音参数")?;
                {
                    let mut local = self.lock();
                    local.projection.playback(
                        &string("sessionID"),
                        &id,
                        &instance,
                        volume,
                        muted,
                    )?;
                }
                self.refresh(true);
                Ok(self.workspace())
            }
            "SetVoicePreferences" | "ConfigureVoice" => {
                let mut config: audio::Config =
                    serde_json::from_value(params).map_err(|_| "语音设置无效")?;
                if method == "SetVoicePreferences" {
                    if self.life.state.borrow().mode == lifecycle::Mode::Connected {
                        return Err("连接期间请使用语音设置".into());
                    }
                    config.enabled = false;
                    config.muted = true;
                }
                self.configure(config, false, None).await
            }
            "ConfigureMicrophoneTest" => {
                let _operation = self.voice_operation.lock().await;
                let config: audio::Config =
                    serde_json::from_value(params).map_err(|_| "麦克风设置无效")?;
                let generation = self.life.state.borrow().generation;
                if config.enabled {
                    let previous = self.lock().monitor_restore.clone();
                    if self.lock().monitor_restore.is_none() {
                        self.lock().monitor_restore =
                            Some(self.audio.state.borrow().config.clone());
                    }
                    let result = self.configure_locked(config, true, Some(generation)).await;
                    if result.is_err() && self.life.state.borrow().generation == generation {
                        self.lock().monitor_restore = previous;
                        self.refresh(true);
                    }
                    result
                } else {
                    let restore = self.lock().monitor_restore.clone();
                    match restore {
                        Some(config) => {
                            let result =
                                self.configure_locked(config, false, Some(generation)).await;
                            if result.is_ok() {
                                self.lock().monitor_restore = None;
                                self.refresh(true);
                            }
                            result.map(|_| self.publish.borrow().microphone_test.clone())
                        }
                        None => Ok(self.publish.borrow().microphone_test.clone()),
                    }
                }
            }
            "SetPushToTalk" => {
                self.audio
                    .ptt(params["pressed"].as_bool().unwrap_or(false))?;
                Ok(self.publish.borrow().voice.clone())
            }
            "SetInputGain" | "SetOutputGain" => {
                let _operation = self.voice_operation.lock().await;
                let mut config = self.audio.state.borrow().config.clone();
                let gain = params[if method == "SetInputGain" {
                    "inputGain"
                } else {
                    "volume"
                }]
                .as_i64()
                .ok_or("缺少音量")?;
                if method == "SetOutputGain" {
                    if self.life.state.borrow().session_id != string("sessionID") {
                        return Err("连接已改变".into());
                    }
                    config.volume = gain.clamp(0, 794) as i32;
                } else {
                    config.input_gain = gain.clamp(0, 200) as i32;
                }
                let value = if method == "SetOutputGain" {
                    config.volume
                } else {
                    config.input_gain
                };
                let monitor = self.lock().monitor_restore.is_some();
                self.configure_locked(config.clone(), monitor, None).await?;
                if monitor {
                    let mut local = self.lock();
                    if let Some(restore) = &mut local.monitor_restore {
                        if method == "SetOutputGain" {
                            restore.volume = config.volume;
                        } else {
                            restore.input_gain = config.input_gain;
                        }
                    }
                }
                Ok(json!(value))
            }
            "GetChannelDetails" | "GetUserDetails" => self.details(method, &params),
            "CancelDetails" => Ok(json!(true)),
            "GetIconResource" => Err("原生服务器不提供图标资源".into()),
            "PlayNotification" => {
                if !self.audio.state.borrow().config.deafened {
                    self.sounds.play(
                        &string("kind"),
                        params["volume"].as_i64().unwrap_or(60).clamp(0, 100) as i32,
                    )?;
                }
                Ok(json!({}))
            }
            "StopNotifications" => {
                self.sounds.stop();
                Ok(json!({}))
            }
            "ExportDiagnostics" => {
                let counters = self.audio.state.borrow().counters.clone();
                use diagnostics::Field as F;
                self.recorder.record(
                    diagnostics::Kind::VoiceCounters,
                    &[
                        (F::Received, counters.received as i64),
                        (F::Dropped, counters.dropped as i64),
                        (F::Decoded, counters.decoded as i64),
                        (F::DecodeErrors, counters.decode_errors as i64),
                        (F::Sent, counters.sent as i64),
                        (F::SendErrors, counters.send_errors as i64),
                    ],
                );
                let recorder = self.recorder.clone();
                let version = self.options.info.version.clone();
                tokio::task::spawn_blocking(move || {
                    recorder.export_default(&version).map(|p| json!(p))
                })
                .await
                .map_err(|_| "导出诊断失败")?
            }
            "GetUpdateStatus" => Ok(self.publish.borrow().update.clone()),
            "CheckUpdate" => {
                self.updates.lock().await.start()?;
                Ok(json!(true))
            }
            "CancelUpdate" => {
                self.updates.lock().await.cancel();
                Ok(json!(true))
            }
            "GetUpdateRelease" => Ok(json!({"url":self.updates.lock().await.release_page()?})),
            "DownloadUpdate" | "InstallUpdate" | "GetUpdatePackage" => {
                Err("此构建不支持应用内安装，请前往发布页手动更新".into())
            }
            "PrepareShutdown" => {
                self.connection_revision.fetch_add(1, Ordering::AcqRel);
                {
                    let mut local = self.lock();
                    local.closing = true;
                    local.ready_generation = None;
                }
                let _lock = self.connection_operation.lock().await;
                self.stop_voice_and_disconnect().await?;
                if params["notificationEnabled"].as_bool().unwrap_or(false) {
                    let _ = self
                        .sounds
                        .play_and_wait(
                            "disconnected",
                            params["notificationVolume"]
                                .as_i64()
                                .unwrap_or(60)
                                .clamp(0, 100) as i32,
                        )
                        .await;
                }
                Ok(json!({"ready":true}))
            }
            "Shutdown" => {
                self.cancel.cancel();
                Ok(json!({}))
            }
            _ => Err("不支持的客户端操作".into()),
        }
    }
    fn details(&self, method: &str, p: &Value) -> Result<Value> {
        let local = self.lock();
        let w = &local.projection.workspace;
        let id = p[if method == "GetChannelDetails" {
            "channelID"
        } else {
            "userID"
        }]
        .as_str()
        .unwrap_or("");
        if p["sessionID"].as_str() != Some(w.session.id.as_str()) || !w.connected() {
            return Err("连接已改变".into());
        }
        if method == "GetChannelDetails" {
            let c = w.channels.iter().find(|c| c.id == id).ok_or("频道不存在")?;
            Ok(
                json!({"id":c.id,"name":c.name,"description":c.description,"codec":format!("Opus · {} kbps",c.bitrate/1000),"members":c.members,"isDefault":c.is_default}),
            )
        } else {
            let u = w.users.iter().find(|u| u.id == id).ok_or("用户不存在")?;
            Ok(
                json!({"id":u.id,"nickname":u.nickname,"channelID":u.channel_id,"inputMuted":u.input_muted,"outputMuted":u.output_muted,"playbackVolume":u.playback_volume,"playbackMuted":u.playback_muted}),
            )
        }
    }
    async fn profile_command(self: &Arc<Self>, method: &str, p: Value) -> Result<Value> {
        let _operation = self.credential_operation.lock().await;
        let id = p["id"].as_str().unwrap_or("").to_owned();
        let current = self.lock().projection.workspace.servers.clone();
        let mut next = current.clone();
        if method == "GetServerCredentialStatus" {
            let profile = current
                .iter()
                .find(|s| s.id == id)
                .ok_or("服务器书签不存在")?
                .clone();
            let store = self.credentials.clone();
            return tokio::task::spawn_blocking(move || {
                let saved = store
                    .lock()
                    .map_err(|_| "密码存储不可用")?
                    .has(&profile.credential_key())
                    .map_err(|_| "无法读取已保存密码状态")?;
                Ok(json!({"saved":saved,"remember":!profile.skip_password_storage}))
            })
            .await
            .map_err(|_| "密码存储不可用")?;
        }
        let mut delete = None;
        match method {
            "SaveServer" => {
                let mut profile: config::ServerProfile =
                    serde_json::from_value(p["profile"].clone()).map_err(|_| "服务器信息无效")?;
                profile = profile.normalized();
                profile.validate().map_err(str::to_owned)?;
                if profile.id.is_empty() {
                    profile.id = config::random_id().map_err(str::to_owned)?;
                }
                if self
                    .life
                    .state
                    .borrow()
                    .profile
                    .as_ref()
                    .is_some_and(|s| s.id == profile.id)
                {
                    return Err("请退出服务器后编辑".into());
                }
                if let Some(old) = next.iter_mut().find(|s| s.id == profile.id) {
                    if old.destination() != profile.destination() {
                        delete = Some(old.credential_key());
                    }
                    profile.skip_password_storage = old.skip_password_storage;
                    *old = profile;
                } else {
                    if next.len() >= 256 {
                        return Err("服务器书签数量已达上限".into());
                    }
                    next.push(profile);
                }
            }
            "DeleteServer" => {
                if self
                    .life
                    .state
                    .borrow()
                    .profile
                    .as_ref()
                    .is_some_and(|s| s.id == id)
                {
                    return Err("请退出服务器后删除".into());
                }
                delete = Some(
                    next.iter()
                        .find(|s| s.id == id)
                        .ok_or("服务器书签不存在")?
                        .credential_key(),
                );
                next.retain(|s| s.id != id);
            }
            "ForgetServerPassword" => {
                let profile = next
                    .iter_mut()
                    .find(|s| s.id == id)
                    .ok_or("服务器书签不存在")?;
                delete = Some(profile.credential_key());
                profile.skip_password_storage = true;
            }
            _ => unreachable!(),
        }
        let store = self.credentials.clone();
        let directory = self.options.directory.clone();
        let persisted = next.clone();
        tokio::task::spawn_blocking(move || {
            if let Some(key) = delete {
                store
                    .lock()
                    .map_err(|_| "密码存储不可用")?
                    .delete(&key)
                    .map_err(|_| "无法清除已保存密码")?;
            }
            config::ProfileStore::new(directory.join("servers.json")).save(&persisted)
        })
        .await
        .map_err(|_| "无法保存服务器")?
        .map_err(str::to_owned)?;
        self.lock().projection.workspace.servers = next;
        self.refresh(true);
        Ok(self.workspace())
    }
    async fn connect(
        self: &Arc<Self>,
        id: String,
        password: Zeroizing<String>,
        remember: bool,
        saved: bool,
    ) -> Result<Value> {
        let already_connecting = {
            let state = self.life.state.borrow();
            state.profile.as_ref().is_some_and(|p| p.id == id)
                && matches!(
                    state.mode,
                    lifecycle::Mode::Connected | lifecycle::Mode::Connecting
                )
        };
        if already_connecting {
            return Ok(self.workspace());
        }
        let revision = self.connection_revision.fetch_add(1, Ordering::AcqRel) + 1;
        let _credentials = self.credential_operation.lock().await;
        let mut profile = self
            .lock()
            .projection
            .workspace
            .servers
            .iter()
            .find(|s| s.id == id)
            .cloned()
            .ok_or("服务器书签不存在")?;
        profile.validate().map_err(str::to_owned)?;
        if self.lock().closing {
            return Err("客户端正在退出".into());
        }
        let store = self.credentials.clone();
        let key = profile.credential_key();
        let secure_storage = self.secure_storage;
        let password = tokio::task::spawn_blocking(move || {
            let mut store = store.lock().map_err(|_| "密码存储不可用")?;
            if saved {
                store
                    .get(&key)
                    .map_err(|_| "无法读取已保存密码，请重新输入")
            } else {
                if remember && !secure_storage {
                    return Err("密码存储不可用，请取消记住密码后连接");
                }
                if !remember && secure_storage {
                    store.delete(&key).map_err(|_| "无法清除已保存密码")?;
                }
                Ok(password)
            }
        })
        .await
        .map_err(|_| "密码存储不可用")?
        .map_err(str::to_owned)?;
        if revision != self.connection_revision.load(Ordering::Acquire) || self.lock().closing {
            return Err("连接已取消".into());
        }
        if !saved {
            profile.skip_password_storage = !remember;
            let mut profiles = self.lock().projection.workspace.servers.clone();
            if let Some(p) = profiles.iter_mut().find(|p| p.id == id) {
                *p = profile.clone();
            }
            let directory = self.options.directory.clone();
            let persisted = profiles.clone();
            tokio::task::spawn_blocking(move || {
                config::ProfileStore::new(directory.join("servers.json")).save(&persisted)
            })
            .await
            .map_err(|_| "无法保存密码偏好")?
            .map_err(str::to_owned)?;
            self.lock().projection.workspace.servers = profiles;
        }
        let _connection = self.connection_operation.lock().await;
        if revision != self.connection_revision.load(Ordering::Acquire) || self.lock().closing {
            return Err("连接已取消".into());
        }
        let remember_password = if remember && !saved {
            Some(password.clone())
        } else {
            None
        };
        let mut accepted = self.life.state.clone();
        let previous_generation = accepted.borrow().generation;
        self.life
            .connect(profile.clone(), password)
            .await
            .map_err(str::to_owned)?;
        let accepted_generation = tokio::time::timeout(Duration::from_secs(2), async {
            loop {
                let state = accepted.borrow_and_update().clone();
                if state.generation > previous_generation
                    && state.profile.as_ref().is_some_and(|p| p.id == id)
                {
                    return Ok(state.generation);
                }
                accepted.changed().await.map_err(|_| "客户端已关闭")?;
            }
        })
        .await
        .map_err(|_| "连接请求未被核心确认")?
        .map_err(str::to_owned)?;
        self.refresh(true);
        if let Some(password) = remember_password {
            let app = self.clone();
            tokio::spawn(async move {
                app.remember(revision, accepted_generation, profile, password)
                    .await;
            });
        }
        Ok(self.workspace())
    }
    async fn remember(
        self: Arc<Self>,
        revision: u64,
        accepted_generation: u64,
        profile: config::ServerProfile,
        password: Zeroizing<String>,
    ) {
        let mut life = self.life.state.clone();
        loop {
            if self.cancel.is_cancelled()
                || revision != self.connection_revision.load(Ordering::Acquire)
            {
                return;
            }
            let state = life.borrow_and_update().clone();
            if state.generation != accepted_generation {
                return;
            }
            if state.generation == accepted_generation
                && state.profile.as_ref().is_some_and(|s| s.id == profile.id)
                && state.mode == lifecycle::Mode::Connected
            {
                break;
            }
            if state.mode == lifecycle::Mode::Failed {
                return;
            }
            tokio::select! {_=self.cancel.cancelled()=>return,r=life.changed()=>if r.is_err(){return;}}
        }
        let _operation = self.credential_operation.lock().await;
        if self.lock().closing
            || revision != self.connection_revision.load(Ordering::Acquire)
            || self.life.state.borrow().generation != accepted_generation
            || self.life.state.borrow().mode != lifecycle::Mode::Connected
        {
            return;
        }
        let store = self.credentials.clone();
        let result = tokio::task::spawn_blocking(move || {
            store
                .lock()
                .map_err(|_| credentials::Error::Unavailable)?
                .set(&profile.credential_key(), &password)
        })
        .await;
        if !matches!(result, Ok(Ok(())))
            && revision == self.connection_revision.load(Ordering::Acquire)
        {
            self.lock().projection.workspace.session.credential_error =
                "已连接，但密码未保存，请检查系统凭据存储".into();
            self.refresh(true);
        }
    }
}
fn channel(value: &str) -> Result<u16> {
    value
        .parse::<u16>()
        .ok()
        .filter(|v| *v != 0)
        .ok_or_else(|| "频道无效".into())
}

#[cfg(test)]
mod tests {
    use super::*;
    async fn test_core() -> (tempfile::TempDir, Core) {
        let directory = tempfile::tempdir().unwrap();
        let mut options = Options::local().unwrap();
        options.directory = directory.path().into();
        options.silent_devices = true;
        options.processor_library = PathBuf::from("/no-optional-library");
        let core = Core::start(options).await.unwrap();
        (directory, core)
    }
    #[tokio::test]
    async fn stopped_monitor_reply_is_independent_of_restored_voice() {
        let (_directory, core) = test_core().await;
        let handle = core.handle.clone();
        handle
            .request(
                "ConfigureMicrophoneTest",
                json!(audio::Config {
                    enabled: true,
                    muted: false,
                    ..Default::default()
                }),
            )
            .await
            .unwrap();
        let started = handle.view.borrow().microphone_test.clone();
        handle.app.lock().monitor_restore = Some(audio::Config {
            enabled: true,
            deafened: true,
            ..Default::default()
        });
        let stopped = handle
            .request("ConfigureMicrophoneTest", json!({"enabled":false}))
            .await
            .unwrap();
        assert_eq!(stopped["enabled"], false);
        assert_eq!(handle.view.borrow().microphone_test, stopped);
        assert!(stopped["operation"].as_u64() > started["operation"].as_u64());
        assert!(handle.app.audio.state.borrow().config.enabled);
        core.close().await;
    }
    #[tokio::test]
    async fn gain_only_updates_cannot_undo_a_queued_mute() {
        let (_directory, core) = test_core().await;
        let app = core.handle.app.clone();
        app.configure(
            audio::Config {
                muted: false,
                ..Default::default()
            },
            false,
            None,
        )
        .await
        .unwrap();
        let guard = app.voice_operation.lock().await;
        let muted = app.clone();
        let mute =
            tokio::spawn(
                async move { muted.configure(audio::Config::default(), false, None).await },
            );
        tokio::task::yield_now().await;
        let handle = core.handle.clone();
        let gain = tokio::spawn(async move {
            handle
                .request("SetInputGain", json!({"inputGain":125}))
                .await
        });
        tokio::task::yield_now().await;
        drop(guard);
        mute.await.unwrap().unwrap();
        gain.await.unwrap().unwrap();
        let config = app.audio.state.borrow().config.clone();
        assert!(config.muted);
        assert_eq!(config.input_gain, 125);
        core.close().await;
    }
    #[tokio::test]
    async fn failed_monitor_stop_is_retryable_and_shutdown_stops_capture() {
        let (_directory, core) = test_core().await;
        let handle = core.handle.clone();
        let app = handle.app.clone();
        let monitor = audio::Config {
            enabled: true,
            muted: false,
            ..Default::default()
        };
        handle
            .request("ConfigureMicrophoneTest", json!(monitor.clone()))
            .await
            .unwrap();
        assert!(app.audio.state.borrow().active);
        app.lock()
            .monitor_restore
            .as_mut()
            .unwrap()
            .processing
            .preprocess[0] = audio::Spec {
            name: "aec".into(),
            backend: "unavailable".into(),
            ..Default::default()
        };
        assert!(
            handle
                .request("ConfigureMicrophoneTest", json!({"enabled":false}))
                .await
                .is_err()
        );
        assert!(app.lock().monitor_restore.is_some());
        assert!(app.audio.state.borrow().active);
        app.lock().monitor_restore = Some(audio::Config::default());
        handle
            .request("ConfigureMicrophoneTest", json!({"enabled":false}))
            .await
            .unwrap();
        assert!(!app.audio.state.borrow().active);
        assert!(app.lock().monitor_restore.is_none());
        handle
            .request("ConfigureMicrophoneTest", json!(monitor))
            .await
            .unwrap();
        let permits = app.slots.clone().acquire_many_owned(8).await.unwrap();
        assert!(handle.request("GetWorkspace", json!({})).await.is_err());
        handle
            .request("PrepareShutdown", json!({"notificationEnabled":false}))
            .await
            .unwrap();
        assert!(!app.audio.state.borrow().active);
        assert!(!app.audio.state.borrow().config.enabled);
        assert!(app.lock().monitor_restore.is_none());
        drop(permits);
        assert!(
            handle
                .request("PlayNotification", json!({"kind":"connected","volume":0}))
                .await
                .is_err()
        );
        core.close().await;
    }
}
