//! Explicit, signed metadata checks. Portable builds only open the release page.
use crate::noise::CancellationToken;
use base64::{Engine as _, engine::general_purpose::STANDARD};
use chrono::{DateTime, Utc};
use ed25519_dalek::{Signature, VerifyingKey};
use serde::{Deserialize, Serialize};
use std::{
    collections::HashMap,
    io::Read,
    path::Path,
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio::{sync::watch, task::JoinHandle};

#[derive(Clone, Debug, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    pub state: String,
    pub version: String,
    pub notes: String,
    pub error: String,
    pub distribution: String,
    pub can_download: bool,
    pub can_install: bool,
    pub can_open_release: bool,
    #[serde(rename = "releaseURL")]
    pub release_url: String,
    pub configured: bool,
    pub unavailable_reason: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Signed {
    payload: Box<serde_json::value::RawValue>,
    signature: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Manifest {
    schema: u32,
    product: String,
    channel: String,
    pub version: String,
    expires: String,
    pub notes: String,
    #[serde(rename = "releaseURL")]
    pub release_url: String,
    assets: HashMap<String, Asset>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Asset {
    distribution: String,
    url: String,
    #[serde(rename = "sha256")]
    digest: String,
    size: u64,
}
pub fn version(value: &str) -> Result<[u32; 3], String> {
    let mut result = [0; 3];
    let parts: Vec<_> = value
        .strip_prefix('v')
        .unwrap_or(value)
        .split('.')
        .collect();
    if parts.len() != 3 {
        return Err("更新仅支持正式的三段版本号".into());
    }
    for (target, part) in result.iter_mut().zip(parts) {
        if part.is_empty()
            || part.len() > 1 && part.starts_with('0')
            || !part.bytes().all(|c| c.is_ascii_digit())
        {
            return Err("更新版本号无效".into());
        }
        *target = part.parse().map_err(|_| "更新版本号无效")?;
    }
    Ok(result)
}
fn https(value: &str) -> bool {
    reqwest::Url::parse(value).is_ok_and(|url| {
        url.scheme() == "https"
            && url.host_str().is_some()
            && url.username().is_empty()
            && url.password().is_none()
            && url.fragment().is_none()
    })
}
pub fn verify(data: &[u8], public_key: &str, now: DateTime<Utc>) -> Result<Manifest, String> {
    let bytes = STANDARD
        .decode(public_key)
        .map_err(|_| "更新签名公钥未配置或无效")?;
    let key = VerifyingKey::from_bytes(
        bytes
            .as_slice()
            .try_into()
            .map_err(|_| "更新签名公钥未配置或无效")?,
    )
    .map_err(|_| "更新签名公钥未配置或无效")?;
    if data.len() > 64 * 1024 {
        return Err("更新清单格式无效".into());
    }
    let signed: Signed = serde_json::from_slice(data).map_err(|_| "更新清单格式无效")?;
    let signature = Signature::from_slice(
        &STANDARD
            .decode(signed.signature)
            .map_err(|_| "更新清单签名验证失败")?,
    )
    .map_err(|_| "更新清单签名验证失败")?;
    key.verify_strict(signed.payload.get().as_bytes(), &signature)
        .map_err(|_| "更新清单签名验证失败")?;
    let manifest: Manifest =
        serde_json::from_str(signed.payload.get()).map_err(|_| "不支持此更新清单版本或产品")?;
    if manifest.schema != 1 || manifest.product != "resona" || manifest.channel != "stable" {
        return Err("不支持此更新清单版本或产品".into());
    }
    version(&manifest.version)?;
    if manifest.release_url
        != format!(
            "https://github.com/tsukiyoz/resona/releases/tag/{}",
            manifest.version
        )
    {
        return Err("更新发布页地址无效".into());
    }
    if DateTime::parse_from_rfc3339(&manifest.expires).map_err(|_| "更新清单有效期无效")? <= now
        || manifest.notes.len() > 8192
        || manifest.assets.len() > 8
    {
        return Err("更新清单已过期或超出限制".into());
    }
    for asset in manifest.assets.values() {
        if asset.digest.len() != 64
            || !asset.digest.bytes().all(|c| c.is_ascii_hexdigit())
            || asset.size == 0
            || asset.size > 512 << 20
            || !https(&asset.url)
            || asset.distribution != "portable"
        {
            return Err("更新包信息无效".into());
        }
    }
    Ok(manifest)
}
pub fn platform() -> String {
    let os = match std::env::consts::OS {
        "macos" => "darwin",
        other => other,
    };
    let arch = match std::env::consts::ARCH {
        "x86_64" => "amd64",
        "aarch64" => "arm64",
        other => other,
    };
    format!("{os}/{arch}")
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Metadata {
    schema: u32,
    product: String,
    platform: String,
    version: String,
    distribution: String,
}
pub fn distribution(executable: &Path, current: &str) -> String {
    let Some(dir) = executable.parent() else {
        return String::new();
    };
    let path = if dir.file_name().is_some_and(|name| name == "MacOS") {
        dir.parent()
            .unwrap_or(dir)
            .join("Resources/resona-package.json")
    } else {
        dir.join("resona-package.json")
    };
    let Ok(info) = std::fs::symlink_metadata(&path) else {
        return String::new();
    };
    if !info.is_file() || info.len() > 4096 {
        return String::new();
    }
    let Ok(file) = std::fs::File::open(path) else {
        return String::new();
    };
    let mut data = Vec::with_capacity(4097);
    if file.take(4097).read_to_end(&mut data).is_err() || data.len() > 4096 {
        return String::new();
    }
    let Ok(metadata) = serde_json::from_slice::<Metadata>(&data) else {
        return String::new();
    };
    if metadata.schema != 1
        || metadata.product != "resona"
        || metadata.platform != platform()
        || metadata.version != current
        || metadata.distribution.len() > 32
    {
        return String::new();
    }
    metadata.distribution
}
#[derive(Clone)]
pub struct Settings {
    pub endpoint: String,
    pub public_key: String,
    pub current: String,
    pub distribution: String,
}
pub struct Manager {
    settings: Settings,
    pub status: watch::Receiver<Status>,
    publish: watch::Sender<Status>,
    cancel: CancellationToken,
    task: Option<JoinHandle<()>>,
    commit: Arc<Mutex<()>>,
}
impl Manager {
    pub fn new(settings: Settings) -> Self {
        let unavailable_reason: String = if settings.distribution != "portable" {
            "此构建未标记有效的便携分发方式"
        } else if !https(&settings.endpoint) || settings.public_key.is_empty() {
            "此构建尚未配置正式更新源和签名公钥"
        } else if version(&settings.current).is_err() {
            "开发版本不检查正式更新，请使用正式发布包"
        } else {
            ""
        }
        .into();
        let (publish, status) = watch::channel(Status {
            state: "idle".into(),
            distribution: settings.distribution.clone(),
            configured: unavailable_reason.is_empty(),
            unavailable_reason,
            ..Default::default()
        });
        Self {
            settings,
            status,
            publish,
            cancel: CancellationToken::new(),
            task: None,
            commit: Arc::new(Mutex::new(())),
        }
    }
    pub fn start(&mut self) -> Result<(), String> {
        if self.task.as_ref().is_some_and(|task| !task.is_finished()) {
            return Err("更新检查正在进行".into());
        }
        let mut initial = self.status.borrow().clone();
        if !initial.configured {
            return Err(initial.unavailable_reason);
        }
        self.cancel = CancellationToken::new();
        let cancel = self.cancel.clone();
        let settings = self.settings.clone();
        let publish = self.publish.clone();
        let commit = self.commit.clone();
        initial.state = "checking".into();
        initial.error.clear();
        initial.version.clear();
        initial.notes.clear();
        initial.release_url.clear();
        initial.can_open_release = false;
        publish.send_replace(initial.clone());
        self.task = Some(tokio::spawn(async move {
            let result = tokio::select! {biased;_ = cancel.cancelled()=>Err("已取消检查更新".into()),result=tokio::time::timeout(Duration::from_secs(10),check(&settings))=>result.unwrap_or_else(|_|Err("连接更新服务超时，请检查网络后重试".into()))};
            let mut status = initial;
            match result {
                Ok(manifest) => {
                    status.state = if version(&manifest.version).unwrap()
                        > version(&settings.current).unwrap()
                    {
                        "available"
                    } else {
                        "current"
                    }
                    .into();
                    status.can_open_release = status.state == "available";
                    status.version = manifest.version;
                    status.notes = manifest.notes;
                    status.release_url = manifest.release_url;
                }
                Err(error) => {
                    status.state = if cancel.is_cancelled() {
                        "cancelled"
                    } else {
                        "failed"
                    }
                    .into();
                    status.error = error;
                }
            }
            let _gate = commit
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            if cancel.is_cancelled() {
                status.state = "cancelled".into();
                status.error = "已取消检查更新".into();
                status.can_open_release = false;
                status.release_url.clear();
            }
            publish.send_replace(status);
        }));
        Ok(())
    }
    pub fn cancel(&self) {
        let _gate = self
            .commit
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        self.cancel.cancel();
        self.publish.send_if_modified(|status| {
            if status.state != "checking" {
                return false;
            }
            status.state = "cancelled".into();
            status.error = "已取消检查更新".into();
            status.can_open_release = false;
            status.release_url.clear();
            true
        });
    }
    pub fn release_page(&self) -> Result<String, String> {
        let status = self.status.borrow();
        if !status.can_open_release {
            return Err("请先检查可用更新".into());
        }
        Ok(status.release_url.clone())
    }
    pub async fn stop(&mut self) {
        self.cancel();
        if let Some(task) = self.task.take() {
            let _ = task.await;
        }
    }
    pub async fn close(mut self) {
        self.stop().await;
    }
}
impl Drop for Manager {
    fn drop(&mut self) {
        self.cancel();
    }
}
async fn check(settings: &Settings) -> Result<Manifest, String> {
    let client = reqwest::Client::builder()
        .https_only(true)
        .pool_max_idle_per_host(0)
        .connect_timeout(Duration::from_secs(3))
        .timeout(Duration::from_secs(10))
        .redirect(reqwest::redirect::Policy::custom(|attempt| {
            if attempt.previous().len() >= 5 || !https(attempt.url().as_str()) {
                attempt.error("更新服务跳转无效")
            } else {
                attempt.follow()
            }
        }))
        .build()
        .map_err(|_| "无法初始化安全更新连接")?;
    let mut response = client
        .get(&settings.endpoint)
        .header(reqwest::header::USER_AGENT, "Resona-Updater/1")
        .send()
        .await
        .map_err(network_error)?;
    if response.status().as_u16() != 200 {
        return Err(match response.status().as_u16() {
            404 => "尚未发布更新清单（HTTP 404），请稍后重试".into(),
            403 => "更新服务拒绝访问（HTTP 403）".into(),
            429 => "更新请求过于频繁（HTTP 429），请稍后重试".into(),
            code => format!("更新服务响应异常（HTTP {code}），请稍后重试"),
        });
    }
    if response.content_length().is_some_and(|n| n > 64 * 1024) {
        return Err("更新清单过大".into());
    }
    let mut data = Vec::with_capacity(4096);
    while let Some(chunk) = response.chunk().await.map_err(network_error)? {
        if data.len() + chunk.len() > 64 * 1024 {
            return Err("更新清单过大".into());
        }
        data.extend_from_slice(&chunk);
    }
    let manifest = verify(&data, &settings.public_key, Utc::now())?;
    if manifest
        .assets
        .get(&platform())
        .is_none_or(|asset| asset.distribution != settings.distribution)
    {
        return Err("此版本没有当前平台和分发方式的更新包".into());
    }
    Ok(manifest)
}
fn network_error(error: reqwest::Error) -> String {
    if error.is_timeout() {
        "连接更新服务超时，请检查网络后重试"
    } else {
        "无法连接更新服务，请检查网络、系统时间或代理设置"
    }
    .into()
}

#[cfg(test)]
mod tests {
    use super::*;
    use ed25519_dalek::{Signer, SigningKey};
    fn signed(payload: &str, key: &SigningKey) -> Vec<u8> {
        format!(
            "{{\"payload\":{},\"signature\":\"{}\"}}",
            payload,
            STANDARD.encode(key.sign(payload.as_bytes()).to_bytes())
        )
        .into_bytes()
    }
    #[test]
    fn verifies_exact_payload_and_rejects_tampering_expiry_and_unsafe_urls() {
        let key = SigningKey::from_bytes(&[7; 32]);
        let public = STANDARD.encode(key.verifying_key().to_bytes());
        let payload = r#"{ "schema":1,"product":"resona","channel":"stable","version":"v1.2.3","expires":"2099-01-01T00:00:00Z","notes":"","releaseURL":"https://github.com/tsukiyoz/resona/releases/tag/v1.2.3","assets":{"darwin/arm64":{"distribution":"portable","url":"https://example.test/app.zip","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":100}} }"#;
        assert!(verify(&signed(payload, &key), &public, Utc::now()).is_ok());
        let mut data = signed(payload, &key);
        let at = data.windows(6).position(|w| w == b"resona").unwrap();
        data[at] = b'x';
        assert!(verify(&data, &public, Utc::now()).is_err());
        for replaced in [
            payload.replace("2099-", "2000-"),
            payload.replace("https://example.test", "http://example.test"),
            payload.replace("\"schema\":1", "\"schema\":1,\"schema\":1"),
        ] {
            assert!(verify(&signed(&replaced, &key), &public, Utc::now()).is_err());
        }
        assert!(version("v01.2.3").is_err());
        assert!(version("v1.2.3-dev").is_err());
    }
}
