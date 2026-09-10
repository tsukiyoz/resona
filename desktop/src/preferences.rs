use serde::{Deserialize, Serialize};
use std::{
    fs,
    path::{Path, PathBuf},
    sync::Mutex,
};

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(default)]
pub struct Preferences {
    pub server_sidebar_collapsed: bool,
    pub channel_sidebar_collapsed: bool,
    pub notifications_enabled: bool,
    pub notification_volume: u8,
    pub activation_mode: String,
    pub vad_threshold_db: i32,
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
    use super::*;

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
            vad_threshold_db: -22,
            echo_cancellation: true,
            ..old.clone()
        };
        latest.save_ordered_at(2, &saved, &path).unwrap();
        old.save_ordered_at(1, &saved, &path).unwrap();
        let loaded: Preferences = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
        assert_eq!(loaded.vad_threshold_db, -22);
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
