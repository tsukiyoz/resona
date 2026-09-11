use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ServerProfile {
    pub id: String,
    pub name: String,
    pub address: String,
    pub nickname: String,
    pub skip_password_storage: bool,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Session {
    pub id: String,
    #[serde(rename = "sendingMessageID")]
    pub sending_message_id: String,
    pub mode: String,
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
    #[serde(rename = "selfID")]
    pub self_id: String,
    pub error: String,
    pub credential_error: String,
    pub member_sync_state: String,
    pub member_sync_error: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Channel {
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

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct User {
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

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Message {
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

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Notification {
    pub id: String,
    pub kind: String,
    #[serde(rename = "channelID")]
    pub channel_id: String,
    pub created_at: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Workspace {
    pub servers: Vec<ServerProfile>,
    pub session: Session,
    pub channels: Vec<Channel>,
    pub users: Vec<User>,
    pub messages: Vec<Message>,
    pub notifications: Vec<Notification>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct VoiceState {
    pub enabled: bool,
    pub muted: bool,
    pub deafened: bool,
    #[serde(rename = "inputDeviceID")]
    pub input_device_id: String,
    #[serde(rename = "outputDeviceID")]
    pub output_device_id: String,
    pub volume: u8,
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
    pub noise_suppression: String,
    pub echo_cancellation: bool,
    pub echo_suppression: bool,
    pub ducking: bool,
    pub push_to_talk_pressed: bool,
    #[serde(rename = "inputLevelDB")]
    pub input_level_db: i32,
}

impl Default for VoiceState {
    fn default() -> Self {
        Self {
            enabled: false,
            muted: true,
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
            noise_suppression: "off".into(),
            echo_cancellation: false,
            echo_suppression: false,
            ducking: false,
            push_to_talk_pressed: false,
            input_level_db: -60,
        }
    }
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct AudioDevice {
    pub id: String,
    pub name: String,
    pub kind: String,
    #[serde(rename = "default")]
    pub is_default: bool,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct CredentialStatus {
    pub saved: bool,
    pub remember: bool,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Capabilities {
    pub protocol_version: u32,
    pub platform: String,
    pub secure_password_storage: bool,
    pub voice: bool,
}

impl Default for Capabilities {
    fn default() -> Self {
        Self {
            protocol_version: 0,
            platform: String::new(),
            secure_password_storage: false,
            voice: false,
        }
    }
}

impl Workspace {
    pub fn connected(&self) -> bool {
        self.session.mode == "connected"
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Deserialize)]
    struct Contract {
        workspace: Workspace,
        voice: VoiceState,
    }

    #[test]
    fn parses_shared_go_desktop_contract() {
        let contract: Contract = serde_json::from_str(include_str!(
            "../../internal/desktopipc/testdata/contract.json"
        ))
        .unwrap();
        let workspace = contract.workspace;
        assert_eq!(workspace.session.sending_message_id, "message-1");
        assert_eq!(workspace.session.channel_id, "channel-1");
        assert_eq!(workspace.session.switching_channel_id, "channel-2");
        assert_eq!(workspace.session.server_id, "server-1");
        assert_eq!(workspace.session.identity_uid, "identity-1");
        assert_eq!(workspace.session.self_id, "user-1");
        assert_eq!(workspace.channels[0].parent_id, "parent-1");
        assert_eq!(workspace.channels[0].icon_id, "1001");
        assert!(!workspace.channels[0].icon_ref.is_empty());
        assert!(workspace.channels[0].icon_data_url.is_empty());
        assert_eq!(workspace.users[0].channel_id, "channel-1");
        assert_eq!(workspace.users[0].instance, "member-1");
        assert_eq!(workspace.users[0].playback_volume, 130);
        assert!(workspace.users[0].playback_muted);
        assert_eq!(workspace.messages[0].author_id, "user-1");
        assert_eq!(workspace.messages[0].channel_id, "channel-1");
        assert_eq!(workspace.notifications[0].channel_id, "channel-1");
        assert_eq!(contract.voice.input_device_id, "input-1");
        assert_eq!(contract.voice.input_gain, 150);
        assert_eq!(contract.voice.output_device_id, "output-1");
        assert_eq!(contract.voice.channel_codec, serde_json::json!(4));
        assert_eq!(contract.voice.speaking_client_ids, ["42"]);
        assert!(!contract.voice.local_speaking);
    }

    #[test]
    fn speaking_fields_default_when_omitted() {
        let voice: VoiceState = serde_json::from_str("{}").unwrap();
        assert!(voice.speaking_client_ids.is_empty());
        assert!(!voice.local_speaking);
    }
}
