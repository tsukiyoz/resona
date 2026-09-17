use crate::model::ProcessingConfig;
use serde::{Deserialize, Serialize};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::Mutex,
};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum ThemePreference {
    Dark,
    Light,
    #[default]
    #[serde(other)]
    System,
}

#[derive(Clone, Debug, PartialEq, Deserialize, Serialize)]
#[serde(default)]
pub struct Preferences {
    pub theme: ThemePreference,
    pub server_sidebar_collapsed: bool,
    pub notifications_enabled: bool,
    pub notification_volume: u8,
    pub activation_mode: String,
    pub vad_threshold_db: i32,
    pub input_gain: u16,
    pub playback_volume: u16,
    pub processing: ProcessingConfig,
    #[serde(skip_serializing)]
    pub noise_suppression: String,
    #[serde(skip_serializing)]
    pub playback_agc: String,
    #[serde(skip_serializing)]
    pub echo_cancellation: bool,
    #[serde(skip_serializing)]
    pub echo_suppression: bool,
    pub ducking: bool,
    pub input_device_id: String,
    pub output_device_id: String,
    pub global_push_to_talk: bool,
    pub push_to_talk_shortcut: String,
}

impl Default for Preferences {
    fn default() -> Self {
        Self {
            theme: ThemePreference::System,
            server_sidebar_collapsed: false,
            notifications_enabled: true,
            notification_volume: 35,
            activation_mode: "continuous".into(),
            vad_threshold_db: -40,
            input_gain: 100,
            playback_volume: 200,
            processing: ProcessingConfig::default(),
            noise_suppression: "off".into(),
            playback_agc: "off".into(),
            echo_cancellation: false,
            echo_suppression: false,
            ducking: false,
            input_device_id: String::new(),
            output_device_id: String::new(),
            global_push_to_talk: true,
            push_to_talk_shortcut: "F8".into(),
        }
    }
}

impl Preferences {
    fn normalize_audio(mut self, migrate_legacy: bool) -> Self {
        if migrate_legacy {
            if self.echo_cancellation {
                let spec = &mut self.processing.preprocess[0];
                spec.backend = "speex".into();
                spec.params.residual = self.echo_suppression;
            }
            let level = match self.noise_suppression.as_str() {
                "low" => 1,
                "medium" => 2,
                "high" => 3,
                _ => 0,
            };
            if level > 0 {
                let spec = &mut self.processing.preprocess[1];
                spec.backend = "speex".into();
                spec.params.level = level;
            }
            if self.playback_agc == "speex" {
                self.processing.postprocess[0].backend = "speex".into();
            }
        }
        self
    }

    pub fn merge_settings(&mut self, baseline: &Self, draft: &Self) {
        macro_rules! merge {
            ($($field:ident),*) => { $(
                if draft.$field != baseline.$field {
                    self.$field = draft.$field.clone();
                }
            )* };
        }
        merge!(
            theme,
            notifications_enabled,
            notification_volume,
            activation_mode,
            vad_threshold_db,
            input_gain,
            playback_volume,
            processing,
            ducking,
            input_device_id,
            output_device_id,
            global_push_to_talk,
            push_to_talk_shortcut
        );
    }

    pub fn save_ordered(&self, revision: u64, saved: &Mutex<u64>) -> std::io::Result<()> {
        let Some(path) = path() else {
            return Ok(());
        };
        self.save_ordered_at(revision, saved, &path)
    }

    fn save_ordered_at(
        &self,
        revision: u64,
        saved: &Mutex<u64>,
        path: &Path,
    ) -> std::io::Result<()> {
        let mut saved = saved
            .lock()
            .map_err(|_| std::io::Error::other("preferences save lock poisoned"))?;
        if revision <= *saved {
            return Ok(());
        }
        self.save_at(path)?;
        *saved = revision;
        Ok(())
    }

    pub fn load() -> Self {
        let Some(path) = path() else {
            return Self::default();
        };
        Self::load_at(&path)
    }

    fn load_at(path: &Path) -> Self {
        fs::read(path)
            .ok()
            .and_then(|bytes| serde_json::from_slice::<serde_json::Value>(&bytes).ok())
            .and_then(|value| {
                let migrate_legacy = value.get("processing").is_none();
                serde_json::from_value::<Self>(value)
                    .ok()
                    .map(|preferences| preferences.normalize_audio(migrate_legacy))
            })
            .unwrap_or_default()
    }

