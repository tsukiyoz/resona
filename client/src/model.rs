use serde::{Deserialize, Deserializer, Serialize};
use serde_json::Value;

pub use crate::config::ServerProfile;

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Session {
    pub chat_state: String,
    pub chat_epoch: u32,
    pub chat_generation: u64,
    pub chat_before: u64,
    pub chat_has_more: bool,
    pub chat_history_limited: bool,
    pub protocol: String,
    pub id: String,
    #[serde(rename = "sendingMessageID")]
    pub sending_message_id: String,
    pub mode: String,
    pub connection_attempt: usize,
    pub connection_waiting: bool,
    pub connection_retry_slow: bool,
    #[serde(rename = "channelID")]
    pub channel_id: String,
    #[serde(rename = "switchingChannelID")]
    pub switching_channel_id: String,
    pub nickname: String,
    #[serde(rename = "serverID")]
    pub server_id: String,
    pub server_name: String,
    #[serde(rename = "identityUID")]
    pub identity_uid: String,
    pub server_role: String,
    pub account_uid: String,
    pub account_name: String,
    pub can_claim_owner: bool,
    pub can_manage_channels: bool,
    pub can_reorder_channels: bool,
    pub can_configure_channel_audio: bool,
    #[serde(rename = "selfID")]
    pub self_id: String,
    pub error: String,
    pub credential_error: String,
    pub member_sync_state: String,
    pub member_sync_error: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Channel {
    pub bitrate: u32,
    pub is_default: bool,
    pub id: String,
    pub name: String,
    pub description: String,
    pub members: usize,
    #[serde(rename = "parentID")]
    pub parent_id: String,
    pub order: String,
    pub password_required: bool,
    pub kind: String,
    pub align: String,
    pub repeat: bool,
    #[serde(rename = "iconID")]
    pub icon_id: String,
    pub icon_ref: String,
    #[serde(rename = "iconDataURL")]
    pub icon_data_url: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", default)]
pub struct User {
    pub account_uid: String,
    pub server_role: String,
    pub voice_state_known: bool,
    pub input_muted: bool,
    pub output_muted: bool,
    pub instance: String,
    #[serde(default = "default_playback_volume")]
    pub playback_volume: u16,
    pub playback_muted: bool,
    pub id: String,
    pub nickname: String,
    #[serde(rename = "channelID")]
    pub channel_id: String,
    #[serde(rename = "self")]
    pub is_self: bool,
}

fn default_playback_volume() -> u16 {
    100
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Message {
    pub sequence: u64,
    pub history: bool,
    pub id: String,
    #[serde(rename = "channelID")]
    pub channel_id: String,
    pub author: String,
    pub text: String,
    pub created_at: String,
    #[serde(rename = "authorID")]
    pub author_id: String,
    pub status: String,
    pub error: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Notification {
    pub id: String,
    pub kind: String,
    #[serde(rename = "channelID")]
    pub channel_id: String,
    pub created_at: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Workspace {
    pub revision: u64,
    pub servers: Vec<ServerProfile>,
    pub session: Session,
    pub channels: Vec<Channel>,
    pub users: Vec<User>,
    pub messages: Vec<Message>,
    pub notifications: Vec<Notification>,
}

#[derive(Clone, Debug, Default, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ProcessorParams {
    pub level: i32,
    pub tail_ms: i32,
    pub residual: bool,
    pub target: i32,
    pub max_gain_db: i32,
    pub headroom_db: i32,
}

#[derive(Clone, Debug, PartialEq, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct ProcessorSpec {
    pub name: String,
    pub backend: String,
    pub params: ProcessorParams,
}

impl ProcessorSpec {
    pub fn disabled(name: &str) -> Self {
        Self {
            name: name.into(),
            backend: "none".into(),
            params: ProcessorParams::default(),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct ProcessingConfig {
    pub preprocess: [ProcessorSpec; 2],
    pub postprocess: [ProcessorSpec; 1],
}

impl Default for ProcessingConfig {
    fn default() -> Self {
        Self {
            preprocess: [
                ProcessorSpec::disabled("aec"),
                ProcessorSpec::disabled("ans"),
            ],
            postprocess: [ProcessorSpec::disabled("agc")],
        }
    }
}

impl<'de> Deserialize<'de> for ProcessingConfig {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        #[derive(Deserialize, Serialize)]
        #[serde(untagged)]
        enum Wire {
            Current {
                preprocess: [ProcessorSpec; 2],
                postprocess: [ProcessorSpec; 1],
            },
            Previous {
                preprocess: [ProcessorSpec; 3],
                postprocess: [ProcessorSpec; 2],
            },
        }
        Ok(match Wire::deserialize(deserializer)? {
            Wire::Current {
                preprocess,
                postprocess,
            } => Self {
                preprocess,
                postprocess,
            },
            Wire::Previous {
                preprocess,
                postprocess,
            } => Self {
                preprocess: [preprocess[0].clone(), preprocess[1].clone()],
                postprocess: [postprocess[1].clone()],
            },
        })
    }
}

#[derive(Clone, Debug, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", default)]
pub struct VoiceState {
    pub operation: u64,
    pub applied_operation: u64,
    pub generation: u64,
    pub enabled: bool,
    pub muted: bool,
    pub auto_unmute_on_connect: bool,
    pub deafened: bool,
    #[serde(rename = "inputDeviceID")]
    pub input_device_id: String,
    #[serde(rename = "outputDeviceID")]
    pub output_device_id: String,
    pub volume: u16,
    pub input_gain: u16,
    pub active: bool,
    pub channel_codec: Value,
    pub error: String,
    pub busy: bool,
    #[serde(rename = "speakingClientIDs")]
    pub speaking_client_ids: Vec<String>,
    pub local_speaking: bool,
    pub activation_mode: String,
    #[serde(rename = "vadThresholdDB")]
    pub vad_threshold_db: i32,
    pub processing: ProcessingConfig,
    pub ducking: bool,
    pub push_to_talk_pressed: bool,
    #[serde(rename = "inputLevelDB")]
    pub input_level_db: i32,
}

impl Default for VoiceState {
    fn default() -> Self {
        Self {
            operation: 0,
            applied_operation: 0,
            generation: 0,
            enabled: false,
            muted: true,
            auto_unmute_on_connect: false,
            deafened: false,
            input_device_id: String::new(),
            output_device_id: String::new(),
            volume: 100,
            input_gain: 100,
            active: false,
            channel_codec: Value::Null,
            error: String::new(),
            busy: false,
            speaking_client_ids: Vec::new(),
            local_speaking: false,
            activation_mode: "continuous".into(),
            vad_threshold_db: -40,
            processing: ProcessingConfig::default(),
            ducking: false,
            push_to_talk_pressed: false,
            input_level_db: -60,
        }
    }
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", default)]
pub struct AudioDevice {
    pub id: String,
    pub name: String,
    pub kind: String,
    #[serde(rename = "default")]
    pub is_default: bool,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct CredentialStatus {
    pub saved: bool,
    pub remember: bool,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Capabilities {
    pub protocol_version: u32,
    pub platform: String,
    pub secure_password_storage: bool,
    pub voice: bool,
    pub audio_processors: Vec<ProcessorOption>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ProcessorOption {
    pub phase: String,
    pub name: String,
    pub id: String,
    pub display_name: String,
    pub description: String,
}

impl Workspace {
    pub fn connected(&self) -> bool {
        self.session.mode == "connected"
    }
}
