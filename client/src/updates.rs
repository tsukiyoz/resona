//! Explicit signed updates: portable release links and installed Windows packages.
use crate::noise::CancellationToken;
use base64::{Engine as _, engine::general_purpose::STANDARD};
use chrono::{DateTime, Utc};
use ed25519_dalek::{Signature, VerifyingKey};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    collections::HashMap,
    io::Read,
    path::Path,
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio::{io::AsyncWriteExt, sync::watch, task::JoinHandle};

mod installer;
pub use installer::bootstrap;

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
    pub downloaded: u64,
    pub total: u64,
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
#[derive(Clone, Deserialize)]
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
    if !matches!(manifest.schema, 1 | 2)
        || manifest.product != "resona"
        || manifest.channel != "stable"
    {
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
    for (platform, asset) in &manifest.assets {
        if asset.digest.len() != 64
            || !asset.digest.bytes().all(|c| c.is_ascii_hexdigit())
            || asset.size == 0
            || asset.size > 512 << 20
            || !https(&asset.url)
            || !match manifest.schema {
                1 => asset.distribution == "portable",
                2 => platform == "windows/amd64" && asset.distribution == "installer",
                _ => false,
            }
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
    candidate: Arc<Mutex<Option<(String, Asset)>>>,
    package: Arc<Mutex<Option<tempfile::TempPath>>>,
    install_task: Option<JoinHandle<Result<(), String>>>,
}

/// Owned handoff retained independently of core shutdown. No helper is started
/// until the desktop has actually released audio and network resources.
pub struct PreparedUpdate {
    current: String,
    version: String,
    asset: Asset,
    path: tempfile::TempPath,
}
impl PreparedUpdate {
    pub fn apply(self) -> Result<(), String> {
        validate_file(&self.path, &self.asset)?;
        installer::apply(&self.current, &self.version, &self.asset, &self.path)
    }
}
impl Manager {
    pub fn new(mut settings: Settings) -> Self {
        let installer_error = if settings.distribution == "installer" {
            installer::validate(&settings.current).err()
        } else {
            None
        };
        // Keep the schema-1 portable feed readable by already released clients.
        if settings.distribution == "installer" && settings.endpoint.ends_with("/manifest.json") {
            settings
                .endpoint
                .truncate(settings.endpoint.len() - "manifest.json".len());
            settings
                .endpoint
                .push_str("manifest-windows-installer.json");
        }
        let unavailable_reason: String =
            if !matches!(settings.distribution.as_str(), "portable" | "installer") {
                "此构建未标记有效的分发方式"
            } else if let Some(error) = installer_error.as_deref() {
                error
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
            candidate: Arc::new(Mutex::new(None)),
            package: Arc::new(Mutex::new(None)),
            install_task: None,
        }
    }
    pub fn start(&mut self) -> Result<(), String> {
        if self.task.as_ref().is_some_and(|task| !task.is_finished()) {
            return Err("更新任务正在进行".into());
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
        let candidate = self.candidate.clone();
        *candidate
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner) = None;
        *self
            .package
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner) = None;
        initial.state = "checking".into();
        initial.error.clear();
        initial.version.clear();
        initial.notes.clear();
        initial.release_url.clear();
        initial.can_open_release = false;
        initial.can_download = false;
        initial.can_install = false;
        initial.downloaded = 0;
        initial.total = 0;
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
                    status.can_open_release =
                        status.state == "available" && settings.distribution == "portable";
                    status.can_download =
                        status.state == "available" && settings.distribution == "installer";
                    if status.can_download {
                        *candidate
                            .lock()
                            .unwrap_or_else(std::sync::PoisonError::into_inner) = Some((
                            manifest.version.clone(),
                            manifest.assets[&platform()].clone(),
                        ));
                    }
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
                status.can_download = false;
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
            if !matches!(status.state.as_str(), "checking" | "downloading") {
                return false;
            }
            status.state = "cancelled".into();
            status.error = "已取消更新任务".into();
            status.can_open_release = false;
            status.can_download = false;
            status.can_install = false;
            status.release_url.clear();
            true
        });
    }
    pub fn download(&mut self) -> Result<(), String> {
        if self.task.as_ref().is_some_and(|task| !task.is_finished()) {
            return Err("更新任务正在进行".into());
        }
        if !self.status.borrow().can_download || self.settings.distribution != "installer" {
            return Err("请先检查可用的安装版更新".into());
        }
        let (version, asset) = self
            .candidate
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .clone()
            .ok_or("更新包信息已失效，请重新检查")?;
        installer::validate(&self.settings.current)?;
        self.cancel = CancellationToken::new();
        let cancel = self.cancel.clone();
        let publish = self.publish.clone();
        let package = self.package.clone();
        let commit = self.commit.clone();
        let mut initial = self.status.borrow().clone();
        initial.state = "downloading".into();
        initial.error.clear();
        initial.can_download = false;
        initial.total = asset.size;
        initial.downloaded = 0;
        publish.send_replace(initial.clone());
        self.task = Some(tokio::spawn(async move {
            let result = tokio::select! {biased;
                _ = cancel.cancelled() => Err("已取消下载".into()),
                result = tokio::time::timeout(Duration::from_secs(30 * 60), download_package(&asset, &publish)) =>
                    result.unwrap_or_else(|_| Err("下载更新超时，请重试".into())),
            };
            let _gate = commit
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            let mut status = initial;
            if cancel.is_cancelled() {
                status.state = "cancelled".into();
                status.error = "已取消下载".into();
            } else {
                match result {
                    Ok(path) => {
                        *package
                            .lock()
                            .unwrap_or_else(std::sync::PoisonError::into_inner) = Some(path);
                        status.state = "ready".into();
                        status.version = version;
                        status.downloaded = asset.size;
                        status.can_install = true;
                    }
                    Err(error) => {
                        status.state = "failed".into();
                        status.error = error;
                        status.can_download = true;
                    }
                }
            }
            publish.send_replace(status);
        }));
        Ok(())
    }
    pub async fn prepare_install(&mut self) -> Result<PreparedUpdate, String> {
        if !self.status.borrow().can_install || self.settings.distribution != "installer" {
            return Err("请先完成下载和验证".into());
        }
        let (version, asset) = self
            .candidate
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .clone()
            .ok_or("更新包信息已失效，请重新检查")?;
        let path = self
            .package
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .as_ref()
            .map(|p| p.to_path_buf())
            .ok_or("更新包已失效，请重新下载")?;
        installer::validate(&self.settings.current)?;
        let check_asset = asset.clone();
        self.install_task = Some(tokio::task::spawn_blocking(move || {
            validate_file(&path, &check_asset)
        }));
        let result = self.install_task.as_mut().unwrap().await;
        self.install_task = None;
        result.map_err(|_| "无法验证更新包".to_owned())??;
        self.publish.send_modify(|s| {
            s.state = "installing".into();
            s.can_install = false;
        });
        let path = self
            .package
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .take()
            .ok_or("更新包已失效，请重新下载")?;
        Ok(PreparedUpdate {
            current: self.settings.current.clone(),
            version,
            asset,
            path,
        })
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
        if let Some(task) = self.install_task.take() {
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
fn http_client(timeout: Duration) -> Result<reqwest::Client, String> {
    reqwest::Client::builder()
        .https_only(true)
        .pool_max_idle_per_host(0)
        .connect_timeout(Duration::from_secs(3))
        .timeout(timeout)
        .redirect(reqwest::redirect::Policy::custom(|attempt| {
            if attempt.previous().len() >= 5 || !https(attempt.url().as_str()) {
                attempt.error("更新服务跳转无效")
            } else {
                attempt.follow()
            }
        }))
        .build()
        .map_err(|_| "无法初始化安全更新连接".into())
}
async fn check(settings: &Settings) -> Result<Manifest, String> {
    let client = http_client(Duration::from_secs(10))?;
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
async fn download_package(
    asset: &Asset,
    publish: &watch::Sender<Status>,
) -> Result<tempfile::TempPath, String> {
    let directory = dirs::cache_dir()
        .ok_or("无法定位更新缓存")?
        .join("resona/updates");
    tokio::fs::create_dir_all(&directory)
        .await
        .map_err(|_| "无法创建更新缓存，请检查磁盘权限")?;
    download_to(
        asset,
        publish,
        http_client(Duration::from_secs(30 * 60))?,
        &directory,
    )
    .await
}
async fn download_to(
    asset: &Asset,
    publish: &watch::Sender<Status>,
    client: reqwest::Client,
    directory: &Path,
) -> Result<tempfile::TempPath, String> {
    let temporary = tempfile::NamedTempFile::new_in(directory)
        .map_err(|_| "无法创建更新文件，请检查磁盘空间")?;
    let (file, path) = temporary.into_parts();
    let mut file = tokio::fs::File::from_std(file);
    let mut response = client
        .get(&asset.url)
        .header(reqwest::header::USER_AGENT, "Resona-Updater/1")
        .send()
        .await
        .map_err(network_error)?;
    if response.status().as_u16() != 200
        || response
            .content_length()
            .is_some_and(|size| size != asset.size)
    {
        return Err("更新包响应无效，请重新检查更新".into());
    }
    let mut hash = Sha256::new();
    let mut size = 0u64;
    let mut last_progress = std::time::Instant::now();
    loop {
        let chunk = tokio::time::timeout(Duration::from_secs(30), response.chunk())
            .await
            .map_err(|_| "下载更新超时，请检查网络后重试")?
            .map_err(network_error)?;
        let Some(chunk) = chunk else {
            break;
        };
        size += chunk.len() as u64;
        if size > asset.size {
            return Err("更新包超出清单大小".into());
        }
        hash.update(&chunk);
        file.write_all(&chunk)
            .await
            .map_err(|_| "写入更新失败，请检查磁盘空间")?;
        if last_progress.elapsed() >= Duration::from_millis(250) {
            publish.send_modify(|s| s.downloaded = size);
            last_progress = std::time::Instant::now();
        }
    }
    validate_digest(size, &format!("{:x}", hash.finalize()), asset)?;
    file.flush().await.map_err(|_| "保存更新文件失败")?;
    file.sync_all().await.map_err(|_| "保存更新文件失败")?;
    drop(file);
    Ok(path)
}
fn validate_digest(size: u64, digest: &str, asset: &Asset) -> Result<(), String> {
    if size != asset.size || !digest.eq_ignore_ascii_case(&asset.digest) {
        return Err("更新包完整性验证失败，请重新下载".into());
    }
    Ok(())
}
fn validate_file(path: &Path, asset: &Asset) -> Result<(), String> {
    let info = std::fs::symlink_metadata(path).map_err(|_| "更新包已失效，请重新下载")?;
    if !info.is_file() || info.len() != asset.size {
        return Err("更新包大小无效，请重新下载".into());
    }
    let mut file = std::fs::File::open(path).map_err(|_| "无法读取更新包")?;
    let mut hash = Sha256::new();
    let size = std::io::copy(&mut file.by_ref().take(asset.size + 1), &mut hash)
        .map_err(|_| "无法验证更新包")?;
    validate_digest(size, &format!("{:x}", hash.finalize()), asset)
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

    #[test]
    fn installer_feed_cannot_reclassify_portable_assets() {
        let key = SigningKey::from_bytes(&[8; 32]);
        let public = STANDARD.encode(key.verifying_key().to_bytes());
        let payload = serde_json::json!({"schema":2,"product":"resona","channel":"stable","version":"v1.2.3","expires":"2099-01-01T00:00:00Z","notes":"","releaseURL":"https://github.com/tsukiyoz/resona/releases/tag/v1.2.3","assets":{"windows/amd64":{"distribution":"installer","url":"https://example.test/Resona-full.nupkg","sha256":"a".repeat(64),"size":100}}}).to_string();
        assert!(verify(&signed(&payload, &key), &public, Utc::now()).is_ok());
        for invalid in [
            payload.replace("installer", "portable"),
            payload.replace("windows/amd64", "darwin/arm64"),
            payload.replace("\"schema\":2", "\"schema\":1"),
        ] {
            assert!(verify(&signed(&invalid, &key), &public, Utc::now()).is_err());
        }
    }

    #[tokio::test]
    async fn download_checks_size_digest_and_removes_incomplete_files() {
        use tokio::io::AsyncReadExt;
        let directory = tempfile::tempdir().unwrap();
        let bytes = b"a full signed package";
        for (content, size, hash, success) in [
            (
                bytes.as_slice(),
                bytes.len(),
                format!("{:x}", Sha256::digest(bytes)),
                true,
            ),
            (bytes.as_slice(), bytes.len(), "0".repeat(64), false),
            (bytes.as_slice(), bytes.len() + 1, "0".repeat(64), false),
        ] {
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let address = listener.local_addr().unwrap();
            let server = tokio::spawn(async move {
                let (mut stream, _) = listener.accept().await.unwrap();
                let mut request = [0; 4096];
                let _ = stream.read(&mut request).await;
                stream
                    .write_all(
                        format!(
                            "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                            content.len()
                        )
                        .as_bytes(),
                    )
                    .await
                    .unwrap();
                stream.write_all(content).await.unwrap();
            });
            let asset = Asset {
                distribution: "installer".into(),
                url: format!("http://{address}/package"),
                digest: hash,
                size: size as u64,
            };
            let (publish, _) = watch::channel(Status::default());
            let result =
                download_to(&asset, &publish, reqwest::Client::new(), directory.path()).await;
            assert_eq!(result.is_ok(), success);
            if let Ok(path) = &result {
                validate_file(path, &asset).unwrap();
            }
            drop(result);
            assert_eq!(std::fs::read_dir(directory.path()).unwrap().count(), 0);
            server.await.unwrap();
        }
    }

    #[tokio::test]
    async fn cancel_cannot_become_ready_and_partial_download_is_removed() {
        use tokio::io::AsyncReadExt;
        let directory = tempfile::tempdir().unwrap();
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let (sent, received) = tokio::sync::oneshot::channel();
        let server = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.unwrap();
            let mut request = [0; 4096];
            let _ = stream.read(&mut request).await;
            stream
                .write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\npartial")
                .await
                .unwrap();
            let _ = sent.send(());
            let _ = stream.read(&mut request).await;
        });
        let asset = Asset {
            distribution: "installer".into(),
            url: format!("http://{address}/package"),
            digest: "0".repeat(64),
            size: 100,
        };
        let manager = Manager::new(Settings {
            endpoint: "https://example.test/manifest.json".into(),
            public_key: String::new(),
            current: "v1.0.0".into(),
            distribution: "portable".into(),
        });
        manager
            .publish
            .send_modify(|s| s.state = "downloading".into());
        {
            let download = download_to(
                &asset,
                &manager.publish,
                reqwest::Client::new(),
                directory.path(),
            );
            tokio::pin!(download);
            tokio::select! {
                result = &mut download => panic!("download unexpectedly completed: {result:?}"),
                _ = received => manager.cancel(),
            }
        }
        assert!(manager.cancel.is_cancelled());
        assert_eq!(manager.status.borrow().state, "cancelled");
        assert!(!manager.status.borrow().can_install);
        assert_eq!(std::fs::read_dir(directory.path()).unwrap().count(), 0);
        server.abort();
        let _ = server.await;
    }
}
