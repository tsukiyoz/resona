use serde::Serialize;

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BuildInfo {
    pub version: String,
    pub commit: String,
    pub build_time: String,
    pub dirty: String,
    pub rust_version: String,
    pub platform: String,
}
impl Default for BuildInfo {
    fn default() -> Self {
        Self {
            version: env!("RESONA_VERSION").into(),
            commit: env!("RESONA_COMMIT").into(),
            build_time: env!("RESONA_BUILD_TIME").into(),
            dirty: env!("RESONA_DIRTY").into(),
            rust_version: env!("RESONA_RUST_VERSION").into(),
            platform: crate::updates::platform(),
        }
    }
}