    fn save_at(&self, path: &Path) -> std::io::Result<()> {
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent)?;
        }
        let temp = path.with_extension("tmp");
        fs::write(&temp, serde_json::to_vec_pretty(self)?)?;
        fs::rename(temp, path)
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn retired_channel_collapse_preference_is_ignored() {
        let preferences: super::Preferences = serde_json::from_str(
            r#"{"channel_sidebar_collapsed":true,"server_sidebar_collapsed":true,"playback_volume":300}"#,
        )
        .unwrap();
        assert!(preferences.server_sidebar_collapsed);
        assert_eq!(preferences.playback_volume, 300);
        assert!(
            serde_json::to_value(preferences)
                .unwrap()
                .get("channel_sidebar_collapsed")
                .is_none()
        );
    }

    #[test]
    fn theme_defaults_to_system_and_merges_without_overwriting_quick_controls() {
        use super::ThemePreference;
        let old: super::Preferences = serde_json::from_str(r#"{"playback_volume":200}"#).unwrap();
        assert_eq!(old.theme, ThemePreference::System);
        let unknown: super::Preferences = serde_json::from_str(r#"{"theme":"old_theme"}"#).unwrap();
        assert_eq!(unknown.theme, ThemePreference::System);
        let mut draft = old.clone();
        draft.theme = ThemePreference::Light;
        let mut current = old.clone();
        current.playback_volume = 250;
        current.merge_settings(&old, &draft);
        assert_eq!(current.theme, ThemePreference::Light);
        assert_eq!(current.playback_volume, 250);
        let restored: super::Preferences =
            serde_json::from_slice(&serde_json::to_vec(&current).unwrap()).unwrap();
        assert_eq!(restored.theme, ThemePreference::Light);
    }

    #[test]
    fn settings_merge_preserves_concurrent_quick_controls_and_navigation() {
        let baseline = super::Preferences::default();
        let mut draft = baseline.clone();
        draft.processing.preprocess[1].backend = "speex".into();
        draft.processing.preprocess[1].params.level = 3;
        draft.processing.postprocess[0].backend = "speex".into();
        draft.notification_volume = 20;
        let mut current = baseline.clone();
        current.playback_volume = 250;
        current.server_sidebar_collapsed = true;
        current.merge_settings(&baseline, &draft);
        assert_eq!(current.playback_volume, 250);
        assert!(current.server_sidebar_collapsed);
        assert_eq!(current.processing.preprocess[1].params.level, 3);
        assert_eq!(current.processing.postprocess[0].backend, "speex");
        assert_eq!(current.notification_volume, 20);
        let mut cancelled = baseline.clone();
        cancelled.merge_settings(&baseline, &baseline);
        assert_eq!(cancelled, baseline);
    }

    use super::*;

    #[test]
    fn input_gain_defaults_and_zero_round_trip() {
        let old: Preferences = serde_json::from_str("{}").unwrap();
        assert_eq!(old.input_gain, 100);
        assert_eq!(old.playback_volume, 200);
        assert_eq!(old.processing.postprocess[0].backend, "none");
        let zero = Preferences {
            input_gain: 0,
            playback_volume: 10,
            ..old
        };
        let restored: Preferences =
            serde_json::from_slice(&serde_json::to_vec(&zero).unwrap()).unwrap();
        assert_eq!(restored.input_gain, 0);
        assert_eq!(restored.playback_volume, 10);
    }

    #[test]
    fn old_residual_only_preference_does_not_enable_aec() {
        let loaded: Preferences =
            serde_json::from_str(r#"{"echo_cancellation":false,"echo_suppression":true}"#).unwrap();
        let normalized = loaded.normalize_audio(true);
        assert_eq!(normalized.processing.preprocess[0].backend, "none");
        assert!(!normalized.processing.preprocess[0].params.residual);
    }

    #[test]
    fn old_audio_preference_migrates_once_without_overriding_new_chain() {
        let old: Preferences = serde_json::from_str(
            r#"{"noise_suppression":"high","echo_cancellation":true,"echo_suppression":true,"playback_agc":"speex"}"#,
        ).unwrap();
        let migrated = old.normalize_audio(true);
        assert_eq!(migrated.processing.preprocess[0].backend, "speex");
        assert!(migrated.processing.preprocess[0].params.residual);
        assert_eq!(migrated.processing.preprocess[1].params.level, 3);
        assert_eq!(migrated.processing.postprocess[0].backend, "speex");
        let encoded = serde_json::to_value(&migrated).unwrap();
        assert!(encoded.get("noise_suppression").is_none());
        assert!(encoded.get("processing").is_some());
    }

    #[test]
    fn previous_processing_shape_keeps_preferences_without_reenabling_old_fields() {
        let directory =
            std::env::temp_dir().join(format!("resona-3a-migration-{}", std::process::id()));
        fs::create_dir_all(&directory).unwrap();
        let path = directory.join("preferences.json");
        fs::write(
            &path,
            r#"{
                "input_gain":145,"notification_volume":17,
                "echo_cancellation":true,"noise_suppression":"high",
                "processing":{
                    "preprocess":[
                        {"name":"aec","backend":"none","params":{}},
                        {"name":"ans","backend":"speex","params":{"level":2}},
                        {"name":"agc","backend":"speex","params":{}}
                    ],
                    "postprocess":[
                        {"name":"ans","backend":"speex","params":{}},
                        {"name":"agc","backend":"speex","params":{"maxGainDb":18}}
                    ]
                }
            }"#,
        )
        .unwrap();
        let migrated = Preferences::load_at(&path);
        assert_eq!(migrated.input_gain, 145);
        assert_eq!(migrated.notification_volume, 17);
        assert_eq!(migrated.processing.preprocess[0].backend, "none");
        assert_eq!(migrated.processing.preprocess[1].params.level, 2);
        assert_eq!(migrated.processing.postprocess[0].backend, "speex");
        assert_eq!(migrated.processing.postprocess[0].params.max_gain_db, 18);
        let saved = serde_json::to_value(migrated).unwrap();
        assert_eq!(
            saved["processing"]["preprocess"].as_array().unwrap().len(),
            2
        );
        assert_eq!(
            saved["processing"]["postprocess"].as_array().unwrap().len(),
            1
        );
        fs::remove_file(&path).unwrap();
        fs::remove_dir(&directory).unwrap();
    }

    #[test]
    fn echo_dependency_changes_only_the_draft_until_applied() {
        let mut applied = Preferences::default();
        applied.processing.preprocess[0].backend = "speex".into();
        applied.processing.preprocess[0].params.residual = true;
        applied.processing.preprocess[1].backend = "speex".into();
        applied.processing.postprocess[0].backend = "speex".into();
        let mut draft = applied.clone();
        draft.processing.preprocess[0].backend = "none".into();
        draft.processing.preprocess[0].params = Default::default();
        assert!(!draft.processing.preprocess[0].params.residual);
        assert_eq!(
            draft.processing.preprocess[1],
            applied.processing.preprocess[1]
        );
        assert_eq!(
            draft.processing.postprocess[0],
            applied.processing.postprocess[0]
        );
        assert!(applied.processing.preprocess[0].params.residual);
        draft = applied.clone(); // cancel
        assert!(draft.processing.preprocess[0].params.residual);
        draft.processing.preprocess[0].params = Default::default();
        assert!(!draft.processing.preprocess[0].params.residual);
    }

    #[test]
    fn delayed_save_cannot_overwrite_final_preferences_and_failure_is_retryable() {
        let directory = std::env::temp_dir().join(format!(
            "resona-preferences-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        fs::create_dir(&directory).unwrap();
        let path = directory.join("preferences.json");
        let saved = Mutex::new(0);
        let old = Preferences::default();
        let latest = Preferences {
            input_gain: 150,
            vad_threshold_db: -22,
            processing: {
                let mut p = ProcessingConfig::default();
                p.preprocess[0].backend = "speex".into();
                p
            },
            ..old.clone()
        };
        latest.save_ordered_at(2, &saved, &path).unwrap();
        old.save_ordered_at(1, &saved, &path).unwrap();
        let loaded: Preferences = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
        assert_eq!(loaded.vad_threshold_db, -22);
        assert_eq!(loaded.input_gain, 150);
        assert_eq!(loaded.processing.preprocess[0].backend, "speex");
        assert!(
            old.save_ordered_at(3, &saved, &path.join("invalid.json"))
                .is_err()
        );
        assert_eq!(*saved.lock().unwrap(), 2);
        old.save_ordered_at(3, &saved, &path).unwrap();
        assert_eq!(*saved.lock().unwrap(), 3);
        fs::remove_dir_all(directory).unwrap();
    }
}

fn path() -> Option<PathBuf> {
    dirs::config_dir().map(|directory| directory.join("resona").join("native-preferences.json"))
}
