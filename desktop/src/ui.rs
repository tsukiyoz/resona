use crate::{
    core::{CoreClient, Incoming},
    model::{
        AudioDevice, Capabilities, CredentialStatus, ServerProfile, User, VoiceState, Workspace,
    },
    preferences::Preferences,
};
use base64::{Engine as _, engine::general_purpose::STANDARD};
use chrono::{DateTime, Local, Utc};
use global_hotkey::{GlobalHotKeyEvent, GlobalHotKeyManager, HotKeyState, hotkey::HotKey};
use gpui::{
    AnyElement, Context, Entity, EntityInputHandler, FocusHandle, Image, ImageFormat,
    InteractiveElement, IntoElement, ParentElement, Render, SharedString,
    StatefulInteractiveElement, Styled, Subscription, Window, div, img, prelude::*, px, rgb, svg,
};
use gpui_component::{
    Disableable, IconName, IconNamed, Selectable,
    button::{Button, ButtonVariants},
    checkbox::Checkbox,
    input::{Input, InputEvent, InputState},
    menu::{ContextMenuExt, PopupMenuItem},
    scroll::ScrollableElement,
    tooltip::Tooltip,
};
use serde_json::{Value, json};
use std::{
    collections::{HashMap, HashSet, VecDeque},
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, AtomicU64, Ordering},
    },
    time::{Duration, Instant},
};

const BG: u32 = 0x090a0c;
const PANEL: u32 = 0x101214;
const PANEL_2: u32 = 0x181a1d;
const HOVER: u32 = 0x24272b;
const LINE: u32 = 0x303337;
const TEXT: u32 = 0xe9edf1;
const MUTED: u32 = 0x929ca7;
const ICE: u32 = 0x7db8e8;
const MINT: u32 = 0x79c9ad;
const AMBER: u32 = 0xd8aa5d;
const RED: u32 = 0xe28282;

gpui::actions!(resona, [DismissModal, Quit]);

#[derive(Clone, Copy)]
enum VoiceIcon {
    Volume,
    Mic,
    MicOff,
    Headphones,
    HeadphonesOff,
}

