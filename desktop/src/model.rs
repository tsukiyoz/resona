pub use resona_client::model::*;

#[cfg(test)]
use serde::Deserialize;

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Deserialize)]
    struct Contract {
        workspace: Workspace,
        voice: VoiceState,
    }

    #[test]
    fn preserves_saved_desktop_model_fields() {
        let contract: Contract =
            serde_json::from_str(include_str!("../tests/data/model.json")).unwrap();
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
        assert_eq!(contract.voice.processing.postprocess[0].backend, "speex");
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

    #[test]
    fn processor_capability_and_agc2_parameters_round_trip() {
        let capabilities: Capabilities = serde_json::from_str(r#"{"audioProcessors":[{"phase":"postprocess","name":"agc","id":"webrtc","displayName":"WebRTC AGC2","description":"每成员自动增益"}]}"#).unwrap();
        assert_eq!(capabilities.audio_processors[0].id, "webrtc");
        let mut config = ProcessingConfig::default();
        config.postprocess[0].backend = "webrtc".into();
        config.postprocess[0].params.headroom_db = 5;
        config.postprocess[0].params.max_gain_db = 18;
        let wire = serde_json::to_value(&config).unwrap();
        assert_eq!(wire["postprocess"][0]["params"]["headroomDb"], 5);
        assert_eq!(
            serde_json::from_value::<ProcessingConfig>(wire).unwrap(),
            config
        );
    }

    #[test]
    fn obsolete_bookmarks_never_enter_password_flow() {
        for protocol in ["", "unsupported"] {
            let profile = super::ServerProfile {
                protocol: protocol.into(),
                ..Default::default()
            };
            assert!(!profile.supports_connection());
        }
        let profile = super::ServerProfile {
            protocol: "resona-noise".into(),
            ..Default::default()
        };
        assert!(profile.supports_connection());
    }
}
