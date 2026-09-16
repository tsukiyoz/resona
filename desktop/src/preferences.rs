use serde::{Deserialize, Serialize};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::Mutex,
};

#[derive(Clone, Debug, PartialEq, Deserialize, Serialize)]
#[serde(default)]
pub struct Preferences {
    pub server_sidebar_collapsed: bool,
    pub channel_sidebar_collapsed: bool,
    pub notifications_enabled: bool,
    pub notification_volume: u8,
    pub activation_mode: String,
    pub vad_threshold_db: i32,
    pub input_gain: u16,
    pub playback_volume: u16,
    pub noise_suppression: String,
    pub echo_cancellation: bool,
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
            server_sidebar_collapsed: false,
            channel_sidebar_collapsed: false,
            notifications_enabled: true,
            notification_volume: 35,
            activation_mode: "continuous".into(),
            vad_threshold_db: -40,
            input_gain: 100,
            playback_volume: 200,
            noise_suppression: "off".into(),
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
    pub fn merge_settings(&mut self, baseline: &Self, draft: &Self) {
        macro_rules! merge {
            ($($field:ident),*) => { $(
                if draft.$field != baseline.$field {
                    self.$field = draft.$field.clone();
                }
            )* };
        }
        merge!(
            notifications_enabled,
            notification_volume,
            activation_mode,
            vad_threshold_db,
            input_gain,
            playback_volume,
            noise_suppression,
            echo_cancellation,
            echo_suppression,
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
        fs::read(path)
            .ok()
            .and_then(|bytes| serde_json::from_slice(&bytes).ok())
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
    fn settings_merge_preserves_concurrent_quick_controls_and_navigation() {
        let baseline = super::Preferences::default();
        let mut draft = baseline.clone();
        draft.noise_suppression = "high".into();
        draft.notification_volume = 20;
        let mut current = baseline.clone();
        current.playback_volume = 250;
        current.server_sidebar_collapsed = true;
        current.merge_settings(&baseline, &draft);
        assert_eq!(current.playback_volume, 250);
        assert!(current.server_sidebar_collapsed);
        assert_eq!(current.noise_suppression, "high");
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
            echo_cancellation: true,
            ..old.clone()
        };
        latest.save_ordered_at(2, &saved, &path).unwrap();
        old.save_ordered_at(1, &saved, &path).unwrap();
        let loaded: Preferences = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
        assert_eq!(loaded.vad_threshold_db, -22);
        assert_eq!(loaded.input_gain, 150);
        assert!(loaded.echo_cancellation);
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