impl IconNamed for VoiceIcon {
    fn path(self) -> SharedString {
        match self {
            Self::Volume => "volume-2.svg",
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
    SaveServer(u64),
    Credential(String, u64),
    Connect(String, bool),
    Voice,
    VoicePreferences {
        connect: Option<String>,
        session: String,
        revision: u64,
    },
    Devices,
    Send(SubmittedDraft),
    Capabilities,
    Notification,
    MicrophoneTest,
    PushToTalk,
    Icon {
        session: String,
        reference: String,
    },
    Details {
        session: String,
        selection: DetailSelection,
        revision: u64,
    },
}

#[derive(Clone, PartialEq)]
enum DetailSelection {
    Channel(String),
    User(String),
}

#[derive(Debug, PartialEq)]
enum DetailChange {
    Keep,
    Clear,
    Refresh,
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
    Audio,
}

#[derive(Clone, Copy, PartialEq)]
enum DeviceMenu {
    Input,
    Output,
}

pub struct ResonaApp {
    logo: Arc<Image>,
    core: Option<CoreClient>,
    workspace: Workspace,
    voice: VoiceState,
    microphone_test: VoiceState,
    capabilities: Capabilities,
    devices: Vec<AudioDevice>,
    icon_cache: HashMap<String, Arc<Image>>,
    icon_sizes: HashMap<String, usize>,
    icon_requested: HashSet<String>,
    icon_failures: HashMap<String, String>,
    detail_selection: Option<DetailSelection>,
    detail_value: Option<Value>,
    detail_error: String,
    detail_revision: u64,
    ptt_pressed: bool,
    ptt_key_down: bool,
    hotkey_manager: Option<GlobalHotKeyManager>,
    hotkey: Option<HotKey>,
    hotkey_attempt: Option<String>,
    hotkey_error: String,
    hotkey_epoch: Arc<AtomicU64>,
    closing: bool,
    connect_revision: u64,
    preferences: Preferences,
    preference_revision: u64,
    preference_save_order: Arc<Mutex<u64>>,
    audio_settings_pending: bool,
    queued_voice: Option<VoiceState>,
    voice_target: Option<VoiceState>,
    _audio_update: Option<gpui::Task<()>>,
    _detail_update: Option<gpui::Task<()>>,
    seen_notifications: HashSet<String>,
    notification_order: VecDeque<String>,
    last_member_notification: Option<Instant>,
    pending: HashMap<u64, Pending>,
    drafts: HashMap<String, String>,
    submitted_drafts: HashMap<String, (String, String)>,
    selected_server: String,
    modal: Option<Modal>,
    device_menu: Option<DeviceMenu>,
    device_trigger_bounds: [Option<gpui::Bounds<gpui::Pixels>>; 2],
    remember_password: bool,
    error: String,
    server_error: String,
    server_form_revision: u64,
    connect_error: String,
    focus: FocusHandle,
    name_input: Entity<InputState>,
    address_input: Entity<InputState>,
    nickname_input: Entity<InputState>,
    password_input: Entity<InputState>,
    chat_input: Entity<InputState>,
    shortcut_input: Entity<InputState>,
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
        let preferences = Preferences::load();
        let shortcut_input = cx.new(|cx| {
            InputState::new(window, cx)
                .default_value(preferences.push_to_talk_shortcut.clone())
                .placeholder("F8")
        });
        let chat_subscription = cx.subscribe_in(
            &chat_input,
            window,
            |this, input, event: &InputEvent, window, cx| {
                if matches!(event, InputEvent::PressEnter { secondary: false }) {
                    if input.update(cx, |input, cx| {
                        input.marked_text_range(window, cx).is_some()
                    }) {
                        return;
                    }
                    input.update(cx, |input, cx| {
                        let value = remove_inserted_newline(input.value().as_ref(), input.cursor());
                        input.set_value(value, window, cx);
                    });
                    this.send_message(window, cx);
                }
            },
        );
        let form_subscriptions = [
            name_input.clone(),
            address_input.clone(),
            nickname_input.clone(),
            password_input.clone(),
        ]
        .into_iter()
        .map(|input| {
            cx.subscribe_in(
                &input,
                window,
                |this, input, event: &InputEvent, window, cx| {
                    if !matches!(event, InputEvent::PressEnter { secondary: false })
                        || input.update(cx, |input, cx| {
                            input.marked_text_range(window, cx).is_some()
                        })
                    {
                        return;
                    }
                    match this.modal.clone() {
                        Some(Modal::Password { server_id }) if *input == this.password_input => {
                            this.connect_with_password(server_id, cx)
                        }
                        Some(Modal::Server { .. }) if *input != this.password_input => {
                            this.save_server(cx)
                        }
                        _ => {}
                    }
                },
            )
        })
        .collect::<Vec<_>>();
        let activation_subscription = cx.observe_window_activation(window, |this, window, cx| {
            if !window.is_window_active() {
                if this.hotkey.is_none() {
                    this.ptt_key_down = false;
                    this.set_push_to_talk(false, cx);
                }
                this.device_menu = None;
                cx.notify();
            }
        });
        let quit_subscription = cx.on_app_quit(|this, cx| {
            this.flush_preferences();
            if let Some(core) = this.core.clone() {
                this.clear_hotkey(cx);
                this.closing = true;
                let _ = cx.background_executor().block_with_timeout(
                    Duration::from_secs(7),
                    core.prepare_shutdown(
                        this.preferences.notifications_enabled && !this.voice.deafened,
                        this.preferences.notification_volume,
                    ),
                );
                let _ = core.request("Shutdown", json!({}));
                core.finish_shutdown();
            }
            async {}
        });
        let mut this = Self {
            logo: Arc::new(Image::from_bytes(
                ImageFormat::Png,
                include_bytes!("../../build/appicon.png").to_vec(),
            )),
            core: None,
            workspace: Workspace::default(),
            voice: VoiceState::default(),
            microphone_test: VoiceState::default(),
            capabilities: Capabilities::default(),
            devices: Vec::new(),
            icon_cache: HashMap::new(),
            icon_sizes: HashMap::new(),
            icon_requested: HashSet::new(),
            icon_failures: HashMap::new(),
            detail_selection: None,
            detail_value: None,
            detail_error: String::new(),
            detail_revision: 0,
            ptt_pressed: false,
            ptt_key_down: false,
            hotkey_manager: GlobalHotKeyManager::new().ok(),
            hotkey: None,
            hotkey_attempt: None,
            hotkey_error: String::new(),
            hotkey_epoch: Arc::new(AtomicU64::new(0)),
            closing: false,
            connect_revision: 0,
            preferences,
            preference_revision: 0,
            preference_save_order: Arc::new(Mutex::new(0)),
            audio_settings_pending: false,
            queued_voice: None,
            voice_target: None,
            _audio_update: None,
            _detail_update: None,
            seen_notifications: HashSet::new(),
            notification_order: VecDeque::new(),
            last_member_notification: None,
            pending: HashMap::new(),
            drafts: HashMap::new(),
            submitted_drafts: HashMap::new(),
            selected_server: String::new(),
            modal: None,
            device_menu: None,
            device_trigger_bounds: [None, None],
            remember_password: false,
            error: String::new(),
            server_error: String::new(),
            server_form_revision: 0,
            connect_error: String::new(),
            focus: cx.focus_handle(),
            name_input,
            address_input,
            nickname_input,
            password_input,
            chat_input,
            shortcut_input,
            _subscriptions: form_subscriptions
                .into_iter()
                .chain(vec![
                    chat_subscription,
                    activation_subscription,
                    quit_subscription,
                ])
                .collect(),
        };
        this.focus.focus(window);
        let (hotkey_tx, hotkey_rx) = async_channel::bounded(16);
        let hotkey_epoch = this.hotkey_epoch.clone();
        let overflow = Arc::new(AtomicBool::new(false));
        let event_overflow = overflow.clone();
        GlobalHotKeyEvent::set_event_handler(Some(move |event| {
            if hotkey_tx
                .try_send((hotkey_epoch.load(Ordering::Acquire), event))
                .is_err()
            {
                event_overflow.store(true, Ordering::Release);
            }
        }));
        cx.spawn_in(window, async move |view, cx| {
            while let Ok((epoch, event)) = hotkey_rx.recv().await {
                let lost = overflow.swap(false, Ordering::AcqRel);
                if cx
                    .update(|_, cx| {
                        view.update(cx, |this, cx| {
                            if lost {
                                this.clear_hotkey(cx);
                                this.hotkey_error =
                                    "快捷键事件积压，已停止按键发言；请重新应用快捷键".into();
                                this.hotkey_attempt =
                                    Some(this.preferences.push_to_talk_shortcut.clone());
                            } else if epoch == this.hotkey_epoch.load(Ordering::Acquire)
                                && this.hotkey.is_some_and(|key| key.id() == event.id)
                            {
                                this.handle_ptt_key(event.state == HotKeyState::Pressed, cx);
                            }
                            cx.notify();
                        })
                    })
                    .is_err()
                {
                    break;
                }
            }
        })
        .detach();
        match CoreClient::spawn() {
            Ok((core, rx)) => {
                this.core = Some(core);
                this.request(
                    "GetWorkspace",
                    json!({}),
                    Pending::Workspace("GetWorkspace"),
                );
                this.request("GetVoiceState", json!({}), Pending::Voice);
                this.request("GetMicrophoneTestState", json!({}), Pending::MicrophoneTest);
                this.request("GetAudioDevices", json!({}), Pending::Devices);
                this.request("GetCapabilities", json!({}), Pending::Capabilities);
                this.store_voice_preferences(None);
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
        if self.closing && !matches!(method, "PrepareShutdown" | "Shutdown" | "SetPushToTalk") {
            return;
        }
        let result = self
            .core
            .as_ref()
            .ok_or_else(|| "核心进程不可用".to_owned())
            .and_then(|core| {
                core.request(method, params)
                    .map_err(|error| error.to_string())
            });
        match result {
            Ok(id) => {
                self.pending.insert(id, pending);
            }
            Err(error) => self.request_failed(pending, error),
        }
    }

    fn request_failed(&mut self, pending: Pending, error: String) {
        match pending {
            Pending::Details {
                session,
                selection,
                revision,
            } => {
                if session == self.workspace.session.id
                    && self.detail_selection.as_ref() == Some(&selection)
                    && revision == self.detail_revision
                {
                    self.detail_error = error;
                }
            }
            Pending::Icon { reference, .. } => {
                self.icon_requested.remove(&reference);
                self.icon_failures.insert(reference, error);
            }
            Pending::MicrophoneTest => {
                self.microphone_test.busy = false;
                self.microphone_test.error = error;
            }
            _ => self.error = error,
        }
    }

    fn clear_selected_details(&mut self) {
        self._detail_update = None;
        if self.core.is_some() && !self.closing {
            self.request("CancelDetails", json!({}), Pending::Notification);
        }
        self.detail_selection = None;
        self.detail_value = None;
        self.detail_error.clear();
        self.detail_revision = self.detail_revision.wrapping_add(1);
    }

    fn handle_incoming(&mut self, incoming: Incoming, window: &mut Window, cx: &mut Context<Self>) {
        match incoming {
            Incoming::Workspace(value) => self.apply_workspace(value, window, cx),
            Incoming::Voice(value) => self.apply_voice(value),
            Incoming::MicrophoneTest(value) => self.apply_microphone_test(value),
            Incoming::ProtocolError(error) => self.error = error,
            Incoming::Exited(error) => {
                if self.closing {
                    self.finish_exit(cx);
                    return;
                }
                self.error = error.clone();
                if let Some(core) = &self.core {
                    core.finish_shutdown();
                }
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
                self.clear_selected_details();
                self.voice.enabled = false;
                self.voice.active = false;
                self.voice.busy = false;
                self.voice.speaking_client_ids.clear();
                self.voice.local_speaking = false;
                self.microphone_test = VoiceState::default();
                self.ptt_pressed = false;
                self.device_menu = None;
            }
            Incoming::Response { id, result } => {
                let pending = self.pending.remove(&id);
                match (pending, result) {
                    (Some(Pending::Workspace(method)), Ok(value)) => {
                        self.apply_workspace(value, window, cx);
                        if method == "DeleteServer" {
                            self.modal = None;
                            self.server_error.clear();
                            self.error.clear();
                        }
                    }
                    (Some(Pending::SaveServer(revision)), Ok(value)) => {
                        self.apply_workspace(value, window, cx);
                        if revision == self.server_form_revision
                            && matches!(self.modal, Some(Modal::Server { .. }))
                        {
                            self.close_modal(window, cx);
                            self.server_error.clear();
                        }
                    }
                    (Some(Pending::Credential(server_id, revision)), Ok(value)) => {
                        if revision != self.connect_revision || self.closing {
                            self.sync_hotkey(cx);
                            cx.notify();
                            return;
                        }
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
                    (Some(Pending::Connect(_server_id, keep_modal)), Ok(value)) => {
                        self.apply_workspace(value, window, cx);
                        if !keep_modal || self.workspace.connected() {
                            self.close_modal(window, cx);
                        }
                        self.connect_error.clear();
                    }
                    (Some(Pending::Voice), Ok(value)) => self.apply_voice(value),
                    (
                        Some(Pending::VoicePreferences {
                            connect,
                            session,
                            revision,
                        }),
                        result,
                    ) => {
                        if session == self.workspace.session.id
                            && can_prepare_voice_preferences(&self.workspace.session.mode)
                        {
                            match result {
                                Ok(value) => {
                                    self.apply_voice(value);
                                    if let Some(server_id) = connect
                                        && revision == self.connect_revision
                                        && !self.closing
                                    {
                                        self.request_connection_credentials(server_id);
                                    }
                                }
                                Err(error) => {
                                    self.error = format!("无法应用音频偏好：{error}");
                                    if connect.is_some() && revision == self.connect_revision {
                                        self.connect_error = self.error.clone();
                                    }
                                }
                            }
                        }
                    }
                    (Some(Pending::PushToTalk), Ok(value)) => self.apply_voice(value),
                    (Some(Pending::MicrophoneTest), Ok(value)) => self.apply_microphone_test(value),
                    (Some(Pending::Icon { session, reference }), Ok(value)) => {
                        let mut accepted = false;
                        if session == self.workspace.session.id
                            && value.get("ref").and_then(Value::as_str) == Some(&reference)
                            && let Some(encoded) = value
                                .get("dataURL")
                                .and_then(Value::as_str)
                                .and_then(|url| url.strip_prefix("data:image/png;base64,"))
                            && encoded.len() <= 350_000
                            && let Ok(bytes) = STANDARD.decode(encoded)
                        {
                            while self.icon_cache.len() >= 128
                                || self.icon_sizes.values().sum::<usize>() + bytes.len()
                                    > 8 * 1024 * 1024
                            {
                                if let Some(key) = self.icon_cache.keys().next().cloned() {
                                    self.icon_cache.remove(&key);
                                    self.icon_sizes.remove(&key);
                                } else {
                                    break;
                                }
                            }
                            self.icon_sizes.insert(reference.clone(), bytes.len());
                            self.icon_cache.insert(
                                reference.clone(),
                                Arc::new(Image::from_bytes(ImageFormat::Png, bytes)),
                            );
                            accepted = true;
                        }
                        if !accepted && session == self.workspace.session.id {
                            self.request_failed(
                                Pending::Icon { session, reference },
                                "图标资源格式无效".into(),
                            );
                        }
                        let workspace = self.workspace.clone();
                        self.update_icon_cache(&workspace);
                    }
                    (
                        Some(Pending::Details {
                            session,
                            selection,
                            revision,
                        }),
                        result,
                    ) => {
                        if session == self.workspace.session.id
                            && self.detail_selection.as_ref() == Some(&selection)
                            && revision == self.detail_revision
                        {
                            match result {
                                Ok(value) => {
                                    self.detail_value = Some(value);
                                    self.detail_error.clear();
                                }
                                Err(error) => self.detail_error = error,
                            }
                        }
                    }
                    (Some(Pending::Icon { session, reference }), Err(error)) => {
                        if session == self.workspace.session.id {
                            self.request_failed(Pending::Icon { session, reference }, error);
                        }
                        let workspace = self.workspace.clone();
                        self.update_icon_cache(&workspace);
                    }
                    (Some(Pending::MicrophoneTest), Err(error)) => {
                        self.microphone_test.busy = false;
                        self.microphone_test.error = error;
                    }
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
                    (Some(Pending::Credential(server_id, revision)), Err(error)) => {
                        if revision != self.connect_revision || self.closing {
                            self.sync_hotkey(cx);
                            cx.notify();
                            return;
                        }
                        self.connect_error = error;
                        self.open_password(server_id, window, cx);
                    }
                    (Some(Pending::SaveServer(revision)), Err(error)) => {
                        if revision == self.server_form_revision
                            && matches!(self.modal, Some(Modal::Server { .. }))
                        {
                            self.server_error = error;
                        } else {
                            self.error = error;
                        }
                    }
                    (Some(_), Err(error)) | (None, Err(error)) => self.error = error,
                    (None, Ok(_)) => {}
                }
            }
        }
        self.flush_queued_voice(cx);
        self.sync_hotkey(cx);
        cx.notify();
    }

    fn flush_queued_voice(&mut self, cx: &mut Context<Self>) {
        if self.closing
            || self.voice.busy
            || !self.workspace.session.switching_channel_id.is_empty()
            || self.pending.values().any(|p| matches!(p, Pending::Voice))
        {
            return;
        }
        self.voice_target = None;
        if let Some(next) = self.queued_voice.take() {
            if self.workspace.connected() {
                self.configure_voice(|v| *v = next, cx);
            }
        }
    }

    fn apply_workspace(&mut self, value: Value, window: &mut Window, cx: &mut Context<Self>) {
        match serde_json::from_value::<Workspace>(value) {
            Ok(workspace) => {
                let detail_update = self
                    .detail_selection
                    .as_ref()
                    .map(|selection| {
                        detail_selection_change(selection, &self.workspace, &workspace)
                    })
                    .unwrap_or(DetailChange::Keep);
                let refresh_details = if detail_update == DetailChange::Refresh {
                    self.detail_selection.clone()
                } else {
                    None
                };
                if detail_update == DetailChange::Clear {
                    self.clear_selected_details();
                }
                if workspace.session.id != self.workspace.session.id {
                    self.queued_voice = None;
                    self.voice_target = None;
                    self.voice = VoiceState::default();
                    self.icon_requested.clear();
                    self.icon_failures.clear();
                    self.icon_cache.clear();
                    self.icon_sizes.clear();
                    self.set_push_to_talk(false, cx);
                }
                if !workspace.connected() {
                    self.device_menu = None;
                    self.detail_value = None;
                }
                let previous_key = draft_key(&self.workspace);
                let next_key = draft_key(&workspace);
                if previous_key != next_key {
                    self.drafts
                        .insert(previous_key, self.chat_input.read(cx).value().to_string());
                    let draft = self.drafts.get(&next_key).cloned().unwrap_or_default();
                    self.chat_input
                        .update(cx, |input, cx| input.set_value(draft, window, cx));
                }
                if self.selected_server != "__preview__"
                    && !workspace
                        .servers
                        .iter()
                        .any(|server| server.id == self.selected_server)
                {
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
                if let Some(selection) = refresh_details {
                    self.select_details(selection, cx);
                }
            }
            Err(error) => self.error = format!("核心工作区格式无效：{error}"),
        }
    }

    fn apply_voice(&mut self, value: Value) {
        match serde_json::from_value::<VoiceState>(value) {
            Ok(voice) => {
                if !voice.enabled || voice.muted || voice.deafened || voice.activation_mode != "ptt"
                {
                    self.ptt_pressed = false;
                }
                if !voice.enabled {
                    self.device_menu = None;
                }
                self.voice = voice;
            }
            Err(error) => self.error = format!("核心语音状态格式无效：{error}"),
        }
    }

    fn apply_microphone_test(&mut self, value: Value) {
        match serde_json::from_value(value) {
            Ok(value) => self.microphone_test = value,
            Err(error) => self.error = format!("无法读取麦克风测试状态：{error}"),
        }
    }

    pub fn begin_shutdown(&mut self, cx: &mut Context<Self>) {
        if self.closing {
            return;
        }
        self._audio_update = None;
        self._detail_update = None;
        self.flush_preferences();
        self.clear_hotkey(cx);
        self.closing = true;
        self.device_menu = None;
        self.modal = None;
        if self.core.is_none() {
            self.finish_exit(cx);
            return;
        }
        let core = self.core.clone().unwrap();
        let enabled = self.preferences.notifications_enabled && !self.voice.deafened;
        let volume = self.preferences.notification_volume;
        cx.spawn(async move |_, _| {
            let _ = core.prepare_shutdown(enabled, volume).await;
            let _ = core.request("Shutdown", json!({}));
            core.finish_shutdown();
        })
        .detach();
        cx.spawn(async |view, cx| {
            cx.background_executor().timer(Duration::from_secs(7)).await;
            let _ = view.update(cx, |this, cx| {
                if this.closing {
                    if let Some(core) = &this.core {
                        core.finish_shutdown();
                    }
                    this.finish_exit(cx);
                }
            });
        })
        .detach();
        cx.notify();
    }

    fn finish_exit(&mut self, cx: &mut Context<Self>) {
        if let Some(core) = &self.core {
            core.finish_shutdown();
        }
        self.core = None;
        cx.quit();
    }

    fn set_push_to_talk(&mut self, pressed: bool, cx: &mut Context<Self>) {
        let pressed = pressed
            && !self.microphone_test.enabled
            && !self.microphone_test.busy
            && ptt_can_send(&self.workspace, &self.voice, self.closing)
            && !self.speaking_transition_pending();
        if self.ptt_pressed == pressed {
            return;
        }
        self.ptt_pressed = pressed;
        self.request(
            "SetPushToTalk",
            json!({"pressed": pressed}),
            Pending::PushToTalk,
        );
        cx.notify();
    }

    fn handle_ptt_key(&mut self, pressed: bool, cx: &mut Context<Self>) {
        if self.ptt_key_down == pressed {
            return;
        }
        self.ptt_key_down = pressed;
        self.set_push_to_talk(pressed, cx);
    }

    fn clear_hotkey(&mut self, cx: &mut Context<Self>) {
        self.set_push_to_talk(false, cx);
        self.hotkey_epoch.fetch_add(1, Ordering::AcqRel);
        if let Some(hotkey) = self.hotkey.take()
            && let Some(manager) = &self.hotkey_manager
        {
            if let Err(error) = manager.unregister(hotkey) {
                self.hotkey_error = format!("无法释放快捷键：{error}");
            }
        }
        self.hotkey_attempt = None;
    }

    fn sync_hotkey(&mut self, cx: &mut Context<Self>) {
        let available = !self.closing
            && self.preferences.global_push_to_talk
            && self.preferences.activation_mode == "ptt"
            && self.voice.activation_mode == "ptt"
            && self.workspace.connected()
            && (self.ptt_key_down
                || self.microphone_test.enabled
                || self.microphone_test.busy
                || (self.voice.enabled
                    && self.voice.active
                    && !self.voice.muted
                    && !self.voice.deafened
                    && !self.voice.busy));
        if !available {
            if self.hotkey.is_some() {
                self.clear_hotkey(cx);
            }
            return;
        }
        if self.hotkey.is_some()
            || self.hotkey_attempt.as_ref() == Some(&self.preferences.push_to_talk_shortcut)
        {
            return;
        }
        self.hotkey_attempt = Some(self.preferences.push_to_talk_shortcut.clone());
        let result = self
            .preferences
            .push_to_talk_shortcut
            .parse::<HotKey>()
            .map_err(|error| error.to_string())
            .and_then(|hotkey| {
                self.hotkey_manager
                    .as_ref()
                    .ok_or_else(|| "当前平台的全局快捷键管理器不可用".to_owned())?
                    .register(hotkey)
                    .map_err(|error| error.to_string())?;
                Ok(hotkey)
            });
        match result {
            Ok(hotkey) => {
                self.hotkey_epoch.fetch_add(1, Ordering::AcqRel);
                self.hotkey = Some(hotkey);
                self.hotkey_error.clear();
            }
            Err(error) => {
                self.hotkey_error = format!("全局快捷键未生效：{error}；当前窗口仍可使用 F8")
            }
        }
    }

    fn select_details(&mut self, selection: DetailSelection, cx: &mut Context<Self>) {
        if !self.workspace.connected() {
            return;
        }
        self.detail_selection = Some(selection.clone());
        self.detail_value = None;
        self.detail_error.clear();
        self.detail_revision = self.detail_revision.wrapping_add(1);
        let revision = self.detail_revision;
        self._detail_update = Some(cx.spawn(async move |view, cx| {
            cx.background_executor()
                .timer(Duration::from_millis(200))
                .await;
            let _ = view.update(cx, |this, cx| {
                if !this.closing && this.detail_revision == revision && this.workspace.connected() {
                    this.request_selected_details();
                    cx.notify();
                }
            });
        }));
        cx.notify();
    }

    fn request_selected_details(&mut self) {
        let Some(selection) = self.detail_selection.clone() else {
            return;
        };
        let session = self.workspace.session.id.clone();
        let (method, params) = match &selection {
            DetailSelection::Channel(id) => (
                "GetChannelDetails",
                json!({"sessionID": session, "channelID": id}),
            ),
            DetailSelection::User(id) => (
                "GetUserDetails",
                json!({"sessionID": session, "userID": id}),
            ),
        };
        self.request(
            method,
            params,
            Pending::Details {
                session,
                selection,
                revision: self.detail_revision,
            },
        );
    }

    fn join_channel(&mut self, id: &str, cx: &mut Context<Self>) {
        if self.closing
            || !self.workspace.session.switching_channel_id.is_empty()
            || !self.workspace.session.sending_message_id.is_empty()
            || self.workspace.session.channel_id == id
            || self
                .pending
                .values()
                .any(|p| matches!(p, Pending::Workspace("SelectChannel")))
        {
            return;
        }
        self.set_push_to_talk(false, cx);
        self.error.clear();
        self.request(
            "SelectChannel",
            json!({"id": id}),
            Pending::Workspace("SelectChannel"),
        );
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
        self.pending.values().any(|pending| {
            !matches!(
                pending,
                Pending::Notification
                    | Pending::Icon { .. }
                    | Pending::Details { .. }
                    | Pending::PushToTalk
            )
        })
    }

    fn speaking_transition_pending(&self) -> bool {
        self.pending.values().any(|pending| match pending {
            Pending::Voice
            | Pending::VoicePreferences { .. }
            | Pending::Credential(_, _)
            | Pending::Connect(_, _) => true,
            Pending::Workspace(method) => matches!(
                *method,
                "SelectChannel" | "DisconnectServer" | "LeavePreview" | "OpenPreview"
            ),
            _ => false,
        })
    }

    fn update_icon_cache(&mut self, workspace: &Workspace) {
        let live = workspace
            .channels
            .iter()
            .map(|c| c.icon_ref.clone())
            .collect::<HashSet<_>>();
        self.icon_cache
            .retain(|reference, _| live.contains(reference));
        self.icon_sizes
            .retain(|reference, _| self.icon_cache.contains_key(reference));
        self.icon_requested
            .retain(|reference| live.contains(reference));
        self.icon_failures
            .retain(|reference, _| live.contains(reference));
        let mut pending = self
            .pending
            .values()
            .filter(|p| matches!(p, Pending::Icon { .. }))
            .count();
        for reference in workspace
            .channels
            .iter()
            .map(|c| &c.icon_ref)
            .filter(|r| !r.is_empty())
        {
            if pending >= 2 || self.icon_requested.len() + self.icon_failures.len() >= 128 {
                break;
            }
            if self.icon_requested.contains(reference) || self.icon_failures.contains_key(reference)
            {
                continue;
            }
            self.icon_requested.insert(reference.clone());
            let session = workspace.session.id.clone();
            self.request(
                "GetIconResource",
                json!({"sessionID": session, "ref": reference}),
                Pending::Icon {
                    session,
                    reference: reference.clone(),
                },
            );
            pending += 1;
        }
    }

    fn process_notifications(&mut self, workspace: &Workspace) {
        if self.closing {
            return;
        }
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

    fn flush_preferences(&mut self) {
        self.preference_revision += 1;
        if let Err(error) = self
            .preferences
            .save_ordered(self.preference_revision, &self.preference_save_order)
        {
            self.error = format!("无法保存声音设置：{error}");
        }
    }

    fn save_preferences(&mut self, cx: &mut Context<Self>) {
        self.preference_revision += 1;
        let revision = self.preference_revision;
        let preferences = self.preferences.clone();
        let order = self.preference_save_order.clone();
        let save = cx
            .background_executor()
            .spawn(async move { preferences.save_ordered(revision, &order) });
        cx.spawn(async move |view, cx| {
            if let Err(error) = save.await {
                let _ = view.update(cx, |this, cx| {
                    if this.preference_revision == revision {
                        this.error = format!("无法保存声音设置：{error}");
                        cx.notify();
                    }
                });
            }
        })
        .detach();
    }

    fn open_server_form(
        &mut self,
        profile: Option<ServerProfile>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.server_form_revision = self.server_form_revision.wrapping_add(1);
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
        if matches!(self.modal, Some(Modal::Audio))
            && (self.microphone_test.enabled || self.microphone_test.busy)
        {
            self.configure_microphone_test(false, cx);
        }
        self.device_menu = None;
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
        } else if self.pending.values().any(|pending| {
            matches!(
                pending,
                Pending::VoicePreferences {
                    connect: Some(_),
                    ..
                } | Pending::Credential(_, _)
            )
        }) {
            self.connect_revision = self.connect_revision.wrapping_add(1);
            cx.notify();
        } else {
            cx.propagate();
        }
    }

    fn save_server(&mut self, cx: &mut Context<Self>) {
        if self.closing || self.is_busy() {
            return;
        }
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
            Pending::SaveServer(self.server_form_revision),
        );
        cx.notify();
    }

    fn begin_connect(&mut self, server_id: String, cx: &mut Context<Self>) {
        if self.microphone_test.enabled || self.microphone_test.busy {
            self.configure_microphone_test(false, cx);
        }
        self.connect_error.clear();
        self.connect_revision = self.connect_revision.wrapping_add(1);
        if can_prepare_voice_preferences(&self.workspace.session.mode) {
            self.store_voice_preferences(Some(server_id));
        } else {
            self.request_connection_credentials(server_id);
        }
        cx.notify();
    }

    fn request_connection_credentials(&mut self, server_id: String) {
        self.request(
            "GetServerCredentialStatus",
            json!({"id": server_id}),
            Pending::Credential(server_id, self.connect_revision),
        );
    }

    fn store_voice_preferences(&mut self, connect: Option<String>) {
        if !can_prepare_voice_preferences(&self.workspace.session.mode) || self.closing {
            return;
        }
        let mut config = VoiceState::default();
        self.apply_audio_preferences(&mut config);
        config.volume = self.voice.volume;
        self.request(
            "SetVoicePreferences",
            voice_params(&config),
            Pending::VoicePreferences {
                connect,
                session: self.workspace.session.id.clone(),
                revision: self.connect_revision,
            },
        );
    }

    fn connect_with_password(&mut self, server_id: String, cx: &mut Context<Self>) {
        if self.closing
            || self.is_busy()
            || !matches!(&self.modal, Some(Modal::Password { server_id: current }) if current == &server_id)
        {
            return;
        }
        self.connect_error.clear();
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
        let mut next = self
            .queued_voice
            .as_ref()
            .or(self.voice_target.as_ref())
            .unwrap_or(&self.voice)
            .clone();
        self.apply_audio_preferences(&mut next);
        change(&mut next);
        if self.preferences.input_device_id != next.input_device_id
            || self.preferences.output_device_id != next.output_device_id
        {
            self.preferences.input_device_id = next.input_device_id.clone();
            self.preferences.output_device_id = next.output_device_id.clone();
            self.save_preferences(cx);
        }
        let stopping = !next.enabled;
        if stopping {
            self._audio_update = None;
            self.audio_settings_pending = false;
        }
        let request_pending = self
            .pending
            .values()
            .any(|pending| matches!(pending, Pending::Voice));
        if (self.voice.busy || request_pending) && !stopping {
            self.queued_voice = Some(next);
            cx.notify();
            return;
        }
        self.queued_voice = None;
        if !self.workspace.connected() {
            self.store_voice_preferences(None);
            cx.notify();
            return;
        }
        if next.muted
            || next.deafened
            || !next.enabled
            || next.activation_mode != self.voice.activation_mode
        {
            self.set_push_to_talk(false, cx);
        }
        if next.deafened && !self.voice.deafened {
            self.request("StopNotifications", json!({}), Pending::Notification);
        }
        self.voice_target = Some(next.clone());
        self.request("ConfigureVoice", voice_params(&next), Pending::Voice);
        cx.notify();
    }

    fn apply_audio_preferences(&self, voice: &mut VoiceState) {
        voice.activation_mode = self.preferences.activation_mode.clone();
        voice.vad_threshold_db = self.preferences.vad_threshold_db;
        voice.noise_suppression = self.preferences.noise_suppression.clone();
        voice.echo_cancellation = self.preferences.echo_cancellation;
        voice.echo_suppression = self.preferences.echo_suppression;
        voice.ducking = self.preferences.ducking;
        voice.input_device_id = self.preferences.input_device_id.clone();
        voice.output_device_id = self.preferences.output_device_id.clone();
    }

    fn update_audio_preferences(
        &mut self,
        change: impl FnOnce(&mut Preferences),
        cx: &mut Context<Self>,
    ) {
        let previous = self.preferences.clone();
        change(&mut self.preferences);
        self.audio_settings_pending = true;
        if previous.activation_mode != self.preferences.activation_mode
            || previous.global_push_to_talk != self.preferences.global_push_to_talk
            || previous.push_to_talk_shortcut != self.preferences.push_to_talk_shortcut
        {
            self.clear_hotkey(cx);
        }
        let original_session = self.workspace.session.id.clone();
        self._audio_update = Some(cx.spawn(async move |view, cx| {
            let deadline = Instant::now() + Duration::from_secs(20);
            cx.background_executor()
                .timer(Duration::from_millis(120))
                .await;
            let _ = view.update(cx, |this, cx| this.save_preferences(cx));
            loop {
                let done = view
                    .update(cx, |this, cx| {
                        if this.closing {
                            this.audio_settings_pending = false;
                            return true;
                        }
                        if Instant::now() >= deadline {
                            this.audio_settings_pending = false;
                            this.error = "音频设置已保存，但尚未应用；请待语音就绪后重试".into();
                            cx.notify();
                            return true;
                        }
                        if this.workspace.session.id != original_session
                            && this.workspace.connected()
                            && !this.voice.enabled
                            && this.voice.error.is_empty()
                        {
                            return false;
                        }
                        if this.voice.busy
                            || matches!(
                                this.workspace.session.mode.as_str(),
                                "connecting" | "disconnecting"
                            )
                            || !this.workspace.session.switching_channel_id.is_empty()
                            || this.pending.values().any(|p| {
                                matches!(p, Pending::Voice | Pending::VoicePreferences { .. })
                            })
                        {
                            return false;
                        }
                        this.audio_settings_pending = false;
                        if this.workspace.connected() && this.voice.enabled {
                            this.configure_voice(|_| {}, cx);
                        } else {
                            this.store_voice_preferences(None);
                        }
                        cx.notify();
                        true
                    })
                    .unwrap_or(true);
                if done {
                    break;
                }
                cx.background_executor()
                    .timer(Duration::from_millis(50))
                    .await;
            }
        }));
        self.sync_hotkey(cx);
        cx.notify();
    }

    fn can_start_microphone_test(&self) -> bool {
        !self.closing
            && self.capabilities.voice
            && matches!(
                self.workspace.session.mode.as_str(),
                "" | "offline" | "failed" | "preview" | "connected"
            )
            && !self.voice.busy
            && !self.microphone_test.enabled
            && !self.microphone_test.busy
            && !self.audio_settings_pending
            && self.queued_voice.is_none()
            && self.workspace.session.switching_channel_id.is_empty()
            && !self.pending.values().any(|pending| {
                matches!(
                    pending,
                    Pending::Voice | Pending::VoicePreferences { .. } | Pending::MicrophoneTest
                )
            })
    }

    fn configure_microphone_test(&mut self, enabled: bool, cx: &mut Context<Self>) {
        if enabled && !self.can_start_microphone_test() {
            return;
        }
        if enabled {
            self.set_push_to_talk(false, cx);
            self._audio_update = None;
            self.queued_voice = None;
            self.voice_target = None;
        }
        let mut config = VoiceState::default();
        self.apply_audio_preferences(&mut config);
        config.enabled = enabled;
        config.muted = false;
        self.microphone_test.busy = true;
        self.request(
            "ConfigureMicrophoneTest",
            voice_params(&config),
            Pending::MicrophoneTest,
        );
        cx.notify();
    }

    fn navigation_width(&self) -> f32 {
        (if self.preferences.server_sidebar_collapsed {
            52.
        } else {
            172.
        }) + if self.preferences.channel_sidebar_collapsed {
            44.
        } else {
            220.
        }
    }

    fn render_rail(&self, view: &Entity<Self>) -> AnyElement {
        let collapsed = self.preferences.server_sidebar_collapsed;
        let mut servers = div().flex().flex_col().gap_1().p_1().w_full();
        for server in &self.workspace.servers {
            let id = server.id.clone();
            let entity = view.clone();
            let menu_entity = view.clone();
            let menu_id = id.clone();
            let keyboard_entity = view.clone();
            let keyboard_id = id.clone();
            let online = self.workspace.connected() && self.workspace.session.server_id == id;
            let tooltip = format!("{}{}", server.name, if online { " · 在线" } else { "" });
            servers = servers.child(
                div()
                    .id(SharedString::from(format!("server-menu-{id}")))
                    .flex_shrink_0()
                    .child(
                        div()
                            .id(SharedString::from(format!("server-{id}")))
                            .tab_index(0)
                            .h(px(42.))
                            .w_full()
                            .flex_shrink_0()
                            .px_2()
                            .flex()
                            .items_center()
                            .gap_2()
                            .rounded(px(6.))
                            .cursor_pointer()
                            .bg(gpui::rgba(if self.selected_server == id {
                                0xffffff14
                            } else {
                                0xffffff00
                            }))
                            .hover(|s| s.bg(rgb(HOVER)))
                            .tooltip(move |window, cx| {
                                Tooltip::new(tooltip.clone()).build(window, cx)
                            })
                            .on_key_down(move |event, window, cx| {
                                if event.keystroke.key == "f2" {
                                    keyboard_entity.update(cx, |this, cx| {
                                        let profile = this
                                            .workspace
                                            .servers
                                            .iter()
                                            .find(|p| p.id == keyboard_id)
                                            .cloned();
                                        if let Some(profile) = profile {
                                            this.open_server_form(Some(profile), window, cx);
                                        }
                                    });
                                    cx.stop_propagation();
                                }
                            })
                            .on_click(move |event, _, cx| {
                                entity.update(cx, |this, cx| {
                                    this.selected_server = id.clone();
                                    if this.preferences.channel_sidebar_collapsed {
                                        this.preferences.channel_sidebar_collapsed = false;
                                        this.save_preferences(cx);
                                    }
                                    if (event.is_keyboard() || event.click_count() >= 2)
                                        && !this.is_busy()
                                    {
                                        this.begin_connect(id.clone(), cx);
                                    }
                                    cx.notify();
                                });
                            })
                            .child(
                                div()
                                    .size(px(24.))
                                    .flex_shrink_0()
                                    .rounded(px(5.))
                                    .bg(rgb(0x303638))
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .text_xs()
                                    .text_color(rgb(if online { MINT } else { TEXT }))
                                    .child(server.name.chars().next().unwrap_or('?').to_string()),
                            )
                            .when(!collapsed, |row| {
                                row.child(
                                    div()
                                        .flex_1()
                                        .min_w_0()
                                        .text_xs()
                                        .truncate()
                                        .child(server.name.clone()),
                                )
                                .when(online, |row| {
                                    row.child(div().size(px(5.)).rounded_full().bg(rgb(MINT)))
                                })
                            })
                            .context_menu(move |menu, _, _| {
                                let editing = menu_id.clone();
                                let deleting = menu_id.clone();
                                let edit = menu_entity.clone();
                                let delete = menu_entity.clone();
                                menu.item(
                                    PopupMenuItem::new("编辑服务器")
                                        .icon(IconName::Settings2)
                                        .on_click(move |_, window, cx| {
                                            edit.update(cx, |this, cx| {
                                                let profile = this
                                                    .workspace
                                                    .servers
                                                    .iter()
                                                    .find(|p| p.id == editing)
                                                    .cloned();
                                                if let Some(profile) = profile {
                                                    this.open_server_form(
                                                        Some(profile),
                                                        window,
                                                        cx,
                                                    );
                                                }
                                            });
                                        }),
                                )
                                .separator()
                                .item(
                                    PopupMenuItem::new("删除服务器")
                                        .icon(IconName::Delete)
                                        .on_click(move |_, _, cx| {
                                            delete.update(cx, |this, cx| {
                                                this.modal = Some(Modal::Delete {
                                                    server_id: deleting.clone(),
                                                });
                                                cx.notify();
                                            });
                                        }),
                                )
                            }),
                    ),
            );
        }
        let toggle = view.clone();
        let add = view.clone();
        let mut footer = div().p_1().flex_shrink_0().flex().flex_col().gap_1();
        #[cfg(debug_assertions)]
        {
            let preview = view.clone();
            footer = footer.child(
                Button::new("preview-navigation")
                    .icon(IconName::Eye)
                    .ghost()
                    .when(!collapsed, |button| button.label("本地预览"))
                    .tooltip("本地预览")
                    .disabled(self.is_busy())
                    .on_click(move |_, _, cx| {
                        preview.update(cx, |this, cx| {
                            this.selected_server = "__preview__".into();
                            this.preferences.channel_sidebar_collapsed = false;
                            this.save_preferences(cx);
                            if this.workspace.session.mode != "preview"
                                && !this.workspace.connected()
                            {
                                this.request(
                                    "OpenPreview",
                                    json!({}),
                                    Pending::Workspace("OpenPreview"),
                                );
                            }
                            cx.notify();
                        });
                    }),
            );
        }
        footer = footer.child(
            Button::new("add-server")
                .icon(IconName::Plus)
                .ghost()
                .when(!collapsed, |button| button.label("添加服务器"))
                .tooltip("添加服务器")
                .on_click(move |_, window, cx| {
                    add.update(cx, |this, cx| this.open_server_form(None, window, cx));
                }),
        );
        div()
            .w(px(if collapsed { 52. } else { 172. }))
            .h_full()
            .flex_shrink_0()
            .bg(rgb(0x111315))
            .border_r_1()
            .border_color(rgb(LINE))
            .flex()
            .flex_col()
            .child(
                div()
                    .h(px(62.))
                    .flex_shrink_0()
                    .px_1()
                    .flex()
                    .items_center()
                    .justify_between()
                    .when(!collapsed, |header| {
                        header.child(
                            div()
                                .pl_2()
                                .flex()
                                .items_center()
                                .gap_2()
                                .child(img(self.logo.clone()).size(px(24.)))
                                .child(
                                    div()
                                        .text_sm()
                                        .font_weight(gpui::FontWeight::SEMIBOLD)
                                        .child("Resona"),
                                ),
                        )
                    })
                    .child(
                        Button::new("toggle-server-sidebar")
                            .icon(if collapsed {
                                IconName::PanelLeftOpen
                            } else {
                                IconName::PanelLeftClose
                            })
                            .ghost()
                            .tooltip(if collapsed {
                                "展开服务器栏"
                            } else {
                                "收起服务器栏"
                            })
                            .on_click(move |_, _, cx| {
                                toggle.update(cx, |this, cx| {
                                    this.preferences.server_sidebar_collapsed =
                                        !this.preferences.server_sidebar_collapsed;
                                    this.save_preferences(cx);
                                    cx.notify();
                                });
                            }),
                    ),
            )
            .child(
                div()
                    .flex_1()
                    .min_h_0()
                    .child(servers.overflow_y_scrollbar()),
            )
            .child(footer)
            .into_any_element()
    }

    fn render_sidebar(&self, view: &Entity<Self>) -> AnyElement {
        let collapsed = self.preferences.channel_sidebar_collapsed;
        let preview_selected = self.selected_server == "__preview__";
        let profile = self.selected_profile().cloned();
        let showing_session = if self.workspace.session.mode == "preview" {
            preview_selected || profile.is_none()
        } else {
            !preview_selected && self.selected_server == self.workspace.session.server_id
        };
        let title = if preview_selected
            || (profile.is_none() && self.workspace.session.mode == "preview")
        {
            "本地预览".to_string()
        } else {
            profile
                .as_ref()
                .map(|p| p.name.clone())
                .unwrap_or_else(|| "频道".into())
        };
        let toggle = view.clone();
        let header = div()
            .h(px(62.))
            .flex_shrink_0()
            .px_1()
            .flex()
            .items_center()
            .gap_1()
            .when(!collapsed, |header| {
                header.child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .pl_2()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .text_sm()
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .truncate()
                                .child(title),
                        )
                        .child(
                            div()
                                .text_size(px(10.))
                                .text_color(rgb(MUTED))
                                .truncate()
                                .child(if showing_session {
                                    self.session_label()
                                } else {
                                    "未连接".into()
                                }),
                        ),
                )
            })
            .child(
                Button::new("toggle-channel-sidebar")
                    .icon(if collapsed {
                        IconName::ChevronRight
                    } else {
                        IconName::ChevronLeft
                    })
                    .ghost()
                    .tooltip(if collapsed {
                        "展开频道栏"
                    } else {
                        "收起频道栏"
                    })
                    .on_click(move |_, _, cx| {
                        toggle.update(cx, |this, cx| {
                            this.preferences.channel_sidebar_collapsed =
                                !this.preferences.channel_sidebar_collapsed;
                            this.save_preferences(cx);
                            cx.notify();
                        });
                    }),
            );
        let mut sidebar = div()
            .w(px(if collapsed { 44. } else { 220. }))
            .h_full()
            .flex_shrink_0()
            .bg(rgb(PANEL))
            .border_r_1()
            .border_color(rgb(LINE))
            .flex()
            .flex_col()
            .child(header);
        if collapsed {
            return sidebar.into_any_element();
        }
        if can_show_session_channels(&self.workspace, &self.selected_server) {
            sidebar = sidebar.child(self.render_channels(view));
        } else {
            let mut empty = div()
                .flex_1()
                .min_h_0()
                .px_3()
                .flex()
                .flex_col()
                .items_center()
                .justify_center()
                .gap_3()
                .text_center()
                .text_xs()
                .text_color(rgb(MUTED))
                .child(if preview_selected && self.workspace.connected() {
                    "断开当前连接后可进入本地预览"
                } else if profile.is_some() {
                    "此服务器尚未连接"
                } else {
                    "选择或添加服务器"
                });
            if let Some(profile) = profile {
                let entity = view.clone();
                empty = empty.child(
                    Button::new("connect-selected-server")
                        .label(if self.workspace.connected() {
                            "切换连接"
                        } else {
                            "连接服务器"
                        })
                        .primary()
                        .disabled(self.is_busy())
                        .on_click(move |_, _, cx| {
                            entity
                                .update(cx, |this, cx| this.begin_connect(profile.id.clone(), cx));
                        }),
                );
            }
            sidebar = sidebar.child(empty);
        }
        if showing_session {
            sidebar = sidebar.child(
                div()
                    .px_3()
                    .py_2()
                    .flex_shrink_0()
                    .text_size(px(10.))
                    .text_color(rgb(self.status_color()))
                    .child(self.status_detail()),
            );
        }
        sidebar.into_any_element()
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
                let inspecting =
                    self.detail_selection.as_ref() == Some(&DetailSelection::Channel(id.clone()));
                let is_switching = id == switching;
                let locked = channel.password_required;
                let channel_image = self.icon_cache.get(&channel.icon_ref).cloned();
                let entity = view.clone();
                let member_rows = users
                    .iter()
                    .filter(|u| u.channel_id == id)
                    .cloned()
                    .map(|user| {
                        let user_id = user.id.clone();
                        let inspecting_user = self.detail_selection.as_ref()
                            == Some(&DetailSelection::User(user_id.clone()));
                        let user_entity = view.clone();
                        let speaking = user_is_speaking(
                            &self.workspace,
                            &self.voice,
                            transition_pending,
                            &user,
                        );
                        div()
                            .id(SharedString::from(format!("tree-user-{user_id}")))
                            .tab_index(0)
                            .cursor_pointer()
                            .border_l_2()
                            .border_color(gpui::rgba(if inspecting_user {
                                0x9dbdafaa
                            } else {
                                0x00000000
                            }))
                            .hover(|s| s.bg(rgb(HOVER)))
                            .on_click(move |_, _, cx| {
                                user_entity.update(cx, |this, cx| {
                                    this.select_details(DetailSelection::User(user_id.clone()), cx)
                                });
                            })
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
                            .border_l_2()
                            .border_color(gpui::rgba(if inspecting {
                                0x9dbdafaa
                            } else {
                                0x00000000
                            }))
                            .bg(rgb(if selected { 0x2a2f31 } else { PANEL }))
                            .hover(|s| s.bg(rgb(HOVER)))
                            .flex()
                            .items_center()
                            .gap_2()
                            .on_click(move |event, _, cx| {
                                entity.update(cx, |this, cx| {
                                    this.select_details(DetailSelection::Channel(id.clone()), cx);
                                    if !locked && (event.is_keyboard() || event.click_count() >= 2)
                                    {
                                        this.join_channel(&id, cx);
                                    }
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
                    .min_h(px(104.))
                    .px_5()
                    .py_4()
                    .flex()
                    .items_start()
                    .gap_3()
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .flex()
                            .flex_col()
                            .gap_2()
                            .child(
                                div()
                                    .text_size(px(24.))
                                    .truncate()
                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                    .child(channel_name),
                            )
                            .child(div().text_xs().truncate().text_color(rgb(MUTED)).child(
                                if self.workspace.session.mode == "preview" {
                                    "本地预览".to_owned()
                                } else {
                                    let description = channel
                                        .as_ref()
                                        .map(|c| c.description.as_str())
                                        .unwrap_or("");
                                    format!(
                                        "{}{}{}",
                                        self.workspace.session.server_name,
                                        if description.is_empty() { "" } else { " · " },
                                        description
                                    )
                                },
                            )),
                    )
                    .when(channel.is_some(), |header| {
                        let entity = view.clone();
                        let id = channel.as_ref().unwrap().id.clone();
                        header.child(
                            Button::new("channel-details")
                                .icon(IconName::Info)
                                .disabled(!self.workspace.connected())
                                .ghost()
                                .tooltip("频道资料")
                                .on_click(move |_, _, cx| {
                                    entity.update(cx, |this, cx| {
                                        this.select_details(
                                            DetailSelection::Channel(id.clone()),
                                            cx,
                                        )
                                    });
                                }),
                        )
                    }),
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
                        if cfg!(debug_assertions) {
                            "选择书签连接，或使用本地预览"
                        } else {
                            "选择或添加服务器"
                        }
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
                    .bg(gpui::rgba(0xffffff04))
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
        div()
            .w(px(280.))
            .h_full()
            .flex_shrink_0()
            .bg(gpui::rgba(0x16181bed))
            .border_l_1()
            .border_color(gpui::rgba(0xffffff22))
            .px_4()
            .child(self.render_selected_details(view))
            .overflow_y_scrollbar()
            .into_any_element()
    }

    fn render_voicebar(&self, view: &Entity<Self>) -> AnyElement {
        let connected = self.workspace.connected();
        let disabled =
            !connected || self.voice.busy || !self.voice.enabled || !self.capabilities.voice;
        let target = self
            .queued_voice
            .as_ref()
            .or(self.voice_target.as_ref())
            .unwrap_or(&self.voice);
        let label = if self.microphone_test.enabled || self.microphone_test.busy {
            "麦克风试听中"
        } else if !self.voice.error.is_empty() {
            &self.voice.error
        } else if !connected {
            &self.session_label()
        } else if self.voice.busy {
            "正在更新语音"
        } else if !self.voice.enabled {
            "语音未启用"
        } else if self.voice.deafened {
            "收听与发送已暂停"
        } else if self.voice.muted {
            "麦克风已静音"
        } else if self.voice.activation_mode == "ptt" && !self.ptt_pressed {
            "等待按键发言"
        } else if self.voice.activation_mode == "vad" {
            "语音检测已开启"
        } else {
            "麦克风已开启"
        };
        let nickname = self
            .workspace
            .users
            .iter()
            .find(|u| u.is_self)
            .map(|u| u.nickname.clone())
            .unwrap_or_else(|| "Resona".into());
        let mode = self.workspace.session.mode.as_str();
        let method = if mode == "preview" {
            "LeavePreview"
        } else {
            "DisconnectServer"
        };
        div()
            .h(px(72.))
            .w_full()
            .flex_shrink_0()
            .px_5()
            .flex()
            .items_center()
            .gap_3()
            .bg(gpui::rgba(0x141619f5))
            .border_t_1()
            .border_color(gpui::rgba(0xffffff24))
            .child(
                div()
                    .size(px(32.))
                    .rounded(px(6.))
                    .bg(rgb(0x293735))
                    .flex()
                    .items_center()
                    .justify_center()
                    .text_sm()
                    .child(nickname.chars().next().unwrap_or('R').to_string()),
            )
            .child(
                div()
                    .flex_1()
                    .min_w_0()
                    .flex()
                    .flex_col()
                    .gap_1()
                    .child(div().text_xs().truncate().child(nickname))
                    .child(
                        div()
                            .text_size(px(11.))
                            .text_color(rgb(if self.voice.error.is_empty() {
                                MUTED
                            } else {
                                RED
                            }))
                            .truncate()
                            .child(label.to_owned()),
                    ),
            )
            .child(
                Button::new("toggle-voice")
                    .label(if self.voice.enabled {
                        "停用语音"
                    } else {
                        "启用语音"
                    })
                    .ghost()
                    .disabled(!connected || !self.capabilities.voice)
                    .on_click({
                        let entity = view.clone();
                        move |_, _, cx| {
                            entity.update(cx, |this, cx| {
                                let enabled = !this.voice.enabled;
                                this.configure_voice(
                                    |v| {
                                        v.enabled = enabled;
                                        if enabled {
                                            v.muted = true;
                                        }
                                    },
                                    cx,
                                );
                            });
                        }
                    }),
            )
            .child(
                Button::new("mute")
                    .icon(if self.voice.muted {
                        VoiceIcon::MicOff
                    } else {
                        VoiceIcon::Mic
                    })
                    .selected(self.voice.muted)
                    .tooltip(if self.voice.muted {
                        "解除麦克风静音"
                    } else {
                        "将麦克风静音"
                    })
                    .disabled(disabled)
                    .on_click({
                        let entity = view.clone();
                        move |_, _, cx| {
                            entity.update(cx, |this, cx| {
                                let muted = !this.voice.muted;
                                this.configure_voice(|v| v.muted = muted, cx);
                            });
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
                    .selected(self.voice.deafened)
                    .tooltip(if self.voice.deafened {
                        "恢复收听"
                    } else {
                        "停止收听与发送"
                    })
                    .disabled(disabled)
                    .on_click({
                        let entity = view.clone();
                        move |_, _, cx| {
                            entity.update(cx, |this, cx| {
                                let deafened = !this.voice.deafened;
                                this.configure_voice(|v| v.deafened = deafened, cx);
                            });
                        }
                    }),
            )
            .child(div().w(px(1.)).h(px(22.)).bg(rgb(LINE)))
            .child(
                Button::new("volume-down")
                    .icon(IconName::Minus)
                    .ghost()
                    .tooltip("降低收听音量")
                    .disabled(!connected || !self.voice.enabled || self.closing)
                    .on_click({
                        let entity = view.clone();
                        move |_, _, cx| {
                            entity.update(cx, |this, cx| {
                                this.configure_voice(|v| v.volume = v.volume.saturating_sub(10), cx)
                            });
                        }
                    }),
            )
            .child(
                div()
                    .w(px(40.))
                    .text_center()
                    .text_xs()
                    .text_color(rgb(MUTED))
                    .child(format!("{}%", target.volume)),
            )
            .child(
                Button::new("volume-up")
                    .icon(IconName::Plus)
                    .ghost()
                    .tooltip("提高收听音量")
                    .disabled(!connected || !self.voice.enabled || self.closing)
                    .on_click({
                        let entity = view.clone();
                        move |_, _, cx| {
                            entity.update(cx, |this, cx| {
                                this.configure_voice(
                                    |v| v.volume = v.volume.saturating_add(10).min(100),
                                    cx,
                                )
                            });
                        }
                    }),
            )
            .child(
                Button::new("audio-settings")
                    .icon(IconName::Settings2)
                    .ghost()
                    .tooltip("语音与设备设置")
                    .on_click({
                        let entity = view.clone();
                        move |_, _, cx| {
                            entity.update(cx, |this, cx| {
                                this.device_menu = None;
                                this.modal = Some(Modal::Audio);
                                cx.notify();
                            });
                        }
                    }),
            )
            .when(
                matches!(
                    mode,
                    "connecting" | "connected" | "disconnecting" | "preview"
                ),
                |bar| {
                    bar.child(
                        Button::new("disconnect")
                            .label(if mode == "connecting" {
                                "取消连接"
                            } else if mode == "preview" {
                                "退出预览"
                            } else {
                                "断开"
                            })
                            .ghost()
                            .on_click({
                                let entity = view.clone();
                                move |_, _, cx| {
                                    entity.update(cx, |this, cx| {
                                        this.request(method, json!({}), Pending::Workspace(method));
                                        cx.notify();
                                    });
                                }
                            }),
                    )
                },
            )
            .into_any_element()
    }
    fn render_device_picker(
        &self,
        label: &'static str,
        menu: DeviceMenu,
        view: &Entity<Self>,
    ) -> AnyElement {
        let id = if menu == DeviceMenu::Input {
            &self.preferences.input_device_id
        } else {
            &self.preferences.output_device_id
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
        let bounds_entity = view.clone();
        div()
            .relative()
            .flex()
            .flex_col()
            .gap_1()
            .child(div().text_size(px(9.)).text_color(rgb(MUTED)).child(label))
            .child(
                gpui::canvas(
                    move |bounds, _, cx| {
                        bounds_entity.update(cx, |this, _| {
                            this.device_trigger_bounds
                                [if menu == DeviceMenu::Input { 0 } else { 1 }] = Some(bounds);
                        });
                    },
                    |_, _, _, _| {},
                )
                .absolute()
                .size_full(),
            )
            .child(
                Button::new(if menu == DeviceMenu::Input {
                    "input-device"
                } else {
                    "output-device"
                })
                .label(name)
                .dropdown_caret(true)
                .disabled(
                    !self.capabilities.voice
                        || self.voice.busy
                        || matches!(
                            self.workspace.session.mode.as_str(),
                            "connecting" | "disconnecting"
                        )
                        || self.microphone_test.enabled
                        || self.microphone_test.busy,
                )
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

    fn dismiss_device_menu(&mut self, position: gpui::Point<gpui::Pixels>, cx: &mut Context<Self>) {
        if self
            .device_trigger_bounds
            .iter()
            .flatten()
            .any(|bounds| bounds.contains(&position))
        {
            return;
        }
        self.device_menu = None;
        cx.notify();
    }

    fn render_selected_details(&self, view: &Entity<Self>) -> AnyElement {
        let Some(selection) = &self.detail_selection else {
            return div().into_any_element();
        };
        let title = match selection {
            DetailSelection::Channel(id) => self
                .workspace
                .channels
                .iter()
                .find(|c| &c.id == id)
                .map(|c| c.name.clone()),
            DetailSelection::User(id) => self
                .workspace
                .users
                .iter()
                .find(|u| &u.id == id)
                .map(|u| u.nickname.clone()),
        }
        .unwrap_or_else(|| "对象已离开".into());
        let entity = view.clone();
        let mut section = div()
            .py_3()
            .border_b_1()
            .border_color(rgb(LINE))
            .flex()
            .flex_col()
            .gap_2()
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap_2()
                    .child(div().flex_1().min_w_0().text_sm().child(title))
                    .child(
                        Button::new("close-details")
                            .icon(IconName::Close)
                            .ghost()
                            .tooltip("关闭资料")
                            .on_click(move |_, _, cx| {
                                entity.update(cx, |this, cx| {
                                    this.clear_selected_details();
                                    cx.notify();
                                });
                            }),
                    ),
            );
        if !self.workspace.connected() {
            return section
                .child(div().text_xs().text_color(rgb(MUTED)).child("已断开连接"))
                .into_any_element();
        }
        if !self.detail_error.is_empty() {
            let selection = selection.clone();
            let entity = view.clone();
            return section
                .child(
                    div()
                        .text_xs()
                        .text_color(rgb(AMBER))
                        .child(self.detail_error.clone()),
                )
                .child(
                    Button::new("retry-details")
                        .label("重试")
                        .outline()
                        .on_click(move |_, _, cx| {
                            entity
                                .update(cx, |this, cx| this.select_details(selection.clone(), cx));
                        }),
                )
                .into_any_element();
        }
        let Some(value) = &self.detail_value else {
            return section
                .child(
                    div()
                        .h(px(30.))
                        .text_xs()
                        .text_color(rgb(MUTED))
                        .child("正在读取资料"),
                )
                .into_any_element();
        };
        let fields: &[(&str, &str)] = match selection {
            DetailSelection::Channel(_) => &[
                ("topic", "主题"),
                ("description", "描述"),
                ("codec", "编码"),
                ("codecQuality", "编码质量"),
                ("permanent", "永久频道"),
                ("semiPermanent", "半永久频道"),
                ("default", "默认频道"),
                ("passwordRequired", "需要密码"),
                ("maxClients", "人数上限"),
                ("maxClientsUnlimited", "人数不设上限"),
                ("maxFamilyClients", "频道组人数上限"),
                ("maxFamilyClientsUnlimited", "频道组不设上限"),
                ("maxFamilyClientsInherited", "继承上级限制"),
            ],
            DetailSelection::User(_) => &[
                ("description", "描述"),
                ("identityUID", "身份"),
                ("channelID", "所在频道"),
                ("away", "离开"),
                ("awayMessage", "离开消息"),
                ("inputMuted", "麦克风静音"),
                ("outputMuted", "输出静音"),
            ],
        };
        for (key, label) in fields {
            let text = if *key == "maxClients"
                && value.get("maxClientsUnlimited").and_then(Value::as_bool) == Some(true)
            {
                "无限制".into()
            } else if *key == "maxFamilyClients"
                && value
                    .get("maxFamilyClientsInherited")
                    .and_then(Value::as_bool)
                    == Some(true)
            {
                "继承上级".into()
            } else if *key == "maxFamilyClients"
                && value
                    .get("maxFamilyClientsUnlimited")
                    .and_then(Value::as_bool)
                    == Some(true)
            {
                "无限制".into()
            } else {
                detail_text(value.get(*key), key, &self.workspace)
            };
            section = section.child(
                div()
                    .flex()
                    .flex_col()
                    .gap_1()
                    .child(
                        div()
                            .text_size(px(10.))
                            .text_color(rgb(MUTED))
                            .child(*label),
                    )
                    .child(div().min_w_0().overflow_hidden().text_xs().child(text)),
            );
        }
        if let DetailSelection::Channel(id) = selection
            && id != &self.workspace.session.channel_id
        {
            let id = id.clone();
            let entity = view.clone();
            let locked = value
                .get("passwordRequired")
                .and_then(Value::as_bool)
                .unwrap_or(false);
            section = section.child(
                Button::new("join-detail-channel")
                    .label("加入频道")
                    .icon(IconName::ArrowRight)
                    .disabled(locked || self.speaking_transition_pending())
                    .on_click(move |_, _, cx| {
                        entity.update(cx, |this, cx| {
                            this.join_channel(&id, cx);
                            cx.notify();
                        });
                    }),
            );
        }
        section.into_any_element()
    }

    fn render_audio_settings(&self, view: &Entity<Self>) -> gpui::Div {
        let busy = self.voice.busy
            || matches!(
                self.workspace.session.mode.as_str(),
                "connecting" | "disconnecting"
            )
            || self.microphone_test.enabled
            || self.microphone_test.busy
            || self.pending.values().any(|p| {
                matches!(
                    p,
                    Pending::Voice | Pending::VoicePreferences { .. } | Pending::MicrophoneTest
                )
            });
        let disabled = busy || !self.capabilities.voice;
        let preferences_disabled = self.closing
            || !self.capabilities.voice
            || matches!(
                self.workspace.session.mode.as_str(),
                "connecting" | "disconnecting"
            )
            || self.microphone_test.enabled
            || self.microphone_test.busy;
        let entity = view.clone();
        let mut content = div()
            .flex()
            .flex_col()
            .gap_4()
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap_3()
                    .child(div().flex_1().child(modal_title("语音与设备")))
                    .child(div().w(px(72.)).text_xs().text_color(rgb(MUTED)).child(
                        if self.audio_settings_pending || self.voice.busy {
                            "应用中"
                        } else {
                            ""
                        },
                    ))
                    .child(
                        Button::new("open-notification-settings")
                            .label("提示音")
                            .ghost()
                            .disabled(self.microphone_test.enabled || self.microphone_test.busy)
                            .on_click({
                                let entity = entity.clone();
                                move |_, _, cx| {
                                    entity.update(cx, |this, cx| {
                                        this.modal = Some(Modal::Settings);
                                        this.device_menu = None;
                                        cx.notify();
                                    });
                                }
                            }),
                    )
                    .child(
                        Button::new("close-audio-settings")
                            .icon(IconName::Close)
                            .ghost()
                            .tooltip("关闭设置")
                            .on_click({
                                let entity = entity.clone();
                                move |_, window, cx| {
                                    entity.update(cx, |this, cx| this.close_modal(window, cx));
                                }
                            }),
                    ),
            )
            .when(!self.capabilities.voice, |s| {
                s.child(
                    div()
                        .text_xs()
                        .text_color(rgb(AMBER))
                        .child("此构建不提供音频设备能力"),
                )
            })
            .child(
                div()
                    .flex()
                    .gap_3()
                    .child(div().flex_1().min_w_0().child(self.render_device_picker(
                        "输入设备",
                        DeviceMenu::Input,
                        view,
                    )))
                    .child(div().flex_1().min_w_0().child(self.render_device_picker(
                        "输出设备",
                        DeviceMenu::Output,
                        view,
                    ))),
            );
        if let Some(menu) = self.device_menu {
            let kind = if menu == DeviceMenu::Input {
                "input"
            } else {
                "output"
            };
            let mut devices = vec![(String::new(), "系统默认".to_owned())];
            devices.extend(
                self.devices
                    .iter()
                    .filter(|d| d.kind == kind && !d.id.is_empty())
                    .map(|d| (d.id.clone(), d.name.clone())),
            );
            let entries = devices
                .into_iter()
                .map(|(id, name)| {
                    let entity = view.clone();
                    Button::new(SharedString::from(format!("audio-device-{kind}-{id}")))
                        .label(name)
                        .ghost()
                        .disabled(disabled)
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
                })
                .collect::<Vec<_>>();
            let entity = view.clone();
            content = content.child(
                div()
                    .max_h(px(125.))
                    .border_1()
                    .border_color(rgb(LINE))
                    .p_2()
                    .flex()
                    .flex_col()
                    .on_mouse_down_out(move |event, _, cx| {
                        entity.update(cx, |this, cx| this.dismiss_device_menu(event.position, cx));
                    })
                    .children(entries)
                    .overflow_y_scrollbar(),
            );
        }
        let mode_buttons = [
            ("continuous", "持续发送"),
            ("ptt", "按键发言"),
            ("vad", "语音检测"),
        ]
        .into_iter()
        .map(|(mode, label)| {
            let entity = view.clone();
            Button::new(SharedString::from(format!("activation-{mode}")))
                .label(label)
                .selected(self.preferences.activation_mode == mode)
                .disabled(preferences_disabled)
                .on_click(move |_, _, cx| {
                    entity.update(cx, |this, cx| {
                        this.update_audio_preferences(|p| p.activation_mode = mode.into(), cx)
                    });
                })
        })
        .collect::<Vec<_>>();
        content = content
            .child(
                div()
                    .border_t_1()
                    .border_color(rgb(LINE))
                    .pt_3()
                    .flex()
                    .flex_col()
                    .gap_2()
                    .child(div().text_xs().text_color(rgb(MUTED)).child("发言激活"))
                    .child(div().flex().gap_2().children(mode_buttons)),
            )
            .when(self.preferences.activation_mode == "ptt", |s| {
                let status = if !self.hotkey_error.is_empty() {
                    self.hotkey_error.clone()
                } else if self.hotkey.is_some() {
                    format!(
                        "{} · 全局快捷键已注册",
                        self.preferences.push_to_talk_shortcut
                    )
                } else if !self.preferences.global_push_to_talk {
                    "F8 · 仅当前窗口".into()
                } else {
                    "启用麦克风后注册全局快捷键".into()
                };
                s.child(self.audio_toggle(
                    "global-ptt",
                    "后台按键发言",
                    self.preferences.global_push_to_talk,
                    disabled,
                    |p, v| p.global_push_to_talk = v,
                    view,
                ))
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(
                            div().flex_1().child(
                                Input::new(&self.shortcut_input)
                                    .disabled(disabled || !self.preferences.global_push_to_talk),
                            ),
                        )
                        .child(
                            Button::new("apply-ptt-shortcut")
                                .label("应用快捷键")
                                .disabled(disabled || !self.preferences.global_push_to_talk)
                                .on_click({
                                    let entity = view.clone();
                                    move |_, _, cx| {
                                        entity.update(cx, |this, cx| {
                                            let value = this
                                                .shortcut_input
                                                .read(cx)
                                                .value()
                                                .trim()
                                                .to_owned();
                                            match value.parse::<HotKey>() {
                                                Ok(_) => this.update_audio_preferences(
                                                    |p| p.push_to_talk_shortcut = value,
                                                    cx,
                                                ),
                                                Err(error) => {
                                                    this.hotkey_error =
                                                        format!("快捷键格式无效：{error}");
                                                    cx.notify();
                                                }
                                            }
                                        });
                                    }
                                }),
                        ),
                )
                .child(
                    div()
                        .text_xs()
                        .text_color(rgb(if self.hotkey_error.is_empty() {
                            MUTED
                        } else {
                            AMBER
                        }))
                        .child(status),
                )
            })
            .when(self.preferences.activation_mode == "vad", |s| {
                s.child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(div().flex_1().text_xs().child("检测阈值"))
                        .child(
                            Button::new("vad-threshold-down")
                                .icon(IconName::Minus)
                                .ghost()
                                .disabled(preferences_disabled)
                                .on_click({
                                    let entity = view.clone();
                                    move |_, _, cx| {
                                        entity.update(cx, |this, cx| {
                                            this.update_audio_preferences(
                                                |p| {
                                                    p.vad_threshold_db =
                                                        (p.vad_threshold_db - 2).max(-60)
                                                },
                                                cx,
                                            )
                                        });
                                    }
                                }),
                        )
                        .child(
                            div()
                                .w(px(70.))
                                .text_xs()
                                .text_center()
                                .child(format!("{} dB", self.preferences.vad_threshold_db)),
                        )
                        .child(
                            Button::new("vad-threshold-up")
                                .icon(IconName::Plus)
                                .ghost()
                                .disabled(preferences_disabled)
                                .on_click({
                                    let entity = view.clone();
                                    move |_, _, cx| {
                                        entity.update(cx, |this, cx| {
                                            this.update_audio_preferences(
                                                |p| {
                                                    p.vad_threshold_db =
                                                        (p.vad_threshold_db + 2).min(0)
                                                },
                                                cx,
                                            )
                                        });
                                    }
                                }),
                        ),
                )
            });
        let suppress_buttons = [
            ("off", "关闭"),
            ("low", "低"),
            ("medium", "中"),
            ("high", "高"),
        ]
        .into_iter()
        .map(|(level, label)| {
            let entity = view.clone();
            Button::new(SharedString::from(format!("noise-{level}")))
                .label(label)
                .selected(self.preferences.noise_suppression == level)
                .disabled(preferences_disabled)
                .on_click(move |_, _, cx| {
                    entity.update(cx, |this, cx| {
                        this.update_audio_preferences(|p| p.noise_suppression = level.into(), cx)
                    });
                })
        })
        .collect::<Vec<_>>();
        content = content.child(
            div()
                .border_t_1()
                .border_color(rgb(LINE))
                .pt_3()
                .flex()
                .flex_col()
                .gap_3()
                .child(div().text_xs().text_color(rgb(MUTED)).child("背景噪声抑制"))
                .child(div().flex().gap_2().children(suppress_buttons))
                .child(self.audio_toggle(
                    "echo-cancellation",
                    "回声消除",
                    self.preferences.echo_cancellation,
                    preferences_disabled,
                    |p, v| p.echo_cancellation = v,
                    view,
                ))
                .child(self.audio_toggle(
                    "echo-suppression",
                    "残余回声抑制",
                    self.preferences.echo_suppression,
                    preferences_disabled,
                    |p, v| p.echo_suppression = v,
                    view,
                ))
                .child(self.audio_toggle(
                    "voice-ducking",
                    "发言时降低频道音量",
                    self.preferences.ducking,
                    preferences_disabled,
                    |p, v| p.ducking = v,
                    view,
                )),
        );
        let running = self.microphone_test.enabled || self.microphone_test.busy;
        let level = if self.microphone_test.active && !self.microphone_test.busy {
            self.microphone_test.input_level_db.clamp(-60, 0)
        } else {
            -60
        };
        content = content.child(
            div()
                .border_t_1()
                .border_color(rgb(LINE))
                .pt_3()
                .flex()
                .flex_col()
                .gap_2()
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(div().flex_1().text_sm().child("本地麦克风测试"))
                        .child(
                            Button::new("toggle-microphone-test")
                                .label(if running {
                                    "停止测试"
                                } else {
                                    "开始测试"
                                })
                                .icon(if running {
                                    VoiceIcon::MicOff
                                } else {
                                    VoiceIcon::Mic
                                })
                                .disabled(!running && !self.can_start_microphone_test())
                                .on_click(move |_, _, cx| {
                                    entity.update(cx, |this, cx| {
                                        this.configure_microphone_test(!running, cx)
                                    });
                                }),
                        ),
                )
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap_3()
                        .child(
                            div().w(px(340.)).h(px(8.)).bg(rgb(LINE)).child(
                                div()
                                    .w(px((level + 60) as f32 / 60. * 340.))
                                    .h_full()
                                    .bg(rgb(if level > -6 { AMBER } else { MINT })),
                            ),
                        )
                        .child(
                            div()
                                .w(px(70.))
                                .text_xs()
                                .text_right()
                                .child(format!("{level} dB")),
                        ),
                )
                .child(
                    div()
                        .min_h(px(18.))
                        .text_xs()
                        .text_color(rgb(if self.microphone_test.error.is_empty() {
                            MUTED
                        } else {
                            RED
                        }))
                        .child(if !self.microphone_test.error.is_empty() {
                            self.microphone_test.error.clone()
                        } else if self.microphone_test.busy {
                            "正在处理设备".into()
                        } else if self.microphone_test.active {
                            "本地回放中".into()
                        } else if matches!(
                            self.workspace.session.mode.as_str(),
                            "connecting" | "disconnecting"
                        ) {
                            "连接正在切换".into()
                        } else {
                            "已停止".into()
                        }),
                ),
        );
        if !self.voice.error.is_empty() {
            content = content.child(
                div()
                    .text_xs()
                    .text_color(rgb(RED))
                    .child(self.voice.error.clone()),
            );
        }
        if !self.error.is_empty() {
            content = content.child(
                div()
                    .text_xs()
                    .text_color(rgb(RED))
                    .child(self.error.clone()),
            );
        }
        div()
            .w(px(600.))
            .h(px(540.))
            .p_5()
            .flex()
            .flex_col()
            .child(content.flex_1().min_h_0().overflow_y_scrollbar())
    }

    fn audio_toggle(
        &self,
        id: &'static str,
        label: &'static str,
        checked: bool,
        disabled: bool,
        change: fn(&mut Preferences, bool),
        view: &Entity<Self>,
    ) -> AnyElement {
        let entity = view.clone();
        Checkbox::new(id)
            .label(label)
            .checked(checked)
            .disabled(disabled)
            .on_click(move |checked, _, cx| {
                entity.update(cx, |this, cx| {
                    this.update_audio_preferences(|p| change(p, *checked), cx)
                });
            })
            .into_any_element()
    }

    fn render_modal(&self, view: &Entity<Self>) -> Option<AnyElement> {
        let modal = self.modal.clone()?;
        let settings_page = matches!(modal, Modal::Audio | Modal::Settings);
        let audio_page = matches!(modal, Modal::Audio);
        let entity = view.clone();
        let card = match modal {
            Modal::Audio => self.render_audio_settings(view),
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
                    .w(px(600.))
                    .h(px(540.))
                    .p_5()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(modal_title("提示音"))
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
                                        this.save_preferences(cx);
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
                                                this.save_preferences(cx);
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
                                                this.save_preferences(cx);
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
        let card = if settings_page {
            let change_disabled = self.microphone_test.enabled || self.microphone_test.busy;
            div()
                .w(px(780.))
                .h(px(540.))
                .flex()
                .rounded(px(8.))
                .overflow_hidden()
                .border_1()
                .border_color(gpui::rgba(0xffffff38))
                .bg(gpui::rgba(0x17191df5))
                .shadow_lg()
                .child(
                    div()
                        .w(px(180.))
                        .h_full()
                        .flex_shrink_0()
                        .p_4()
                        .flex()
                        .flex_col()
                        .gap_3()
                        .bg(gpui::rgba(0x080a0d55))
                        .border_r_1()
                        .border_color(gpui::rgba(0xffffff14))
                        .child(
                            div()
                                .py_3()
                                .text_lg()
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .child("设置"),
                        )
                        .child(
                            Button::new("settings-nav-audio")
                                .label("语音与设备")
                                .icon(IconName::Settings2)
                                .ghost()
                                .selected(audio_page)
                                .w_full()
                                .disabled(change_disabled)
                                .on_click({
                                    let entity = view.clone();
                                    move |_, _, cx| {
                                        entity.update(cx, |this, cx| {
                                            this.device_menu = None;
                                            this.modal = Some(Modal::Audio);
                                            cx.notify();
                                        });
                                    }
                                }),
                        )
                        .child(
                            Button::new("settings-nav-notifications")
                                .label("提示音")
                                .icon(VoiceIcon::Volume)
                                .ghost()
                                .selected(!audio_page)
                                .w_full()
                                .disabled(change_disabled)
                                .on_click({
                                    let entity = view.clone();
                                    move |_, _, cx| {
                                        entity.update(cx, |this, cx| {
                                            this.device_menu = None;
                                            this.modal = Some(Modal::Settings);
                                            cx.notify();
                                        });
                                    }
                                }),
                        ),
                )
                .child(card)
                .into_any_element()
        } else {
            card.into_any_element()
        };
        Some(
            div()
                .absolute()
                .inset_0()
                .bg(gpui::rgba(0x00000066))
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
            .on_action(cx.listener(|this, _: &Quit, _, cx| this.begin_shutdown(cx)))
            .capture_key_down(cx.listener(|this, event: &gpui::KeyDownEvent, _, cx| {
                if event.keystroke.key == "f8" && this.hotkey.is_none() {
                    if !event.is_held {
                        this.handle_ptt_key(true, cx);
                    }
                    cx.stop_propagation();
                }
            }))
            .capture_key_up(cx.listener(|this, event: &gpui::KeyUpEvent, _, cx| {
                if event.keystroke.key == "f8" && this.hotkey.is_none() {
                    this.handle_ptt_key(false, cx);
                    cx.stop_propagation();
                }
            }))
            .font_family(if cfg!(target_os = "windows") {
                "Segoe UI"
            } else {
                ".SystemUIFont"
            })
            .text_color(rgb(TEXT))
            .bg(rgb(BG))
            .flex()
            .flex_col()
            .child(
                div()
                    .flex_1()
                    .min_h_0()
                    .w_full()
                    .flex()
                    .child(self.render_rail(&view))
                    .child(self.render_sidebar(&view))
                    .child(self.render_chat(&view))
                    .when(self.detail_selection.is_some(), |body| {
                        body.child(self.render_details(&view))
                    }),
            )
            .child(self.render_voicebar(&view))
            .when(!self.error.is_empty() && self.modal.is_none(), |root| {
                let entity = view.clone();
                root.child(
                    div()
                        .absolute()
                        .left(px(self.navigation_width() + 16.))
                        .right(px(16.))
                        .top(px(64.))
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
            .when(self.closing, |root| {
                root.child(
                    div()
                        .absolute()
                        .inset_0()
                        .bg(gpui::rgba(0x101214ed))
                        .flex()
                        .items_center()
                        .justify_center()
                        .child(div().text_sm().child("正在断开并退出")),
                )
            })
    }
}

fn can_show_session_channels(workspace: &Workspace, selected: &str) -> bool {
    if workspace.channels.is_empty() {
        return false;
    }
    if workspace.session.mode == "preview" {
        return selected == "__preview__" || !workspace.servers.iter().any(|s| s.id == selected);
    }
    workspace.connected() && selected == workspace.session.server_id
}

fn voice_params(voice: &VoiceState) -> Value {
    json!({
        "enabled": voice.enabled, "muted": voice.muted, "deafened": voice.deafened,
        "inputDeviceID": voice.input_device_id, "outputDeviceID": voice.output_device_id,
        "volume": voice.volume, "activationMode": voice.activation_mode,
        "vadThresholdDB": voice.vad_threshold_db, "noiseSuppression": voice.noise_suppression,
        "echoCancellation": voice.echo_cancellation, "echoSuppression": voice.echo_suppression,
        "ducking": voice.ducking,
    })
}

fn can_prepare_voice_preferences(mode: &str) -> bool {
    matches!(mode, "" | "offline" | "failed" | "preview")
}

fn detail_selection_change(
    selection: &DetailSelection,
    previous: &Workspace,
    next: &Workspace,
) -> DetailChange {
    if !next.connected() || previous.session.id != next.session.id {
        return DetailChange::Clear;
    }
    match selection {
        DetailSelection::User(id) => {
            let Some(user) = next.users.iter().find(|user| &user.id == id) else {
                return DetailChange::Clear;
            };
            let Some(old) = previous.users.iter().find(|user| &user.id == id) else {
                return DetailChange::Refresh;
            };
            if old.nickname != user.nickname
                || old.channel_id != user.channel_id
                || old.is_self != user.is_self
            {
                DetailChange::Refresh
            } else {
                DetailChange::Keep
            }
        }
        DetailSelection::Channel(id) => {
            let Some(channel) = next
                .channels
                .iter()
                .find(|channel| &channel.id == id && channel.kind != "separator")
            else {
                return DetailChange::Clear;
            };
            let Some(old) = previous.channels.iter().find(|channel| &channel.id == id) else {
                return DetailChange::Refresh;
            };
            if old.name != channel.name
                || old.description != channel.description
                || old.parent_id != channel.parent_id
                || old.password_required != channel.password_required
                || old.kind != channel.kind
            {
                DetailChange::Refresh
            } else {
                DetailChange::Keep
            }
        }
    }
}

fn ptt_can_send(workspace: &Workspace, voice: &VoiceState, closing: bool) -> bool {
    !closing
        && workspace.connected()
        && workspace.session.switching_channel_id.is_empty()
        && voice.enabled
        && voice.active
        && !voice.muted
        && !voice.deafened
        && !voice.busy
        && voice.activation_mode == "ptt"
}

fn detail_text(value: Option<&Value>, key: &str, workspace: &Workspace) -> String {
    match value {
        None | Some(Value::Null) => "未提供".into(),
        Some(Value::Bool(value)) => if *value { "是" } else { "否" }.into(),
        Some(Value::String(value)) if value.is_empty() => "未设置".into(),
        Some(Value::String(value)) if key == "channelID" => workspace
            .channels
            .iter()
            .find(|c| &c.id == value)
            .map(|c| c.name.clone())
            .unwrap_or_else(|| value.clone()),
        Some(Value::String(value)) => value.clone(),
        Some(Value::Number(value)) if key == "codec" => match value.as_i64() {
            Some(0) => "Speex Narrowband".into(),
            Some(1) => "Speex Wideband".into(),
            Some(2) => "Speex Ultra-Wideband".into(),
            Some(3) => "CELT Mono".into(),
            Some(4) => "Opus Voice".into(),
            Some(5) => "Opus Music".into(),
            _ => value.to_string(),
        },
        Some(value) => value.to_string(),
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
    use super::{
        DetailChange, DetailSelection, can_prepare_voice_preferences, can_show_session_channels,
        detail_selection_change, detail_text, ptt_can_send, remove_inserted_newline,
        user_is_speaking, voice_params,
    };
    use crate::model::{User, VoiceState, Workspace};

    #[test]
    fn viewed_bookmark_never_borrows_another_servers_channels() {
        let mut workspace = Workspace::default();
        workspace.servers = vec![
            crate::model::ServerProfile {
                id: "a".into(),
                ..Default::default()
            },
            crate::model::ServerProfile {
                id: "b".into(),
                ..Default::default()
            },
        ];
        workspace.channels.push(crate::model::Channel::default());
        workspace.session.server_id = "a".into();
        workspace.session.mode = "connected".into();
        assert!(can_show_session_channels(&workspace, "a"));
        assert!(!can_show_session_channels(&workspace, "b"));
        assert!(!can_show_session_channels(&workspace, "__preview__"));
        // Cached channels after a disconnect must not hide the reconnect action.
        workspace.session.mode = "offline".into();
        assert!(!can_show_session_channels(&workspace, "a"));
        workspace.session.mode = "preview".into();
        assert!(!can_show_session_channels(&workspace, "a"));
        assert!(can_show_session_channels(&workspace, "__preview__"));
    }

    #[test]
    fn reused_user_ids_refresh_details_and_departure_invalidates_selection() {
        let (mut previous, _, _, user) = speaking_context();
        previous.session.id = "session-1".into();
        previous.users = vec![user.clone()];
        let selection = DetailSelection::User(user.id.clone());
        let mut next = previous.clone();
        assert_eq!(
            detail_selection_change(&selection, &previous, &next),
            DetailChange::Keep
        );
        next.users[0].nickname = "Different user".into();
        assert_eq!(
            detail_selection_change(&selection, &previous, &next),
            DetailChange::Refresh
        );
        next.users.clear();
        assert_eq!(
            detail_selection_change(&selection, &previous, &next),
            DetailChange::Clear
        );
        next = previous.clone();
        next.session.id = "session-2".into();
        assert_eq!(
            detail_selection_change(&selection, &previous, &next),
            DetailChange::Clear
        );
    }

    #[test]
    fn channel_removal_and_rename_invalidate_details_without_joining() {
        let (mut previous, _, _, _) = speaking_context();
        previous.channels.push(crate::model::Channel {
            id: "other".into(),
            name: "Old".into(),
            ..Default::default()
        });
        let selection = DetailSelection::Channel("other".into());
        let mut next = previous.clone();
        next.channels[0].name = "New".into();
        assert_eq!(
            detail_selection_change(&selection, &previous, &next),
            DetailChange::Refresh
        );
        assert_eq!(next.session.channel_id, "channel-1");
        next.channels.clear();
        assert_eq!(
            detail_selection_change(&selection, &previous, &next),
            DetailChange::Clear
        );
    }

    #[test]
    fn offline_preferences_cannot_race_a_live_session_transition() {
        for mode in ["", "offline", "failed", "preview"] {
            assert!(can_prepare_voice_preferences(mode));
        }
        for mode in ["connecting", "connected", "disconnecting"] {
            assert!(!can_prepare_voice_preferences(mode));
        }
    }

    #[test]
    fn push_to_talk_requires_explicit_active_unmuted_session() {
        let (mut workspace, mut voice, _, _) = speaking_context();
        voice.activation_mode = "ptt".into();
        assert!(ptt_can_send(&workspace, &voice, false));
        assert!(!ptt_can_send(&workspace, &voice, true));
        for unavailable in [
            VoiceState {
                muted: true,
                ..voice.clone()
            },
            VoiceState {
                deafened: true,
                ..voice.clone()
            },
            VoiceState {
                enabled: false,
                ..voice.clone()
            },
            VoiceState {
                active: false,
                ..voice.clone()
            },
            VoiceState {
                busy: true,
                ..voice.clone()
            },
            VoiceState {
                activation_mode: "continuous".into(),
                ..voice.clone()
            },
        ] {
            assert!(!ptt_can_send(&workspace, &unavailable, false));
        }
        workspace.session.switching_channel_id = "new-channel".into();
        assert!(!ptt_can_send(&workspace, &voice, false));
        workspace.session.switching_channel_id.clear();
        workspace.session.mode = "offline".into();
        assert!(!ptt_can_send(&workspace, &voice, false));
    }

    #[test]
    fn audio_control_contract_keeps_privacy_and_processing_fields() {
        let voice = VoiceState {
            activation_mode: "vad".into(),
            vad_threshold_db: -32,
            noise_suppression: "high".into(),
            echo_cancellation: true,
            echo_suppression: true,
            ducking: true,
            ..VoiceState::default()
        };
        let params = voice_params(&voice);
        assert_eq!(params["muted"], true);
        assert_eq!(params["enabled"], false);
        assert_eq!(params["vadThresholdDB"], -32);
        assert_eq!(params["activationMode"], "vad");
        assert_eq!(params["noiseSuppression"], "high");
        assert_eq!(params["echoCancellation"], true);
        assert_eq!(params["echoSuppression"], true);
        assert_eq!(params["ducking"], true);
    }

    #[test]
    fn details_distinguish_unknown_empty_false_and_zero() {
        let workspace = Workspace::default();
        assert_eq!(detail_text(None, "description", &workspace), "未提供");
        assert_eq!(
            detail_text(Some(&serde_json::json!("")), "description", &workspace),
            "未设置"
        );
        assert_eq!(
            detail_text(Some(&serde_json::json!(false)), "permanent", &workspace),
            "否"
        );
        assert_eq!(
            detail_text(Some(&serde_json::json!(0)), "maxClients", &workspace),
            "0"
        );
        assert_eq!(
            detail_text(Some(&serde_json::json!(4)), "codec", &workspace),
            "Opus Voice"
        );
    }

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
