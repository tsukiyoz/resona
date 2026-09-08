use crate::{
    core::{CoreClient, Incoming},
    model::{
        AudioDevice, Capabilities, CredentialStatus, ServerProfile, User, VoiceState, Workspace,
    },
    preferences::Preferences,
};
use base64::{Engine as _, engine::general_purpose::STANDARD};
use chrono::{DateTime, Local, Utc};
use gpui::{
    AnyElement, Context, Entity, FocusHandle, Image, ImageFormat, InteractiveElement, IntoElement,
    ParentElement, Render, SharedString, StatefulInteractiveElement, Styled, Subscription, Window,
    div, img, prelude::*, px, rgb, svg,
};
use gpui_component::{
    Disableable, IconName, IconNamed, Selectable,
    button::{Button, ButtonVariants},
    checkbox::Checkbox,
    input::{Input, InputEvent, InputState},
    scroll::ScrollableElement,
    tooltip::Tooltip,
};
use serde_json::{Value, json};
use std::{
    collections::{HashMap, HashSet, VecDeque, hash_map::DefaultHasher},
    hash::{Hash, Hasher},
    sync::Arc,
    time::{Duration, Instant},
};

const BG: u32 = 0x171a1e;
const RAIL: u32 = 0x101214;
const PANEL: u32 = 0x1e2227;
const PANEL_2: u32 = 0x24292f;
const HOVER: u32 = 0x2b3239;
const LINE: u32 = 0x343b43;
const TEXT: u32 = 0xe9edf1;
const MUTED: u32 = 0x929ca7;
const ICE: u32 = 0x7db8e8;
const MINT: u32 = 0x79c9ad;
const AMBER: u32 = 0xd8aa5d;
const RED: u32 = 0xe28282;

gpui::actions!(resona, [DismissModal]);

#[derive(Clone, Copy)]
enum VoiceIcon {
    Mic,
    MicOff,
    Headphones,
    HeadphonesOff,
}

impl IconNamed for VoiceIcon {
    fn path(self) -> SharedString {
        match self {
            Self::Mic => "mic.svg",
            Self::MicOff => "mic-off.svg",
            Self::Headphones => "headphones.svg",
            Self::HeadphonesOff => "headphones-off.svg",
        }
        .into()
    }
}

#[derive(Clone)]
enum Pending {
    Workspace(&'static str),
    Credential(String),
    Connect(String, bool),
    Voice,
    Devices,
    Send(SubmittedDraft),
    Capabilities,
    Notification,
}

#[derive(Clone)]
struct SubmittedDraft {
    key: String,
    text: String,
    before_ids: HashSet<String>,
    retry_id: Option<String>,
}

#[derive(Clone)]
enum Modal {
    Server { editing_id: String },
    Password { server_id: String },
    Delete { server_id: String },
    Duplicate { message_id: String },
    Settings,
}

#[derive(Clone, Copy, PartialEq)]
enum DeviceMenu {
    Input,
    Output,
}

pub struct ResonaApp {
    core: Option<CoreClient>,
    workspace: Workspace,
    voice: VoiceState,
    capabilities: Capabilities,
    devices: Vec<AudioDevice>,
    icon_cache: HashMap<String, (u64, Arc<Image>)>,
    preferences: Preferences,
    seen_notifications: HashSet<String>,
    notification_order: VecDeque<String>,
    last_member_notification: Option<Instant>,
    pending: HashMap<u64, Pending>,
    drafts: HashMap<String, String>,
    submitted_drafts: HashMap<String, (String, String)>,
    selected_server: String,
    modal: Option<Modal>,
    device_menu: Option<DeviceMenu>,
    remember_password: bool,
    error: String,
    server_error: String,
    connect_error: String,
    focus: FocusHandle,
    name_input: Entity<InputState>,
    address_input: Entity<InputState>,
    nickname_input: Entity<InputState>,
    password_input: Entity<InputState>,
    chat_input: Entity<InputState>,
    _subscriptions: Vec<Subscription>,
}

impl ResonaApp {
    pub fn new(window: &mut Window, cx: &mut Context<Self>) -> Self {
        let name_input = cx.new(|cx| InputState::new(window, cx).placeholder("服务器名称"));
        let address_input =
            cx.new(|cx| InputState::new(window, cx).placeholder("voice.example.com:9987"));
        let nickname_input = cx.new(|cx| InputState::new(window, cx).placeholder("昵称"));
        let password_input = cx.new(|cx| {
            InputState::new(window, cx)
                .placeholder("服务器密码，可留空")
                .masked(true)
        });
        let chat_input = cx.new(|cx| {
            InputState::new(window, cx)
                .auto_grow(1, 5)
                .placeholder("发送到当前频道")
        });
        let chat_subscription = cx.subscribe_in(
            &chat_input,
            window,
            |this, input, event: &InputEvent, window, cx| {
                if matches!(event, InputEvent::PressEnter { secondary: false }) {
                    input.update(cx, |input, cx| {
                        let value = remove_inserted_newline(input.value().as_ref(), input.cursor());
                        input.set_value(value, window, cx);
                    });
                    this.send_message(window, cx);
                }
            },
        );
        let mut this = Self {
            core: None,
            workspace: Workspace::default(),
            voice: VoiceState::default(),
            capabilities: Capabilities::default(),
            devices: Vec::new(),
            icon_cache: HashMap::new(),
            preferences: Preferences::load(),
            seen_notifications: HashSet::new(),
            notification_order: VecDeque::new(),
            last_member_notification: None,
            pending: HashMap::new(),
            drafts: HashMap::new(),
            submitted_drafts: HashMap::new(),
            selected_server: String::new(),
            modal: None,
            device_menu: None,
            remember_password: false,
            error: String::new(),
            server_error: String::new(),
            connect_error: String::new(),
            focus: cx.focus_handle(),
            name_input,
            address_input,
            nickname_input,
            password_input,
            chat_input,
            _subscriptions: vec![chat_subscription],
        };
        this.focus.focus(window);
        match CoreClient::spawn() {
            Ok((core, rx)) => {
                this.core = Some(core);
                this.request(
                    "GetWorkspace",
                    json!({}),
                    Pending::Workspace("GetWorkspace"),
                );
                this.request("GetVoiceState", json!({}), Pending::Voice);
                this.request("GetAudioDevices", json!({}), Pending::Devices);
                this.request("GetCapabilities", json!({}), Pending::Capabilities);
                cx.spawn_in(window, async move |view, cx| {
                    while let Ok(incoming) = rx.recv().await {
                        if cx
                            .update(|window, cx| {
                                view.update(cx, |this, cx| {
                                    this.handle_incoming(incoming, window, cx)
                                })
                            })
                            .is_err()
                        {
                            break;
                        }
                    }
                })
                .detach();
            }
            Err(error) => this.error = error.to_string(),
        }
        this
    }

    fn request(&mut self, method: &'static str, params: Value, pending: Pending) {
        let Some(core) = &self.core else {
            self.error = "核心进程不可用".into();
            return;
        };
        match core.request(method, params) {
            Ok(id) => {
                self.pending.insert(id, pending);
            }
            Err(error) => self.error = error.to_string(),
        }
    }

    fn handle_incoming(&mut self, incoming: Incoming, window: &mut Window, cx: &mut Context<Self>) {
        match incoming {
            Incoming::Workspace(value) => self.apply_workspace(value, window, cx),
            Incoming::Voice(value) => self.apply_voice(value),
            Incoming::ProtocolError(error) => self.error = error,
            Incoming::Exited(error) => {
                self.error = error.clone();
                self.core = None;
                self.pending.clear();
                self.workspace.session.mode = "failed".into();
                self.workspace.session.error = error;
                self.workspace.session.switching_channel_id.clear();
                self.workspace.session.sending_message_id.clear();
                for channel in &mut self.workspace.channels {
                    channel.members = 0;
                }
                for message in &mut self.workspace.messages {
                    if message.status == "sending" {
                        message.status = "unconfirmed".into();
                        message.error = "核心退出，无法确认消息是否送达".into();
                    }
                }
                self.workspace.users.clear();
                self.voice.enabled = false;
                self.voice.active = false;
                self.voice.busy = false;
                self.voice.speaking_client_ids.clear();
                self.voice.local_speaking = false;
                self.device_menu = None;
            }
            Incoming::Response { id, result } => {
                let pending = self.pending.remove(&id);
                match (pending, result) {
                    (Some(Pending::Workspace(method)), Ok(value)) => {
                        self.apply_workspace(value, window, cx);
                        if matches!(method, "SaveServer" | "DeleteServer") {
                            self.modal = None;
                            self.server_error.clear();
                            self.error.clear();
                        }
                    }
                    (Some(Pending::Credential(server_id)), Ok(value)) => {
                        match serde_json::from_value::<CredentialStatus>(value) {
                            Ok(status)
                                if status.saved && self.capabilities.secure_password_storage =>
                            {
                                self.remember_password =
                                    status.remember && self.capabilities.secure_password_storage;
                                self.request(
                                    "ConnectSavedServer",
                                    json!({"id": server_id}),
                                    Pending::Connect(server_id, false),
                                );
                            }
                            Ok(status) => {
                                self.remember_password =
                                    status.remember && self.capabilities.secure_password_storage;
                                self.open_password(server_id, window, cx);
                            }
                            Err(error) => self.error = format!("无法读取密码状态：{error}"),
                        }
                    }
                    (Some(Pending::Connect(server_id, keep_modal)), Ok(value)) => {
                        self.apply_workspace(value, window, cx);
                        self.selected_server = server_id;
                        if !keep_modal || self.workspace.connected() {
                            self.close_modal(window, cx);
                        }
                        self.connect_error.clear();
                    }
                    (Some(Pending::Voice), Ok(value)) => self.apply_voice(value),
                    (Some(Pending::Devices), Ok(value)) => match serde_json::from_value(value) {
                        Ok(devices) => self.devices = devices,
                        Err(error) => self.error = format!("无法读取音频设备：{error}"),
                    },
                    (Some(Pending::Capabilities), Ok(value)) => match serde_json::from_value::<
                        Capabilities,
                    >(value)
                    {
                        Ok(capabilities) if capabilities.protocol_version == 1 => {
                            self.remember_password &= capabilities.secure_password_storage;
                            self.capabilities = capabilities;
                        }
                        Ok(capabilities) => {
                            self.error =
                                format!("不支持的核心协议版本：{}", capabilities.protocol_version)
                        }
                        Err(error) => self.error = format!("无法读取核心能力：{error}"),
                    },
                    (Some(Pending::Notification), Ok(_)) => {}
                    (Some(Pending::Send(submitted)), Ok(value)) => {
                        self.register_submitted_draft(&value, submitted);
                        self.apply_workspace(value, window, cx);
                    }
                    (Some(Pending::Connect(server_id, _)), Err(error)) => {
                        self.connect_error = error;
                        if !matches!(&self.modal, Some(Modal::Password { server_id: open }) if open == &server_id)
                        {
                            self.open_password(server_id, window, cx);
                        } else {
                            self.password_input
                                .update(cx, |input, cx| input.focus(window, cx));
                        }
                    }
                    (Some(Pending::Credential(server_id)), Err(error)) => {
                        self.connect_error = error;
                        self.open_password(server_id, window, cx);
                    }
                    (Some(Pending::Workspace("SaveServer")), Err(error)) => {
                        self.server_error = error;
                    }
                    (Some(_), Err(error)) | (None, Err(error)) => self.error = error,
                    (None, Ok(_)) => {}
                }
            }
        }
        cx.notify();
    }

