use crate::model::ProcessingConfig;
use serde::{Deserialize, Serialize};
use std::{
    fs::{self, OpenOptions},
    io::{self, Read, Write},
    path::{Path, PathBuf},
    sync::Mutex,
};

pub const SCHEMA_VERSION: u32 = 1;
const MAX_BYTES: u64 = 1024 * 1024;

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
    pub schema_version: u32,
    #[serde(skip)]
    read_only: bool,
    pub theme: ThemePreference,
    pub server_sidebar_collapsed: bool,
    pub notifications_enabled: bool,
    pub notification_volume: u8,
    pub activation_mode: String,
    pub auto_unmute_on_connect: bool,
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
            schema_version: SCHEMA_VERSION,
            read_only: false,
            theme: ThemePreference::System,
            server_sidebar_collapsed: false,
            notifications_enabled: true,
            notification_volume: 35,
            activation_mode: "continuous".into(),
            auto_unmute_on_connect: true,
            vad_threshold_db: -40,
            input_gain: 100,
            playback_volume: 200,
            processing: {
                let mut config = ProcessingConfig::default();
                config.preprocess[0].backend = "speex".into();
                config.preprocess[1].backend = "speex".into();
                config.postprocess[0].backend = "webrtc".into();
                config
            },
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
    pub fn recovery() -> Self {
        Self {
            read_only: true,
            auto_unmute_on_connect: false,
            processing: ProcessingConfig::default(),
            ..Self::default()
        }
    }

    fn normalize_audio(mut self, migrate_legacy: bool) -> Self {
        if migrate_legacy {
            self.processing = ProcessingConfig::default();
            // Older saved preferences had an explicit playback_agc field.
            self.processing.postprocess[0].backend = "none".into();
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
            auto_unmute_on_connect,
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
            return Err(io::Error::other("configuration directory unavailable"));
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

    pub fn load() -> io::Result<Self> {
        let Some(path) = path() else {
            return Err(io::Error::other("configuration directory unavailable"));
        };
        Self::load_at(&path)
    }

    fn load_at(path: &Path) -> io::Result<Self> {
        match read_document(path)? {
            Some(bytes) => Self::decode(&bytes).map(|(preferences, _)| preferences),
            None => Ok(Self::default()),
        }
    }

    fn decode(bytes: &[u8]) -> io::Result<(Self, u32)> {
        let value: serde_json::Value = serde_json::from_slice(bytes)?;
        if !value.is_object() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "preferences must be an object",
            ));
        }
        let version = match value.get("schema_version") {
            None => 0,
            Some(version) => version
                .as_u64()
                .filter(|version| *version == SCHEMA_VERSION as u64)
                .map(|version| version as u32)
                .ok_or_else(|| {
                    io::Error::new(
                        io::ErrorKind::InvalidData,
                        "unsupported preferences schema_version",
                    )
                })?,
        };
        if version == SCHEMA_VERSION {
            check_fields(&value, &serde_json::to_value(Self::default())?)?;
        }
        let migrate_legacy = version == 0 && value.get("processing").is_none();
        let mut preferences =
            serde_json::from_value::<Self>(value)?.normalize_audio(migrate_legacy);
        preferences.schema_version = SCHEMA_VERSION;
        Ok((preferences, version))
    }

    fn save_at(&self, path: &Path) -> std::io::Result<()> {
        if self.read_only {
            return Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "preferences are protected for this run",
            ));
        }
        if let Some(bytes) = read_document(path)? {
            let (_, version) = Self::decode(&bytes)?;
            if version == 0 {
                write_backup(path, &bytes)?;
            }
        }
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent)?;
        }
        let bytes = serde_json::to_vec_pretty(self)?;
        if bytes.len() as u64 > MAX_BYTES {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "preferences exceed the 1 MiB limit",
            ));
        }
        let temp = path.with_extension(format!(
            "{}.{}.tmp",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap_or_default()
                .as_nanos()
        ));
        let result = (|| {
            let mut file = private_file(&temp)?;
            file.write_all(&bytes)?;
            file.sync_all()?;
            drop(file);
            fs::rename(&temp, path)
        })();
        let _ = fs::remove_file(&temp);
        result
    }
}

