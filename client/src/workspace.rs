//! Product projections and local delivery state. No UI framework or network I/O.
use crate::{config, lifecycle, model::*, session};
use chrono::{DateTime, SecondsFormat, Utc};
use std::collections::{HashMap, VecDeque};

pub struct Projection {
    pub workspace: Workspace,
    scope: (u64, u32),
    event: u64,
    playback: HashMap<String, (u16, bool)>,
    playback_order: VecDeque<String>,
    outbox: VecDeque<Message>,
}
pub fn timestamp(nanos: i64) -> String {
    DateTime::from_timestamp(
        nanos.div_euclid(1_000_000_000),
        nanos.rem_euclid(1_000_000_000) as u32,
    )
    .unwrap_or_default()
    .to_rfc3339_opts(SecondsFormat::Nanos, true)
}
pub fn now() -> String {
    Utc::now().to_rfc3339_opts(SecondsFormat::Nanos, true)
}
impl Projection {
    pub fn preview(&mut self, method: &str, id: &str, text: &str) -> Result<(), String> {
        match method {
            "OpenPreview" => {
                if self.workspace.session.mode == "preview" {
                    return Ok(());
                }
                self.workspace.session = Session {
                    mode: "preview".into(),
                    channel_id: "lobby".into(),
                    nickname: "Resona".into(),
                    ..Default::default()
                };
                self.workspace.channels =
                    [("lobby", "大厅"), ("music", "音乐"), ("workshop", "工作间")]
                        .into_iter()
                        .map(|(id, name)| Channel {
                            id: id.into(),
                            name: name.into(),
                            description: "本地预览".into(),
                            members: usize::from(id == "lobby"),
                            ..Default::default()
                        })
                        .collect();
                self.workspace.users = vec![User {
                    id: "preview-self".into(),
                    nickname: "Resona".into(),
                    channel_id: "lobby".into(),
                    is_self: true,
                    ..Default::default()
                }];
                self.workspace.messages.clear();
                self.workspace.notifications.clear();
            }
            "LeavePreview" => {
                self.workspace.session = Session {
                    mode: "offline".into(),
                    nickname: "Resona".into(),
                    ..Default::default()
                };
                self.workspace.channels.clear();
                self.workspace.users.clear();
                self.workspace.messages.clear();
            }
            "SelectChannel" if self.workspace.session.mode == "preview" => {
                if !self.workspace.channels.iter().any(|c| c.id == id) {
                    return Err("频道不存在".into());
                }
                if self.workspace.session.channel_id != id {
                    self.workspace.messages.clear();
                }
                self.workspace.session.channel_id = id.into();
                for channel in &mut self.workspace.channels {
                    channel.members = usize::from(channel.id == id);
                }
                for user in &mut self.workspace.users {
                    user.channel_id = id.into();
                }
            }
            "SendMessage" if self.workspace.session.mode == "preview" => {
                let text = text.trim();
                if text.is_empty() || text.chars().count() > 2000 {
                    return Err("消息需包含 1 至 2000 个字符".into());
                }
                self.workspace.messages.push(Message {
                    id: format!("preview-{}", self.workspace.revision),
                    channel_id: self.workspace.session.channel_id.clone(),
                    author: "Resona".into(),
                    author_id: "preview-self".into(),
                    text: text.into(),
                    created_at: now(),
                    status: "sent".into(),
                    ..Default::default()
                });
                while self.workspace.messages.len() > 500
                    || self
                        .workspace
                        .messages
                        .iter()
                        .map(|m| m.text.len())
                        .sum::<usize>()
                        > 512 * 1024
                {
                    self.workspace.messages.remove(0);
                }
            }
            _ => return Err("请先打开本地预览".into()),
        }
        Ok(())
    }
    pub fn new(servers: Vec<config::ServerProfile>) -> Self {
        Self {
            workspace: Workspace {
                servers,
                session: Session {
                    mode: "offline".into(),
                    nickname: "Resona".into(),
                    ..Default::default()
                },
                ..Default::default()
            },
            scope: (0, 0),
            event: 0,
            playback: HashMap::new(),
            playback_order: VecDeque::new(),
            outbox: VecDeque::new(),
        }
    }
    pub fn apply(&mut self, life: &lifecycle::State, snapshot: Option<&session::Snapshot>) {
        if self.workspace.session.mode == "preview"
            && matches!(
                life.mode,
                lifecycle::Mode::Offline | lifecycle::Mode::Failed
            )
        {
            return;
        }
        if life.generation < self.scope.0 {
            return;
        }
        let connected = life.mode == lifecycle::Mode::Connected;
        let scope = (
            life.generation,
            snapshot.filter(|_| connected).map_or(0, |s| s.state.epoch),
        );
        let recovering = life.mode == lifecycle::Mode::Reconnecting;
        if self.workspace.session.id != life.session_id {
            self.playback.clear();
            self.playback_order.clear();
        }
        if scope != self.scope && !recovering {
            self.outbox.clear();
            self.workspace.messages.clear();
            self.workspace.session.sending_message_id.clear();
            self.workspace.session.switching_channel_id.clear();
            if life.generation != self.scope.0 {
                self.workspace.notifications.clear();
                self.event = 0;
            }
            self.scope = scope;
        }
        let current = &mut self.workspace.session;
        current.mode = life.mode.as_str().into();
        current.id = life.session_id.clone();
        current.error = life.error.clone();
        current.connection_attempt = life.attempt as usize;
        current.connection_waiting = life.waiting;
        current.connection_retry_slow = life.slow;
        if let Some(profile) = &life.profile {
            current.protocol = profile.protocol.clone();
            current.server_id = profile.id.clone();
            current.server_name = profile.name.clone();
            current.nickname = profile.nickname.clone();
        } else {
            *current = Session {
                mode: life.mode.as_str().into(),
                id: life.session_id.clone(),
                nickname: "Resona".into(),
                ..Default::default()
            };
        }
        let Some(snapshot) = snapshot.filter(|_| connected) else {
            self.workspace.channels.clear();
            self.workspace.users.clear();
            if !recovering {
                self.workspace.messages.clear();
            }
            current.channel_id.clear();
            current.self_id.clear();
            current.server_role.clear();
            current.identity_uid.clear();
            current.can_claim_owner = false;
            current.can_manage_channels = false;
            current.can_reorder_channels = false;
            current.can_configure_channel_audio = false;
            current.chat_state.clear();
            current.chat_epoch = 0;
            return;
        };
        let state = &snapshot.state;
        let channel = snapshot.channel();
        current.channel_id = channel.to_string();
        current.self_id = state.self_.to_string();
        current.server_name = state.name.clone();
        current.identity_uid = state.identity_uid.clone();
        current.server_role = state.server_role.clone();
        current.can_claim_owner = state.can_claim_owner;
        current.can_manage_channels = state.can_manage_channels;
        current.can_reorder_channels = state.can_reorder_channels;
        current.can_configure_channel_audio = state.can_configure_channel_audio;
        current.member_sync_state = "ready".into();
        current.member_sync_error.clear();
        current.chat_epoch = snapshot.chat_status.epoch;
        current.chat_state = match snapshot.chat_status.status {
            1 => "ready",
            2 => "failed",
            _ => "loading",
        }
        .into();
        self.workspace.channels = state
            .channels
            .iter()
            .map(|c| Channel {
                id: c.id.to_string(),
                name: c.name.clone(),
                description: c.description.clone(),
                bitrate: c.bitrate,
                is_default: c.id == state.default_channel,
                members: state.members.iter().filter(|m| m.channel == c.id).count(),
                kind: "channel".into(),
                ..Default::default()
            })
            .collect();
        self.workspace.users = state
            .members
            .iter()
            .map(|m| {
                let (volume, muted) = self
                    .playback
                    .get(&m.instance)
                    .copied()
                    .unwrap_or((100, false));
                User {
                    id: m.id.to_string(),
                    nickname: m.nickname.clone(),
                    channel_id: m.channel.to_string(),
                    instance: m.instance.clone(),
                    is_self: m.id == state.self_,
                    voice_state_known: true,
                    input_muted: m.muted,
                    output_muted: m.deafened,
                    playback_volume: volume,
                    playback_muted: muted,
                }
            })
            .collect();
        self.workspace.messages = snapshot
            .messages
            .iter()
            .map(|m| {
                let id = if m.self_ && !m.client_id.is_empty() {
                    m.client_id.clone()
                } else {
                    format!("chat-{}", m.sequence)
                };
                Message {
                    id,
                    sequence: m.sequence,
                    history: m.history,
                    channel_id: m.channel.to_string(),
                    author: m.nickname.clone(),
                    author_id: m.sender.to_string(),
                    text: m.text.clone(),
                    created_at: timestamp(m.timestamp),
                    status: if m.self_ { "sent" } else { "received" }.into(),
                    error: String::new(),
                }
            })
            .collect();
        self.outbox.retain(|pending| {
            !snapshot
                .messages
                .iter()
                .any(|m| m.self_ && m.client_id == pending.id)
        });
        self.workspace.messages.extend(self.outbox.iter().cloned());
        let mut size: usize = self
            .workspace
            .messages
            .iter()
            .map(|m| m.text.len() + m.author.len() + m.error.len())
            .sum();
        while self.workspace.messages.len() > 500 || size > 512 * 1024 {
            let index = usize::from(
                self.workspace.messages[0].id == current.sending_message_id
                    && self.workspace.messages.len() > 1,
            );
            let removed = self.workspace.messages.remove(index);
            size -= removed.text.len() + removed.author.len() + removed.error.len();
            self.outbox.retain(|m| m.id != removed.id);
        }
        self.observe_events(life, snapshot);
    }
    pub fn observe_events(&mut self, life: &lifecycle::State, snapshot: &session::Snapshot) {
        let channel = snapshot.channel();
        for event in snapshot
            .events
            .iter()
            .filter(|event| event.serial > self.event)
        {
            if event.channel == channel && event.epoch == snapshot.state.epoch {
                if self.workspace.notifications.len() == 64 {
                    self.workspace.notifications.remove(0);
                }
                self.workspace.notifications.push(Notification {
                    id: format!("{}-{}", life.generation, event.serial),
                    kind: event.kind.into(),
                    channel_id: channel.to_string(),
                    created_at: event.timestamp.clone(),
                });
            }
        }
        self.event = snapshot.events.back().map_or(self.event, |e| e.serial);
    }
    pub fn enqueue(
        &mut self,
        session: &str,
        channel: &str,
        text: String,
    ) -> Result<Message, String> {
        let current = &mut self.workspace.session;
        if current.mode != "connected" || current.id != session || current.channel_id != channel {
            return Err("当前频道已改变，消息未发送".into());
        }
        if !current.switching_channel_id.is_empty() || !current.sending_message_id.is_empty() {
            return Err("上一条操作仍在进行，请稍后重试".into());
        }
        if current.chat_state != "ready" {
            return Err("频道消息尚未就绪，请等待或重试加载".into());
        }
        if text.trim().is_empty() || text.contains('\0') || text.len() > 8192 {
            return Err("消息为空或超过 8192 字节".into());
        }
        let message = Message {
            id: config::random_id().map_err(str::to_owned)?,
            channel_id: channel.into(),
            author: current.nickname.clone(),
            author_id: current.self_id.clone(),
            text,
            created_at: now(),
            status: "sending".into(),
            ..Default::default()
        };
        current.sending_message_id = message.id.clone();
        let mut bytes: usize = self
            .outbox
            .iter()
            .map(|m| m.text.len() + m.author.len() + m.error.len())
            .sum();
        while self.outbox.len() >= 500
            || bytes + message.text.len() + message.author.len() > 512 * 1024
        {
            if let Some(old) = self.outbox.pop_front() {
                bytes -= old.text.len() + old.author.len() + old.error.len();
                self.workspace.messages.retain(|m| m.id != old.id);
            } else {
                break;
            }
        }
        self.outbox.push_back(message.clone());
        self.workspace.messages.push(message.clone());
        Ok(message)
    }
    pub fn retry(&mut self, id: &str, allow_duplicate: bool) -> Result<Message, String> {
        let current = &mut self.workspace.session;
        if current.mode != "connected"
            || !current.sending_message_id.is_empty()
            || !current.switching_channel_id.is_empty()
            || current.chat_state != "ready"
        {
            return Err("当前暂不能重试消息".into());
        }
        let message = self
            .outbox
            .iter_mut()
            .find(|m| m.id == id && m.channel_id == current.channel_id)
            .ok_or("消息不存在")?;
        if !matches!(message.status.as_str(), "failed" | "unconfirmed") {
            return Err("此消息不能重试".into());
        }
        if message.status == "unconfirmed" && !allow_duplicate {
            return Err("上一条消息可能已送达，请确认是否重新发送".into());
        }
        message.status = "sending".into();
        message.error.clear();
        current.sending_message_id = id.into();
        Ok(message.clone())
    }
    pub fn delivered(&mut self, scope: (u64, u32), id: &str, result: session::Result<()>) {
        if scope != self.scope {
            return;
        }
        if self.workspace.session.sending_message_id == id {
            self.workspace.session.sending_message_id.clear();
        }
        if let Some(message) = self.outbox.iter_mut().find(|m| m.id == id) {
            let status = match &result {
                Ok(()) => "sent",
                Err(
                    session::Error::Rejected(1..=8)
                    | session::Error::InvalidInput
                    | session::Error::Busy,
                ) => "failed",
                Err(_) => "unconfirmed",
            };
            message.status = status.into();
            message.error = result
                .err()
                .map_or_else(String::new, |error| error.message().into());
        }
    }
    pub fn scope(&self) -> (u64, u32) {
        self.scope
    }
    pub fn playback(
        &mut self,
        session: &str,
        user: &str,
        instance: &str,
        volume: i32,
        muted: bool,
    ) -> Result<(), String> {
        if self.workspace.session.mode != "connected" || self.workspace.session.id != session {
            return Err("服务器会话已更改或未连接".into());
        }
        if !self.workspace.session.switching_channel_id.is_empty() {
            return Err("频道切换中".into());
        }
        let channel = self.workspace.session.channel_id.clone();
        let user = self
            .workspace
            .users
            .iter_mut()
            .find(|u| {
                u.id == user && u.instance == instance && !u.is_self && u.channel_id == channel
            })
            .ok_or("成员已离开或不能调整自己")?;
        user.playback_volume = volume.clamp(0, 794) as u16;
        user.playback_muted = muted;
        self.playback_order.retain(|key| key != instance);
        self.playback_order.push_back(instance.into());
        self.playback
            .insert(instance.into(), (user.playback_volume, muted));
        while self.playback_order.len() > 128 {
            if let Some(old) = self.playback_order.pop_front() {
                self.playback.remove(&old);
            }
        }
        Ok(())
    }
    pub fn playback_values(&self, instance: &str) -> (u16, bool) {
        self.playback.get(instance).copied().unwrap_or((100, false))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::wire::pb;
    use std::sync::Arc;
    fn fixture() -> (Projection, lifecycle::State, session::Snapshot) {
        let life = lifecycle::State {
            generation: 1,
            session_id: "session".into(),
            mode: lifecycle::Mode::Connected,
            ..Default::default()
        };
        let snapshot = session::Snapshot {
            state: Arc::new(pb::State {
                self_: 1,
                epoch: 2,
                default_channel: 1,
                channels: vec![pb::Channel {
                    id: 1,
                    name: "channel".into(),
                    ..Default::default()
                }],
                members: vec![
                    pb::Member {
                        id: 1,
                        channel: 1,
                        instance: "self".into(),
                        ..Default::default()
                    },
                    pb::Member {
                        id: 2,
                        channel: 1,
                        instance: "peer".into(),
                        ..Default::default()
                    },
                ],
                ..Default::default()
            }),
            messages: Arc::new(VecDeque::new()),
            chat_status: pb::ChatStatus {
                channel: 1,
                epoch: 2,
                status: 1,
                ..Default::default()
            },
            closed: None,
            events: Arc::new(VecDeque::new()),
        };
        let mut p = Projection::new(Vec::new());
        p.apply(&life, Some(&snapshot));
        (p, life, snapshot)
    }
    #[test]
    fn unknown_delivery_requires_confirmation_and_echo_deduplicates() {
        let (mut p, life, mut s) = fixture();
        let message = p.enqueue("session", "1", "room link".into()).unwrap();
        let scope = p.scope();
        p.delivered(scope, &message.id, Err(session::Error::Timeout));
        assert!(p.retry(&message.id, false).is_err());
        p.retry(&message.id, true).unwrap();
        Arc::make_mut(&mut s.messages).push_back(pb::Message {
            channel: 1,
            sender: 1,
            sequence: 10,
            client_id: message.id.clone(),
            self_: true,
            text: message.text,
            timestamp: 1,
            epoch: 2,
            ..Default::default()
        });
        p.apply(&life, Some(&s));
        assert_eq!(p.workspace.messages.len(), 1);
        assert_eq!(p.workspace.messages[0].id, message.id);
        assert_eq!(p.workspace.messages[0].status, "sent");
        p.delivered(scope, &message.id, Err(session::Error::Timeout));
        p.apply(&life, Some(&s));
        assert_eq!(p.workspace.messages[0].status, "sent");
    }
    #[test]
    fn channel_replacement_and_disconnect_clear_scoped_data() {
        let (mut p, mut life, mut s) = fixture();
        p.enqueue("session", "1", "temporary".into()).unwrap();
        let old = p.scope();
        Arc::make_mut(&mut s.state).epoch += 1;
        p.apply(&life, Some(&s));
        assert!(p.workspace.messages.is_empty());
        p.delivered(old, "old", Ok(()));
        assert!(p.workspace.messages.is_empty());
        life.mode = lifecycle::Mode::Offline;
        life.profile = None;
        p.apply(&life, None);
        assert!(p.workspace.channels.is_empty());
        assert!(p.workspace.users.is_empty());
        assert!(p.workspace.session.channel_id.is_empty());
    }
    #[test]
    fn playback_targets_current_instance_excludes_self_and_clamps() {
        let (mut p, _, _) = fixture();
        assert!(p.playback("session", "1", "self", 100, false).is_err());
        assert!(p.playback("session", "2", "old", 100, false).is_err());
        p.playback("session", "2", "peer", 1000, true).unwrap();
        assert_eq!(p.playback_values("peer"), (794, true));
    }
    #[test]
    fn recovery_keeps_unknown_text_and_new_session_resets_playback() {
        let (mut p, mut life, s) = fixture();
        let message = p.enqueue("session", "1", "unconfirmed".into()).unwrap();
        p.delivered(p.scope(), &message.id, Err(session::Error::Timeout));
        p.apply(&life, Some(&s));
        p.playback("session", "2", "peer", 200, true).unwrap();
        life.generation += 1;
        life.mode = lifecycle::Mode::Reconnecting;
        p.apply(&life, None);
        assert_eq!(p.workspace.messages[0].status, "unconfirmed");
        life.mode = lifecycle::Mode::Connected;
        life.session_id = "another".into();
        p.apply(&life, Some(&s));
        assert!(p.workspace.messages.is_empty());
        assert_eq!(p.playback_values("peer"), (100, false));
    }
    #[test]
    fn notifications_preserve_event_time_and_ignore_old_channel_epoch() {
        let (mut p, life, mut s) = fixture();
        Arc::make_mut(&mut s.events).extend([
            session::MemberEvent {
                serial: 1,
                kind: "member_left",
                channel: 1,
                epoch: 1,
                timestamp: "old".into(),
            },
            session::MemberEvent {
                serial: 2,
                kind: "member_joined",
                channel: 1,
                epoch: 2,
                timestamp: "original".into(),
            },
        ]);
        p.apply(&life, Some(&s));
        assert_eq!(p.workspace.notifications.len(), 1);
        assert_eq!(p.workspace.notifications[0].created_at, "original");
        p.apply(&life, Some(&s));
        assert_eq!(p.workspace.notifications.len(), 1);
    }
}