    fn apply_workspace(&mut self, value: Value, window: &mut Window, cx: &mut Context<Self>) {
        match serde_json::from_value::<Workspace>(value) {
            Ok(workspace) => {
                let previous_key = draft_key(&self.workspace);
                let next_key = draft_key(&workspace);
                if previous_key != next_key {
                    self.drafts
                        .insert(previous_key, self.chat_input.read(cx).value().to_string());
                    let draft = self.drafts.get(&next_key).cloned().unwrap_or_default();
                    self.chat_input
                        .update(cx, |input, cx| input.set_value(draft, window, cx));
                }
                if self.selected_server.is_empty() {
                    self.selected_server = if workspace.session.server_id.is_empty() {
                        workspace
                            .servers
                            .first()
                            .map(|s| s.id.clone())
                            .unwrap_or_default()
                    } else {
                        workspace.session.server_id.clone()
                    };
                }
                self.update_icon_cache(&workspace);
                self.process_notifications(&workspace);
                self.reconcile_submitted_drafts(&workspace, window, cx);
                if let Some(Modal::Password { server_id }) = &self.modal
                    && workspace.session.server_id == *server_id
                {
                    if workspace.connected() {
                        self.password_input
                            .update(cx, |input, cx| input.set_value("", window, cx));
                        self.modal = None;
                        self.connect_error.clear();
                        self.focus.focus(window);
                    } else if workspace.session.mode == "failed" {
                        self.connect_error = if workspace.session.credential_error.is_empty() {
                            workspace.session.error.clone()
                        } else {
                            workspace.session.credential_error.clone()
                        };
                    }
                }
                self.workspace = workspace;
            }
            Err(error) => self.error = format!("核心工作区格式无效：{error}"),
        }
    }

    fn apply_voice(&mut self, value: Value) {
        match serde_json::from_value::<VoiceState>(value) {
            Ok(voice) => self.voice = voice,
            Err(error) => self.error = format!("核心语音状态格式无效：{error}"),
        }
    }

    fn register_submitted_draft(&mut self, value: &Value, submitted: SubmittedDraft) {
        let Ok(workspace) = serde_json::from_value::<Workspace>(value.clone()) else {
            return;
        };
        let message = submitted
            .retry_id
            .as_ref()
            .and_then(|id| workspace.messages.iter().find(|message| message.id == *id))
            .or_else(|| {
                workspace
                    .messages
                    .iter()
                    .find(|message| message.id == workspace.session.sending_message_id)
            })
            .or_else(|| {
                workspace.messages.iter().find(|message| {
                    !submitted.before_ids.contains(&message.id)
                        && (workspace.session.mode == "preview"
                            || message.author_id == workspace.session.self_id)
                        && message.channel_id == workspace.session.channel_id
                        && message.text == submitted.text
                })
            });
        if let Some(message) = message {
            self.submitted_drafts.insert(
                message.id.clone(),
                (submitted.key.clone(), submitted.text.clone()),
            );
        }
    }

    fn reconcile_submitted_drafts(
        &mut self,
        workspace: &Workspace,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let current_key = draft_key(workspace);
        let submitted = self
            .submitted_drafts
            .iter()
            .map(|(id, draft)| (id.clone(), draft.clone()))
            .collect::<Vec<_>>();
        for (id, (key, text)) in submitted {
            let Some(message) = workspace.messages.iter().find(|message| message.id == id) else {
                continue;
            };
            match message.status.as_str() {
                "sent" => {
                    if self.drafts.get(&key).is_some_and(|draft| draft == &text) {
                        self.drafts.remove(&key);
                    }
                    if current_key == key && self.chat_input.read(cx).value().as_ref() == text {
                        self.chat_input
                            .update(cx, |input, cx| input.set_value("", window, cx));
                    }
                    self.submitted_drafts.remove(&id);
                }
                "failed" | "unconfirmed" => {
                    self.drafts.entry(key).or_insert(text);
                    self.submitted_drafts.remove(&id);
                }
                _ => {}
            }
        }
    }

    fn selected_profile(&self) -> Option<&ServerProfile> {
        self.workspace
            .servers
            .iter()
            .find(|s| s.id == self.selected_server)
    }

    fn is_busy(&self) -> bool {
        self.pending
            .values()
            .any(|pending| !matches!(pending, Pending::Notification))
    }

    fn speaking_transition_pending(&self) -> bool {
        self.pending.values().any(|pending| match pending {
            Pending::Voice | Pending::Credential(_) | Pending::Connect(_, _) => true,
            Pending::Workspace(method) => matches!(
                *method,
                "SelectChannel" | "DisconnectServer" | "LeavePreview" | "OpenPreview"
            ),
            _ => false,
        })
    }

    fn update_icon_cache(&mut self, workspace: &Workspace) {
        let live_ids = workspace
            .channels
            .iter()
            .filter(|c| !c.icon_data_url.is_empty())
            .map(|c| c.icon_id.clone())
            .collect::<HashSet<_>>();
        self.icon_cache.retain(|id, _| live_ids.contains(id));
        for channel in &workspace.channels {
            let Some(encoded) = channel.icon_data_url.strip_prefix("data:image/png;base64,") else {
                continue;
            };
            if channel.icon_id.is_empty() || encoded.len() > 350_000 {
                continue;
            }
            let mut hasher = DefaultHasher::new();
            channel.icon_data_url.hash(&mut hasher);
            let fingerprint = hasher.finish();
            if self
                .icon_cache
                .get(&channel.icon_id)
                .is_some_and(|(current, _)| *current == fingerprint)
            {
                continue;
            }
            if let Ok(bytes) = STANDARD.decode(encoded) {
                self.icon_cache.insert(
                    channel.icon_id.clone(),
                    (
                        fingerprint,
                        Arc::new(Image::from_bytes(ImageFormat::Png, bytes)),
                    ),
                );
            }
        }
    }

    fn process_notifications(&mut self, workspace: &Workspace) {
        for notification in &workspace.notifications {
            if self.seen_notifications.contains(&notification.id) {
                continue;
            }
            self.seen_notifications.insert(notification.id.clone());
            self.notification_order.push_back(notification.id.clone());
            while self.notification_order.len() > 256 {
                if let Some(id) = self.notification_order.pop_front() {
                    self.seen_notifications.remove(&id);
                }
            }
            let fresh = DateTime::parse_from_rfc3339(&notification.created_at)
                .ok()
                .is_some_and(|created| {
                    let age = Utc::now()
                        .signed_duration_since(created.with_timezone(&Utc))
                        .num_milliseconds();
                    (-1_000..=5_000).contains(&age)
                });
            if !fresh || !self.preferences.notifications_enabled || self.voice.deafened {
                continue;
            }
            let applicable = match notification.kind.as_str() {
                "connected" => workspace.connected(),
                "disconnected" => true,
                "member_joined" | "member_left" => {
                    workspace.connected() && notification.channel_id == workspace.session.channel_id
                }
                _ => false,
            };
            if !applicable {
                continue;
            }
            if notification.kind.starts_with("member_") {
                if self
                    .last_member_notification
                    .is_some_and(|last| last.elapsed() < Duration::from_millis(400))
                {
                    continue;
                }
                self.last_member_notification = Some(Instant::now());
            }
            self.request(
                "PlayNotification",
                json!({"kind": notification.kind, "volume": self.preferences.notification_volume}),
                Pending::Notification,
            );
        }
    }

    fn save_preferences(&mut self) {
        if let Err(error) = self.preferences.save() {
            self.error = format!("无法保存声音设置：{error}");
        }
    }

    fn open_server_form(
        &mut self,
        profile: Option<ServerProfile>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.server_error.clear();
        let profile = profile.unwrap_or_else(|| ServerProfile {
            nickname: "Resona".into(),
            ..Default::default()
        });
        self.name_input
            .update(cx, |input, cx| input.set_value(profile.name, window, cx));
        self.address_input
            .update(cx, |input, cx| input.set_value(profile.address, window, cx));
        self.nickname_input.update(cx, |input, cx| {
            input.set_value(profile.nickname, window, cx)
        });
        self.modal = Some(Modal::Server {
            editing_id: profile.id,
        });
        self.name_input
            .update(cx, |input, cx| input.focus(window, cx));
    }