fn read_document(path: &Path) -> io::Result<Option<Vec<u8>>> {
    let file = match fs::File::open(path) {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    let mut bytes = Vec::new();
    file.take(MAX_BYTES + 1).read_to_end(&mut bytes)?;
    if bytes.len() as u64 > MAX_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "preferences exceed the 1 MiB limit",
        ));
    }
    Ok(Some(bytes))
}

// Current schemas must not silently discard fields from a newer writer.
fn check_fields(value: &serde_json::Value, known: &serde_json::Value) -> io::Result<()> {
    if let (Some(value), Some(known)) = (value.as_object(), known.as_object()) {
        for (key, value) in value {
            let field = known.get(key).ok_or_else(|| {
                io::Error::new(io::ErrorKind::InvalidData, "unknown preferences field")
            })?;
            check_fields(value, field)?;
        }
    } else if let (Some(value), Some(known)) = (value.as_array(), known.as_array()) {
        if value.len() != known.len() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "unsupported processing chain shape",
            ));
        }
        for (value, field) in value.iter().zip(known) {
            check_fields(value, field)?;
        }
    }
    Ok(())
}

fn private_file(path: &Path) -> io::Result<fs::File> {
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    options.open(path)
}

fn write_backup(path: &Path, bytes: &[u8]) -> io::Result<()> {
    let backup = path.with_extension("json.pre-v1.bak");
    let mut file = match private_file(&backup) {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::AlreadyExists => {
            // A backup from an interrupted migration is reusable only for the same source.
            if read_document(&backup)?.as_deref() == Some(bytes) {
                return Ok(());
            }
            return Err(io::Error::other(
                "migration backup already exists with different contents",
            ));
        }
        Err(error) => return Err(error),
    };
    let result = file.write_all(bytes).and_then(|_| file.sync_all());
    drop(file);
    if result.is_err() {
        let _ = fs::remove_file(backup);
    }
    result
}

