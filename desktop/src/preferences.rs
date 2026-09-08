use serde::{Deserialize, Serialize};
use std::{fs, path::PathBuf};

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(default)]
pub struct Preferences {
    pub notifications_enabled: bool,
    pub notification_volume: u8,
}

impl Default for Preferences {
    fn default() -> Self {
        Self {
            notifications_enabled: true,
            notification_volume: 35,
        }
    }
}

impl Preferences {
    pub fn load() -> Self {
        let Some(path) = path() else {
            return Self::default();
        };
        fs::read(path)
            .ok()
            .and_then(|bytes| serde_json::from_slice(&bytes).ok())
            .unwrap_or_default()
    }

    pub fn save(&self) -> std::io::Result<()> {
        let Some(path) = path() else {
            return Ok(());
        };
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent)?;
        }
        let temp = path.with_extension("tmp");
        fs::write(&temp, serde_json::to_vec_pretty(self)?)?;
        fs::rename(temp, path)
    }
}

fn path() -> Option<PathBuf> {
    dirs::config_dir().map(|directory| directory.join("resona").join("native-preferences.json"))
}