    fn open_password(&mut self, server_id: String, window: &mut Window, cx: &mut Context<Self>) {
        self.password_input
            .update(cx, |input, cx| input.set_value("", window, cx));
        self.modal = Some(Modal::Password { server_id });
        self.password_input
            .update(cx, |input, cx| input.focus(window, cx));
    }

    fn close_modal(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let cancel_connect = matches!(&self.modal, Some(Modal::Password { .. }))
            && (self.workspace.session.mode == "connecting"
                || self
                    .pending
                    .values()
                    .any(|pending| matches!(pending, Pending::Connect(_, _))));
        if cancel_connect {
            self.request(
                "DisconnectServer",
                json!({}),
                Pending::Workspace("DisconnectServer"),
            );
        }
        self.password_input
            .update(cx, |input, cx| input.set_value("", window, cx));
        self.modal = None;
        self.connect_error.clear();
        self.focus.focus(window);
        cx.notify();
    }

    fn dismiss_modal(&mut self, _: &DismissModal, window: &mut Window, cx: &mut Context<Self>) {
        if self.modal.is_some() {
            self.close_modal(window, cx);
        } else if self.device_menu.take().is_some() {
            self.focus.focus(window);
            cx.notify();
        } else {
            cx.propagate();
        }
    }

    fn save_server(&mut self, cx: &mut Context<Self>) {
        let editing_id = match &self.modal {
            Some(Modal::Server { editing_id }) => editing_id.clone(),
            _ => return,
        };
        let skip_password_storage = self
            .workspace
            .servers
            .iter()
            .find(|server| server.id == editing_id)
            .is_some_and(|server| server.skip_password_storage);
        let profile = ServerProfile {
            id: editing_id,
            name: self.name_input.read(cx).value().to_string(),
            address: self.address_input.read(cx).value().to_string(),
            nickname: self.nickname_input.read(cx).value().to_string(),
            skip_password_storage,
        };
        self.server_error.clear();
        self.request(
            "SaveServer",
            json!({"profile": profile}),
            Pending::Workspace("SaveServer"),
        );
        cx.notify();
    }

    fn begin_connect(&mut self, server_id: String, cx: &mut Context<Self>) {
        self.connect_error.clear();
        self.request(
            "GetServerCredentialStatus",
            json!({"id": server_id}),
            Pending::Credential(server_id),
        );
        cx.notify();
    }

    fn connect_with_password(&mut self, server_id: String, cx: &mut Context<Self>) {
        let password = self.password_input.read(cx).unmask_value().to_string();
        let remember = self.remember_password && self.capabilities.secure_password_storage;
        self.request(
            "ConnectServerWithPassword",
            json!({"id": server_id, "password": password, "remember": remember}),
            Pending::Connect(server_id, true),
        );
        cx.notify();
    }

    fn send_message(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.pending.values().any(|p| matches!(p, Pending::Send(_))) {
            return;
        }
        let text = self.chat_input.read(cx).value().to_string();
        if text.trim().is_empty() {
            return;
        }
        if self.workspace.connected()
            && let Some(message) = self.workspace.messages.iter().find(|message| {
                message.status == "unconfirmed"
                    && message.author_id == self.workspace.session.self_id
                    && message.channel_id == self.workspace.session.channel_id
                    && message.text == text
            })
        {
            self.modal = Some(Modal::Duplicate {
                message_id: message.id.clone(),
            });
            cx.notify();
            return;
        }
        let submitted = SubmittedDraft {
            key: draft_key(&self.workspace),
            text: text.clone(),
            before_ids: self
                .workspace
                .messages
                .iter()
                .map(|message| message.id.clone())
                .collect(),
            retry_id: None,
        };
        if self.workspace.session.mode == "preview" {
            self.request(
                "SendMessage",
                json!({"text": text}),
                Pending::Send(submitted),
            );
        } else {
            self.request(
                "SendChannelMessage",
                json!({
                    "sessionID": self.workspace.session.id,
                    "channelID": self.workspace.session.channel_id,
                    "text": text,
                }),
                Pending::Send(submitted),
            );
        }
        self.chat_input
            .update(cx, |input, cx| input.focus(window, cx));
        cx.notify();
    }

    fn retry_message(&mut self, message_id: String, allow_duplicate: bool, cx: &mut Context<Self>) {
        let Some(message) = self
            .workspace
            .messages
            .iter()
            .find(|message| message.id == message_id)
            .cloned()
        else {
            self.error = "待重试消息已不存在".into();
            cx.notify();
            return;
        };
        if message.status == "unconfirmed" && !allow_duplicate {
            self.modal = Some(Modal::Duplicate { message_id });
            cx.notify();
            return;
        }
        let submitted = SubmittedDraft {
            key: draft_key(&self.workspace),
            text: message.text,
            before_ids: self
                .workspace
                .messages
                .iter()
                .map(|message| message.id.clone())
                .collect(),
            retry_id: Some(message_id.clone()),
        };
        self.modal = None;
        self.request(
            "RetryMessage",
            json!({"id": message_id, "allowDuplicate": allow_duplicate}),
            Pending::Send(submitted),
        );
        cx.notify();
    }

    fn configure_voice(&mut self, change: impl FnOnce(&mut VoiceState), cx: &mut Context<Self>) {
        let mut next = self.voice.clone();
        change(&mut next);
        let stopping = self.voice.enabled && !next.enabled;
        let request_pending = self
            .pending
            .values()
            .any(|pending| matches!(pending, Pending::Voice));
        if (self.voice.busy || request_pending) && !stopping {
            return;
        }
        if next.deafened && !self.voice.deafened {
            self.request("StopNotifications", json!({}), Pending::Notification);
        }
        self.request(
            "ConfigureVoice",
            json!({
                "enabled": next.enabled, "muted": next.muted, "deafened": next.deafened,
                "inputDeviceID": next.input_device_id, "outputDeviceID": next.output_device_id,
                "volume": next.volume,
            }),
            Pending::Voice,
        );
        cx.notify();
    }

    fn render_rail(&self, view: &Entity<Self>) -> AnyElement {
        let mut rail = div()
            .w(px(64.))
            .h_full()
            .flex_shrink_0()
            .bg(rgb(RAIL))
            .flex()
            .flex_col()
            .items_center()
            .gap_2()
            .py_3()
            .tab_group()
            .child(svg().path("resona.svg").size(px(38.)));
        for server in self.workspace.servers.clone() {
            let selected = server.id == self.selected_server;
            let id = server.id.clone();
            let initial = server.name.chars().next().unwrap_or('R').to_string();
            let entity = view.clone();
            rail = rail.child(
                div()
                    .id(SharedString::from(format!("server-{id}")))
                    .tab_index(0)
                    .size(px(38.))
                    .rounded(px(7.))
                    .cursor_pointer()
                    .flex()
                    .items_center()
                    .justify_center()
                    .text_sm()
                    .font_weight(gpui::FontWeight::SEMIBOLD)
                    .bg(rgb(if selected { 0x2c4658 } else { PANEL_2 }))
                    .text_color(rgb(if selected { ICE } else { MUTED }))
                    .on_click(move |event, _, cx| {
                        entity.update(cx, |this, cx| {
                            this.selected_server = id.clone();
                            if event.is_keyboard() || event.click_count() >= 2 {
                                this.begin_connect(id.clone(), cx);
                            }
                            cx.notify();
                        })
                    })
                    .child(initial),
            );
        }
        let entity = view.clone();
        let entity_settings = view.clone();
        rail.child(div().flex_1())
            .child(
                Button::new("settings")
                    .icon(IconName::Settings)
                    .ghost()
                    .tooltip("声音设置")
                    .on_click(move |_, _, cx| {
                        entity_settings.update(cx, |this, cx| {
                            this.modal = Some(Modal::Settings);
                            cx.notify();
                        });
                    }),
            )
            .child(
                Button::new("add-server")
                    .icon(IconName::Plus)
                    .ghost()
                    .tooltip("添加服务器")
                    .on_click(move |_, window, cx| {
                        entity.update(cx, |this, cx| this.open_server_form(None, window, cx));
                    }),
            )
            .into_any_element()
    }