#[cfg(test)]
mod tests {
    #[test]
    fn entry_preference_and_new_processing_defaults_round_trip() {
        let baseline = super::Preferences::default();
        assert!(baseline.auto_unmute_on_connect);
        assert_eq!(baseline.processing.preprocess[0].backend, "speex");
        assert_eq!(baseline.processing.preprocess[1].backend, "speex");
        assert_eq!(baseline.processing.postprocess[0].backend, "webrtc");
        let mut draft = baseline.clone();
        draft.auto_unmute_on_connect = false;
        let mut current = baseline.clone();
        current.merge_settings(&baseline, &draft);
        let restored: super::Preferences =
            serde_json::from_slice(&serde_json::to_vec(&current).unwrap()).unwrap();
        assert!(!restored.auto_unmute_on_connect);
    }
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
        assert_eq!(old.processing.postprocess[0].backend, "webrtc");
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
        assert_eq!(normalized.processing.postprocess[0].backend, "none");
    }

    #[test]
    fn saved_disabled_agc_stays_off_and_new_install_defaults_on() {
        let directory =
            std::env::temp_dir().join(format!("resona-agc-default-{}", std::process::id()));
        fs::create_dir_all(&directory).unwrap();
        let path = directory.join("preferences.json");
        fs::write(&path, r#"{"playback_agc":"off"}"#).unwrap();
        assert_eq!(
            Preferences::load_at(&path).unwrap().processing.postprocess[0].backend,
            "none"
        );
        let saved = serde_json::to_vec(&Preferences::load_at(&path).unwrap()).unwrap();
        fs::write(&path, saved).unwrap();
        assert_eq!(
            Preferences::load_at(&path).unwrap().processing.postprocess[0].backend,
            "none"
        );
        fs::remove_file(path).unwrap();
        assert_eq!(
            Preferences::load_at(&directory.join("missing.json"))
                .unwrap()
                .processing
                .postprocess[0]
                .backend,
            "webrtc"
        );
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
        let migrated = Preferences::load_at(&path).unwrap();
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

    fn test_directory() -> PathBuf {
        static NEXT: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!(
            "resona-schema-{}-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, std::sync::atomic::Ordering::Relaxed),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        fs::create_dir(&path).unwrap();
        path
    }

    #[test]
    fn legacy_migration_is_read_only_then_backs_up_exact_source_once() {
        let directory = test_directory();
        let path = directory.join("native-preferences.json");
        let original =
            br#"{ "auto_unmute_on_connect":false, "playback_agc":"off", "input_gain":145 }"#;
        fs::write(&path, original).unwrap();
        let preferences = Preferences::load_at(&path).unwrap();
        assert!(!preferences.auto_unmute_on_connect);
        assert_eq!(preferences.input_gain, 145);
        assert_eq!(fs::read(&path).unwrap(), original);
        assert!(!path.with_extension("json.pre-v1.bak").exists());
        preferences.save_at(&path).unwrap();
        assert_eq!(
            fs::read(path.with_extension("json.pre-v1.bak")).unwrap(),
            original
        );
        assert_eq!(Preferences::load_at(&path).unwrap(), preferences);
        preferences.save_at(&path).unwrap();
        assert_eq!(
            fs::read(path.with_extension("json.pre-v1.bak")).unwrap(),
            original
        );
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                0o600
            );
        }
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn corrupt_future_and_unknown_fields_are_never_replaced_by_save() {
        let directory = test_directory();
        let path = directory.join("native-preferences.json");
        let mut nested = serde_json::to_value(Preferences::default()).unwrap();
        nested["processing"]["preprocess"][0]["params"]["futureParam"] = 1.into();
        for bytes in [
            "{broken".to_string(),
            "null".into(),
            "{} trailing".into(),
            r#"{"schema_version":2}"#.into(),
            r#"{"schema_version":"1"}"#.into(),
            r#"{"schema_version":1,"future_option":true}"#.into(),
            nested.to_string(),
            " ".repeat(MAX_BYTES as usize + 1),
        ] {
            fs::write(&path, &bytes).unwrap();
            assert!(Preferences::load_at(&path).is_err());
            assert!(Preferences::default().save_at(&path).is_err());
            assert_eq!(fs::read(&path).unwrap(), bytes.as_bytes());
        }
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn recovery_cannot_unmute_or_save_even_after_manual_repair() {
        let directory = test_directory();
        let path = directory.join("native-preferences.json");
        let recovery = Preferences::recovery();
        assert!(!recovery.auto_unmute_on_connect);
        assert_eq!(recovery.processing, ProcessingConfig::default());
        Preferences::default().save_at(&path).unwrap();
        let original = fs::read(&path).unwrap();
        let mut changed = recovery.clone();
        changed.playback_volume = 300;
        assert!(changed.save_ordered_at(1, &Mutex::new(0), &path).is_err());
        assert_eq!(fs::read(&path).unwrap(), original);
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn conflicting_backup_blocks_migration_without_overwriting_either_file() {
        let directory = test_directory();
        let path = directory.join("native-preferences.json");
        let backup = path.with_extension("json.pre-v1.bak");
        fs::write(&path, b"{}").unwrap();
        fs::write(&backup, b"previous backup").unwrap();
        assert!(Preferences::load_at(&path).unwrap().save_at(&path).is_err());
        assert_eq!(fs::read(&path).unwrap(), b"{}");
        assert_eq!(fs::read(&backup).unwrap(), b"previous backup");
        fs::remove_dir_all(directory).unwrap();
    }
}

fn path() -> Option<PathBuf> {
    directory().map(|directory| directory.join("native-preferences.json"))
}

pub fn directory() -> Option<PathBuf> {
    dirs::config_dir().map(|directory| directory.join("resona"))
}
