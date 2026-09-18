use crate::{
    core::{CoreClient, Incoming},
    model::{
        AudioDevice, Capabilities, CredentialStatus, ServerProfile, User, VoiceState, Workspace,
    },
    preferences::{Preferences, ThemePreference},
    theme::{self, rgb, rgba},
};
use base64::{Engine as _, engine::general_purpose::STANDARD};
use chrono::{DateTime, Local, Utc};
use global_hotkey::{GlobalHotKeyEvent, GlobalHotKeyManager, HotKeyState, hotkey::HotKey};
use gpui::{
    AnyElement, Context, Entity, EntityInputHandler, FocusHandle, Image, ImageFormat,
    InteractiveElement, IntoElement, ParentElement, Render, SharedString,
    StatefulInteractiveElement, Styled, Subscription, Window, div, img, prelude::*, px, svg,
};
use gpui_component::{
    Disableable, IconName, IconNamed, Selectable,
    button::{Button, ButtonVariants},
    checkbox::Checkbox,
    input::{Input, InputEvent, InputState},
    menu::{ContextMenuExt, DropdownMenu, PopupMenuItem},
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
    ExportDiagnostics,
    Probe(Instant),
    CoreInfo,
    ResourceInterest(u64),
    ChannelMutation {
        session: String,
        revision: u64,
    },
    ClaimOwner {
        session: String,
        revision: u64,
    },
    OutputGain(String),
    UserPlayback(UserPlaybackTarget),
    Workspace(&'static str),
    SaveServer(u64),
    Credential(String, u64),
    Connect(String, bool),
    Voice,
    SettingsApply(String, u64),
    SettingsStatus(String, u64),
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
}

#[derive(Clone, PartialEq)]
enum DetailSelection {
    Channel(String),
    User(String),
}

#[derive(Clone, PartialEq)]
struct UserPlaybackTarget {
    session: String,
    user: String,
    instance: String,
}

#[derive(Clone, Copy)]
enum UserPlaybackAction {
    Mute(bool),
    Step(i16),
    Volume(u16),
}

impl UserPlaybackAction {
    fn apply(self, user: &User) -> (u16, bool) {
        match self {
            Self::Mute(muted) => (user.playback_volume.min(794), muted),
            Self::Step(step) => (
                step_playback_volume(user.playback_volume, step),
                user.playback_muted,
            ),
            Self::Volume(volume) => (volume.min(794), user.playback_muted),
        }
    }
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
    Channel {
        session: String,
        id: String,
        delete: bool,
    },
    Ownership {
        session: String,
    },
    Server {
        editing_id: String,
    },
    Password {
        server_id: String,
    },
    Delete {
        server_id: String,
    },
    Duplicate {
        message_id: String,
    },
    Settings,
    Devices,
    Audio,
    Appearance,
    Diagnostics,
    About,
}

#[derive(Clone, Copy, PartialEq)]
enum DeviceMenu {
    Input,
    Output,
}

pub struct ResonaApp {
    resource_interest: Option<(bool, bool)>,
    resource_generation: u64,
    resources_syncing: bool,
    composer_height: f32,
    composer_drag: Option<(f32, f32)>,
    chat_collapsed: bool,
    chat_unread: bool,
    chat_follow_bottom: bool,
    chat_scroll: gpui::ScrollHandle,
    logos: [Arc<Image>; 2],
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
    playback_error: Option<(UserPlaybackTarget, String)>,
    input_gain_error: String,
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
    settings_draft: Option<Preferences>,
    settings_baseline: Option<Preferences>,
    settings_apply: Option<(Preferences, String, bool)>,
    settings_operation: Option<(u64, u64)>,
    settings_revision: u64,
    _settings_watch: Option<gpui::Task<()>>,
    settings_notice: String,
    settings_discard: bool,
    probe_result: String,
    core_info: Option<Value>,
    preference_revision: u64,
    preference_save_order: Arc<Mutex<u64>>,
    audio_settings_pending: bool,
    queued_voice: Option<VoiceState>,
    voice_target: Option<VoiceState>,
    _audio_update: Option<gpui::Task<()>>,
    seen_notifications: HashSet<String>,
    notification_order: VecDeque<String>,
    last_member_notification: Option<Instant>,
    pending: HashMap<u64, Pending>,
    drafts: HashMap<String, String>,
    submitted_drafts: HashMap<String, (String, String)>,
    selected_server: String,
    modal: Option<Modal>,
    ownership_revision: u64,
    channel_revision: u64,
    channel_error: String,
    channel_name_input: Entity<InputState>,
    channel_description_input: Entity<InputState>,
    channel_bitrate_input: Entity<InputState>,
    channel_audio_preset: u32,
    ownership_error: String,
    claim_input: Entity<InputState>,
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
    public_key_input: Entity<InputState>,
    password_input: Entity<InputState>,
    chat_input: Entity<InputState>,
    shortcut_input: Entity<InputState>,
    playback_input: Entity<InputState>,
    playback_input_target: Option<UserPlaybackTarget>,
    playback_input_confirmed: u16,
    playback_input_error: String,
    _subscriptions: Vec<Subscription>,
}

impl ResonaApp {
    pub fn new(window: &mut Window, cx: &mut Context<Self>) -> Self {
        let name_input = cx.new(|cx| InputState::new(window, cx).placeholder("服务器名称"));
        let channel_name_input = cx.new(|cx| InputState::new(window, cx).placeholder("频道名称"));
        let channel_bitrate_input = cx.new(|cx| InputState::new(window, cx).placeholder("16–64"));
        let channel_description_input = cx.new(|cx| {
            InputState::new(window, cx)
                .multi_line(true)
                .placeholder("频道描述")
        });
        let address_input =
            cx.new(|cx| InputState::new(window, cx).placeholder("voice.example.com"));
        let nickname_input = cx.new(|cx| InputState::new(window, cx).placeholder("昵称"));
        let public_key_input =
            cx.new(|cx| InputState::new(window, cx).placeholder("必填，64位十六进制服务器公钥"));
        let password_input = cx.new(|cx| {
            InputState::new(window, cx)
                .placeholder("服务器密码，可留空")
                .masked(true)
        });
        let claim_input = cx.new(|cx| {
            InputState::new(window, cx)
                .placeholder("一次性所有者认领码")
                .masked(true)
        });
        let chat_input = cx.new(|cx| {
            InputState::new(window, cx)
                .multi_line(true)
                .placeholder("发送到当前频道")
        });
        let preferences = Preferences::load();
        theme::apply(preferences.theme, Some(window), cx);
        let playback_input = cx.new(|cx| InputState::new(window, cx).default_value("100"));
        let playback_subscription = cx.subscribe_in(
            &playback_input,
            window,
            |this, input, event: &InputEvent, window, cx| {
                let pending = this
                    .pending
                    .values()
                    .any(|p| matches!(p, Pending::UserPlayback(_)));
                match event {
                    InputEvent::PressEnter { secondary: false } if !pending => {
                        if input.update(cx, |input, cx| {
                            input.marked_text_range(window, cx).is_some()
                        }) {
                            return;
                        }
                        let Some(target) = this.playback_input_target.clone() else {
                            return;
                        };
                        match parse_playback_volume(input.read(cx).value().as_ref()) {
                            Ok(volume) => {
                                this.playback_input_error.clear();
                                input.update(cx, |input, cx| {
                                    input.set_value(playback_db(volume).to_string(), window, cx)
                                });
                                this.user_playback_action(
                                    target,
                                    UserPlaybackAction::Volume(volume),
                                    cx,
                                );
                            }
                            Err(error) => {
                                this.playback_input_error = error.into();
                                cx.notify();
                            }
                        }
                    }
                    InputEvent::Blur if !pending => {
                        this.playback_input_error.clear();
                        input.update(cx, |input, cx| {
                            input.set_value(
                                playback_db(this.playback_input_confirmed).to_string(),
                                window,
                                cx,
                            )
                        });
                        cx.notify();
                    }
                    _ => {}
                }
            },
        );
        let shortcut_input = cx.new(|cx| {
            InputState::new(window, cx)
                .default_value(preferences.push_to_talk_shortcut.clone())
                .placeholder("F8")
        });
        cx.subscribe(&shortcut_input, |this, input, event: &InputEvent, cx| {
            if matches!(event, InputEvent::Change) && this.settings_draft.is_some() {
                let value = input.read(cx).value().trim().to_owned();
                this.update_audio_preferences(|p| p.push_to_talk_shortcut = value, cx);
            }
        })
        .detach();
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
            public_key_input.clone(),
            password_input.clone(),
            claim_input.clone(),
            channel_name_input.clone(),
            channel_bitrate_input.clone(),
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
                        Some(Modal::Channel { delete: false, .. })
                            if *input == this.channel_name_input
                                || *input == this.channel_bitrate_input =>
                        {
                            this.submit_channel(cx)
                        }
                        Some(Modal::Ownership { .. }) if *input == this.claim_input => {
                            this.claim_owner(window, cx)
                        }
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
            this.sync_resource_interest(window.is_window_active());
            if !window.is_window_active() {
                this.composer_drag = None;
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
            resource_interest: None,
            resource_generation: 0,
            resources_syncing: false,
            composer_height: 210.,
            composer_drag: None,
            chat_collapsed: false,
            chat_unread: false,
            chat_follow_bottom: true,
            chat_scroll: gpui::ScrollHandle::new(),
            logos: [crate::app_icon::DARK, crate::app_icon::LIGHT]
                .map(|bytes| Arc::new(Image::from_bytes(ImageFormat::Png, bytes.to_vec()))),
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
            playback_error: None,
            input_gain_error: String::new(),
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
            settings_draft: None,
            settings_baseline: None,
            settings_apply: None,
            settings_operation: None,
            settings_revision: 0,
            _settings_watch: None,
            settings_notice: String::new(),
            settings_discard: false,
            probe_result: String::new(),
            core_info: None,
            preference_revision: 0,
            preference_save_order: Arc::new(Mutex::new(0)),
            audio_settings_pending: false,
            queued_voice: None,
            voice_target: None,
            _audio_update: None,
            seen_notifications: HashSet::new(),
            notification_order: VecDeque::new(),
            last_member_notification: None,
            pending: HashMap::new(),
            drafts: HashMap::new(),
            submitted_drafts: HashMap::new(),
            selected_server: String::new(),
            modal: None,
            ownership_revision: 0,
            channel_revision: 0,
            channel_error: String::new(),
            channel_name_input,
            channel_description_input,
            channel_bitrate_input,
            channel_audio_preset: 32000,
            ownership_error: String::new(),
            claim_input,
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
            public_key_input,
            password_input,
            chat_input,
            shortcut_input,
            playback_input,
            playback_input_target: None,
            playback_input_confirmed: 100,
            playback_input_error: String::new(),
            _subscriptions: form_subscriptions
                .into_iter()
                .chain(vec![
                    chat_subscription,
                    playback_subscription,
                    activation_subscription,
                    quit_subscription,
                    cx.observe_window_appearance(window, |this, window, cx| {
                        theme::apply(this.editing_preferences().theme, Some(window), cx);
                        cx.notify();
                    }),
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
            Pending::SettingsApply(session, revision)
            | Pending::SettingsStatus(session, revision) => {
                if revision == self.settings_revision
                    && self
                        .settings_apply
                        .as_ref()
                        .is_some_and(|(_, current, _)| *current == session)
                {
                    self.settings_apply = None;
                    self.settings_notice = format!("尚未确认：{error}");
                }
            }
            Pending::ChannelMutation { session, revision } => {
                if self.channel_matches(&session, revision) {
                    self.channel_error = error;
                } else if !self.closing && self.workspace.session.id == session {
                    self.error = format!("频道操作：{error}");
                }
            }
            Pending::ClaimOwner { session, revision } => {
                if self.ownership_matches(&session, revision)
                    && self.workspace.session.server_role != "owner"
                {
                    self.ownership_error = error;
                }
            }
            Pending::UserPlayback(target) => self.user_playback_failed(target, error),
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
        self.detail_selection = None;
        self.detail_value = None;
        self.detail_error.clear();
    }

    fn handle_incoming(&mut self, incoming: Incoming, window: &mut Window, cx: &mut Context<Self>) {
        match incoming {
            Incoming::Notifications(value) => {
                if let Ok(workspace) = serde_json::from_value::<Workspace>(value) {
                    if workspace.session.id == self.workspace.session.id {
                        self.process_notifications(&workspace);
                    }
                }
            }
            Incoming::ResourceSync(value) => {
                if self.core.is_some()
                    && value.get("generation").and_then(Value::as_u64)
                        == Some(self.resource_generation)
                {
                    self.apply_workspace(value["workspace"].clone(), window, cx);
                    self.apply_voice(value["voice"].clone());
                    self.apply_microphone_test(value["microphoneTest"].clone());
                    self.resources_syncing = false;
                    if let Some(error) = value["error"].as_str().filter(|s| !s.is_empty()) {
                        self.error = error.to_owned();
                    }
                }
            }
            Incoming::Workspace(value) => self.apply_workspace(value, window, cx),
            Incoming::Voice(value) => {
                if self.core.is_some() {
                    self.apply_voice(value);
                }
            }
            Incoming::MicrophoneTest(value) => self.apply_microphone_test(value),
            Incoming::ProtocolError(error) => self.error = error,
            Incoming::Exited(error) => {
                if self.closing {
                    self.finish_exit(cx);
                    return;
                }
                self.error = error.clone();
                self.resources_syncing = false;
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
                self.voice.operation = 0;
                self.voice.applied_operation = 0;
                self.voice.generation = 0;
                self.voice.speaking_client_ids.clear();
                self.voice.local_speaking = false;
                self.microphone_test = VoiceState::default();
                self.ptt_pressed = false;
                self.device_menu = None;
                if matches!(
                    self.modal,
                    Some(Modal::Ownership { .. } | Modal::Channel { .. })
                ) {
                    self.close_modal(window, cx);
                }
            }
            Incoming::Response { id, result } => {
                let pending = self.pending.remove(&id);
                match (pending, result) {
                    (Some(Pending::ResourceInterest(generation)), result) => {
                        if generation == self.resource_generation {
                            if let Err(error) = result {
                                self.resources_syncing = false;
                                self.error = error;
                            }
                        }
                    }
                    (Some(Pending::ChannelMutation { session, revision }), result) => {
                        if self.channel_matches(&session, revision) {
                            match result {
                                Ok(_) => self.close_modal(window, cx),
                                Err(error) => self.channel_error = error,
                            }
                        } else if !self.closing && self.workspace.session.id == session {
                            if let Err(error) = result {
                                self.error = format!("频道操作：{error}");
                            }
                        }
                    }
                    (Some(Pending::ClaimOwner { session, revision }), result) => {
                        if self.ownership_matches(&session, revision) {
                            match result.and_then(|value| {
                                serde_json::from_value::<Workspace>(value)
                                    .map_err(|error| error.to_string())
                            }) {
                                Ok(workspace) if workspace.session.id == session => {
                                    // Events can be newer than this reply; merge only ownership metadata.
                                    if workspace.session.server_role == "owner" {
                                        self.workspace.session.server_role =
                                            workspace.session.server_role;
                                        self.workspace.session.can_claim_owner = false;
                                        self.ownership_error.clear();
                                    }
                                }
                                Ok(_) => {}
                                Err(error) if self.workspace.session.server_role != "owner" => {
                                    self.ownership_error = error
                                }
                                Err(_) => {}
                            }
                        }
                    }
                    (Some(Pending::UserPlayback(target)), result) => {
                        if target.session == self.workspace.session.id {
                            match result.and_then(|value| {
                                serde_json::from_value::<Workspace>(value)
                                    .map_err(|error| error.to_string())
                            }) {
                                Ok(workspace) => {
                                    if workspace.session.id == target.session {
                                        if let Some(updated) = workspace.users.iter().find(|u| {
                                            u.id == target.user && u.instance == target.instance
                                        }) {
                                            if let Some(user) =
                                                self.workspace.users.iter_mut().find(|u| {
                                                    u.id == target.user
                                                        && u.instance == target.instance
                                                })
                                            {
                                                user.playback_volume = updated.playback_volume;
                                                user.playback_muted = updated.playback_muted;
                                            }
                                        }
                                    }
                                }
                                Err(error) => self.user_playback_failed(target, error),
                            }
                        }
                    }
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
                    (Some(Pending::SettingsApply(session, revision)), result)
                        if self.settings_apply.as_ref().is_some_and(|(_, current, _)| {
                            *current == session
                                && session == self.workspace.session.id
                                && revision == self.settings_revision
                        }) =>
                    {
                        match result {
                            Ok(value) => match serde_json::from_value::<VoiceState>(value) {
                                Ok(voice) => {
                                    self.settings_operation =
                                        Some((voice.generation, voice.operation));
                                    self.request(
                                        "GetVoiceState",
                                        json!({}),
                                        Pending::SettingsStatus(session, revision),
                                    );
                                }
                                Err(error) => {
                                    self.settings_apply = None;
                                    self.settings_notice = format!("无法读取操作编号：{error}");
                                }
                            },
                            Err(error) => {
                                self.settings_apply = None;
                                self.settings_notice = format!("未应用：{error}");
                            }
                        }
                    }
                    (Some(Pending::SettingsStatus(session, revision)), result)
                        if self.settings_apply.as_ref().is_some_and(|(_, current, _)| {
                            *current == session
                                && session == self.workspace.session.id
                                && revision == self.settings_revision
                        }) =>
                    {
                        match result.and_then(|value| {
                            serde_json::from_value::<VoiceState>(value).map_err(|e| e.to_string())
                        }) {
                            Ok(voice) => {
                                if voice_state_current(&voice, &self.voice) {
                                    self.voice = voice;
                                }
                                if let Some((_, _, ready)) = &mut self.settings_apply {
                                    *ready = true;
                                }
                            }
                            Err(error) => {
                                self.settings_apply = None;
                                self.settings_notice = format!("无法确认应用结果：{error}");
                            }
                        }
                    }
                    (Some(Pending::SettingsApply(_, _) | Pending::SettingsStatus(_, _)), _) => {}
                    (Some(Pending::OutputGain(session)), result) => {
                        if session == self.workspace.session.id {
                            match result.and_then(|v| {
                                serde_json::from_value::<u16>(v).map_err(|e| e.to_string())
                            }) {
                                Ok(volume) if volume <= 794 => {
                                    self.preferences.playback_volume = volume;
                                    self.voice.volume = volume;
                                    self.save_preferences(cx);
                                }
                                Ok(_) => self.error = "收听增益响应无效".into(),
                                Err(error) => self.error = format!("收听增益未应用：{error}"),
                            }
                        }
                    }
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
                    (Some(Pending::ExportDiagnostics), Ok(value)) => {
                        if !self.closing {
                            if let Some(path) = value.as_str() {
                                cx.reveal_path(std::path::Path::new(path));
                            } else {
                                self.error = "诊断导出结果无效".into();
                            }
                        }
                    }
                    (Some(Pending::Probe(started)), Ok(value)) => {
                        self.probe_result = match self.update_capabilities(value) {
                            Ok(()) => format!(
                                "核心响应正常 · {} ms · {}",
                                started.elapsed().as_millis(),
                                self.capabilities.platform
                            ),
                            Err(error) => format!("核心探测失败：{error}"),
                        };
                    }
                    (Some(Pending::Probe(_)), Err(error)) => {
                        self.probe_result = format!("核心探测失败：{error}");
                    }
                    (Some(Pending::CoreInfo), Ok(value)) => self.core_info = Some(value),
                    (Some(Pending::CoreInfo), Err(error)) => {
                        self.core_info = None;
                        self.error = format!("无法读取核心版本：{error}");
                    }
                    (Some(Pending::Devices), Ok(value)) => match serde_json::from_value(value) {
                        Ok(devices) => self.devices = devices,
                        Err(error) => self.error = format!("无法读取音频设备：{error}"),
                    },
                    (Some(Pending::Capabilities), Ok(value)) => {
                        if let Err(error) = self.update_capabilities(value) {
                            self.error = error;
                        }
                    }
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
                        if self.check_connection_profile(&server_id) {
                            self.connect_error = error;
                            self.open_password(server_id, window, cx);
                        }
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
        self.finish_settings_apply(cx);
        self.flush_queued_voice(cx);
        self.sync_hotkey(cx);
        cx.notify();
    }

    fn sync_resource_interest(&mut self, active: bool) {
        if self.closing || self.core.is_none() {
            return;
        }
        let all = active
            && resource_needs_full_list(
                &self.workspace,
                &self.selected_server,
                self.detail_selection.as_ref(),
            );
        let next = (active, all);
        if self.resource_interest == Some(next) {
            return;
        }
        self.resource_interest = Some(next);
        self.resource_generation = self.resource_generation.wrapping_add(1);
        self.resources_syncing = active;
        self.request("SetResourceInterest", json!({"active":active, "allChannels":all, "allMembers":all, "generation":self.resource_generation}), Pending::ResourceInterest(self.resource_generation));
    }

    fn flush_queued_voice(&mut self, cx: &mut Context<Self>) {
        if self.closing
            || self.voice.busy
            || !self.workspace.session.switching_channel_id.is_empty()
            || self
                .pending
                .values()
                .any(|p| matches!(p, Pending::Voice | Pending::OutputGain(_)))
        {
            return;
        }
        self.voice_target = None;
        if let Some(mut next) = self.queued_voice.take() {
            next.input_gain = self.preferences.input_gain.min(200);
            next.volume = self.preferences.playback_volume.min(794);
            if self.workspace.connected() {
                self.configure_voice(|v| *v = next, cx);
            }
        }
    }

    fn apply_workspace(&mut self, value: Value, window: &mut Window, cx: &mut Context<Self>) {
        match serde_json::from_value::<Workspace>(value) {
            Ok(workspace) => {
                let changed_channel = workspace.session.id != self.workspace.session.id
                    || workspace.session.channel_id != self.workspace.session.channel_id
                    || (workspace.session.mode == "offline"
                        && self.workspace.session.mode != "offline");
                let new_message = workspace.messages.last().map(|m| &m.id)
                    != self.workspace.messages.last().map(|m| &m.id);
                if changed_channel {
                    self.chat_unread = false;
                    self.chat_follow_bottom = true;
                    self.chat_scroll = gpui::ScrollHandle::new();
                    self.submitted_drafts.clear();
                } else if new_message && !workspace.messages.is_empty() {
                    if !self.chat_collapsed && self.chat_follow_bottom {
                        self.chat_scroll.scroll_to_bottom();
                    } else {
                        self.chat_unread = true;
                    }
                }
                if let Some(Modal::Channel { session, .. }) = &self.modal {
                    if !workspace.connected()
                        || workspace.session.id != *session
                        || !workspace.session.can_manage_channels
                    {
                        self.close_modal(window, cx);
                    }
                }
                if let Some(Modal::Ownership { session }) = &self.modal {
                    if !workspace.connected() || workspace.session.id != *session {
                        self.close_modal(window, cx);
                    } else if workspace.session.server_role == "owner"
                        || !workspace.session.can_claim_owner
                    {
                        self.claim_input
                            .update(cx, |input, cx| input.set_value("", window, cx));
                        self.ownership_error.clear();
                    }
                }
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
                if visible_text_channel(&self.workspace, &self.selected_server).is_none() {
                    self.composer_drag = None;
                }
                if let Some(selection) = refresh_details {
                    self.select_details(selection, cx);
                } else {
                    if let Some(selection) = &self.detail_selection {
                        let next = native_detail_value(&self.workspace, selection);
                        if next != self.detail_value {
                            self.detail_value = next;
                            self.detail_error.clear();
                        }
                    }
                }
            }
            Err(error) => self.error = format!("核心工作区格式无效：{error}"),
        }
    }

    fn apply_voice(&mut self, value: Value) {
        match serde_json::from_value::<VoiceState>(value) {
            Ok(voice) => {
                if !voice_state_current(&voice, &self.voice) {
                    return;
                }
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
        self.settings_apply = None;
        self.settings_draft = None;
        self.settings_baseline = None;
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
        self.detail_value = native_detail_value(&self.workspace, &selection);
        if self.detail_value.is_none() {
            self.clear_selected_details();
        }
        cx.notify();
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
                    | Pending::ExportDiagnostics
                    | Pending::ResourceInterest(_)
                    | Pending::Icon { .. }
                    | Pending::PushToTalk
            )
        })
    }

    fn speaking_transition_pending(&self) -> bool {
        if self.settings_apply.is_some() {
            return true;
        }
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
            if self.resources_syncing
                || !fresh
                || !self.preferences.notifications_enabled
                || self.voice.deafened
            {
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
        self.public_key_input.update(cx, |input, cx| {
            input.set_value(profile.server_public_key.clone(), window, cx)
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

    fn ownership_matches(&self, session: &str, revision: u64) -> bool {
        ownership_response_current(
            &self.workspace,
            self.modal.as_ref(),
            self.closing,
            self.ownership_revision,
            session,
            revision,
        )
    }

    fn channel_matches(&self, session: &str, revision: u64) -> bool {
        channel_response_current(
            &self.workspace,
            self.modal.as_ref(),
            self.closing,
            self.channel_revision,
            session,
            revision,
        )
    }

    fn channel_pending(&self) -> bool {
        self.pending.values().any(|p| matches!(p, Pending::ChannelMutation { session, .. } if *session == self.workspace.session.id))
    }

    fn open_channel_form(
        &mut self,
        session: String,
        id: String,
        delete: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.closing
            || !self.workspace.connected()
            || !self.workspace.session.can_manage_channels
            || session != self.workspace.session.id
        {
            return;
        }
        let channel = self
            .workspace
            .channels
            .iter()
            .find(|ch| ch.id == id)
            .cloned();
        if !id.is_empty() && channel.is_none() {
            self.error = "频道已不存在".into();
            cx.notify();
            return;
        }
        if delete
            && channel
                .as_ref()
                .is_none_or(|ch| ch.is_default || ch.members != 0)
        {
            self.error = "默认频道或有成员的频道不能删除".into();
            cx.notify();
            return;
        }
        self.close_modal(window, cx);
        if let Some(channel) = channel {
            let bitrate = if channel.bitrate == 0 {
                48000
            } else {
                channel.bitrate
            };
            self.channel_audio_preset = if [20000, 32000, 48000].contains(&bitrate) {
                bitrate
            } else {
                0
            };
            self.channel_bitrate_input.update(cx, |input, cx| {
                input.set_value((bitrate / 1000).to_string(), window, cx)
            });
            self.channel_name_input
                .update(cx, |input, cx| input.set_value(channel.name, window, cx));
            self.channel_description_input.update(cx, |input, cx| {
                input.set_value(channel.description, window, cx)
            });
        }
        self.modal = Some(Modal::Channel {
            session,
            id,
            delete,
        });
        if !delete {
            self.channel_name_input
                .update(cx, |input, cx| input.focus(window, cx));
        }
        cx.notify();
    }

    fn submit_channel(&mut self, cx: &mut Context<Self>) {
        if self.resources_syncing {
            return;
        }
        let Some(Modal::Channel {
            session,
            id,
            delete,
        }) = self.modal.clone()
        else {
            return;
        };
        if !self.channel_matches(&session, self.channel_revision) || self.channel_pending() {
            return;
        }
        if !id.is_empty() {
            let target = self.workspace.channels.iter().find(|ch| ch.id == id);
            if target.is_none()
                || (delete && target.is_some_and(|ch| ch.is_default || ch.members != 0))
            {
                self.channel_error = "频道已不存在，或当前不允许删除".into();
                cx.notify();
                return;
            }
        }
        let name = self.channel_name_input.read(cx).value().trim().to_owned();
        let description = self.channel_description_input.read(cx).value().to_string();
        if !delete
            && (name.is_empty()
                || name.chars().count() > 100
                || name.chars().any(char::is_control)
                || description.len() > 1024
                || description.contains('\0'))
        {
            self.channel_error = "名称须为 1–100 个字符，描述最多 1024 字节".into();
            cx.notify();
            return;
        }
        self.channel_error.clear();
        let bitrate = if !delete && self.workspace.session.can_configure_channel_audio {
            match channel_bitrate(
                self.channel_audio_preset,
                self.channel_bitrate_input.read(cx).value().as_ref(),
            ) {
                Some(value) => value,
                None => {
                    self.channel_error = "目标码率须为 16–64 kbps 的整数".into();
                    cx.notify();
                    return;
                }
            }
        } else {
            0
        };
        self.request(if delete { "DeleteChannel" } else if id.is_empty() { "CreateChannel" } else { "UpdateChannel" },
            json!({"sessionID": session, "channelID": id, "name": name, "description": description, "bitrate": bitrate}),
            Pending::ChannelMutation { session, revision: self.channel_revision });
        cx.notify();
    }

    fn claim_owner_pending(&self) -> bool {
        ownership_pending(self.pending.values(), &self.workspace.session.id)
    }

    fn open_ownership(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if !self.workspace.connected()
            || (self.workspace.session.server_role.is_empty()
                && !self.workspace.session.can_claim_owner)
        {
            return;
        }
        self.close_modal(window, cx);
        self.modal = Some(Modal::Ownership {
            session: self.workspace.session.id.clone(),
        });
        if self.workspace.session.can_claim_owner {
            self.claim_input
                .update(cx, |input, cx| input.focus(window, cx));
        }
        cx.notify();
    }

    fn claim_owner(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(Modal::Ownership { session }) = self.modal.clone() else {
            return;
        };
        if !self.ownership_matches(&session, self.ownership_revision)
            || !self.workspace.session.can_claim_owner
            || self.claim_owner_pending()
        {
            return;
        }
        let token = self.claim_input.read(cx).unmask_value().to_string();
        if token.trim().is_empty() {
            self.ownership_error = "请输入所有者认领码".into();
            cx.notify();
            return;
        }
        self.claim_input
            .update(cx, |input, cx| input.set_value("", window, cx));
        self.ownership_error.clear();
        self.request(
            "ClaimServerOwner",
            json!({"sessionID": session, "token": token}),
            Pending::ClaimOwner {
                session,
                revision: self.ownership_revision,
            },
        );
        cx.notify();
    }

    fn close_modal(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if matches!(
            self.modal,
            Some(
                Modal::Devices
                    | Modal::Audio
                    | Modal::Settings
                    | Modal::Appearance
                    | Modal::Diagnostics
                    | Modal::About
            )
        ) {
            if self.settings_apply.is_some() {
                return;
            }
            if self.settings_dirty() {
                self.settings_discard = true;
                cx.notify();
                return;
            }
            self.settings_draft = None;
            self.settings_baseline = None;
            self.settings_discard = false;
        }
        self.channel_audio_preset = 32000;
        self.channel_bitrate_input
            .update(cx, |input, cx| input.set_value("32", window, cx));
        self.channel_revision = self.channel_revision.wrapping_add(1);
        self.channel_error.clear();
        self.channel_name_input
            .update(cx, |input, cx| input.set_value("", window, cx));
        self.channel_description_input
            .update(cx, |input, cx| input.set_value("", window, cx));
        self.claim_input
            .update(cx, |input, cx| input.set_value("", window, cx));
        self.ownership_error.clear();
        self.ownership_revision = self.ownership_revision.wrapping_add(1);
        if matches!(
            self.modal,
            Some(
                Modal::Devices
                    | Modal::Audio
                    | Modal::Settings
                    | Modal::Appearance
                    | Modal::Diagnostics
                    | Modal::About
            )
        ) && (self.microphone_test.enabled || self.microphone_test.busy)
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
            protocol: "resona-noise".into(),
            server_public_key: self.public_key_input.read(cx).value().to_string(),
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
        if navigation_blocked(self.modal.as_ref(), self.closing) {
            return;
        }
        if !self.check_connection_profile(&server_id) {
            cx.notify();
            return;
        }
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
        if !self.check_connection_profile(&server_id) {
            return;
        }
        self.request(
            "GetServerCredentialStatus",
            json!({"id": server_id}),
            Pending::Credential(server_id, self.connect_revision),
        );
    }

    fn check_connection_profile(&mut self, server_id: &str) -> bool {
        match self
            .workspace
            .servers
            .iter()
            .find(|server| server.id == server_id)
        {
            Some(server) if server.supports_connection() => true,
            Some(_) => {
                self.error = "此书签版本已停止支持，请使用服务器地址和公钥重新添加".into();
                false
            }
            None => {
                self.error = "服务器书签不存在".into();
                false
            }
        }
    }

    fn store_voice_preferences(&mut self, connect: Option<String>) {
        if !can_prepare_voice_preferences(&self.workspace.session.mode) || self.closing {
            return;
        }
        let mut config = VoiceState::default();
        self.apply_audio_preferences(&mut config);
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
        if self.resources_syncing
            || visible_text_channel(&self.workspace, &self.selected_server).is_none()
            || !self.workspace.session.switching_channel_id.is_empty()
            || !self.workspace.session.sending_message_id.is_empty()
        {
            return;
        }
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
        if self.resources_syncing {
            return;
        }
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
            .any(|pending| matches!(pending, Pending::Voice | Pending::OutputGain(_)));
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
        apply_preferences(&self.preferences, voice);
    }

    fn editing_preferences(&self) -> &Preferences {
        self.settings_draft.as_ref().unwrap_or(&self.preferences)
    }

    fn settings_dirty(&self) -> bool {
        self.settings_draft != self.settings_baseline
    }

    fn settings_needs_voice_update(&self) -> bool {
        let (Some(baseline), Some(draft)) = (&self.settings_baseline, &self.settings_draft) else {
            return false;
        };
        let mut desired = self.preferences.clone();
        desired.merge_settings(baseline, draft);
        let mut before = VoiceState::default();
        let mut after = VoiceState::default();
        apply_preferences(&self.preferences, &mut before);
        apply_preferences(&desired, &mut after);
        voice_params(&before) != voice_params(&after)
    }

    fn update_capabilities(&mut self, value: Value) -> Result<(), String> {
        let capabilities: Capabilities =
            serde_json::from_value(value).map_err(|error| format!("无法读取核心能力：{error}"))?;
        if capabilities.protocol_version != 1 {
            return Err(format!(
                "不支持的核心协议版本：{}",
                capabilities.protocol_version
            ));
        }
        self.remember_password &= capabilities.secure_password_storage;
        self.capabilities = capabilities;
        Ok(())
    }

    fn apply_settings(&mut self, cx: &mut Context<Self>) {
        if !self.settings_dirty() || self.settings_apply.is_some() || self.closing {
            return;
        }
        self.device_menu = None;
        let mut desired = self.preferences.clone();
        desired.merge_settings(
            self.settings_baseline.as_ref().unwrap(),
            self.settings_draft.as_ref().unwrap(),
        );
        if desired.global_push_to_talk
            && (desired.global_push_to_talk != self.preferences.global_push_to_talk
                || desired.push_to_talk_shortcut != self.preferences.push_to_talk_shortcut)
            && desired.push_to_talk_shortcut.parse::<HotKey>().is_err()
        {
            self.settings_notice = "快捷键格式无效，请检查后再应用".into();
            cx.notify();
            return;
        }
        let mut next = self.voice.clone();
        apply_preferences(&desired, &mut next);
        let mut before = VoiceState::default();
        let mut after = VoiceState::default();
        apply_preferences(&self.preferences, &mut before);
        apply_preferences(&desired, &mut after);
        if voice_params(&before) == voice_params(&after) {
            self.commit_settings(desired, cx);
            cx.notify();
            return;
        }
        if self.global_gain_busy()
            || self.core.is_none()
            || self.is_busy()
            || !self.workspace.session.switching_channel_id.is_empty()
            || matches!(
                self.workspace.session.mode.as_str(),
                "connecting" | "reconnecting" | "disconnecting"
            )
        {
            return;
        }
        self.settings_notice = "应用中".into();
        self.settings_revision = self.settings_revision.wrapping_add(1);
        let revision = self.settings_revision;
        self.settings_operation = None;
        self.settings_apply = Some((desired, self.workspace.session.id.clone(), false));
        self.set_push_to_talk(false, cx);
        self.request(
            if self.workspace.connected() {
                "ConfigureVoice"
            } else {
                "SetVoicePreferences"
            },
            voice_params(&next),
            Pending::SettingsApply(self.workspace.session.id.clone(), revision),
        );
        let session = self.workspace.session.id.clone();
        self._settings_watch = Some(cx.spawn(async move |view, cx| {
            for _ in 0..20 {
                cx.background_executor().timer(Duration::from_secs(1)).await;
                let pending = view.update(cx, |this, _| {
                    if this.settings_revision != revision
                        || this.settings_apply.as_ref().is_none_or(|(_, current, _)| *current != session)
                        || this.core.is_none() || this.closing
                    {
                        return false;
                    }
                    if this.settings_operation.is_some()
                        && !this.pending.values().any(|p| matches!(p, Pending::SettingsStatus(_, pending_revision) if *pending_revision == revision))
                    {
                        this.request("GetVoiceState", json!({}), Pending::SettingsStatus(session.clone(), revision));
                    }
                    true
                }).unwrap_or(false);
                if !pending { return; }
            }
            let _ = view.update(cx, |this, cx| {
                if this.settings_revision == revision && this.settings_apply.as_ref().is_some_and(|(_, current, _)| *current == session) {
                    this.settings_apply = None;
                    this.settings_notice = "应用结果尚未确认，请查看当前语音状态后重试".into();
                    cx.notify();
                }
            });
        }));
        cx.notify();
    }

    fn finish_settings_apply(&mut self, cx: &mut Context<Self>) {
        let Some((desired, session, ready)) = self.settings_apply.as_ref() else {
            return;
        };
        if *session != self.workspace.session.id
            || self.core.is_none()
            || matches!(
                self.workspace.session.mode.as_str(),
                "connecting" | "reconnecting" | "disconnecting"
            )
        {
            self.settings_apply = None;
            self.settings_notice = "连接已变化，请重新应用设置".into();
            return;
        }
        if !ready || self.voice.busy {
            return;
        }
        let Some((generation, operation)) = self.settings_operation else {
            return;
        };
        if self.voice.generation != generation || self.voice.operation != operation {
            self.settings_apply = None;
            self.settings_notice = "语音配置已被其他操作替代，请重新应用".into();
            return;
        }
        if !self.voice.error.is_empty() {
            self.settings_notice = format!("未应用：{}", self.voice.error);
            self.settings_apply = None;
            return;
        }
        if !voice_operation_applied(&self.voice, generation, operation) {
            self.settings_notice = "等待核心确认应用".into();
            return;
        }
        let mut expected = self.voice.clone();
        apply_preferences(desired, &mut expected);
        if voice_params(&expected) != voice_params(&self.voice) {
            self.settings_apply = None;
            self.settings_notice = "设备配置未完整应用，请检查设备后重试".into();
            return;
        }
        let mut next = desired.clone();
        next.server_sidebar_collapsed = self.preferences.server_sidebar_collapsed;
        self.commit_settings(next, cx);
    }

    fn commit_settings(&mut self, next: Preferences, cx: &mut Context<Self>) {
        let mut before = VoiceState::default();
        let mut after = VoiceState::default();
        apply_preferences(&self.preferences, &mut before);
        apply_preferences(&next, &mut after);
        let entry_changed = before.auto_unmute_on_connect != after.auto_unmute_on_connect;
        before.auto_unmute_on_connect = after.auto_unmute_on_connect;
        let audio_changed = voice_params(&before) != voice_params(&after);
        let hotkey_changed = self.preferences.activation_mode != next.activation_mode
            || self.preferences.global_push_to_talk != next.global_push_to_talk
            || self.preferences.push_to_talk_shortcut != next.push_to_talk_shortcut;
        let stop_notifications =
            self.preferences.notifications_enabled && !next.notifications_enabled;
        self.preferences = next;
        self.settings_apply = None;
        self.settings_operation = None;
        self.settings_draft = Some(self.preferences.clone());
        self.settings_baseline = self.settings_draft.clone();
        self.settings_notice = if !audio_changed && entry_changed {
            "已保存，下次主动连接生效"
        } else if !audio_changed {
            "已保存"
        } else if self.workspace.connected() && self.voice.active {
            "已应用到当前语音"
        } else if self.workspace.connected() {
            "已保存，启用语音后自动应用（无需重启应用）"
        } else {
            "已保存，连接并启用语音后自动应用（无需重启应用）"
        }
        .into();
        if hotkey_changed {
            self.clear_hotkey(cx);
        }
        if stop_notifications && self.core.is_some() {
            self.request("StopNotifications", json!({}), Pending::Notification);
        }
        self.save_preferences(cx);
    }

    fn change_theme(
        &mut self,
        choice: ThemePreference,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if let Some(draft) = &mut self.settings_draft {
            if self.settings_apply.is_some() {
                return;
            }
            draft.theme = choice;
            self.settings_notice.clear();
            theme::apply(choice, Some(window), cx);
            cx.notify();
        }
    }

    fn change_global_gain(&mut self, step: Option<i16>, cx: &mut Context<Self>) {
        if self.closing
            || self.voice.busy
            || self.microphone_test.enabled
            || self.microphone_test.busy
            || self.audio_settings_pending
            || self.queued_voice.is_some()
            || self.pending.values().any(|p| {
                matches!(
                    p,
                    Pending::Voice | Pending::VoicePreferences { .. } | Pending::OutputGain(_)
                )
            })
        {
            return;
        }
        let current = if self.workspace.connected() {
            self.voice.volume
        } else {
            self.preferences.playback_volume
        };
        let volume = step.map_or(200, |step| step_playback_volume(current, step));
        self.request(
            "SetOutputGain",
            json!({"sessionID":self.workspace.session.id, "volume": volume}),
            Pending::OutputGain(self.workspace.session.id.clone()),
        );
        cx.notify();
    }

    fn global_gain_busy(&self) -> bool {
        self.settings_apply.is_some()
            || self.closing
            || self.voice.busy
            || self.microphone_test.enabled
            || self.microphone_test.busy
            || self.audio_settings_pending
            || self.queued_voice.is_some()
            || self.pending.values().any(|p| {
                matches!(
                    p,
                    Pending::Voice | Pending::VoicePreferences { .. } | Pending::OutputGain(_)
                )
            })
    }

    fn update_audio_preferences(
        &mut self,
        change: impl FnOnce(&mut Preferences),
        cx: &mut Context<Self>,
    ) {
        if let Some(draft) = &mut self.settings_draft {
            if self.settings_apply.is_none() {
                change(draft);
                self.settings_notice.clear();
                cx.notify();
            }
            return;
        }
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
                                "connecting" | "reconnecting" | "disconnecting"
                            )
                            || !this.workspace.session.switching_channel_id.is_empty()
                            || this.pending.values().any(|p| {
                                matches!(
                                    p,
                                    Pending::Voice
                                        | Pending::OutputGain(_)
                                        | Pending::VoicePreferences { .. }
                                )
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
        if self.settings_dirty() || self.settings_apply.is_some() {
            return false;
        }
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
                    Pending::Voice
                        | Pending::OutputGain(_)
                        | Pending::VoicePreferences { .. }
                        | Pending::MicrophoneTest
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
        if self.preferences.server_sidebar_collapsed {
            52.
        } else {
            172.
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
                            .bg(rgba(if self.selected_server == id {
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
                                        if navigation_blocked(this.modal.as_ref(), this.closing) {
                                            return;
                                        }
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
                                    if navigation_blocked(this.modal.as_ref(), this.closing) {
                                        return;
                                    }
                                    this.selected_server = id.clone();
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
                                .child(
                                    img(self.logos[usize::from(theme::is_light())].clone())
                                        .size(px(24.)),
                                )
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
        let header = div()
            .h(px(62.))
            .flex_shrink_0()
            .px_1()
            .flex()
            .items_center()
            .gap_1()
            .when(
                showing_session
                    && self.workspace.connected()
                    && self.workspace.session.can_manage_channels,
                |header| {
                    let view = view.clone();
                    header.child(
                        Button::new("create-channel")
                            .icon(IconName::Plus)
                            .ghost()
                            .tooltip("新建频道")
                            .on_click(move |_, window, cx| {
                                view.update(cx, |this, cx| {
                                    this.open_channel_form(
                                        this.workspace.session.id.clone(),
                                        String::new(),
                                        false,
                                        window,
                                        cx,
                                    );
                                });
                            }),
                    )
                },
            )
            .child(
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
            .when(
                showing_session
                    && self.workspace.connected()
                    && (!self.workspace.session.server_role.is_empty()
                        || self.workspace.session.can_claim_owner),
                |header| {
                    let view = view.clone();
                    header.child(
                        Button::new("server-ownership")
                            .icon(IconName::Settings2)
                            .ghost()
                            .tooltip("服务器身份与权限")
                            .on_click(move |_, window, cx| {
                                view.update(cx, |this, cx| this.open_ownership(window, cx));
                            }),
                    )
                },
            );
        let mut sidebar = div()
            .flex_1()
            .min_w_0()
            .h_full()
            .flex_shrink_0()
            .bg(rgb(PANEL))
            .border_r_1()
            .border_color(rgb(LINE))
            .flex()
            .flex_col()
            .child(header);
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
        let native_session = self.workspace.connected();
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
                let menu_entity = view.clone();
                let menu_session = self.workspace.session.id.clone();
                let menu_id = channel.id.clone();
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
                        let menu_entity = view.clone();
                        let menu_target = UserPlaybackTarget {
                            session: self.workspace.session.id.clone(),
                            user: user.id.clone(),
                            instance: user.instance.clone(),
                        };
                        let speaking = user_is_speaking(
                            &self.workspace,
                            &self.voice,
                            transition_pending,
                            &user,
                        );
                        div()
                            .id(SharedString::from(format!(
                                "user-menu-{}-{}-{}",
                                menu_target.session, menu_target.user, menu_target.instance
                            )))
                            .child(
                                div()
                                    .id(SharedString::from(format!("tree-user-{user_id}")))
                                    .tab_index(0)
                                    .cursor_pointer()
                                    .border_l_2()
                                    .border_color(rgba(if inspecting_user {
                                        0x9dbdafaa
                                    } else {
                                        0x00000000
                                    }))
                                    .hover(|s| s.bg(rgb(HOVER)))
                                    .on_click(move |_, _, cx| {
                                        user_entity.update(cx, |this, cx| {
                                            this.select_details(
                                                DetailSelection::User(user_id.clone()),
                                                cx,
                                            )
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
                                    .map(|row| {
                                        if user.is_self {
                                            return row.into_any_element();
                                        }
                                        row.context_menu(move |menu, window, cx| {
                                            let this = menu_entity.read(cx);
                                            let current = this.workspace.users.iter().find(|u| {
                                                u.id == menu_target.user
                                                    && u.instance == menu_target.instance
                                            });
                                            let enabled = current.is_some_and(|u| {
                                                !u.is_self
                                                    && u.channel_id
                                                        == this.workspace.session.channel_id
                                            }) && this.workspace.connected()
                                                && this.workspace.session.id == menu_target.session
                                                && !menu_target.instance.is_empty()
                                                && this.capabilities.voice
                                                && !this.closing
                                                && this
                                                    .workspace
                                                    .session
                                                    .switching_channel_id
                                                    .is_empty()
                                                && !this
                                                    .pending
                                                    .values()
                                                    .any(|p| matches!(p, Pending::UserPlayback(_)));
                                            let volume = current
                                                .map(|u| u.playback_volume.min(794))
                                                .unwrap_or(100);
                                            let muted = current.is_some_and(|u| u.playback_muted);
                                            let mut menu = menu.label(format!(
                                                "本机收听 · {}",
                                                playback_label(volume)
                                            ));
                                            for (label, action, disabled) in [
                                                (
                                                    if muted {
                                                        "取消本机静音"
                                                    } else {
                                                        "在本机静音"
                                                    },
                                                    UserPlaybackAction::Mute(!muted),
                                                    false,
                                                ),
                                                (
                                                    "降低 1 dB",
                                                    UserPlaybackAction::Step(-1),
                                                    volume <= 10,
                                                ),
                                                (
                                                    "提高 1 dB",
                                                    UserPlaybackAction::Step(1),
                                                    volume >= 794,
                                                ),
                                                (
                                                    "恢复原始增益 0 dB",
                                                    UserPlaybackAction::Volume(100),
                                                    volume == 100,
                                                ),
                                            ] {
                                                let entity = menu_entity.clone();
                                                let target = menu_target.clone();
                                                menu = menu.item(
                                                    PopupMenuItem::new(label)
                                                        .disabled(!enabled || disabled)
                                                        .on_click(move |_, _, cx| {
                                                            entity.update(cx, |this, cx| {
                                                                this.user_playback_action(
                                                                    target.clone(),
                                                                    action,
                                                                    cx,
                                                                )
                                                            });
                                                        }),
                                                );
                                            }
                                            let entity = menu_entity.clone();
                                            let target = menu_target.clone();
                                            menu.separator().submenu(
                                                "音量档位",
                                                window,
                                                cx,
                                                move |mut menu, _, _| {
                                                    for db in [-20, -12, -6, 0, 6, 12, 18] {
                                                        let value = playback_from_db(db);
                                                        let entity = entity.clone();
                                                        let target = target.clone();
                                                        menu =
                                                            menu.item(
                                                                PopupMenuItem::new(playback_label(
                                                                    value,
                                                                ))
                                                                .checked(value == volume)
                                                                .disabled(!enabled)
                                                                .on_click(move |_, _, cx| {
                                                                    entity.update(cx, |this, cx| {
                                                            this.user_playback_action(
                                                                target.clone(),
                                                                UserPlaybackAction::Volume(value),
                                                                cx,
                                                            )
                                                        });
                                                                }),
                                                            );
                                                    }
                                                    menu
                                                },
                                            )
                                        })
                                        .into_any_element()
                                    }),
                            )
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
                            .border_color(rgba(if inspecting { 0x9dbdafaa } else { 0x00000000 }))
                            .bg(rgb(if selected { 0x2a2f31 } else { PANEL }))
                            .hover(|s| s.bg(rgb(HOVER)))
                            .flex()
                            .items_center()
                            .gap_2()
                            .on_click(move |event, _, cx| {
                                entity.update(cx, |this, cx| {
                                    if navigation_blocked(this.modal.as_ref(), this.closing) {
                                        return;
                                    }
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
                            )
                            .map(|row| {
                                if !native_session {
                                    return row.into_any_element();
                                }
                                row.context_menu(move |mut menu, _, cx| {
                                    let current = menu_entity.read(cx);
                                    let enabled = current.workspace.connected()
                                        && current.workspace.session.id == menu_session
                                        && current.workspace.session.can_manage_channels
                                        && !current.channel_pending();
                                    let channel = current
                                        .workspace
                                        .channels
                                        .iter()
                                        .find(|ch| ch.id == menu_id);
                                    let deletable =
                                        channel.is_some_and(|ch| !ch.is_default && ch.members == 0);
                                    let delete_label = if channel.is_some_and(|ch| ch.is_default) {
                                        "删除频道（默认频道不可删除）"
                                    } else if channel.is_some_and(|ch| ch.members != 0) {
                                        "删除频道（频道内有成员）"
                                    } else {
                                        "删除频道"
                                    };
                                    let edit = menu_entity.clone();
                                    let create = menu_entity.clone();
                                    let create_session = menu_session.clone();
                                    let edit_session = menu_session.clone();
                                    let edit_id = menu_id.clone();
                                    let delete = menu_entity.clone();
                                    let delete_session = menu_session.clone();
                                    let delete_id = menu_id.clone();
                                    if !current.workspace.session.can_manage_channels {
                                        menu = menu.label("服务器未授权频道管理");
                                    } else if current.channel_pending() {
                                        menu = menu.label("频道操作正在进行");
                                    }
                                    menu.item(
                                        PopupMenuItem::new("新建频道").disabled(!enabled).on_click(
                                            move |_, window, cx| {
                                                create.update(cx, |this, cx| {
                                                    this.open_channel_form(
                                                        create_session.clone(),
                                                        String::new(),
                                                        false,
                                                        window,
                                                        cx,
                                                    )
                                                });
                                            },
                                        ),
                                    )
                                    .item(
                                        PopupMenuItem::new("编辑频道")
                                            .disabled(!enabled || channel.is_none())
                                            .on_click(move |_, window, cx| {
                                                edit.update(cx, |this, cx| {
                                                    this.open_channel_form(
                                                        edit_session.clone(),
                                                        edit_id.clone(),
                                                        false,
                                                        window,
                                                        cx,
                                                    )
                                                });
                                            }),
                                    )
                                    .item(
                                        PopupMenuItem::new(delete_label)
                                            .disabled(!enabled || !deletable)
                                            .on_click(move |_, window, cx| {
                                                delete.update(cx, |this, cx| {
                                                    this.open_channel_form(
                                                        delete_session.clone(),
                                                        delete_id.clone(),
                                                        true,
                                                        window,
                                                        cx,
                                                    )
                                                });
                                            }),
                                    )
                                })
                                .into_any_element()
                            }),
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

    fn render_chat(&self, channel: &crate::model::Channel, view: &Entity<Self>) -> AnyElement {
        let can_send = (self.workspace.session.mode == "preview" || self.workspace.connected())
            && !self.resources_syncing
            && self.workspace.session.switching_channel_id.is_empty()
            && self.workspace.session.sending_message_id.is_empty()
            && !self.pending.values().any(|p| matches!(p, Pending::Send(_)));
        let toggle = view.clone();
        let latest = view.clone();
        let send = view.clone();
        let drag = view.clone();
        let scrolled = view.clone();
        div()
            .w_full()
            .flex_shrink_0()
            .bg(rgb(BG))
            .flex()
            .flex_col()
            .h(px(if self.chat_collapsed {
                36.
            } else {
                self.composer_height
            }))
            .child(
                div()
                    .id("text-panel-resize")
                    .h(px(5.))
                    .w_full()
                    .flex_shrink_0()
                    .border_t_1()
                    .border_color(rgb(LINE))
                    .cursor_row_resize()
                    .hover(|s| s.bg(rgba(0xffffff20)))
                    .on_mouse_down(gpui::MouseButton::Left, move |event, _, cx| {
                        drag.update(cx, |this, cx| {
                            this.chat_collapsed = false;
                            this.composer_drag =
                                Some((f32::from(event.position.y), this.composer_height));
                            cx.notify();
                        });
                        cx.stop_propagation();
                    }),
            )
            .child(
                div()
                    .h(px(30.))
                    .px_3()
                    .flex_shrink_0()
                    .flex()
                    .items_center()
                    .gap_2()
                    .child(
                        Button::new("toggle-channel-text")
                            .icon(if self.chat_collapsed {
                                IconName::ChevronUp
                            } else {
                                IconName::ChevronDown
                            })
                            .ghost()
                            .tooltip(if self.chat_collapsed {
                                "展开频道文字"
                            } else {
                                "收起频道文字"
                            })
                            .on_click(move |_, _, cx| {
                                toggle.update(cx, |this, cx| {
                                    this.chat_collapsed = !this.chat_collapsed;
                                    cx.notify();
                                });
                            }),
                    )
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .truncate()
                            .text_xs()
                            .text_color(rgb(MUTED))
                            .child(format!("频道文字 · {}", channel.name)),
                    )
                    .when(self.chat_unread, |header| {
                        header.child(
                            Button::new("latest-channel-text")
                                .label("新消息")
                                .icon(IconName::ArrowDown)
                                .ghost()
                                .on_click(move |_, _, cx| {
                                    latest.update(cx, |this, cx| {
                                        this.chat_collapsed = false;
                                        this.chat_unread = false;
                                        this.chat_follow_bottom = true;
                                        this.chat_scroll.scroll_to_bottom();
                                        cx.notify();
                                    });
                                }),
                        )
                    }),
            )
            .when(!self.chat_collapsed, |panel| {
                panel
                    .child(
                        div()
                            .id("channel-transcript")
                            .flex_1()
                            .min_h_0()
                            .px_3()
                            .overflow_y_scroll()
                            .track_scroll(&self.chat_scroll)
                            .relative()
                            .vertical_scrollbar(&self.chat_scroll)
                            .on_scroll_wheel(move |_, window, cx| {
                                let view = scrolled.clone();
                                view.update(cx, |this, _| this.chat_follow_bottom = false);
                                window.on_next_frame(move |_, cx| {
                                    view.update(cx, |this, cx| {
                                        this.chat_follow_bottom =
                                            f32::from(this.chat_scroll.max_offset().height)
                                                + f32::from(this.chat_scroll.offset().y)
                                                <= 4.;
                                        if this.chat_unread
                                            && !this.chat_collapsed
                                            && this.chat_follow_bottom
                                        {
                                            this.chat_unread = false;
                                            cx.notify();
                                        }
                                    });
                                });
                            })
                            .when(self.workspace.messages.is_empty(), |list| {
                                list.child(
                                    div()
                                        .py_2()
                                        .text_xs()
                                        .text_color(rgb(MUTED))
                                        .child("暂无实时消息"),
                                )
                            })
                            .children(
                                self.workspace
                                    .messages
                                    .iter()
                                    .filter(|m| m.channel_id == self.workspace.session.channel_id)
                                    .map(|message| {
                                        let retry = view.clone();
                                        let retry_id = message.id.clone();
                                        let copy_text = message.text.clone();
                                        div()
                                            .id(SharedString::from(format!("text-{}", message.id)))
                                            .py_1()
                                            .flex()
                                            .items_start()
                                            .gap_2()
                                            .child(
                                                div()
                                                    .w(px(40.))
                                                    .flex_shrink_0()
                                                    .text_xs()
                                                    .text_color(rgb(MUTED))
                                                    .child(short_time(&message.created_at)),
                                            )
                                            .child(
                                                div()
                                                    .max_w(px(100.))
                                                    .flex_shrink_0()
                                                    .truncate()
                                                    .text_xs()
                                                    .text_color(rgb(
                                                        if message.author_id
                                                            == self.workspace.session.self_id
                                                        {
                                                            MINT
                                                        } else {
                                                            ICE
                                                        },
                                                    ))
                                                    .child(message.author.clone()),
                                            )
                                            .child(
                                                div()
                                                    .flex_1()
                                                    .min_w_0()
                                                    .text_sm()
                                                    .text_color(rgb(TEXT))
                                                    .child(message.text.clone())
                                                    .when(!message.error.is_empty(), |body| {
                                                        body.child(
                                                            div()
                                                                .text_xs()
                                                                .text_color(rgb(RED))
                                                                .child(message.error.clone()),
                                                        )
                                                    })
                                                    .when(
                                                        matches!(
                                                            message.status.as_str(),
                                                            "sending" | "failed" | "unconfirmed"
                                                        ),
                                                        |body| {
                                                            body.child(
                                                                div()
                                                                    .text_xs()
                                                                    .text_color(rgb(
                                                                        message_status_color(
                                                                            &message.status,
                                                                        ),
                                                                    ))
                                                                    .child(message_status_label(
                                                                        &message.status,
                                                                    )),
                                                            )
                                                        },
                                                    ),
                                            )
                                            .children(
                                                message
                                                    .text
                                                    .split_whitespace()
                                                    .filter(|word| {
                                                        word.starts_with("https://")
                                                            || word.starts_with("http://")
                                                    })
                                                    .take(8)
                                                    .enumerate()
                                                    .map(|(i, url)| {
                                                        let url = url.to_owned();
                                                        Button::new(SharedString::from(format!(
                                                            "link-{}-{i}",
                                                            message.id
                                                        )))
                                                        .icon(IconName::ExternalLink)
                                                        .ghost()
                                                        .tooltip(url.clone())
                                                        .on_click(move |_, _, cx| cx.open_url(&url))
                                                    }),
                                            )
                                            .child(
                                                Button::new(SharedString::from(format!(
                                                    "copy-{}",
                                                    message.id
                                                )))
                                                .icon(IconName::Copy)
                                                .ghost()
                                                .tooltip("复制消息")
                                                .on_click(move |_, _, cx| {
                                                    cx.write_to_clipboard(
                                                        gpui::ClipboardItem::new_string(
                                                            copy_text.clone(),
                                                        ),
                                                    )
                                                }),
                                            )
                                            .when(
                                                matches!(
                                                    message.status.as_str(),
                                                    "failed" | "unconfirmed"
                                                ),
                                                |row| {
                                                    row.child(
                                                        Button::new(SharedString::from(format!(
                                                            "retry-{}",
                                                            message.id
                                                        )))
                                                        .icon(IconName::Undo2)
                                                        .ghost()
                                                        .tooltip("重试发送")
                                                        .on_click(move |_, _, cx| {
                                                            retry.update(cx, |this, cx| {
                                                                this.retry_message(
                                                                    retry_id.clone(),
                                                                    false,
                                                                    cx,
                                                                )
                                                            });
                                                        }),
                                                    )
                                                },
                                            )
                                    }),
                            ),
                    )
                    .child(
                        div()
                            .mx_3()
                            .my_2()
                            .min_w_0()
                            .h(px(64.))
                            .flex_shrink_0()
                            .relative()
                            .rounded(px(6.))
                            .border_1()
                            .border_color(rgb(LINE))
                            .bg(rgba(0xffffff06))
                            .child(
                                Input::new(&self.chat_input)
                                    .appearance(false)
                                    .w_full()
                                    .h_full()
                                    .pr(px(44.))
                                    .disabled(!can_send),
                            )
                            .child(
                                div()
                                    .absolute()
                                    .right(px(5.))
                                    .bottom(px(5.))
                                    .size(px(28.))
                                    .rounded(px(5.))
                                    .border_1()
                                    .border_color(rgba(0xffffff00))
                                    .hover(|s| s.border_color(rgba(0xffffff40)))
                                    .child(
                                        Button::new("send-message")
                                            .icon(IconName::ArrowUp)
                                            .text()
                                            .size_full()
                                            .tooltip("发送消息")
                                            .disabled(!can_send)
                                            .on_click(move |_, window, cx| {
                                                send.update(cx, |this, cx| {
                                                    this.send_message(window, cx)
                                                });
                                            }),
                                    ),
                            ),
                    )
            })
            .into_any_element()
    }

    fn render_details(&self, view: &Entity<Self>) -> AnyElement {
        div()
            .w(px(280.))
            .h_full()
            .flex_shrink_0()
            .bg(rgba(0x16181bed))
            .border_l_1()
            .border_color(rgba(0xffffff22))
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
            .bg(rgba(0x141619f5))
            .border_t_1()
            .border_color(rgba(0xffffff24))
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
                    .tooltip("全局收听增益降低 1 dB")
                    .disabled(!connected || !self.voice.enabled || self.global_gain_busy())
                    .on_click({
                        let entity = view.clone();
                        move |_, _, cx| {
                            entity.update(cx, |this, cx| this.change_global_gain(Some(-1), cx));
                        }
                    }),
            )
            .child(
                div()
                    .w(px(64.))
                    .text_center()
                    .text_xs()
                    .text_color(rgb(MUTED))
                    .child(playback_label(if connected {
                        target.volume
                    } else {
                        self.preferences.playback_volume
                    })),
            )
            .child(
                Button::new("volume-up")
                    .icon(IconName::Plus)
                    .ghost()
                    .tooltip("全局收听增益提高 1 dB")
                    .disabled(!connected || !self.voice.enabled || self.global_gain_busy())
                    .on_click({
                        let entity = view.clone();
                        move |_, _, cx| {
                            entity.update(cx, |this, cx| this.change_global_gain(Some(1), cx));
                        }
                    }),
            )
            .child(
                Button::new("audio-settings")
                    .icon(IconName::Settings2)
                    .ghost()
                    .tooltip("设备与声音设置")
                    .on_click({
                        let entity = view.clone();
                        move |_, _, cx| {
                            entity.update(cx, |this, cx| {
                                this.device_menu = None;
                                this.modal = Some(Modal::Devices);
                                cx.notify();
                            });
                        }
                    }),
            )
            .when(
                matches!(
                    mode,
                    "connecting" | "reconnecting" | "connected" | "disconnecting" | "preview"
                ),
                |bar| {
                    bar.child(
                        Button::new("disconnect")
                            .label(if mode == "connecting" {
                                "取消连接"
                            } else if mode == "reconnecting" {
                                "取消重连"
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
            &self.editing_preferences().input_device_id
        } else {
            &self.editing_preferences().output_device_id
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
                        || self.settings_apply.is_some()
                        || self.voice.busy
                        || matches!(
                            self.workspace.session.mode.as_str(),
                            "connecting" | "reconnecting" | "disconnecting"
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

    fn set_user_playback(
        &mut self,
        target: UserPlaybackTarget,
        volume: u16,
        muted: bool,
        cx: &mut Context<Self>,
    ) {
        if self.closing
            || !self.capabilities.voice
            || !self.workspace.session.switching_channel_id.is_empty()
        {
            self.user_playback_failed(target, "音频或频道正在切换，暂时无法调整".into());
            cx.notify();
            return;
        }
        if self
            .pending
            .values()
            .any(|pending| matches!(pending, Pending::UserPlayback(_)))
        {
            self.user_playback_failed(target, "请等待上一项收听调整完成".into());
            cx.notify();
            return;
        }
        if target.session != self.workspace.session.id
            || !self.workspace.connected()
            || !self.workspace.users.iter().any(|user| {
                user.id == target.user
                    && user.instance == target.instance
                    && !user.is_self
                    && user.channel_id == self.workspace.session.channel_id
            })
        {
            self.user_playback_failed(target, "该成员已离开或会话/频道已改变，请重新选择".into());
            cx.notify();
            return;
        }
        self.playback_error = None;
        if self.workspace.users.iter().any(|user| {
            user.id == target.user
                && user.instance == target.instance
                && user.playback_volume == volume
                && user.playback_muted == muted
        }) {
            cx.notify();
            return;
        }
        self.request(
            "SetUserPlayback",
            json!({
                "sessionID": target.session, "userID": target.user, "instance": target.instance,
                "volume": volume, "muted": muted,
            }),
            Pending::UserPlayback(target),
        );
        cx.notify();
    }

    fn user_playback_failed(&mut self, target: UserPlaybackTarget, error: String) {
        if self.playback_input_target.as_ref() == Some(&target) {
            self.playback_input_target = None;
        }
        self.playback_error = Some((target, error));
    }

    fn user_playback_action(
        &mut self,
        target: UserPlaybackTarget,
        action: UserPlaybackAction,
        cx: &mut Context<Self>,
    ) {
        let Some(user) = self
            .workspace
            .users
            .iter()
            .find(|u| u.id == target.user && u.instance == target.instance)
        else {
            self.user_playback_failed(target, "该成员已离开，请重新选择".into());
            cx.notify();
            return;
        };
        let (volume, muted) = action.apply(user);
        self.set_user_playback(target, volume, muted, cx);
    }

    fn render_user_playback(&self, user: &User, view: &Entity<Self>) -> AnyElement {
        let target = UserPlaybackTarget {
            session: self.workspace.session.id.clone(),
            user: user.id.clone(),
            instance: user.instance.clone(),
        };
        let pending = self
            .pending
            .values()
            .any(|pending| matches!(pending, Pending::UserPlayback(_)));
        let unavailable = !self.capabilities.voice
            || user.instance.is_empty()
            || user.channel_id != self.workspace.session.channel_id
            || !self.workspace.session.switching_channel_id.is_empty();
        let disabled = pending || unavailable;
        let pending_current = self
            .pending
            .values()
            .any(|pending| matches!(pending, Pending::UserPlayback(current) if current == &target));
        let mut controls = div()
            .py_3()
            .flex()
            .flex_col()
            .gap_2()
            .child(div().text_xs().text_color(rgb(MUTED)).child("收听音量"));
        let mute = view.clone();
        let mute_target = target.clone();
        let volume = user.playback_volume.min(794);
        let muted = user.playback_muted;
        let mut row = div().flex().items_center().gap_1().child(
            Button::new("peer-mute")
                .icon(if muted {
                    VoiceIcon::HeadphonesOff
                } else {
                    VoiceIcon::Volume
                })
                .ghost()
                .selected(muted)
                .disabled(disabled)
                .tooltip(if muted {
                    "取消本机静音"
                } else {
                    "在本机静音"
                })
                .on_click(move |_, _, cx| {
                    mute.update(cx, |this, cx| {
                        this.set_user_playback(mute_target.clone(), volume, !muted, cx)
                    })
                }),
        );
        let minus = view.clone();
        let minus_target = target.clone();
        row = row
            .child(
                Button::new("peer-volume-down")
                    .icon(IconName::Minus)
                    .ghost()
                    .tooltip("降低收听音量")
                    .disabled(disabled || volume <= 10)
                    .on_click(move |_, _, cx| {
                        minus.update(cx, |this, cx| {
                            this.set_user_playback(
                                minus_target.clone(),
                                step_playback_volume(volume, -1),
                                muted,
                                cx,
                            )
                        })
                    }),
            )
            .child(
                div().w(px(88.)).flex_shrink_0().child(
                    Input::new(&self.playback_input)
                        .disabled(disabled)
                        .suffix(div().text_xs().child("dB")),
                ),
            );
        let plus = view.clone();
        let plus_target = target.clone();
        row = row.child(
            Button::new("peer-volume-up")
                .icon(IconName::Plus)
                .ghost()
                .tooltip("提高收听音量")
                .disabled(disabled || volume >= 794)
                .on_click(move |_, _, cx| {
                    plus.update(cx, |this, cx| {
                        this.set_user_playback(
                            plus_target.clone(),
                            step_playback_volume(volume, 1),
                            muted,
                            cx,
                        )
                    })
                }),
        );
        let reset = view.clone();
        let reset_target = target.clone();
        row = row.child(
            Button::new("peer-volume-reset")
                .icon(IconName::Undo2)
                .ghost()
                .tooltip("恢复原始增益 0 dB")
                .disabled(disabled || volume == 100)
                .on_click(move |_, _, cx| {
                    reset.update(cx, |this, cx| {
                        this.set_user_playback(reset_target.clone(), 100, muted, cx)
                    })
                }),
        );
        controls = controls.child(row);
        if !self.playback_input_error.is_empty() {
            controls = controls.child(
                div()
                    .text_xs()
                    .text_color(rgb(RED))
                    .child(self.playback_input_error.clone()),
            );
        }
        if let Some((failed, error)) = &self.playback_error {
            if failed == &target {
                controls =
                    controls.child(div().text_xs().text_color(rgb(RED)).child(error.clone()));
            }
        }
        if pending || muted || unavailable {
            controls = controls.child(div().text_xs().text_color(rgb(MUTED)).child(
                if unavailable {
                    "仅当前频道成员可调节"
                } else if pending_current {
                    "正在应用"
                } else if pending {
                    "等待其他调整完成"
                } else {
                    "已在本机静音"
                },
            ));
        }
        controls.into_any_element()
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
        if let DetailSelection::User(id) = selection {
            if let Some(user) = self.workspace.users.iter().find(|user| &user.id == id) {
                if !user.is_self {
                    section = section.child(self.render_user_playback(user, view));
                }
            }
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
        let fields = detail_fields(selection);
        for (key, label) in fields {
            let text = if *key == "members"
                && value.get("memberSyncState").and_then(Value::as_str) != Some("ready")
            {
                "同步中".into()
            } else if *key == "maxClients"
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

    fn render_audio_settings(&self, view: &Entity<Self>, devices_page: bool) -> gpui::Div {
        let busy = self.settings_apply.is_some()
            || self.voice.busy
            || matches!(
                self.workspace.session.mode.as_str(),
                "connecting" | "reconnecting" | "disconnecting"
            )
            || self.microphone_test.enabled
            || self.microphone_test.busy
            || self.pending.values().any(|p| {
                matches!(
                    p,
                    Pending::Voice
                        | Pending::OutputGain(_)
                        | Pending::VoicePreferences { .. }
                        | Pending::MicrophoneTest
                )
            });
        let disabled = busy || !self.capabilities.voice;
        let preferences_disabled = self.settings_apply.is_some()
            || self.closing
            || !self.capabilities.voice
            || matches!(
                self.workspace.session.mode.as_str(),
                "connecting" | "reconnecting" | "disconnecting"
            )
            || self.microphone_test.enabled
            || self.microphone_test.busy;
        let entity = view.clone();
        let mut content = div()
            .flex()
            .flex_col()
            .gap_4()
            .when(!self.capabilities.voice, |s| {
                s.child(
                    div()
                        .text_xs()
                        .text_color(rgb(AMBER))
                        .child("此构建不提供音频设备能力"),
                )
            })
            .when(devices_page, |s| {
                s.child(
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
                )
            });
        if let Some(menu) = self.device_menu.filter(|_| devices_page) {
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
                                this.update_audio_preferences(
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
        if !devices_page {
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
                    .selected(self.editing_preferences().activation_mode == mode)
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
                .when(self.editing_preferences().activation_mode == "ptt", |s| {
                    let status = if !self.hotkey_error.is_empty() {
                        self.hotkey_error.clone()
                    } else if self.hotkey.is_some() {
                        format!(
                            "{} · 全局快捷键已注册",
                            self.editing_preferences().push_to_talk_shortcut
                        )
                    } else if !self.editing_preferences().global_push_to_talk {
                        "F8 · 仅当前窗口".into()
                    } else {
                        "启用麦克风后注册全局快捷键".into()
                    };
                    s.child(self.audio_toggle(
                        "global-ptt",
                        "后台按键发言",
                        self.editing_preferences().global_push_to_talk,
                        disabled,
                        |p, v| p.global_push_to_talk = v,
                        view,
                    ))
                    .child(div().flex().items_center().gap_2().child(
                        div().flex_1().child(
                            Input::new(&self.shortcut_input).disabled(
                                disabled || !self.editing_preferences().global_push_to_talk,
                            ),
                        ),
                    ))
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
                .when(self.editing_preferences().activation_mode == "vad", |s| {
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
                            .child(div().w(px(70.)).text_xs().text_center().child(format!(
                                "{} dB",
                                self.editing_preferences().vad_threshold_db
                            )))
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
            content = content.child(self.audio_toggle(
                "auto-unmute-on-connect",
                "连接后自动开启麦克风",
                self.editing_preferences().auto_unmute_on_connect,
                preferences_disabled,
                |p, v| p.auto_unmute_on_connect = v,
                view,
            ));
            content = content.child(
                div()
                    .border_t_1()
                    .border_color(rgb(LINE))
                    .pt_3()
                    .flex()
                    .flex_col()
                    .gap_3()
                    .child(div().text_sm().child("麦克风处理"))
                    .child(self.processor_selector(
                        "pre-aec",
                        "AEC（回声消除）",
                        false,
                        0,
                        preferences_disabled,
                        view,
                    ))
                    .child(
                        self.audio_toggle(
                            "pre-aec-residual",
                            "残余回声抑制",
                            self.editing_preferences().processing.preprocess[0]
                                .params
                                .residual,
                            preferences_disabled
                                || self.editing_preferences().processing.preprocess[0].backend
                                    != "speex",
                            |p, v| p.processing.preprocess[0].params.residual = v,
                            view,
                        ),
                    )
                    .child(self.processor_stepper(
                        "pre-aec-tail",
                        "AEC 尾长",
                        false,
                        0,
                        "tail",
                        200,
                        40,
                        500,
                        20,
                        "ms",
                        preferences_disabled,
                        view,
                    ))
                    .child(self.processor_selector(
                        "pre-ans",
                        "ANS（噪声抑制）",
                        false,
                        1,
                        preferences_disabled,
                        view,
                    ))
                    .child(self.ans_level_selector("pre-ans-level", preferences_disabled, view))
                    .child(
                        div()
                            .border_t_1()
                            .border_color(rgb(LINE))
                            .pt_3()
                            .text_sm()
                            .child("收听处理"),
                    )
                    .child(self.processor_selector(
                        "post-agc",
                        "AGC（自动增益控制）",
                        true,
                        0,
                        preferences_disabled,
                        view,
                    ))
                    .child(self.processor_stepper(
                        "post-agc-target",
                        "增益目标 · 每人",
                        true,
                        0,
                        "target",
                        8192,
                        2048,
                        16384,
                        1024,
                        "",
                        preferences_disabled,
                        view,
                    ))
                    .child(self.processor_stepper(
                        "post-agc-max",
                        "最大自动增益 · 每人",
                        true,
                        0,
                        "max",
                        18,
                        1,
                        24,
                        1,
                        "dB",
                        preferences_disabled,
                        view,
                    ))
                    .child(self.processor_stepper(
                        "post-agc-headroom",
                        "目标余量 · 每人",
                        true,
                        0,
                        "headroom",
                        5,
                        1,
                        20,
                        1,
                        "dB",
                        preferences_disabled,
                        view,
                    ))
                    .child(self.audio_toggle(
                        "voice-ducking",
                        "发言时降低频道音量",
                        self.editing_preferences().ducking,
                        preferences_disabled,
                        |p, v| p.ducking = v,
                        view,
                    )),
            );
        }
        if devices_page {
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
                                    .tooltip(if self.settings_dirty() {
                                        "先应用或取消修改，再测试麦克风"
                                    } else {
                                        "使用已应用的语音设置"
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
                                div().flex_1().min_w_0().h(px(8.)).bg(rgb(LINE)).child(
                                    div()
                                        .w(gpui::relative((level + 60) as f32 / 60.))
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
                                "connecting" | "reconnecting" | "disconnecting"
                            ) {
                                "连接正在切换".into()
                            } else {
                                "已停止".into()
                            }),
                    ),
            );
        }
        if devices_page && !self.input_gain_error.is_empty() {
            content = content.child(
                div()
                    .text_xs()
                    .text_color(rgb(RED))
                    .child(self.input_gain_error.clone()),
            );
        }
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
            .w_full()
            .min_w_0()
            .h(px(420.))
            .flex()
            .flex_col()
            .child(
                content
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scrollbar()
                    .p_5()
                    .pr_6(),
            )
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

    fn processor_selector(
        &self,
        id: &'static str,
        label: &'static str,
        receive: bool,
        index: usize,
        disabled: bool,
        view: &Entity<Self>,
    ) -> AnyElement {
        let selected = if receive {
            &self.editing_preferences().processing.postprocess[index]
        } else {
            &self.editing_preferences().processing.preprocess[index]
        };
        let phase = if receive { "postprocess" } else { "preprocess" };
        let name = if receive {
            "agc"
        } else if index == 0 {
            "aec"
        } else {
            "ans"
        };
        let options = self
            .capabilities
            .audio_processors
            .iter()
            .filter(|option| option.phase == phase && option.name == name)
            .cloned()
            .collect::<Vec<_>>();
        let mut description = options
            .iter()
            .filter(|option| option.id != "none")
            .map(|option| format!("{}：{}", option.display_name, option.description))
            .collect::<Vec<_>>()
            .join("\n");
        if description.is_empty() {
            description = "尚未取得可用处理器信息，可在诊断页重新探测 Core。".into();
        }
        let selected_label = options
            .iter()
            .find(|option| option.id == selected.backend)
            .map(|option| option.display_name.clone())
            .unwrap_or_else(|| {
                if options.is_empty() {
                    format!("{}（信息未获取）", selected.backend)
                } else {
                    format!("{}（当前不可用）", selected.backend)
                }
            });
        let entity = view.clone();
        div()
            .flex()
            .flex_col()
            .gap_2()
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap_2()
                    .child(div().text_xs().text_color(rgb(MUTED)).child(label))
                    .child(
                        div()
                            .id(SharedString::from(format!("{id}-help")))
                            .tab_index(0)
                            .size(px(16.))
                            .flex()
                            .items_center()
                            .justify_center()
                            .tooltip(move |window, cx| {
                                Tooltip::new(description.clone()).build(window, cx)
                            })
                            .child(
                                svg()
                                    .path(IconName::Info.path())
                                    .size(px(12.))
                                    .text_color(rgb(MUTED)),
                            ),
                    ),
            )
            .child(
                Button::new(id)
                    .label(selected_label)
                    .icon(IconName::ChevronDown)
                    .disabled(disabled || options.is_empty())
                    .dropdown_menu(move |mut menu, _, _| {
                        for option in &options {
                            let entity = entity.clone();
                            let backend = option.id.clone();
                            menu = menu.item(
                                PopupMenuItem::new(option.display_name.clone()).on_click(
                                    move |_, _, cx| {
                                        entity.update(cx, |this, cx| {
                                            if this.settings_apply.is_some()
                                                || this.settings_draft.is_none()
                                            {
                                                return;
                                            }
                                            this.update_audio_preferences(
                                                |p| {
                                                    let spec = if receive {
                                                        &mut p.processing.postprocess[index]
                                                    } else {
                                                        &mut p.processing.preprocess[index]
                                                    };
                                                    spec.backend = backend.clone();
                                                    spec.params = Default::default();
                                                },
                                                cx,
                                            );
                                        });
                                    },
                                ),
                            );
                        }
                        menu
                    }),
            )
            .into_any_element()
    }

    fn ans_level_selector(
        &self,
        id: &'static str,
        disabled: bool,
        view: &Entity<Self>,
    ) -> AnyElement {
        let spec = &self.editing_preferences().processing.preprocess[1];
        if spec.backend != "speex" && spec.backend != "webrtc" {
            return div().into_any_element();
        }
        let current = if spec.params.level == 0 {
            2
        } else {
            spec.params.level
        };
        let is_webrtc = spec.backend == "webrtc";
        let entity = view.clone();
        field(
            "降噪等级",
            Button::new(id)
                .label(match current {
                    1 => "低",
                    3 => "高",
                    4 => "极高",
                    _ => "中",
                })
                .icon(IconName::ChevronDown)
                .disabled(disabled)
                .dropdown_menu(move |mut menu, _, _| {
                    let mut levels = vec![("低", 1), ("中", 2), ("高", 3)];
                    if is_webrtc {
                        levels.push(("极高", 4));
                    }
                    for (label, level) in levels {
                        let entity = entity.clone();
                        menu = menu.item(PopupMenuItem::new(label).on_click(move |_, _, cx| {
                            entity.update(cx, |this, cx| {
                                if this.settings_apply.is_some() || this.settings_draft.is_none() {
                                    return;
                                }
                                this.update_audio_preferences(
                                    |p| {
                                        let spec = &mut p.processing.preprocess[1];
                                        spec.params.level = level;
                                    },
                                    cx,
                                );
                            });
                        }));
                    }
                    menu
                }),
        )
        .into_any_element()
    }

    #[allow(clippy::too_many_arguments)]
    fn processor_stepper(
        &self,
        id: &'static str,
        label: &'static str,
        receive: bool,
        index: usize,
        parameter: &'static str,
        default: i32,
        minimum: i32,
        maximum: i32,
        step: i32,
        unit: &'static str,
        disabled: bool,
        view: &Entity<Self>,
    ) -> AnyElement {
        let spec = if receive {
            &self.editing_preferences().processing.postprocess[index]
        } else {
            &self.editing_preferences().processing.preprocess[index]
        };
        if !matches!(
            (spec.backend.as_str(), parameter),
            ("speex", "tail" | "target" | "max") | ("webrtc", "max" | "headroom")
        ) {
            return div().into_any_element();
        }
        let value = match parameter {
            "tail" => spec.params.tail_ms,
            "target" => spec.params.target,
            "headroom" => spec.params.headroom_db,
            _ => spec.params.max_gain_db,
        };
        let value = if value == 0 { default } else { value };
        let controls = div().flex().items_center().gap_2();
        let controls = [-1, 1].into_iter().fold(controls, |controls, direction| {
            let entity = view.clone();
            let button = Button::new(SharedString::from(format!("{id}-{direction}")))
                .icon(if direction < 0 {
                    IconName::Minus
                } else {
                    IconName::Plus
                })
                .ghost()
                .disabled(
                    disabled
                        || (direction < 0 && value <= minimum)
                        || (direction > 0 && value >= maximum),
                )
                .on_click(move |_, _, cx| {
                    entity.update(cx, |this, cx| {
                        if this.settings_apply.is_some() || this.settings_draft.is_none() {
                            return;
                        }
                        this.update_audio_preferences(
                            |p| {
                                let spec = if receive {
                                    &mut p.processing.postprocess[index]
                                } else {
                                    &mut p.processing.preprocess[index]
                                };
                                let field = match parameter {
                                    "tail" => &mut spec.params.tail_ms,
                                    "target" => &mut spec.params.target,
                                    "headroom" => &mut spec.params.headroom_db,
                                    _ => &mut spec.params.max_gain_db,
                                };
                                *field = (if *field == 0 { default } else { *field }
                                    + direction * step)
                                    .clamp(minimum, maximum);
                            },
                            cx,
                        );
                    });
                });
            if direction < 0 {
                controls.child(button).child(
                    div()
                        .w(px(80.))
                        .text_xs()
                        .text_center()
                        .child(format!("{value} {unit}")),
                )
            } else {
                controls.child(button)
            }
        });
        field(label, controls).into_any_element()
    }

    fn render_modal(&self, view: &Entity<Self>) -> Option<AnyElement> {
        let modal = self.modal.clone()?;
        let settings_page = matches!(
            modal,
            Modal::Devices
                | Modal::Audio
                | Modal::Settings
                | Modal::Appearance
                | Modal::Diagnostics
                | Modal::About
        );
        let audio_page = matches!(modal, Modal::Devices | Modal::Audio);
        let entity = view.clone();
        let card = match modal.clone() {
            Modal::Channel { id, delete, .. } => {
                let pending = self.channel_pending();
                let audio_entity = entity.clone();
                let audio_session = self.workspace.session.id.clone();
                let audio_channel = id.clone();
                let audio_revision = self.channel_revision;
                let close = entity.clone();
                let target = self.workspace.channels.iter().find(|ch| ch.id == id);
                let unavailable = !id.is_empty()
                    && (target.is_none()
                        || (delete && target.is_some_and(|ch| ch.is_default || ch.members != 0)));
                div().child(
                    div()
                        .id("channel-management-panel")
                        .w(px(440.))
                        .max_h(px(480.))
                        .overflow_y_scrollbar()
                        .p_5()
                        .rounded(px(7.))
                        .border_1()
                        .border_color(rgb(LINE))
                        .bg(rgb(PANEL_2))
                        .shadow_lg()
                        .flex()
                        .flex_col()
                        .gap_4()
                        .child(modal_title(if delete {
                            "删除频道"
                        } else if id.is_empty() {
                            "新建频道"
                        } else {
                            "编辑频道"
                        }))
                        .when(delete, |card| {
                            card.child(div().text_sm().child(format!(
                                "确认删除频道「{}」？",
                                target.map(|ch| ch.name.as_str()).unwrap_or("已删除")
                            )))
                        })
                        .when(!delete, |card| {
                            card.child(div().text_xs().text_color(rgb(MUTED)).child("名称"))
                                .child(
                                    Input::new(&self.channel_name_input)
                                        .disabled(pending || unavailable),
                                )
                                .child(div().text_xs().text_color(rgb(MUTED)).child("描述"))
                                .child(
                                    Input::new(&self.channel_description_input)
                                        .h(px(110.))
                                        .disabled(pending || unavailable),
                                )
                        })
                        .when(
                            !delete && self.workspace.session.can_configure_channel_audio,
                            |card| {
                                card.child(field(
                                    "音质",
                                    Button::new("channel-audio-preset")
                                        .label(match self.channel_audio_preset {
                                            20000 => "省流 · 20 kbps",
                                            32000 => "游戏语音 · 32 kbps",
                                            48000 => "高清语音 · 48 kbps",
                                            _ => "自定义",
                                        })
                                        .icon(IconName::ChevronDown)
                                        .disabled(pending || unavailable)
                                        .dropdown_menu(move |mut menu, _, _| {
                                            for (label, value) in [
                                                ("省流 · 20 kbps", 20000),
                                                ("游戏语音 · 32 kbps", 32000),
                                                ("高清语音 · 48 kbps", 48000),
                                                ("自定义", 0),
                                            ] {
                                                let entity = audio_entity.clone();
                                                let session = audio_session.clone();
                                                let channel = audio_channel.clone();
                                                menu =
                                                    menu.item(PopupMenuItem::new(label).on_click(
                                                        move |_, _, cx| {
                                                            entity.update(cx, |this, cx| {
                                                                if this.channel_pending()
                                                                    || !this.channel_matches(&session, audio_revision)
                                                                    || !this.workspace.session.can_configure_channel_audio
                                                                    || !matches!(&this.modal, Some(Modal::Channel { id, delete: false, .. }) if *id == channel)
                                                                    || (!channel.is_empty() && !this.workspace.channels.iter().any(|ch| ch.id == channel))
                                                                {
                                                                    return;
                                                                }
                                                                this.channel_audio_preset = value;
                                                                this.channel_error.clear();
                                                                cx.notify();
                                                            });
                                                        },
                                                    ));
                                            }
                                            menu
                                        }),
                                ))
                                .when(
                                    self.channel_audio_preset == 0,
                                    |card| {
                                        card.child(field(
                                            "目标码率 (kbps)",
                                            Input::new(&self.channel_bitrate_input)
                                                .disabled(pending || unavailable),
                                        ))
                                    },
                                )
                            },
                        )
                        .when(
                            !delete && !self.workspace.session.can_configure_channel_audio,
                            |card| {
                                card.child(
                                    div()
                                        .text_xs()
                                        .text_color(rgb(MUTED))
                                        .child("服务器不支持音质设置"),
                                )
                            },
                        )
                        .when(unavailable, |card| {
                            card.child(
                                div()
                                    .text_sm()
                                    .text_color(rgb(RED))
                                    .child("频道已不存在，或当前不允许删除"),
                            )
                        })
                        .when(!self.channel_error.is_empty(), |card| {
                            card.child(
                                div()
                                    .text_sm()
                                    .text_color(rgb(RED))
                                    .child(self.channel_error.clone()),
                            )
                        })
                        .child(
                            div()
                                .flex()
                                .justify_end()
                                .gap_2()
                                .child(
                                    Button::new("close-channel-form")
                                        .label(if pending { "关闭" } else { "取消" })
                                        .ghost()
                                        .on_click(move |_, window, cx| {
                                            close.update(cx, |this, cx| {
                                                this.close_modal(window, cx)
                                            });
                                        }),
                                )
                                .child(
                                    Button::new("submit-channel-form")
                                        .label(if pending {
                                            "处理中…"
                                        } else if delete {
                                            "删除"
                                        } else {
                                            "保存"
                                        })
                                        .primary()
                                        .disabled(pending || unavailable)
                                        .on_click(move |_, _, cx| {
                                            entity.update(cx, |this, cx| this.submit_channel(cx));
                                        }),
                                ),
                        ),
                )
            }
            Modal::Ownership { .. } => {
                let pending =
                    self.claim_owner_pending() && self.workspace.session.server_role != "owner";
                let role = match self.workspace.session.server_role.as_str() {
                    "owner" => "所有者",
                    "admin" => "管理员",
                    "member" => "成员",
                    _ => "未知角色",
                };
                div().child(
                    div()
                        .id("server-ownership-panel")
                        .w(px(440.))
                        .max_h(px(500.))
                        .overflow_y_scrollbar()
                        .p_5()
                        .rounded(px(7.))
                        .border_1()
                        .border_color(rgb(LINE))
                        .bg(rgb(PANEL_2))
                        .shadow_lg()
                        .flex()
                        .flex_col()
                        .gap_4()
                        .child(modal_title("服务器身份与权限"))
                        .child(
                            div()
                                .text_sm()
                                .truncate()
                                .child(self.workspace.session.server_name.clone()),
                        )
                        .child(div().text_sm().child(format!("当前角色：{role}")))
                        .child(div().text_xs().text_color(rgb(MUTED)).child("身份 ID"))
                        .child(
                            div()
                                .text_size(px(10.))
                                .child(
                                    div().child(
                                        self.workspace
                                            .session
                                            .identity_uid
                                            .chars()
                                            .take(32)
                                            .collect::<String>(),
                                    ),
                                )
                                .child(
                                    div().child(
                                        self.workspace
                                            .session
                                            .identity_uid
                                            .chars()
                                            .skip(32)
                                            .collect::<String>(),
                                    ),
                                ),
                        )
                        .when(self.workspace.session.can_claim_owner, |card| {
                            card.child(Input::new(&self.claim_input).disabled(pending))
                        })
                        .when(pending, |card| {
                            card.child(
                                div()
                                    .text_sm()
                                    .text_color(rgb(MUTED))
                                    .child("正在确认所有权…"),
                            )
                        })
                        .when(!self.ownership_error.is_empty(), |card| {
                            card.child(
                                div()
                                    .text_sm()
                                    .text_color(rgb(RED))
                                    .child(self.ownership_error.clone()),
                            )
                        })
                        .child(
                            div()
                                .flex()
                                .justify_end()
                                .gap_2()
                                .child(
                                    Button::new("close-ownership")
                                        .label("关闭")
                                        .ghost()
                                        .on_click({
                                            let entity = entity.clone();
                                            move |_, window, cx| {
                                                entity.update(cx, |this, cx| {
                                                    this.close_modal(window, cx)
                                                });
                                            }
                                        }),
                                )
                                .when(self.workspace.session.can_claim_owner, |row| {
                                    row.child(
                                        Button::new("claim-owner")
                                            .label("认领所有者")
                                            .primary()
                                            .disabled(pending)
                                            .on_click(move |_, window, cx| {
                                                entity.update(cx, |this, cx| {
                                                    this.claim_owner(window, cx)
                                                });
                                            }),
                                    )
                                }),
                        ),
                )
            }
            Modal::Devices => self.render_audio_settings(view, true),
            Modal::Audio => self.render_audio_settings(view, false),
            Modal::Server { editing_id } => {
                let editing = !editing_id.is_empty();
                div().child(
                    div()
                        .id("server-profile-form")
                        .w(px(440.))
                        .max_h(px(500.))
                        .overflow_y_scrollbar()
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
                        .child(field(
                            "服务器公钥 X25519（必填）",
                            Input::new(&self.public_key_input),
                        ))
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
                                                entity.update(cx, |this, cx| {
                                                    this.close_modal(window, cx)
                                                })
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
                            this.request("PlayNotification", json!({"kind": kind, "volume": this.editing_preferences().notification_volume}), Pending::Notification);
                            cx.notify();
                        });
                    })
                }).collect::<Vec<_>>();
                div()
                    .w_full()
                    .min_w_0()
                    .h(px(420.))
                    .p_5()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(
                        Checkbox::new("notifications-enabled")
                            .label("播放连接与成员提示音")
                            .checked(self.editing_preferences().notifications_enabled)
                            .on_click({
                                let entity = entity.clone();
                                move |enabled, _, cx| {
                                    entity.update(cx, |this, cx| {
                                        this.update_audio_preferences(
                                            |p| p.notifications_enabled = *enabled,
                                            cx,
                                        );
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
                                                this.update_audio_preferences(
                                                    |p| {
                                                        p.notification_volume =
                                                            p.notification_volume.saturating_sub(5)
                                                    },
                                                    cx,
                                                );
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
                                            .w(px(self.editing_preferences().notification_volume
                                                as f32
                                                * 2.))
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
                                    .child(
                                        self.editing_preferences().notification_volume.to_string(),
                                    ),
                            )
                            .child(
                                Button::new("notification-volume-up")
                                    .icon(IconName::Plus)
                                    .ghost()
                                    .on_click({
                                        let entity = entity.clone();
                                        move |_, _, cx| {
                                            entity.update(cx, |this, cx| {
                                                this.update_audio_preferences(
                                                    |p| {
                                                        p.notification_volume = p
                                                            .notification_volume
                                                            .saturating_add(5)
                                                            .min(100)
                                                    },
                                                    cx,
                                                );
                                            })
                                        }
                                    }),
                            ),
                    )
                    .child(div().flex().flex_wrap().gap_2().children(previews))
            }
            Modal::Appearance => {
                let choices = [
                    (ThemePreference::System, "跟随系统", "theme-system"),
                    (ThemePreference::Dark, "深色", "theme-dark"),
                    (ThemePreference::Light, "浅色", "theme-light"),
                ];
                div()
                    .w_full()
                    .min_w_0()
                    .h(px(420.))
                    .p_5()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(div().text_sm().text_color(rgb(MUTED)).child("界面主题"))
                    .child(
                        div()
                            .flex()
                            .flex_wrap()
                            .gap_2()
                            .children(choices.into_iter().map(|(choice, label, id)| {
                                let entity = entity.clone();
                                Button::new(id)
                                    .label(label)
                                    .outline()
                                    .selected(self.editing_preferences().theme == choice)
                                    .disabled(self.settings_apply.is_some())
                                    .on_click(move |_, window, cx| {
                                        entity.update(cx, |this, cx| {
                                            this.change_theme(choice, window, cx)
                                        });
                                    })
                            })),
                    )
            }
            Modal::Diagnostics => {
                let probing = self
                    .pending
                    .values()
                    .any(|p| matches!(p, Pending::Probe(_)));
                let exporting = self
                    .pending
                    .values()
                    .any(|p| matches!(p, Pending::ExportDiagnostics));
                div()
                    .w_full()
                    .min_w_0()
                    .h(px(420.))
                    .p_5()
                    .flex()
                    .flex_col()
                    .gap_5()
                    .child(div().text_sm().text_color(rgb(MUTED)).child("核心状态"))
                    .child(
                        div()
                            .text_sm()
                            .text_color(rgb(TEXT))
                            .child(if self.core.is_some() {
                                "核心进程运行中"
                            } else {
                                "核心进程不可用"
                            }),
                    )
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap_3()
                            .child(
                                Button::new("probe-core")
                                    .label(if probing { "探测中" } else { "探测 Core" })
                                    .icon(IconName::CircleCheck)
                                    .outline()
                                    .disabled(self.core.is_none() || probing)
                                    .on_click({
                                        let entity = entity.clone();
                                        move |_, _, cx| {
                                            entity.update(cx, |this, cx| {
                                                this.probe_result = "正在等待核心响应".into();
                                                this.request(
                                                    "GetCapabilities",
                                                    json!({}),
                                                    Pending::Probe(Instant::now()),
                                                );
                                                cx.notify();
                                            });
                                        }
                                    }),
                            )
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(rgb(MUTED))
                                    .child(self.probe_result.clone()),
                            ),
                    )
                    .child(div().w_full().h(px(1.)).bg(rgb(LINE)))
                    .child(div().text_sm().text_color(rgb(MUTED)).child("本机诊断"))
                    .child(
                        Button::new("export-diagnostics")
                            .label(if exporting {
                                "正在导出"
                            } else {
                                "导出诊断"
                            })
                            .icon(IconName::ArrowDown)
                            .outline()
                            .disabled(self.core.is_none() || exporting || self.closing)
                            .on_click({
                                let entity = entity.clone();
                                move |_, _, cx| {
                                    entity.update(cx, |this, cx| {
                                        this.request(
                                            "ExportDiagnostics",
                                            json!({}),
                                            Pending::ExportDiagnostics,
                                        );
                                        cx.notify();
                                    });
                                }
                            }),
                    )
            }
            Modal::About => {
                let info = self.core_info.as_ref();
                let value = |key: &str| {
                    info.and_then(|v| v.get(key))
                        .and_then(Value::as_str)
                        .unwrap_or("--")
                        .to_string()
                };
                div()
                    .w_full()
                    .min_w_0()
                    .h(px(420.))
                    .p_5()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(
                        div()
                            .text_lg()
                            .font_weight(gpui::FontWeight::SEMIBOLD)
                            .child("Resona"),
                    )
                    .child(div().w_full().h(px(1.)).bg(rgb(LINE)))
                    .child(
                        div()
                            .flex()
                            .gap_6()
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .flex()
                                    .flex_col()
                                    .gap_4()
                                    .child(field(
                                        "桌面版本",
                                        div().text_sm().child(env!("CARGO_PKG_VERSION")),
                                    ))
                                    .child(field(
                                        "桌面平台",
                                        div().text_sm().child(format!(
                                            "{}/{}",
                                            std::env::consts::OS,
                                            std::env::consts::ARCH
                                        )),
                                    )),
                            )
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .flex()
                                    .flex_col()
                                    .gap_4()
                                    .child(field(
                                        "Core 版本",
                                        div().text_sm().child(value("version")),
                                    ))
                                    .child(field(
                                        "提交版本",
                                        div().text_sm().child(
                                            value("commit").chars().take(12).collect::<String>(),
                                        ),
                                    ))
                                    .child(field(
                                        "构建时间",
                                        div().text_sm().child(value("buildTime")),
                                    ))
                                    .child(field(
                                        "Go 运行时",
                                        div().text_sm().child(value("goVersion")),
                                    ))
                                    .child(field(
                                        "Core 平台",
                                        div().text_sm().child(value("platform")),
                                    )),
                            ),
                    )
            }
        };
        let card = if settings_page {
            let change_disabled = self.microphone_test.enabled || self.microphone_test.busy;
            div()
                .w(px(780.))
                .max_w_full()
                .h(px(540.))
                .flex()
                .rounded(px(8.))
                .overflow_hidden()
                .border_1()
                .border_color(rgba(0xffffff38))
                .bg(rgba(0x17191df5))
                .shadow_lg()
                .child(
                    div()
                        .w(px(148.))
                        .h_full()
                        .flex_shrink_0()
                        .p_4()
                        .flex()
                        .flex_col()
                        .gap_3()
                        .bg(rgba(0x080a0d55))
                        .border_r_1()
                        .border_color(rgba(0xffffff14))
                        .child(
                            div()
                                .py_3()
                                .text_lg()
                                .font_weight(gpui::FontWeight::SEMIBOLD)
                                .child("设置"),
                        )
                        .child(
                            Button::new("settings-nav-devices")
                                .child(settings_nav_contents("设备", IconName::Settings2))
                                .compact()
                                .justify_start()
                                .ghost()
                                .selected(matches!(modal, Modal::Devices))
                                .w_full()
                                .disabled(change_disabled)
                                .on_click({
                                    let entity = view.clone();
                                    move |_, _, cx| {
                                        entity.update(cx, |this, cx| {
                                            this.device_menu = None;
                                            this.modal = Some(Modal::Devices);
                                            cx.notify();
                                        });
                                    }
                                }),
                        )
                        .child(
                            Button::new("settings-nav-audio")
                                .child(settings_nav_contents("声音", VoiceIcon::Headphones))
                                .compact()
                                .justify_start()
                                .ghost()
                                .selected(matches!(modal, Modal::Audio))
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
                                .child(settings_nav_contents("提示音", VoiceIcon::Volume))
                                .compact()
                                .justify_start()
                                .ghost()
                                .selected(matches!(modal, Modal::Settings))
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
                        )
                        .child(
                            Button::new("settings-nav-appearance")
                                .child(settings_nav_contents("外观", IconName::Palette))
                                .compact()
                                .justify_start()
                                .ghost()
                                .selected(matches!(modal, Modal::Appearance))
                                .w_full()
                                .disabled(change_disabled)
                                .on_click({
                                    let entity = view.clone();
                                    move |_, _, cx| {
                                        entity.update(cx, |this, cx| {
                                            this.device_menu = None;
                                            this.modal = Some(Modal::Appearance);
                                            cx.notify();
                                        });
                                    }
                                }),
                        )
                        .child(div().flex_1())
                        .child(
                            Button::new("settings-nav-diagnostics")
                                .child(settings_nav_contents("诊断", IconName::CircleCheck))
                                .compact()
                                .justify_start()
                                .ghost()
                                .selected(matches!(modal, Modal::Diagnostics))
                                .w_full()
                                .disabled(change_disabled)
                                .on_click({
                                    let entity = view.clone();
                                    move |_, _, cx| {
                                        entity.update(cx, |this, cx| {
                                            this.device_menu = None;
                                            this.modal = Some(Modal::Diagnostics);
                                            cx.notify();
                                        });
                                    }
                                }),
                        )
                        .child(
                            Button::new("settings-nav-about")
                                .child(settings_nav_contents("关于", IconName::Info))
                                .compact()
                                .justify_start()
                                .ghost()
                                .selected(matches!(modal, Modal::About))
                                .w_full()
                                .disabled(change_disabled)
                                .on_click({
                                    let entity = view.clone();
                                    move |_, _, cx| {
                                        entity.update(cx, |this, cx| {
                                            this.device_menu = None;
                                            this.modal = Some(Modal::About);
                                            if this.core_info.is_none() && this.core.is_some() {
                                                this.request(
                                                    "GetCoreInfo",
                                                    json!({}),
                                                    Pending::CoreInfo,
                                                );
                                            }
                                            cx.notify();
                                        });
                                    }
                                }),
                        ),
                )
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .flex()
                        .flex_col()
                        .child(
                            div()
                                .h(px(60.))
                                .flex_shrink_0()
                                .px_5()
                                .border_b_1()
                                .border_color(rgb(LINE))
                                .flex()
                                .items_center()
                                .gap_3()
                                .child(div().flex_1().min_w_0().child(modal_title(match modal {
                                    Modal::Devices => "设备",
                                    Modal::Audio => "声音",
                                    Modal::Settings => "提示音",
                                    Modal::Appearance => "外观",
                                    Modal::Diagnostics => "诊断",
                                    _ => "关于",
                                })))
                                .when(
                                    audio_page && (self.audio_settings_pending || self.voice.busy),
                                    |s| {
                                        s.child(
                                            div().text_xs().text_color(rgb(MUTED)).child("应用中"),
                                        )
                                    },
                                )
                                .child(
                                    Button::new("close-settings")
                                        .icon(IconName::Close)
                                        .ghost()
                                        .tooltip("关闭设置")
                                        .on_click({
                                            let entity = view.clone();
                                            move |_, window, cx| {
                                                entity.update(cx, |this, cx| {
                                                    this.close_modal(window, cx)
                                                });
                                            }
                                        }),
                                ),
                        )
                        .child(card)
                        .child(
                            div()
                                .h(px(60.))
                                .flex_shrink_0()
                                .px_4()
                                .border_t_1()
                                .border_color(rgb(LINE))
                                .flex()
                                .items_center()
                                .gap_2()
                                .child(
                                    div()
                                        .flex_1()
                                        .min_w_0()
                                        .text_xs()
                                        .text_color(rgb(AMBER))
                                        .child(if self.settings_discard {
                                            "放弃未应用的修改？".to_owned()
                                        } else {
                                            self.settings_notice.clone()
                                        }),
                                )
                                .child(
                                    Button::new("cancel-settings")
                                        .label(if self.settings_discard {
                                            "放弃修改"
                                        } else {
                                            "取消"
                                        })
                                        .ghost()
                                        .disabled(self.settings_apply.is_some())
                                        .on_click({
                                            let entity = view.clone();
                                            move |_, window, cx| {
                                                entity.update(cx, |this, cx| {
                                                    this.settings_draft =
                                                        this.settings_baseline.clone();
                                                    theme::apply(
                                                        this.preferences.theme,
                                                        Some(window),
                                                        cx,
                                                    );
                                                    this.close_modal(window, cx);
                                                });
                                            }
                                        }),
                                )
                                .child(
                                    Button::new("apply-settings")
                                        .label(if self.settings_discard {
                                            "继续编辑"
                                        } else {
                                            "应用"
                                        })
                                        .primary()
                                        .disabled(
                                            !self.settings_discard
                                                && (!self.settings_dirty()
                                                    || self.settings_apply.is_some()
                                                    || (self.settings_needs_voice_update()
                                                        && (self.global_gain_busy()
                                                            || self.is_busy()
                                                            || matches!(
                                                                self.workspace
                                                                    .session
                                                                    .mode
                                                                    .as_str(),
                                                                "connecting"
                                                                    | "reconnecting"
                                                                    | "disconnecting"
                                                            )))),
                                        )
                                        .on_click({
                                            let entity = view.clone();
                                            move |_, _, cx| {
                                                entity.update(cx, |this, cx| {
                                                    if this.settings_discard {
                                                        this.settings_discard = false;
                                                        cx.notify();
                                                    } else {
                                                        this.apply_settings(cx);
                                                    }
                                                });
                                            }
                                        }),
                                ),
                        ),
                )
                .into_any_element()
        } else {
            card.into_any_element()
        };
        Some(
            div()
                .id("modal-scrim")
                .absolute()
                .inset_0()
                .bg(rgba(0x00000066))
                .on_mouse_down(gpui::MouseButton::Left, |_, _, cx| cx.stop_propagation())
                .on_mouse_down(gpui::MouseButton::Right, |_, _, cx| cx.stop_propagation())
                .on_click(|_, _, cx| cx.stop_propagation())
                .flex()
                .items_center()
                .justify_center()
                .px_3()
                .child(card)
                .into_any_element(),
        )
    }

    fn session_label(&self) -> String {
        match self.workspace.session.mode.as_str() {
            "preview" => "本地预览",
            "connecting" => "正在连接",
            "reconnecting" => "正在重连",
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
            "preview" | "connecting" | "reconnecting" | "disconnecting" => AMBER,
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
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        if matches!(
            self.modal,
            Some(
                Modal::Devices
                    | Modal::Audio
                    | Modal::Settings
                    | Modal::Appearance
                    | Modal::Diagnostics
                    | Modal::About
            )
        ) && self.settings_draft.is_none()
        {
            self.settings_draft = Some(self.preferences.clone());
            self.settings_baseline = self.settings_draft.clone();
            self.settings_notice.clear();
            let shortcut = self.preferences.push_to_talk_shortcut.clone();
            self.shortcut_input
                .update(cx, |input, cx| input.set_value(shortcut, window, cx));
        }
        self.sync_resource_interest(window.is_window_active());
        self.composer_height = composer_height(
            self.composer_height,
            f32::from(window.viewport_size().height),
        );
        let selected = match &self.detail_selection {
            Some(DetailSelection::User(id)) if self.workspace.connected() => self
                .workspace
                .users
                .iter()
                .find(|u| &u.id == id && !u.is_self),
            _ => None,
        };
        let target = selected.map(|user| UserPlaybackTarget {
            session: self.workspace.session.id.clone(),
            user: user.id.clone(),
            instance: user.instance.clone(),
        });
        let confirmed = selected
            .map(|user| user.playback_volume.min(794))
            .unwrap_or(100);
        if target != self.playback_input_target || confirmed != self.playback_input_confirmed {
            self.playback_input_target = target;
            self.playback_input_confirmed = confirmed;
            self.playback_input_error.clear();
            self.playback_input.update(cx, |input, cx| {
                input.set_value(playback_db(confirmed).to_string(), window, cx)
            });
        }
        let view = cx.entity().clone();
        let display_error = self
            .playback_error
            .as_ref()
            .map(|(target, error)| {
                let name = self
                    .workspace
                    .users
                    .iter()
                    .find(|u| {
                        target.session == self.workspace.session.id
                            && u.id == target.user
                            && u.instance == target.instance
                    })
                    .map(|u| u.nickname.clone())
                    .unwrap_or_else(|| format!("成员 {}", target.user));
                format!("{name} 的收听设置未应用：{error}")
            })
            .unwrap_or_else(|| self.error.clone());
        let content = div()
            .id("resona-root")
            .relative()
            .w_full()
            .flex_1()
            .min_h_0()
            .min_w_0()
            .overflow_hidden()
            .track_focus(&self.focus)
            .on_mouse_move(
                cx.listener(|this, event: &gpui::MouseMoveEvent, window, cx| {
                    if let Some((start_y, start_height)) = this.composer_drag {
                        if event.pressed_button != Some(gpui::MouseButton::Left)
                            || visible_text_channel(&this.workspace, &this.selected_server)
                                .is_none()
                        {
                            this.composer_drag = None;
                            return;
                        }
                        this.composer_height = composer_height(
                            start_height + start_y - f32::from(event.position.y),
                            f32::from(window.viewport_size().height),
                        );
                        cx.notify();
                    }
                }),
            )
            .on_mouse_up(
                gpui::MouseButton::Left,
                cx.listener(|this, _, _, _| {
                    this.composer_drag = None;
                    this.chat_follow_bottom = f32::from(this.chat_scroll.max_offset().height)
                        + f32::from(this.chat_scroll.offset().y)
                        <= 4.;
                }),
            )
            .on_mouse_up_out(
                gpui::MouseButton::Left,
                cx.listener(|this, _, _, _| this.composer_drag = None),
            )
            .on_action(cx.listener(Self::dismiss_modal))
            .on_action(cx.listener(|this, _: &Quit, _, cx| this.begin_shutdown(cx)))
            .capture_key_down(cx.listener(|this, event: &gpui::KeyDownEvent, _, cx| {
                if event.keystroke.key == "escape" && this.composer_drag.take().is_some() {
                    cx.stop_propagation();
                    return;
                }
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
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .flex()
                            .flex_col()
                            .child(
                                div()
                                    .flex_1()
                                    .min_h_0()
                                    .w_full()
                                    .flex()
                                    .child(self.render_sidebar(&view))
                                    .when(self.detail_selection.is_some(), |body| {
                                        body.child(self.render_details(&view))
                                    }),
                            )
                            .when_some(
                                visible_text_channel(&self.workspace, &self.selected_server),
                                |body, channel| body.child(self.render_chat(channel, &view)),
                            ),
                    ),
            )
            .child(self.render_voicebar(&view))
            .when(
                self.resources_syncing && self.workspace.connected(),
                |root| {
                    root.child(
                        div()
                            .absolute()
                            .top_0()
                            .left_0()
                            .right_0()
                            .bottom(px(72.))
                            .bg(rgba(0x08090bdd))
                            .flex()
                            .items_center()
                            .justify_center()
                            .on_mouse_down(gpui::MouseButton::Left, |_, _, cx| {
                                cx.stop_propagation()
                            })
                            .child("正在同步当前资源"),
                    )
                },
            )
            .when(!display_error.is_empty() && self.modal.is_none(), |root| {
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
                                .child(display_error.clone()),
                        )
                        .child(
                            Button::new("dismiss-error")
                                .icon(IconName::Close)
                                .ghost()
                                .tooltip("关闭")
                                .on_click(move |_, _, cx| {
                                    entity.update(cx, |this, cx| {
                                        this.error.clear();
                                        this.playback_error = None;
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
                        .bg(rgba(0x101214ed))
                        .flex()
                        .items_center()
                        .justify_center()
                        .child(div().text_sm().child("正在断开并退出")),
                )
            });
        div()
            .w(window.viewport_size().width)
            .h(window.viewport_size().height)
            .flex()
            .flex_col()
            .bg(rgb(PANEL))
            .when(cfg!(target_os = "macos"), |root| {
                root.child(gpui_component::TitleBar::new().bg(rgb(PANEL)).border_b_0())
            })
            .child(content)
    }
}

fn channel_bitrate(preset: u32, custom: &str) -> Option<u32> {
    if [20000, 32000, 48000].contains(&preset) {
        return Some(preset);
    }
    if preset != 0 {
        return None;
    }
    let value = custom.trim();
    if value.is_empty() || !value.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let kbps = value.parse::<u32>().ok()?;
    (16..=64).contains(&kbps).then(|| kbps * 1000)
}

fn playback_from_db(db: i16) -> u16 {
    (100.0 * 10_f64.powf(f64::from(db.clamp(-20, 18)) / 20.0)).round() as u16
}

fn playback_db(volume: u16) -> i16 {
    (20.0 * (f64::from(volume.max(1)) / 100.0).log10()).round() as i16
}

fn playback_label(volume: u16) -> String {
    if volume == 0 {
        return "静音".into();
    }
    format!("{:+} dB", playback_db(volume))
}

fn step_playback_volume(volume: u16, step: i16) -> u16 {
    playback_from_db(playback_db(volume).saturating_add(step))
}

fn parse_playback_volume(value: &str) -> Result<u16, &'static str> {
    let value = value.trim();
    let (negative, digits) = if let Some(rest) = value.strip_prefix('-') {
        (true, rest)
    } else {
        (false, value.strip_prefix('+').unwrap_or(value))
    };
    if digits.is_empty() || !digits.bytes().all(|b| b.is_ascii_digit()) {
        return Err("请输入整数增益（-20 到 +18 dB）");
    }
    let magnitude = digits
        .bytes()
        .fold(0i16, |n, b| (n * 10 + i16::from(b - b'0')).min(100));
    Ok(playback_from_db(if negative {
        -magnitude
    } else {
        magnitude
    }))
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

fn visible_text_channel<'a>(
    workspace: &'a Workspace,
    selected: &str,
) -> Option<&'a crate::model::Channel> {
    if !can_show_session_channels(workspace, selected) || workspace.session.channel_id.is_empty() {
        return None;
    }
    workspace
        .channels
        .iter()
        .find(|channel| channel.id == workspace.session.channel_id && channel.kind != "separator")
}

fn voice_params(voice: &VoiceState) -> Value {
    json!({
        "enabled": voice.enabled, "muted": voice.muted, "deafened": voice.deafened,
        "autoUnmuteOnConnect": voice.auto_unmute_on_connect,
        "inputDeviceID": voice.input_device_id, "outputDeviceID": voice.output_device_id,
        "volume": voice.volume, "inputGain": voice.input_gain, "activationMode": voice.activation_mode,
        "vadThresholdDB": voice.vad_threshold_db, "processing": voice.processing,
        "ducking": voice.ducking,
    })
}

fn voice_state_current(next: &VoiceState, current: &VoiceState) -> bool {
    (next.generation, next.operation) >= (current.generation, current.operation)
}

fn navigation_blocked(modal: Option<&Modal>, closing: bool) -> bool {
    modal.is_some() || closing
}

fn voice_operation_applied(voice: &VoiceState, generation: u64, operation: u64) -> bool {
    !voice.busy
        && voice.error.is_empty()
        && voice.generation == generation
        && voice.operation == operation
        && voice.applied_operation == operation
}

fn can_prepare_voice_preferences(mode: &str) -> bool {
    matches!(mode, "" | "offline" | "failed" | "preview")
}

fn ownership_response_current(
    workspace: &Workspace,
    modal: Option<&Modal>,
    closing: bool,
    current_revision: u64,
    session: &str,
    revision: u64,
) -> bool {
    !closing
        && workspace.connected()
        && workspace.session.id == session
        && current_revision == revision
        && matches!(modal, Some(Modal::Ownership { session: open }) if open == session)
}

fn channel_response_current(
    workspace: &Workspace,
    modal: Option<&Modal>,
    closing: bool,
    current_revision: u64,
    session: &str,
    revision: u64,
) -> bool {
    !closing
        && workspace.connected()
        && workspace.session.can_manage_channels
        && workspace.session.id == session
        && current_revision == revision
        && matches!(modal, Some(Modal::Channel { session: open, .. }) if open == session)
}

fn ownership_pending<'a>(pending: impl Iterator<Item = &'a Pending>, session_id: &str) -> bool {
    pending.into_iter().any(
        |pending| matches!(pending, Pending::ClaimOwner { session, .. } if session == session_id),
    )
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
            if old.instance != user.instance {
                return DetailChange::Clear;
            }
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
                || old.bitrate != channel.bitrate
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

fn composer_height(requested: f32, viewport_height: f32) -> f32 {
    requested.clamp(160., (viewport_height * 0.55).clamp(160., 480.))
}

fn apply_preferences(preferences: &Preferences, voice: &mut VoiceState) {
    voice.auto_unmute_on_connect = preferences.auto_unmute_on_connect;
    voice.volume = preferences.playback_volume.min(794);
    voice.input_gain = preferences.input_gain.min(200);
    voice.activation_mode = preferences.activation_mode.clone();
    voice.vad_threshold_db = preferences.vad_threshold_db;
    voice.processing = preferences.processing.clone();
    voice.ducking = preferences.ducking;
    voice.input_device_id = preferences.input_device_id.clone();
    voice.output_device_id = preferences.output_device_id.clone();
}

fn resource_needs_full_list(
    workspace: &Workspace,
    viewed_server: &str,
    selection: Option<&DetailSelection>,
) -> bool {
    if viewed_server == workspace.session.server_id {
        return true;
    }
    match selection {
        Some(DetailSelection::Channel(id)) => *id != workspace.session.channel_id,
        Some(DetailSelection::User(id)) => workspace
            .users
            .iter()
            .find(|u| &u.id == id)
            .is_none_or(|u| u.channel_id != workspace.session.channel_id),
        None => false,
    }
}

// Project incoming workspace notifications into the selected native detail cache.
fn native_detail_value(workspace: &Workspace, selection: &DetailSelection) -> Option<Value> {
    if !workspace.connected() {
        return None;
    }
    match selection {
        DetailSelection::Channel(id) => {
            workspace.channels.iter().find(|ch| &ch.id == id).map(|ch| {
                let bitrate = if ch.bitrate == 0 { 48000 } else { ch.bitrate };
                json!({
                    "id": ch.id, "name": ch.name, "description": ch.description,
                    "codec": format!("Opus mono / 48 kHz / 20 ms / {} kbps", bitrate / 1000),
                    "members": ch.members, "memberSyncState": if ch.id == workspace.session.channel_id && workspace.session.member_sync_state == "limited" { "ready" } else { &workspace.session.member_sync_state },
                    "default": ch.is_default
                })
            })
        }
        DetailSelection::User(id) => {
            workspace
                .users
                .iter()
                .find(|user| &user.id == id)
                .map(|user| {
                    json!({
                        "id": user.id, "nickname": user.nickname, "channelID": user.channel_id,
                        "inputMuted": user.voice_state_known.then_some(user.input_muted),
                        "outputMuted": user.voice_state_known.then_some(user.output_muted)
                    })
                })
        }
    }
}

fn detail_fields(selection: &DetailSelection) -> &'static [(&'static str, &'static str)] {
    match selection {
        DetailSelection::Channel(_) => &[
            ("description", "描述"),
            ("codec", "编码"),
            ("members", "当前人数"),
            ("default", "默认频道"),
        ],
        DetailSelection::User(_) => &[
            ("channelID", "所在频道"),
            ("inputMuted", "麦克风静音"),
            ("outputMuted", "输出静音"),
        ],
    }
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
        Some(value) => value.to_string(),
    }
}

fn settings_nav_contents(label: &'static str, icon: impl IconNamed) -> AnyElement {
    div()
        .w_full()
        .flex()
        .items_center()
        .gap_2()
        .child(
            div()
                .w(px(20.))
                .flex_shrink_0()
                .flex()
                .justify_center()
                .child(svg().path(icon.path()).size(px(16.)).text_color(rgb(MUTED))),
        )
        .child(div().text_sm().child(label))
        .into_any_element()
}

fn field(label: &'static str, input: impl IntoElement) -> AnyElement {
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
    let locally_muted = !user.is_self && user.playback_muted;
    let tooltip: SharedString = if locally_muted {
        "已在本机静音".into()
    } else if speaking {
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
                .path(if locally_muted {
                    "headphones-off.svg"
                } else if user.is_self {
                    "mic.svg"
                } else {
                    "volume-2.svg"
                })
                .text_color(rgb(if locally_muted {
                    AMBER
                } else if speaking {
                    MINT
                } else {
                    0x66717c
                }))
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
    #[test]
    fn viewed_server_and_details_determine_resource_scope() {
        let mut workspace = Workspace::default();
        workspace.session.server_id = "live".into();
        workspace.session.channel_id = "1".into();
        assert!(super::resource_needs_full_list(&workspace, "live", None));
        assert!(!super::resource_needs_full_list(&workspace, "other", None));
        assert!(!super::resource_needs_full_list(
            &workspace,
            "other",
            Some(&DetailSelection::Channel("1".into()))
        ));
        assert!(super::resource_needs_full_list(
            &workspace,
            "other",
            Some(&DetailSelection::Channel("2".into()))
        ));
    }
    #[test]
    fn composer_resize_preserves_space_for_chat_and_voicebar() {
        assert_eq!(super::composer_height(20., 600.), 160.);
        assert_eq!(super::composer_height(800., 600.), 330.);
        assert_eq!(super::composer_height(800., 1200.), 480.);
        assert_eq!(super::composer_height(210., 600.), 210.);
    }
    #[test]
    fn native_detail_cache_projects_membership_and_metadata_notifications() {
        let mut workspace = Workspace::default();
        workspace.session.mode = "connected".into();
        workspace.session.member_sync_state = "ready".into();
        workspace.channels.push(crate::model::Channel {
            id: "1".into(),
            members: 1,
            ..Default::default()
        });
        let selection = DetailSelection::Channel("1".into());
        let cached = super::native_detail_value(&workspace, &selection).unwrap();
        assert_eq!(cached["members"], 1);
        workspace.channels[0].members = 4;
        workspace.channels[0].description = "Updated".into();
        workspace.channels[0].bitrate = 32000;
        let updated = super::native_detail_value(&workspace, &selection).unwrap();
        assert_eq!(cached["members"], 1);
        assert_eq!(updated["members"], 4);
        assert_eq!(updated["description"], "Updated");
        assert_eq!(updated["codec"], "Opus mono / 48 kHz / 20 ms / 32 kbps");
        workspace.session.member_sync_state = "pending".into();
        assert_eq!(
            super::native_detail_value(&workspace, &selection).unwrap()["memberSyncState"],
            "pending"
        );
        workspace.session.mode = "offline".into();
        assert!(super::native_detail_value(&workspace, &selection).is_none());
    }

    #[test]
    fn native_user_detail_notifications_distinguish_unknown_and_unmuted() {
        let mut workspace = Workspace::default();
        workspace.session.mode = "connected".into();
        workspace.users.push(crate::model::User {
            id: "2".into(),
            ..Default::default()
        });
        let selection = DetailSelection::User("2".into());
        assert!(
            super::native_detail_value(&workspace, &selection).unwrap()["inputMuted"].is_null()
        );
        workspace.users[0].voice_state_known = true;
        assert_eq!(
            super::native_detail_value(&workspace, &selection).unwrap()["inputMuted"],
            false
        );
        workspace.users[0].input_muted = true;
        workspace.users[0].output_muted = true;
        let updated = super::native_detail_value(&workspace, &selection).unwrap();
        assert_eq!(updated["inputMuted"], true);
        assert_eq!(updated["outputMuted"], true);
        workspace.users.clear();
        assert!(super::native_detail_value(&workspace, &selection).is_none());
    }
    #[test]
    fn details_show_supported_fields() {
        let channel = DetailSelection::Channel("1".into());
        let fields = super::detail_fields(&channel);
        assert_eq!(
            fields.iter().map(|f| f.0).collect::<Vec<_>>(),
            vec!["description", "codec", "members", "default"]
        );
        let user = DetailSelection::User("2".into());
        assert_eq!(super::detail_fields(&user).len(), 3);
    }
    #[test]
    fn channel_audio_presets_and_custom_bounds() {
        for value in [20000, 32000, 48000] {
            assert_eq!(super::channel_bitrate(value, "invalid"), Some(value));
        }
        for (input, want) in [("16", 16000), (" 37 ", 37000), ("64", 64000)] {
            assert_eq!(super::channel_bitrate(0, input), Some(want));
        }
        for input in [
            "",
            "15",
            "65",
            "-1",
            "16.5",
            "+32",
            "abc",
            "999999999999999999999999",
        ] {
            assert_eq!(super::channel_bitrate(0, input), None);
        }
        assert_eq!(super::channel_bitrate(1, "32"), None);
    }
    #[test]
    fn channel_management_replies_require_current_modal_session_and_permission() {
        let mut workspace = crate::model::Workspace::default();
        workspace.session.id = "a".into();
        workspace.session.mode = "connected".into();
        workspace.session.can_manage_channels = true;
        let modal = super::Modal::Channel {
            session: "a".into(),
            id: "2".into(),
            delete: false,
        };
        let check = |w: &crate::model::Workspace, m, closing, revision| {
            super::channel_response_current(w, m, closing, revision, "a", 4)
        };
        assert!(check(&workspace, Some(&modal), false, 4));
        assert!(!check(&workspace, None, false, 4));
        assert!(!check(&workspace, Some(&modal), false, 5));
        assert!(!check(&workspace, Some(&modal), true, 4));
        workspace.session.can_manage_channels = false;
        assert!(!check(&workspace, Some(&modal), false, 4));
        workspace.session.can_manage_channels = true;
        workspace.session.id = "b".into();
        assert!(!check(&workspace, Some(&modal), false, 4));
        workspace.session.id = "a".into();
        workspace.session.mode = "failed".into();
        assert!(!check(&workspace, Some(&modal), false, 4));
    }
    use super::{
        DetailChange, DetailSelection, can_prepare_voice_preferences, can_show_session_channels,
        detail_selection_change, detail_text, ptt_can_send, remove_inserted_newline,
        user_is_speaking, visible_text_channel, voice_operation_applied, voice_params,
        voice_state_current,
    };
    use crate::model::{User, VoiceState, Workspace};

    #[test]
    fn ownership_replies_cannot_cross_session_or_modal_lifetimes() {
        let mut workspace = Workspace::default();
        workspace.session.mode = "connected".into();
        workspace.session.id = "session-a".into();
        let modal = super::Modal::Ownership {
            session: "session-a".into(),
        };
        let accepts = |workspace: &Workspace, modal: Option<&super::Modal>, closing, revision| {
            super::ownership_response_current(workspace, modal, closing, revision, "session-a", 7)
        };
        assert!(accepts(&workspace, Some(&modal), false, 7));
        assert!(!accepts(&workspace, None, false, 7));
        assert!(!accepts(&workspace, Some(&modal), false, 8));
        assert!(!accepts(&workspace, Some(&modal), true, 7));
        workspace.session.id = "session-b".into();
        assert!(!accepts(&workspace, Some(&modal), false, 7));
        workspace.session.id = "session-a".into();
        workspace.session.mode = "offline".into();
        assert!(!accepts(&workspace, Some(&modal), false, 7));
    }

    #[test]
    fn pending_claim_blocks_repeat_even_after_panel_reopens() {
        let pending = [super::Pending::ClaimOwner {
            session: "session-a".into(),
            revision: 1,
        }];
        assert!(super::ownership_pending(pending.iter(), "session-a"));
        assert!(!super::ownership_pending(pending.iter(), "session-b"));
        assert!(!super::ownership_pending([].iter(), "session-a"));
    }

    #[test]
    fn manual_playback_volume_validates_before_clamping() {
        for db in -20..=18 {
            assert_eq!(super::playback_db(super::playback_from_db(db)), db);
        }
        for (value, expected) in [
            ("6", 200),
            ("  +18 ", 794),
            ("200", 794),
            ("-20", 10),
            ("-1", 89),
            ("0", 100),
            ("999999999999999999999999999", 794),
            ("-999999999999999999999999", 10),
        ] {
            assert_eq!(super::parse_playback_volume(value), Ok(expected));
        }
        for value in ["", " ", "+", "-", "1.5", "1e2", "100%", "999999999999abc"] {
            assert!(
                super::parse_playback_volume(value).is_err(),
                "accepted {value}"
            );
        }
    }

    #[test]
    fn playback_menu_actions_use_current_values_and_preserve_mute() {
        use super::UserPlaybackAction;
        let user = User {
            playback_volume: 150,
            playback_muted: true,
            ..User::default()
        };
        assert_eq!(UserPlaybackAction::Step(1).apply(&user), (178, true));
        assert_eq!(UserPlaybackAction::Mute(false).apply(&user), (150, false));
        assert_eq!(UserPlaybackAction::Volume(100).apply(&user), (100, true));
        assert_eq!(UserPlaybackAction::Volume(0).apply(&user), (0, true));
        let max = User {
            playback_volume: 794,
            ..user.clone()
        };
        assert_eq!(UserPlaybackAction::Step(1).apply(&max), (794, true));
        let zero = User {
            playback_volume: 0,
            ..user
        };
        assert_eq!(UserPlaybackAction::Step(-1).apply(&zero), (10, true));
    }

    #[test]
    fn channel_text_requires_joined_channel_in_viewed_server() {
        let mut workspace = Workspace::default();
        workspace.session.server_id = "a".into();
        workspace.session.channel_id = "room".into();
        workspace.channels.push(crate::model::Channel {
            id: "room".into(),
            ..Default::default()
        });
        for mode in ["offline", "connecting", "failed", "reconnecting"] {
            workspace.session.mode = mode.into();
            assert!(visible_text_channel(&workspace, "a").is_none());
        }
        workspace.session.mode = "connected".into();
        assert!(visible_text_channel(&workspace, "a").is_some());
        assert!(visible_text_channel(&workspace, "b").is_none());
        workspace.session.switching_channel_id = "next".into();
        assert!(visible_text_channel(&workspace, "a").is_some());
        workspace.session.channel_id = "missing".into();
        assert!(visible_text_channel(&workspace, "a").is_none());
        workspace.session.channel_id.clear();
        assert!(visible_text_channel(&workspace, "a").is_none());
        workspace.session.channel_id = "room".into();
        workspace.channels[0].kind = "separator".into();
        assert!(visible_text_channel(&workspace, "a").is_none());
        workspace.channels[0].kind.clear();
        workspace.session.mode = "preview".into();
        assert!(visible_text_channel(&workspace, "__preview__").is_some());
    }

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
    fn replaced_member_closes_details_even_with_identical_name_and_id() {
        let (mut previous, _, _, mut user) = speaking_context();
        user.instance = "first".into();
        previous.users = vec![user.clone()];
        let mut next = previous.clone();
        next.users[0].instance = "replacement".into();
        assert_eq!(
            detail_selection_change(&DetailSelection::User(user.id), &previous, &next),
            DetailChange::Clear
        );
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
            processing: {
                let mut p = crate::model::ProcessingConfig::default();
                p.preprocess[0].backend = "speex".into();
                p.preprocess[0].params.residual = true;
                p.preprocess[1].backend = "speex".into();
                p.preprocess[1].params.level = 3;
                p.postprocess[0].backend = "speex".into();
                p
            },
            ducking: true,
            ..VoiceState::default()
        };
        let params = voice_params(&voice);
        assert_eq!(params["muted"], true);
        assert_eq!(params["enabled"], false);
        assert_eq!(params["vadThresholdDB"], -32);
        assert_eq!(params["activationMode"], "vad");
        assert_eq!(params["processing"]["preprocess"][1]["params"]["level"], 3);
        assert_eq!(
            params["processing"]["preprocess"][0]["params"]["residual"],
            true
        );
        assert_eq!(params["ducking"], true);
        assert_eq!(params["processing"]["postprocess"][0]["backend"], "speex");
        let echoed: VoiceState = serde_json::from_value(params).unwrap();
        assert_eq!(echoed.processing.postprocess[0].backend, "speex");
    }

    #[test]
    fn settings_wait_for_matching_applied_operation_and_ignore_old_voice_events() {
        let current = VoiceState {
            generation: 4,
            operation: 12,
            applied_operation: 11,
            busy: true,
            ..VoiceState::default()
        };
        let stale = VoiceState {
            generation: 4,
            operation: 11,
            applied_operation: 11,
            ..VoiceState::default()
        };
        assert!(!voice_state_current(&stale, &current));
        assert!(!voice_operation_applied(&current, 4, 12));
        let querying_early = VoiceState {
            busy: false,
            ..current.clone()
        };
        assert!(!voice_operation_applied(&querying_early, 4, 12));
        let confirmed = VoiceState {
            applied_operation: 12,
            ..querying_early
        };
        assert!(voice_operation_applied(&confirmed, 4, 12));
        assert!(!voice_operation_applied(&confirmed, 3, 12));
        assert!(!voice_operation_applied(&confirmed, 4, 11));
        assert!(!voice_state_current(&stale, &current));
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
            detail_text(Some(&serde_json::json!(false)), "default", &workspace),
            "否"
        );
        assert_eq!(
            detail_text(Some(&serde_json::json!(0)), "members", &workspace),
            "0"
        );
        assert_eq!(
            detail_text(Some(&serde_json::json!("Opus mono")), "codec", &workspace),
            "Opus mono"
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
            ..User::default()
        };
        let remote = User {
            id: "remote-1".into(),
            nickname: "Remote".into(),
            channel_id: "channel-1".into(),
            is_self: false,
            ..User::default()
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