    fn render_sidebar(&self, view: &Entity<Self>) -> AnyElement {
        let profile = self.selected_profile().cloned();
        let active_profile = self
            .workspace
            .servers
            .iter()
            .find(|s| s.id == self.workspace.session.server_id);
        let title = if !self.workspace.channels.is_empty() {
            if self.workspace.session.server_name.is_empty() {
                active_profile
                    .map(|s| s.name.clone())
                    .unwrap_or_else(|| "Resona".into())
            } else {
                self.workspace.session.server_name.clone()
            }
        } else {
            profile
                .as_ref()
                .map(|s| s.name.clone())
                .unwrap_or_else(|| "Resona".into())
        };
        let mut actions = div().flex().gap_1();
        if let Some(profile) = profile.clone() {
            let editing = profile.clone();
            let entity = view.clone();
            actions = actions.child(
                Button::new("edit-server")
                    .icon(IconName::Settings2)
                    .ghost()
                    .tooltip("编辑服务器")
                    .on_click(move |_, window, cx| {
                        entity.update(cx, |this, cx| {
                            this.open_server_form(Some(editing.clone()), window, cx)
                        });
                    }),
            );
            let id = profile.id;
            let entity = view.clone();
            actions = actions.child(
                Button::new("delete-server")
                    .icon(IconName::Delete)
                    .ghost()
                    .tooltip("删除服务器")
                    .on_click(move |_, _, cx| {
                        entity.update(cx, |this, cx| {
                            this.modal = Some(Modal::Delete {
                                server_id: id.clone(),
                            });
                            cx.notify();
                        });
                    }),
            );
        }
        let mut sidebar = div()
            .w(px(278.))
            .h_full()
            .flex_shrink_0()
            .bg(rgb(PANEL))
            .border_r_1()
            .border_color(rgb(LINE))
            .flex()
            .flex_col()
            .child(
                div()
                    .h(px(62.))
                    .px_4()
                    .border_b_1()
                    .border_color(rgb(LINE))
                    .flex()
                    .items_center()
                    .justify_between()
                    .child(
                        div()
                            .flex()
                            .flex_col()
                            .gap_1()
                            .min_w_0()
                            .child(
                                div()
                                    .text_lg()
                                    .font_weight(gpui::FontWeight::BOLD)
                                    .text_color(rgb(TEXT))
                                    .child(title),
                            )
                            .child(
                                div()
                                    .text_size(px(10.))
                                    .text_color(rgb(MUTED))
                                    .child(self.session_label()),
                            ),
                    )
                    .child(actions),
            );
        if self.workspace.channels.is_empty() {
            let entity = view.clone();
            let mut empty = div()
                .flex_1()
                .px_4()
                .flex()
                .flex_col()
                .items_center()
                .justify_center()
                .gap_3()
                .text_center()
                .text_sm()
                .text_color(rgb(MUTED))
                .child(if profile.is_some() {
                    "此书签尚未连接"
                } else {
                    "添加服务器书签，或打开本地预览"
                });
            if let Some(profile) = profile {
                let id = profile.id;
                empty = empty.child(
                    Button::new("connect-server")
                        .label("连接服务器")
                        .primary()
                        .disabled(self.is_busy())
                        .on_click(move |_, _, cx| {
                            let entity = entity.clone();
                            entity.update(cx, |this, cx| this.begin_connect(id.clone(), cx));
                        }),
                );
            }
            let entity = view.clone();
            empty = empty.child(
                Button::new("open-preview")
                    .label("本地预览")
                    .ghost()
                    .on_click(move |_, _, cx| {
                        entity.update(cx, |this, cx| {
                            this.request(
                                "OpenPreview",
                                json!({}),
                                Pending::Workspace("OpenPreview"),
                            );
                            cx.notify();
                        });
                    }),
            );
            sidebar = sidebar.child(empty);
        } else {
            if let Some(selected) = profile.clone()
                && (selected.id != self.workspace.session.server_id
                    || matches!(
                        self.workspace.session.mode.as_str(),
                        "offline" | "failed" | "preview" | ""
                    ))
            {
                let id = selected.id.clone();
                let label = if self.workspace.session.server_id.is_empty()
                    || self.workspace.session.mode == "failed"
                {
                    format!("连接 {}", selected.name)
                } else {
                    format!("切换到 {}", selected.name)
                };
                let entity = view.clone();
                sidebar = sidebar.child(
                    div()
                        .px_3()
                        .py_2()
                        .border_b_1()
                        .border_color(rgb(LINE))
                        .child(
                            Button::new("connect-selected-server")
                                .label(label)
                                .primary()
                                .w_full()
                                .disabled(self.is_busy())
                                .on_click(move |_, _, cx| {
                                    entity
                                        .update(cx, |this, cx| this.begin_connect(id.clone(), cx));
                                }),
                        ),
                );
            }
            sidebar = sidebar.child(self.render_channels(view));
        }
        let mode = self.workspace.session.mode.clone();
        let method = if mode == "preview" {
            "LeavePreview"
        } else {
            "DisconnectServer"
        };
        let entity = view.clone();
        sidebar
            .child(
                div()
                    .min_h(px(60.))
                    .border_t_1()
                    .border_color(rgb(LINE))
                    .px_3()
                    .py_2()
                    .flex()
                    .items_center()
                    .gap_3()
                    .child(
                        div()
                            .size(px(8.))
                            .rounded_full()
                            .bg(rgb(self.status_color())),
                    )
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .flex()
                            .flex_col()
                            .gap_1()
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(rgb(TEXT))
                                    .child(self.session_label()),
                            )
                            .child(
                                div()
                                    .truncate()
                                    .text_size(px(9.))
                                    .text_color(rgb(MUTED))
                                    .child(self.status_detail()),
                            ),
                    )
                    .when(
                        matches!(
                            mode.as_str(),
                            "connecting" | "connected" | "disconnecting" | "preview"
                        ),
                        |row| {
                            row.child(
                                Button::new("disconnect")
                                    .label(if mode == "connecting" {
                                        "取消"
                                    } else {
                                        "断开"
                                    })
                                    .ghost()
                                    .on_click(move |_, _, cx| {
                                        let entity = entity.clone();
                                        entity.update(cx, |this, cx| {
                                            this.request(
                                                method,
                                                json!({}),
                                                Pending::Workspace(method),
                                            );
                                            cx.notify();
                                        });
                                    }),
                            )
                        },
                    ),
            )
            .into_any_element()
    }

    fn render_channels(&self, view: &Entity<Self>) -> AnyElement {
        let current = self.workspace.session.channel_id.clone();
        let switching = self.workspace.session.switching_channel_id.clone();
        let users = self.workspace.users.clone();
        let transition_pending = self.speaking_transition_pending();
        let rows = ordered_channels(&self.workspace.channels)
            .into_iter()
            .map(|(channel, depth)| {
                if channel.kind == "separator" {
                    if channel.name.trim().is_empty() {
                        return div().h(px(18.)).into_any_element();
                    }
                    if channel.repeat {
                        let repeated = channel.name.chars().cycle().take(96).collect::<String>();
                        return div()
                            .h(px(28.))
                            .px_3()
                            .overflow_hidden()
                            .flex()
                            .items_center()
                            .text_size(px(10.))
                            .text_color(rgb(MUTED))
                            .child(repeated)
                            .into_any_element();
                    }
                    let label = channel.name;
                    let text = || {
                        div()
                            .text_size(px(10.))
                            .text_color(rgb(MUTED))
                            .child(label.clone())
                    };
                    let line = || div().flex_1().h(px(1.)).bg(rgb(LINE));
                    return match channel.align.as_str() {
                        "left" => div()
                            .h(px(28.))
                            .px_3()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(text())
                            .child(line()),
                        "right" => div()
                            .h(px(28.))
                            .px_3()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(line())
                            .child(text()),
                        _ => div()
                            .h(px(28.))
                            .px_3()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(line())
                            .child(text())
                            .child(line()),
                    }
                    .into_any_element();
                }
                let id = channel.id.clone();
                let selected = id == current;
                let is_switching = id == switching;
                let locked = channel.password_required;
                let channel_image = self
                    .icon_cache
                    .get(&channel.icon_id)
                    .map(|(_, image)| image.clone());
                let entity = view.clone();
                let member_rows = users
                    .iter()
                    .filter(|u| u.channel_id == id)
                    .cloned()
                    .map(|user| {
                        let speaking = user_is_speaking(
                            &self.workspace,
                            &self.voice,
                            transition_pending,
                            &user,
                        );
                        div()
                            .h(px(25.))
                            .pl(px(42.))
                            .pr_2()
                            .flex()
                            .items_center()
                            .gap_2()
                            .text_size(px(11.))
                            .text_color(rgb(if user.is_self { MINT } else { MUTED }))
                            .child(speaking_indicator(&user, speaking, "tree"))
                            .child(if user.is_self {
                                format!("{} · 我", user.nickname)
                            } else {
                                user.nickname
                            })
                            .into_any_element()
                    })
                    .collect::<Vec<_>>();
                div()
                    .flex()
                    .flex_col()
                    .child(
                        div()
                            .id(SharedString::from(format!("channel-{id}")))
                            .tab_index(0)
                            .h(px(34.))
                            .pl(px(12. + depth as f32 * 14.))
                            .pr_3()
                            .rounded(px(5.))
                            .cursor_pointer()
                            .bg(rgb(if selected { 0x293c4b } else { PANEL }))
                            .hover(|s| s.bg(rgb(HOVER)))
                            .flex()
                            .items_center()
                            .gap_2()
                            .on_click(move |_, _, cx| {
                                if locked {
                                    return;
                                }
                                entity.update(cx, |this, cx| {
                                    this.request(
                                        "SelectChannel",
                                        json!({"id": id.clone()}),
                                        Pending::Workspace("SelectChannel"),
                                    );
                                    cx.notify();
                                });
                            })
                            .child(if let Some(image) = channel_image {
                                img(image).size(px(16.)).into_any_element()
                            } else {
                                div()
                                    .w(px(17.))
                                    .text_color(rgb(if selected { ICE } else { MUTED }))
                                    .child(if locked { "◆" } else { "#" })
                                    .into_any_element()
                            })
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .truncate()
                                    .text_sm()
                                    .text_color(rgb(if selected { TEXT } else { 0xc3c9cf }))
                                    .child(channel.name),
                            )
                            .child(
                                div()
                                    .text_size(px(10.))
                                    .text_color(rgb(if is_switching { AMBER } else { MUTED }))
                                    .child(if is_switching {
                                        "…".into()
                                    } else {
                                        channel.members.to_string()
                                    }),
                            ),
                    )
                    .children(member_rows)
                    .into_any_element()
            })
            .collect::<Vec<_>>();
        div()
            .flex_1()
            .min_h_0()
            .p_2()
            .children(rows)
            .overflow_y_scrollbar()
            .into_any_element()
    }

    fn render_chat(&self, view: &Entity<Self>) -> AnyElement {
        let channel = self
            .workspace
            .channels
            .iter()
            .find(|c| c.id == self.workspace.session.channel_id)
            .cloned();
        let channel_name = channel
            .as_ref()
            .map(|c| c.name.clone())
            .unwrap_or_else(|| "未选择频道".into());
        let messages = self
            .workspace
            .messages
            .iter()
            .filter(|m| m.channel_id == self.workspace.session.channel_id)
            .cloned()
            .map(|message| {
                let own = message.author_id == self.workspace.session.self_id;
                let retryable = matches!(message.status.as_str(), "failed" | "unconfirmed");
                let retry_id = message.id.clone();
                let entity = view.clone();
                div()
                    .px_5()
                    .py_2()
                    .flex()
                    .gap_3()
                    .hover(|s| s.bg(rgb(0x1b1f24)))
                    .child(
                        div()
                            .size(px(30.))
                            .rounded(px(6.))
                            .bg(rgb(if own { 0x315546 } else { 0x344351 }))
                            .flex()
                            .items_center()
                            .justify_center()
                            .text_xs()
                            .text_color(rgb(TEXT))
                            .child(message.author.chars().next().unwrap_or('?').to_string()),
                    )
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .flex()
                            .flex_col()
                            .gap_1()
                            .child(
                                div()
                                    .flex()
                                    .items_center()
                                    .gap_2()
                                    .child(
                                        div()
                                            .text_xs()
                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                            .text_color(rgb(if own { MINT } else { ICE }))
                                            .child(message.author),
                                    )
                                    .child(
                                        div()
                                            .text_size(px(9.))
                                            .text_color(rgb(MUTED))
                                            .child(short_time(&message.created_at)),
                                    )
                                    .when(message.status != "received", |line| {
                                        line.child(
                                            div()
                                                .text_size(px(9.))
                                                .text_color(rgb(message_status_color(
                                                    &message.status,
                                                )))
                                                .child(message_status_label(&message.status)),
                                        )
                                    }),
                            )
                            .child(
                                div()
                                    .text_sm()
                                    .text_color(rgb(0xd8dde2))
                                    .child(message.text),
                            )
                            .when(!message.error.is_empty(), |body| {
                                body.child(
                                    div().text_xs().text_color(rgb(RED)).child(message.error),
                                )
                            })
                            .when(retryable, |body| {
                                body.child(
                                    Button::new(SharedString::from(format!("retry-{retry_id}")))
                                        .label("重试")
                                        .warning()
                                        .outline()
                                        .on_click(move |_, _, cx| {
                                            entity.update(cx, |this, cx| {
                                                this.retry_message(retry_id.clone(), false, cx)
                                            });
                                        }),
                                )
                            }),
                    )
                    .into_any_element()
            })
            .collect::<Vec<_>>();
        let can_send = (self.workspace.session.mode == "preview" || self.workspace.connected())
            && channel.as_ref().is_some_and(|c| c.kind != "separator")
            && self.workspace.session.switching_channel_id.is_empty()
            && self.workspace.session.sending_message_id.is_empty()
            && !self.pending.values().any(|p| matches!(p, Pending::Send(_)));
        let entity = view.clone();
        div()
            .flex_1()
            .min_w_0()
            .h_full()
            .bg(rgb(BG))
            .flex()
            .flex_col()
            .child(
                div()
                    .h(px(62.))
                    .px_5()
                    .border_b_1()
                    .border_color(rgb(LINE))
                    .flex()
                    .items_center()
                    .justify_between()
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap_3()
                            .child(
                                div()
                                    .text_xl()
                                    .font_weight(gpui::FontWeight::BOLD)
                                    .text_color(rgb(TEXT))
                                    .child(format!("# {channel_name}")),
                            )
                            .when(self.workspace.session.mode == "preview", |h| {
                                h.child(
                                    div()
                                        .px_2()
                                        .py_1()
                                        .rounded(px(4.))
                                        .bg(rgb(0x493d27))
                                        .text_size(px(9.))
                                        .text_color(rgb(AMBER))
                                        .child("本地预览"),
                                )
                            }),
                    )
                    .child(
                        div().text_xs().text_color(rgb(MUTED)).child(
                            channel
                                .as_ref()
                                .map(|c| c.description.clone())
                                .unwrap_or_default(),
                        ),
                    ),
            )
            .child(if messages.is_empty() {
                div()
                    .flex_1()
                    .flex()
                    .flex_col()
                    .items_center()
                    .justify_center()
                    .gap_2()
                    .text_color(rgb(MUTED))
                    .child(
                        div()
                            .text_lg()
                            .text_color(rgb(TEXT))
                            .child(if channel.is_some() {
                                "开始这个频道的对话"
                            } else {
                                "选择服务器开始"
                            }),
                    )
                    .child(div().text_xs().child(if channel.is_some() {
                        "消息只会发送到你当前所在的频道"
                    } else {
                        "选择书签连接，或使用本地预览"
                    }))
                    .into_any_element()
            } else {
                div()
                    .flex_1()
                    .min_h_0()
                    .py_3()
                    .children(messages)
                    .overflow_y_scrollbar()
                    .into_any_element()
            })
            .child(
                div()
                    .px_4()
                    .pt_2()
                    .pb_3()
                    .border_t_1()
                    .border_color(rgb(LINE))
                    .bg(rgb(0x191d21))
                    .flex()
                    .gap_2()
                    .items_end()
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .child(Input::new(&self.chat_input).h(px(64.)).disabled(!can_send)),
                    )
                    .child(
                        Button::new("send-message")
                            .icon(IconName::ArrowUp)
                            .primary()
                            .tooltip("发送消息")
                            .disabled(!can_send)
                            .on_click(move |_, window, cx| {
                                entity.update(cx, |this, cx| this.send_message(window, cx));
                            }),
                    ),
            )
            .into_any_element()
    }

    fn render_details(&self, view: &Entity<Self>) -> AnyElement {
        let transition_pending = self.speaking_transition_pending();
        let members = self
            .workspace
            .users
            .iter()
            .filter(|u| u.channel_id == self.workspace.session.channel_id)
            .cloned()
            .map(|user| {
                let speaking =
                    user_is_speaking(&self.workspace, &self.voice, transition_pending, &user);
                div()
                    .h(px(34.))
                    .flex()
                    .items_center()
                    .gap_2()
                    .child(
                        div()
                            .size(px(24.))
                            .rounded(px(5.))
                            .bg(rgb(if user.is_self { 0x315546 } else { 0x303840 }))
                            .flex()
                            .items_center()
                            .justify_center()
                            .text_size(px(10.))
                            .child(user.nickname.chars().next().unwrap_or('?').to_string()),
                    )
                    .child(speaking_indicator(&user, speaking, "details"))
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .truncate()
                            .text_xs()
                            .text_color(rgb(TEXT))
                            .child(if user.is_self {
                                format!("{} · 我", user.nickname)
                            } else {
                                user.nickname
                            }),
                    )
                    .child(div().size(px(6.)).rounded_full().bg(rgb(MINT)))
                    .into_any_element()
            })
            .collect::<Vec<_>>();
        let connected = self.workspace.connected();
        let controls_disabled =
            !connected || self.voice.busy || !self.voice.enabled || !self.capabilities.voice;
        let voice_label = if self.voice.busy {
            "正在更新"
        } else if self.voice.active {
            "已启用"
        } else if self.voice.enabled {
            "不可用"
        } else {
            "未启用"
        };
        let entity = view.clone();
        let mut panel = div()
            .relative()
            .w(px(248.))
            .h_full()
            .flex_shrink_0()
            .bg(rgb(PANEL))
            .border_l_1()
            .border_color(rgb(LINE))
            .flex()
            .flex_col()
            .child(
                div()
                    .h(px(62.))
                    .px_4()
                    .border_b_1()
                    .border_color(rgb(LINE))
                    .flex()
                    .items_center()
                    .justify_between()
                    .child(
                        div()
                            .text_sm()
                            .font_weight(gpui::FontWeight::SEMIBOLD)
                            .text_color(rgb(TEXT))
                            .child("频道成员"),
                    )
                    .child(
                        div()
                            .text_xs()
                            .text_color(rgb(MUTED))
                            .child(members.len().to_string()),
                    ),
            )
            .child(
                div()
                    .flex_1()
                    .min_h_0()
                    .px_3()
                    .py_2()
                    .children(members)
                    .overflow_y_scrollbar(),
            )
            .child(
                div()
                    .border_t_1()
                    .border_color(rgb(LINE))
                    .p_3()
                    .flex()
                    .flex_col()
                    .gap_3()
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .justify_between()
                            .child(
                                div()
                                    .flex()
                                    .items_center()
                                    .gap_2()
                                    .child(div().size(px(7.)).rounded_full().bg(rgb(
                                        if self.voice.active {
                                            MINT
                                        } else if self.voice.error.is_empty() {
                                            MUTED
                                        } else {
                                            RED
                                        },
                                    )))
                                    .child(
                                        div()
                                            .text_xs()
                                            .text_color(rgb(TEXT))
                                            .child(format!("语音 · {voice_label}")),
                                    ),
                            )
                            .child(
                                Button::new("toggle-voice")
                                    .label(if self.voice.enabled {
                                        "停用"
                                    } else {
                                        "启用"
                                    })
                                    .ghost()
                                    .disabled(!connected || !self.capabilities.voice)
                                    .on_click({
                                        let entity = entity.clone();
                                        move |_, _, cx| {
                                            entity.update(cx, |this, cx| {
                                                this.configure_voice(|v| v.enabled = !v.enabled, cx)
                                            });
                                        }
                                    }),
                            ),
                    )
                    .when(!self.capabilities.voice && connected, |s| {
                        s.child(
                            div()
                                .px_2()
                                .py_2()
                                .rounded(px(4.))
                                .bg(rgb(0x493d27))
                                .text_size(px(10.))
                                .text_color(rgb(AMBER))
                                .child("此构建未提供音频设备能力"),
                        )
                    })
                    .when(!self.voice.error.is_empty(), |s| {
                        s.child(
                            div()
                                .px_2()
                                .py_2()
                                .rounded(px(4.))
                                .bg(rgb(0x40282b))
                                .text_size(px(10.))
                                .text_color(rgb(RED))
                                .child(self.voice.error.clone()),
                        )
                    })
                    .child(
                        div()
                            .flex()
                            .gap_2()
                            .child(
                                Button::new("mute")
                                    .icon(if self.voice.muted {
                                        VoiceIcon::MicOff
                                    } else {
                                        VoiceIcon::Mic
                                    })
                                    .tooltip(if self.voice.muted {
                                        "解除麦克风静音"
                                    } else {
                                        "将麦克风静音"
                                    })
                                    .selected(self.voice.muted)
                                    .disabled(controls_disabled)
                                    .on_click({
                                        let entity = entity.clone();
                                        move |_, _, cx| {
                                            entity.update(cx, |this, cx| {
                                                this.configure_voice(|v| v.muted = !v.muted, cx)
                                            })
                                        }
                                    }),
                            )
                            .child(
                                Button::new("deafen")
                                    .icon(if self.voice.deafened {
                                        VoiceIcon::HeadphonesOff
                                    } else {
                                        VoiceIcon::Headphones
                                    })
                                    .tooltip(if self.voice.deafened {
                                        "恢复收听"
                                    } else {
                                        "停止收听与发送"
                                    })
                                    .selected(self.voice.deafened)
                                    .disabled(controls_disabled)
                                    .on_click({
                                        let entity = entity.clone();
                                        move |_, _, cx| {
                                            entity.update(cx, |this, cx| {
                                                this.configure_voice(
                                                    |v| v.deafened = !v.deafened,
                                                    cx,
                                                )
                                            })
                                        }
                                    }),
                            ),
                    )
                    .child(self.render_device_picker("输入设备", DeviceMenu::Input, view))
                    .child(self.render_device_picker("输出设备", DeviceMenu::Output, view))
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap_1()
                            .child(div().text_size(px(9.)).text_color(rgb(MUTED)).child("音量"))
                            .child(
                                Button::new("volume-down")
                                    .icon(IconName::Minus)
                                    .ghost()
                                    .disabled(controls_disabled)
                                    .on_click({
                                        let entity = entity.clone();
                                        move |_, _, cx| {
                                            entity.update(cx, |this, cx| {
                                                this.configure_voice(
                                                    |v| v.volume = v.volume.saturating_sub(10),
                                                    cx,
                                                )
                                            })
                                        }
                                    }),
                            )
                            .child(
                                div()
                                    .flex_1()
                                    .h(px(4.))
                                    .rounded_full()
                                    .bg(rgb(0x303840))
                                    .child(
                                        div()
                                            .h_full()
                                            .w(px(self.voice.volume as f32))
                                            .rounded_full()
                                            .bg(rgb(ICE)),
                                    ),
                            )
                            .child(
                                div()
                                    .w(px(25.))
                                    .text_right()
                                    .text_size(px(9.))
                                    .text_color(rgb(TEXT))
                                    .child(self.voice.volume.to_string()),
                            )
                            .child(
                                Button::new("volume-up")
                                    .icon(IconName::Plus)
                                    .ghost()
                                    .disabled(controls_disabled)
                                    .on_click({
                                        let entity = entity.clone();
                                        move |_, _, cx| {
                                            entity.update(cx, |this, cx| {
                                                this.configure_voice(
                                                    |v| {
                                                        v.volume =
                                                            v.volume.saturating_add(10).min(100)
                                                    },
                                                    cx,
                                                )
                                            })
                                        }
                                    }),
                            ),
                    ),
            );
        if let Some(menu) = self.device_menu {
            let kind = if menu == DeviceMenu::Input {
                "input"
            } else {
                "output"
            };
            let entity = view.clone();
            let mut entries = vec![
                div()
                    .id(SharedString::from(format!("device-{kind}-default")))
                    .tab_index(0)
                    .px_3()
                    .py_2()
                    .cursor_pointer()
                    .rounded(px(4.))
                    .hover(|s| s.bg(rgb(HOVER)))
                    .text_xs()
                    .text_color(rgb(TEXT))
                    .child("系统默认")
                    .on_click(move |_, _, cx| {
                        entity.update(cx, |this, cx| {
                            this.device_menu = None;
                            this.configure_voice(
                                |v| {
                                    if menu == DeviceMenu::Input {
                                        v.input_device_id.clear();
                                    } else {
                                        v.output_device_id.clear();
                                    }
                                },
                                cx,
                            );
                        });
                    })
                    .into_any_element(),
            ];
            entries.extend(
                self.devices
                    .iter()
                    .filter(|d| d.kind == kind && !d.id.is_empty())
                    .cloned()
                    .map(|device| {
                        let id = device.id.clone();
                        let label = if device.is_default {
                            format!("{} · 默认", device.name)
                        } else {
                            device.name
                        };
                        let entity = view.clone();
                        div()
                            .id(SharedString::from(format!("device-{kind}-{id}")))
                            .tab_index(0)
                            .px_3()
                            .py_2()
                            .cursor_pointer()
                            .rounded(px(4.))
                            .hover(|s| s.bg(rgb(HOVER)))
                            .text_xs()
                            .text_color(rgb(TEXT))
                            .child(label)
                            .on_click(move |_, _, cx| {
                                entity.update(cx, |this, cx| {
                                    this.device_menu = None;
                                    this.configure_voice(
                                        |v| {
                                            if menu == DeviceMenu::Input {
                                                v.input_device_id = id.clone();
                                            } else {
                                                v.output_device_id = id.clone();
                                            }
                                        },
                                        cx,
                                    );
                                });
                            })
                            .into_any_element()
                    }),
            );
            panel = panel.child(
                div()
                    .absolute()
                    .right(px(12.))
                    .bottom(px(122.))
                    .w(px(224.))
                    .max_h(px(210.))
                    .p_1()
                    .rounded(px(6.))
                    .border_1()
                    .border_color(rgb(LINE))
                    .bg(rgb(0x20252a))
                    .shadow_lg()
                    .when(entries.is_empty(), |m| {
                        m.child(
                            div()
                                .p_3()
                                .text_xs()
                                .text_color(rgb(MUTED))
                                .child("没有可用设备"),
                        )
                    })
                    .children(entries)
                    .overflow_y_scrollbar(),
            );
        }
        panel.into_any_element()
    }

    fn render_device_picker(
        &self,
        label: &'static str,
        menu: DeviceMenu,
        view: &Entity<Self>,
    ) -> AnyElement {
        let id = if menu == DeviceMenu::Input {
            &self.voice.input_device_id
        } else {
            &self.voice.output_device_id
        };
        let kind = if menu == DeviceMenu::Input {
            "input"
        } else {
            "output"
        };
        let name = if id.is_empty() {
            "系统默认".into()
        } else {
            self.devices
                .iter()
                .find(|d| d.kind == kind && d.id == *id)
                .map(|d| d.name.clone())
                .unwrap_or_else(|| "设备不可用".into())
        };
        let entity = view.clone();
        div()
            .flex()
            .flex_col()
            .gap_1()
            .child(div().text_size(px(9.)).text_color(rgb(MUTED)).child(label))
            .child(
                Button::new(if menu == DeviceMenu::Input {
                    "input-device"
                } else {
                    "output-device"
                })
                .label(name)
                .dropdown_caret(true)
                .disabled(!self.workspace.connected() || !self.voice.enabled || self.voice.busy)
                .on_click(move |_, _, cx| {
                    entity.update(cx, |this, cx| {
                        let opening = this.device_menu != Some(menu);
                        this.device_menu = if opening { Some(menu) } else { None };
                        if opening {
                            this.request("GetAudioDevices", json!({}), Pending::Devices);
                        }
                        cx.notify();
                    });
                }),
            )
            .into_any_element()
    }

    fn render_modal(&self, view: &Entity<Self>) -> Option<AnyElement> {
        let modal = self.modal.clone()?;
        let entity = view.clone();
        let card = match modal {
            Modal::Server { editing_id } => {
                let editing = !editing_id.is_empty();
                div()
                    .w(px(440.))
                    .p_5()
                    .rounded(px(7.))
                    .border_1()
                    .border_color(rgb(LINE))
                    .bg(rgb(PANEL_2))
                    .shadow_lg()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(modal_title(if editing {
                        "编辑服务器"
                    } else {
                        "添加服务器"
                    }))
                    .when(!self.server_error.is_empty(), |card| {
                        card.child(
                            div()
                                .p_3()
                                .rounded(px(5.))
                                .bg(rgb(0x40282b))
                                .text_xs()
                                .text_color(rgb(RED))
                                .child(self.server_error.clone()),
                        )
                    })
                    .child(field("名称", Input::new(&self.name_input)))
                    .child(field("地址", Input::new(&self.address_input)))
                    .child(field("昵称", Input::new(&self.nickname_input)))
                    .child(
                        div()
                            .flex()
                            .justify_end()
                            .gap_2()
                            .child(
                                Button::new("cancel-server-form")
                                    .label("取消")
                                    .ghost()
                                    .on_click({
                                        let entity = entity.clone();
                                        move |_, window, cx| {
                                            entity
                                                .update(cx, |this, cx| this.close_modal(window, cx))
                                        }
                                    }),
                            )
                            .child(
                                Button::new("save-server")
                                    .label("保存")
                                    .primary()
                                    .disabled(self.is_busy())
                                    .on_click(move |_, _, cx| {
                                        entity.update(cx, |this, cx| this.save_server(cx))
                                    }),
                            ),
                    )
            }
            Modal::Password { server_id } => {
                let name = self
                    .workspace
                    .servers
                    .iter()
                    .find(|s| s.id == server_id)
                    .map(|s| s.name.clone())
                    .unwrap_or_else(|| "服务器".into());
                div()
                    .w(px(420.))
                    .p_5()
                    .rounded(px(7.))
                    .border_1()
                    .border_color(rgb(LINE))
                    .bg(rgb(PANEL_2))
                    .shadow_lg()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(modal_title(format!("连接 {name}")))
                    .when(!self.connect_error.is_empty(), |c| {
                        c.child(
                            div()
                                .p_3()
                                .rounded(px(5.))
                                .bg(rgb(0x40282b))
                                .text_xs()
                                .text_color(rgb(RED))
                                .child(self.connect_error.clone()),
                        )
                    })
                    .child(field(
                        "服务器密码",
                        Input::new(&self.password_input).mask_toggle(),
                    ))
                    .child(
                        Checkbox::new("remember-password")
                            .label("记住密码")
                            .checked(self.remember_password)
                            .disabled(!self.capabilities.secure_password_storage)
                            .on_click({
                                let entity = entity.clone();
                                move |remember, _, cx| {
                                    entity.update(cx, |this, cx| {
                                        this.remember_password = *remember;
                                        cx.notify();
                                    })
                                }
                            }),
                    )
                    .when(!self.capabilities.secure_password_storage, |c| {
                        c.child(
                            div()
                                .text_xs()
                                .text_color(rgb(AMBER))
                                .child("此平台暂不保存密码，本次连接仍可使用输入的密码。"),
                        )
                    })
                    .child(
                        div()
                            .flex()
                            .justify_between()
                            .items_center()
                            .child(
                                Button::new("forget-password")
                                    .label("忘记密码")
                                    .danger()
                                    .outline()
                                    .on_click({
                                        let entity = entity.clone();
                                        let id = server_id.clone();
                                        move |_, _, cx| {
                                            entity.update(cx, |this, cx| {
                                                this.request(
                                                    "ForgetServerPassword",
                                                    json!({"id": id.clone()}),
                                                    Pending::Workspace("ForgetServerPassword"),
                                                );
                                                cx.notify();
                                            })
                                        }
                                    }),
                            )
                            .child(
                                div()
                                    .flex()
                                    .gap_2()
                                    .child(
                                        Button::new("cancel-password")
                                            .label("取消")
                                            .ghost()
                                            .on_click({
                                                let entity = entity.clone();
                                                move |_, window, cx| {
                                                    entity.update(cx, |this, cx| {
                                                        this.close_modal(window, cx)
                                                    })
                                                }
                                            }),
                                    )
                                    .child(
                                        Button::new("connect-password")
                                            .label("连接")
                                            .primary()
                                            .disabled(self.is_busy())
                                            .on_click(move |_, _, cx| {
                                                entity.update(cx, |this, cx| {
                                                    this.connect_with_password(
                                                        server_id.clone(),
                                                        cx,
                                                    )
                                                })
                                            }),
                                    ),
                            ),
                    )
            }
            Modal::Delete { server_id } => {
                let name = self
                    .workspace
                    .servers
                    .iter()
                    .find(|s| s.id == server_id)
                    .map(|s| s.name.clone())
                    .unwrap_or_else(|| "此服务器".into());
                div()
                    .w(px(390.))
                    .p_5()
                    .rounded(px(7.))
                    .border_1()
                    .border_color(rgb(LINE))
                    .bg(rgb(PANEL_2))
                    .shadow_lg()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(modal_title("删除服务器书签"))
                    .child(
                        div()
                            .text_sm()
                            .text_color(rgb(MUTED))
                            .child(format!("将删除“{name}”及其已保存密码。此操作无法撤销。")),
                    )
                    .child(
                        div()
                            .flex()
                            .justify_end()
                            .gap_2()
                            .child(
                                Button::new("cancel-delete")
                                    .label("取消")
                                    .ghost()
                                    .on_click({
                                        let entity = entity.clone();
                                        move |_, window, cx| {
                                            entity
                                                .update(cx, |this, cx| this.close_modal(window, cx))
                                        }
                                    }),
                            )
                            .child(
                                Button::new("confirm-delete")
                                    .label("删除")
                                    .danger()
                                    .disabled(self.is_busy())
                                    .on_click(move |_, _, cx| {
                                        entity.update(cx, |this, cx| {
                                            this.request(
                                                "DeleteServer",
                                                json!({"id": server_id.clone()}),
                                                Pending::Workspace("DeleteServer"),
                                            );
                                            cx.notify();
                                        })
                                    }),
                            ),
                    )
            }
            Modal::Duplicate { message_id } => {
                let cancel_entity = entity.clone();
                div()
                    .w(px(410.))
                    .p_5()
                    .rounded(px(7.))
                    .border_1()
                    .border_color(rgb(LINE))
                    .bg(rgb(PANEL_2))
                    .shadow_lg()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(modal_title("确认重复消息"))
                    .child(
                        div()
                            .text_sm()
                            .text_color(rgb(MUTED))
                            .child("上一条相同消息的发送结果未知，继续可能产生重复消息。"),
                    )
                    .child(
                        div()
                            .flex()
                            .justify_end()
                            .gap_2()
                            .child(
                                Button::new("cancel-duplicate")
                                    .label("取消")
                                    .ghost()
                                    .on_click(move |_, window, cx| {
                                        cancel_entity
                                            .update(cx, |this, cx| this.close_modal(window, cx))
                                    }),
                            )
                            .child(
                                Button::new("confirm-duplicate")
                                    .label("仍然发送")
                                    .warning()
                                    .on_click(move |_, _, cx| {
                                        entity.update(cx, |this, cx| {
                                            this.retry_message(message_id.clone(), true, cx)
                                        })
                                    }),
                            ),
                    )
            }
            Modal::Settings => {
                let previews = [
                    ("connected", "已连接"),
                    ("disconnected", "已断开"),
                    ("member_joined", "成员加入"),
                    ("member_left", "成员离开"),
                ].into_iter().map(|(kind, label)| {
                    let entity = entity.clone();
                    Button::new(SharedString::from(format!("preview-{kind}"))).label(label).outline().disabled(self.core.is_none() || self.voice.deafened).on_click(move |_, _, cx| {
                        entity.update(cx, |this, cx| {
                            this.request("PlayNotification", json!({"kind": kind, "volume": this.preferences.notification_volume}), Pending::Notification);
                            cx.notify();
                        });
                    })
                }).collect::<Vec<_>>();
                div()
                    .w(px(410.))
                    .p_5()
                    .rounded(px(7.))
                    .border_1()
                    .border_color(rgb(LINE))
                    .bg(rgb(PANEL_2))
                    .shadow_lg()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(modal_title("声音设置"))
                    .child(
                        Checkbox::new("notifications-enabled")
                            .label("播放连接与成员提示音")
                            .checked(self.preferences.notifications_enabled)
                            .on_click({
                                let entity = entity.clone();
                                move |enabled, _, cx| {
                                    entity.update(cx, |this, cx| {
                                        this.preferences.notifications_enabled = *enabled;
                                        if !*enabled {
                                            this.request(
                                                "StopNotifications",
                                                json!({}),
                                                Pending::Notification,
                                            );
                                        }
                                        this.save_preferences();
                                        cx.notify();
                                    })
                                }
                            }),
                    )
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(div().text_xs().text_color(rgb(MUTED)).child("提示音量"))
                            .child(
                                Button::new("notification-volume-down")
                                    .icon(IconName::Minus)
                                    .ghost()
                                    .on_click({
                                        let entity = entity.clone();
                                        move |_, _, cx| {
                                            entity.update(cx, |this, cx| {
                                                this.preferences.notification_volume = this
                                                    .preferences
                                                    .notification_volume
                                                    .saturating_sub(5);
                                                this.save_preferences();
                                                cx.notify();
                                            })
                                        }
                                    }),
                            )
                            .child(
                                div()
                                    .flex_1()
                                    .h(px(4.))
                                    .rounded_full()
                                    .bg(rgb(0x303840))
                                    .child(
                                        div()
                                            .h_full()
                                            .w(px(self.preferences.notification_volume as f32 * 2.))
                                            .rounded_full()
                                            .bg(rgb(ICE)),
                                    ),
                            )
                            .child(
                                div()
                                    .w(px(28.))
                                    .text_right()
                                    .text_xs()
                                    .text_color(rgb(TEXT))
                                    .child(self.preferences.notification_volume.to_string()),
                            )
                            .child(
                                Button::new("notification-volume-up")
                                    .icon(IconName::Plus)
                                    .ghost()
                                    .on_click({
                                        let entity = entity.clone();
                                        move |_, _, cx| {
                                            entity.update(cx, |this, cx| {
                                                this.preferences.notification_volume = this
                                                    .preferences
                                                    .notification_volume
                                                    .saturating_add(5)
                                                    .min(100);
                                                this.save_preferences();
                                                cx.notify();
                                            })
                                        }
                                    }),
                            ),
                    )
                    .child(div().flex().flex_wrap().gap_2().children(previews))
                    .child(
                        div().flex().justify_end().child(
                            Button::new("close-settings")
                                .label("完成")
                                .primary()
                                .on_click(move |_, window, cx| {
                                    entity.update(cx, |this, cx| this.close_modal(window, cx))
                                }),
                        ),
                    )
            }
        };
        Some(
            div()
                .absolute()
                .inset_0()
                .bg(gpui::rgba(0x0a0c0ed9))
                .flex()
                .items_center()
                .justify_center()
                .child(card)
                .into_any_element(),
        )
    }

    fn session_label(&self) -> String {
        match self.workspace.session.mode.as_str() {
            "preview" => "本地预览",
            "connecting" => "正在连接",
            "connected" => "在线",
            "disconnecting" => "正在断开",
            "failed" => "连接失败",
            _ => "离线",
        }
        .into()
    }

    fn status_color(&self) -> u32 {
        match self.workspace.session.mode.as_str() {
            "connected" => MINT,
            "preview" | "connecting" | "disconnecting" => AMBER,
            "failed" => RED,
            _ => MUTED,
        }
    }

    fn status_detail(&self) -> String {
        if !self.workspace.session.error.is_empty() {
            return self.workspace.session.error.clone();
        }
        if !self.workspace.session.credential_error.is_empty() {
            return self.workspace.session.credential_error.clone();
        }
        if self.workspace.session.member_sync_state == "limited" {
            return format!(
                "成员范围受限 · {}",
                self.workspace.session.member_sync_error
            );
        }
        if self.workspace.session.member_sync_state == "pending" && self.workspace.connected() {
            return "正在同步可见成员".into();
        }
        self.workspace.session.server_name.clone()
    }
}

impl Render for ResonaApp {
    fn render(&mut self, _: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let view = cx.entity().clone();
        div()
            .id("resona-root")
            .relative()
            .size_full()
            .min_w(px(900.))
            .min_h(px(600.))
            .overflow_hidden()
            .track_focus(&self.focus)
            .on_action(cx.listener(Self::dismiss_modal))
            .font_family("-apple-system")
            .text_color(rgb(TEXT))
            .bg(rgb(BG))
            .flex()
            .child(self.render_rail(&view))
            .child(self.render_sidebar(&view))
            .child(self.render_chat(&view))
            .child(self.render_details(&view))
            .when(!self.error.is_empty() && self.modal.is_none(), |root| {
                let entity = view.clone();
                root.child(
                    div()
                        .absolute()
                        .left(px(84.))
                        .right(px(268.))
                        .top(px(12.))
                        .p_3()
                        .rounded(px(5.))
                        .border_1()
                        .border_color(rgb(0x724146))
                        .bg(rgb(0x3b2528))
                        .shadow_md()
                        .flex()
                        .items_center()
                        .gap_3()
                        .child(
                            div()
                                .flex_1()
                                .text_xs()
                                .text_color(rgb(RED))
                                .child(self.error.clone()),
                        )
                        .child(
                            Button::new("dismiss-error")
                                .icon(IconName::Close)
                                .ghost()
                                .tooltip("关闭")
                                .on_click(move |_, _, cx| {
                                    entity.update(cx, |this, cx| {
                                        this.error.clear();
                                        cx.notify();
                                    })
                                }),
                        ),
                )
            })
            .children(self.render_modal(&view))
    }
}

fn field(label: &'static str, input: Input) -> AnyElement {
    div()
        .flex()
        .flex_col()
        .gap_2()
        .child(div().text_xs().text_color(rgb(MUTED)).child(label))
        .child(input)
        .into_any_element()
}

fn modal_title(title: impl Into<SharedString>) -> AnyElement {
    div()
        .text_lg()
        .font_weight(gpui::FontWeight::BOLD)
        .text_color(rgb(TEXT))
        .child(title.into())
        .into_any_element()
}

fn user_is_speaking(
    workspace: &Workspace,
    voice: &VoiceState,
    transition_pending: bool,
    user: &User,
) -> bool {
    if !workspace.connected()
        || workspace.session.channel_id.is_empty()
        || !workspace.session.switching_channel_id.is_empty()
        || user.channel_id != workspace.session.channel_id
        || transition_pending
        || voice.busy
        || !voice.enabled
        || !voice.active
        || voice.deafened
    {
        return false;
    }

    if user.is_self {
        !voice.muted && voice.local_speaking
    } else {
        !user.id.is_empty()
            && voice
                .speaking_client_ids
                .iter()
                .any(|client_id| client_id == &user.id)
    }
}

fn speaking_indicator(user: &User, speaking: bool, location: &'static str) -> AnyElement {
    let tooltip: SharedString = if speaking {
        "正在说话".into()
    } else {
        "未在说话".into()
    };
    div()
        .id(SharedString::from(format!(
            "speaking-{location}-{}",
            user.id
        )))
        .size(px(16.))
        .flex_none()
        .flex()
        .items_center()
        .justify_center()
        .tooltip(move |window, cx| Tooltip::new(tooltip.clone()).build(window, cx))
        .child(
            svg()
                .path(if user.is_self {
                    "mic.svg"
                } else {
                    "volume-2.svg"
                })
                .text_color(rgb(if speaking { MINT } else { 0x66717c }))
                .size(px(14.)),
        )
        .into_any_element()
}

fn short_time(value: &str) -> String {
    DateTime::parse_from_rfc3339(value)
        .map(|time| time.with_timezone(&Local).format("%H:%M").to_string())
        .unwrap_or_else(|_| value.get(11..16).unwrap_or(value).to_string())
}

fn draft_key(workspace: &Workspace) -> String {
    let owner = if workspace.session.mode == "preview" {
        "preview".to_string()
    } else if workspace.session.server_id.is_empty() {
        "offline".to_string()
    } else {
        workspace.session.server_id.clone()
    };
    format!("{owner}\u{0}{}", workspace.session.channel_id)
}

fn remove_inserted_newline(value: &str, cursor: usize) -> String {
    let mut value = value.to_owned();
    if cursor > 0 && cursor <= value.len() && value.as_bytes()[cursor - 1] == b'\n' {
        value.replace_range(cursor - 1..cursor, "");
    }
    value
}
fn message_status_label(status: &str) -> &'static str {
    match status {
        "sending" => "发送中",
        "sent" => "已发送",
        "failed" => "发送失败",
        "unconfirmed" => "结果未知",
        _ => "",
    }
}
fn message_status_color(status: &str) -> u32 {
    match status {
        "sent" => MINT,
        "failed" => RED,
        "unconfirmed" => AMBER,
        _ => MUTED,
    }
}

fn ordered_channels(channels: &[crate::model::Channel]) -> Vec<(crate::model::Channel, usize)> {
    let ids = channels
        .iter()
        .map(|channel| channel.id.clone())
        .collect::<HashSet<_>>();
    let mut groups: HashMap<String, Vec<crate::model::Channel>> = HashMap::new();
    for channel in channels {
        let parent = if ids.contains(&channel.parent_id) {
            channel.parent_id.clone()
        } else {
            String::new()
        };
        groups.entry(parent).or_default().push(channel.clone());
    }
    fn siblings_in_order(mut siblings: Vec<crate::model::Channel>) -> Vec<crate::model::Channel> {
        let mut result = Vec::with_capacity(siblings.len());
        let mut predecessor = "0".to_string();
        while let Some(index) = siblings.iter().position(|channel| {
            channel.order.is_empty() && predecessor == "0" || channel.order == predecessor
        }) {
            let channel = siblings.remove(index);
            predecessor = channel.id.clone();
            result.push(channel);
        }
        result.extend(siblings);
        result
    }
    fn visit(
        parent: &str,
        depth: usize,
        groups: &mut HashMap<String, Vec<crate::model::Channel>>,
        seen: &mut HashSet<String>,
        output: &mut Vec<(crate::model::Channel, usize)>,
    ) {
        for channel in siblings_in_order(groups.remove(parent).unwrap_or_default()) {
            if !seen.insert(channel.id.clone()) {
                continue;
            }
            let id = channel.id.clone();
            output.push((channel, depth));
            visit(&id, depth + 1, groups, seen, output);
        }
    }
    let mut output = Vec::with_capacity(channels.len());
    let mut seen = HashSet::new();
    visit("", 0, &mut groups, &mut seen, &mut output);
    for channel in channels {
        if seen.insert(channel.id.clone()) {
            output.push((channel.clone(), 0));
        }
    }
    output
}

#[cfg(test)]
mod tests {
    use super::{remove_inserted_newline, user_is_speaking};
    use crate::model::{User, VoiceState, Workspace};

    #[test]
    fn removes_only_newline_immediately_before_utf8_byte_cursor() {
        assert_eq!(remove_inserted_newline("abc\n", 4), "abc");
        assert_eq!(remove_inserted_newline("中文\nabc", 7), "中文abc");
        assert_eq!(remove_inserted_newline("🙂\nrest", 5), "🙂rest");
        assert_eq!(remove_inserted_newline("kept\nline\n", 10), "kept\nline");
        assert_eq!(remove_inserted_newline("kept\nline", 9), "kept\nline");
    }

    fn speaking_context() -> (Workspace, VoiceState, User, User) {
        let mut workspace = Workspace::default();
        workspace.session.mode = "connected".into();
        workspace.session.channel_id = "channel-1".into();

        let voice = VoiceState {
            enabled: true,
            muted: false,
            active: true,
            speaking_client_ids: vec!["remote-1".into()],
            ..VoiceState::default()
        };
        let local = User {
            id: "self-1".into(),
            nickname: "Local".into(),
            channel_id: "channel-1".into(),
            is_self: true,
        };
        let remote = User {
            id: "remote-1".into(),
            nickname: "Remote".into(),
            channel_id: "channel-1".into(),
            is_self: false,
        };
        (workspace, voice, local, remote)
    }

    #[test]
    fn speaking_state_uses_local_and_remote_sources() {
        let (workspace, mut voice, local, remote) = speaking_context();

        assert!(user_is_speaking(&workspace, &voice, false, &remote));
        assert!(!user_is_speaking(&workspace, &voice, false, &local));

        voice.local_speaking = true;
        assert!(user_is_speaking(&workspace, &voice, false, &local));
        voice.muted = true;
        assert!(!user_is_speaking(&workspace, &voice, false, &local));
    }

    #[test]
    fn speaking_state_dims_during_inactive_or_transitional_states() {
        let (mut workspace, mut voice, _local, mut remote) = speaking_context();

        assert!(user_is_speaking(&workspace, &voice, false, &remote));
        assert!(!user_is_speaking(&workspace, &voice, true, &remote));

        voice.busy = true;
        assert!(!user_is_speaking(&workspace, &voice, false, &remote));
        voice.busy = false;
        voice.deafened = true;
        assert!(!user_is_speaking(&workspace, &voice, false, &remote));
        voice.deafened = false;
        voice.enabled = false;
        assert!(!user_is_speaking(&workspace, &voice, false, &remote));
        voice.enabled = true;
        voice.active = false;
        assert!(!user_is_speaking(&workspace, &voice, false, &remote));
        voice.active = true;

        workspace.session.switching_channel_id = "channel-2".into();
        assert!(!user_is_speaking(&workspace, &voice, false, &remote));
        workspace.session.switching_channel_id.clear();
        remote.channel_id = "channel-2".into();
        assert!(!user_is_speaking(&workspace, &voice, false, &remote));
        remote.channel_id = "channel-1".into();
        workspace.session.mode = "offline".into();
        assert!(!user_is_speaking(&workspace, &voice, false, &remote));
    }
}
