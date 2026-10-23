//! Existing on-disk identities and bookmarks; never migrate by resetting data.
use ed25519_dalek::SigningKey;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    collections::HashSet,
    fs,
    io::{self, Read, Write},
    net::IpAddr,
    path::{Path, PathBuf},
};
use tempfile::NamedTempFile;
use zeroize::Zeroizing;

const MAX_BYTES: u64 = 1 << 20;
pub type Result<T> = std::result::Result<T, &'static str>;

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq)]
#[serde(rename_all = "camelCase", default, deny_unknown_fields)]
pub struct ServerProfile {
    pub protocol: String,
    pub server_public_key: String,
    pub id: String,
    pub name: String,
    pub address: String,
    pub nickname: String,
    pub skip_password_storage: bool,
    #[serde(skip_serializing)]
    pub certificate_fingerprint: Option<serde_json::Value>,
}

impl ServerProfile {
    pub fn normalized(mut self) -> Self {
        self.protocol = self.protocol.trim().to_ascii_lowercase();
        self.server_public_key = self.server_public_key.trim().to_ascii_lowercase();
        self.name = self.name.trim().into();
        self.address = self.address.trim().into();
        self.nickname = self.nickname.trim().into();
        self
    }

    pub fn supports_connection(&self) -> bool {
        self.protocol == "resona-noise"
    }

    pub fn public_key(&self) -> Result<[u8; 32]> {
        if !self.supports_connection()
            || self.server_public_key.len() != 64
            || !self.server_public_key.is_ascii()
        {
            return Err("此书签格式不受支持或服务器公钥无效");
        }
        let mut key = [0; 32];
        for (i, byte) in key.iter_mut().enumerate() {
            *byte = u8::from_str_radix(&self.server_public_key[i * 2..i * 2 + 2], 16)
                .map_err(|_| "服务器公钥必须为 64 位十六进制")?;
        }
        if key == [0; 32] {
            return Err("服务器公钥无效");
        }
        Ok(key)
    }

    pub fn validate(&self) -> Result<()> {
        self.public_key()?;
        for (value, limit) in [(&self.name, 100), (&self.nickname, 30)] {
            if value.trim().is_empty()
                || value.chars().count() > limit
                || value.chars().any(char::is_control)
            {
                return Err("服务器名称或昵称无效");
            }
        }
        self.endpoint()?;
        Ok(())
    }

    pub fn endpoint(&self) -> Result<String> {
        let address = &self.address;
        if address.is_empty()
            || address.len() > 253
            || address
                .chars()
                .any(|c| c.is_whitespace() || c.is_control() || "/\\@?#".contains(c))
        {
            return Err("服务器地址无效");
        }
        if let Ok(ip) = address.parse::<IpAddr>() {
            return Ok(format!("{}", std::net::SocketAddr::new(ip, 9988)));
        }
        let (host, port) = if address.starts_with('[') {
            let end = address.find(']').ok_or("IPv6 地址无效")?;
            address[1..end]
                .parse::<std::net::Ipv6Addr>()
                .map_err(|_| "IPv6 地址无效")?;
            (
                &address[..=end],
                address[end + 1..].strip_prefix(':').ok_or("端口无效")?,
            )
        } else if let Some((h, p)) = address.rsplit_once(':') {
            (h, p)
        } else {
            (address.as_str(), "9988")
        };
        let port = port.parse::<u16>().map_err(|_| "端口无效")?;
        if port == 0 {
            return Err("端口无效");
        }
        if !host.starts_with('[') && host.parse::<IpAddr>().is_err() {
            for label in host.trim_end_matches('.').split('.') {
                if label.is_empty()
                    || label.len() > 63
                    || label.starts_with('-')
                    || label.ends_with('-')
                    || !label
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || b == b'-')
                {
                    return Err("主机名无效");
                }
            }
        }
        Ok(format!("{host}:{port}"))
    }

    pub fn destination(&self) -> String {
        format!(
            "{}\0{}\0{}",
            self.protocol, self.address, self.server_public_key
        )
    }
    pub fn credential_key(&self) -> String {
        hex(&Sha256::digest(
            format!("{}\0{}", self.id, self.destination()).as_bytes(),
        ))
    }
    pub fn account_credential_key(&self) -> String {
        format!("account-token:{}", self.credential_key())
    }
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

pub fn random_id() -> Result<String> {
    let mut id = [0; 16];
    getrandom::fill(&mut id).map_err(|_| "系统随机源不可用")?;
    Ok(hex(&id))
}

pub fn directory() -> Result<PathBuf> {
    dirs::config_dir()
        .map(|p| p.join("resona"))
        .ok_or("无法定位配置目录")
}

fn private_directory(path: &Path) -> io::Result<()> {
    fs::create_dir_all(path)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(0o700))?;
    }
    Ok(())
}

fn read(path: &Path, limit: u64) -> io::Result<Vec<u8>> {
    let meta = fs::symlink_metadata(path)?;
    if !meta.is_file() || meta.len() > limit {
        return Err(io::ErrorKind::InvalidData.into());
    }
    let mut data = Vec::new();
    fs::File::open(path)?
        .take(limit + 1)
        .read_to_end(&mut data)?;
    if data.len() as u64 > limit {
        return Err(io::ErrorKind::InvalidData.into());
    }
    Ok(data)
}

fn write(path: &Path, data: &[u8], replace: bool) -> io::Result<()> {
    let parent = path.parent().ok_or(io::ErrorKind::InvalidInput)?;
    private_directory(parent)?;
    let mut tmp = NamedTempFile::new_in(parent)?;
    tmp.write_all(data)?;
    tmp.as_file().sync_all()?;
    if replace {
        tmp.persist(path).map_err(|e| e.error)?;
    } else {
        tmp.persist_noclobber(path).map_err(|e| e.error)?;
    }
    #[cfg(unix)]
    fs::File::open(parent)?.sync_all()?;
    Ok(())
}

pub struct Identity {
    seed: Zeroizing<[u8; 32]>,
}
impl Identity {
    pub fn load_or_create(path: &Path) -> Result<Self> {
        #[cfg(unix)]
        if let Ok(meta) = fs::symlink_metadata(path) {
            use std::os::unix::fs::PermissionsExt;
            if !meta.is_file() {
                return Err("身份文件无效，原文件已保留");
            }
            fs::set_permissions(path, fs::Permissions::from_mode(0o600))
                .map_err(|_| "无法保护身份文件权限")?;
        }
        match read(path, 32) {
            Ok(bytes) => Self::from_bytes(bytes),
            Err(e) if e.kind() == io::ErrorKind::NotFound => {
                let mut seed = Zeroizing::new([0; 32]);
                getrandom::fill(seed.as_mut()).map_err(|_| "系统随机源不可用")?;
                if let Err(e) = write(path, seed.as_ref(), false)
                    && e.kind() != io::ErrorKind::AlreadyExists
                {
                    return Err("无法创建身份文件");
                }
                Self::from_bytes(read(path, 32).map_err(|_| "无法读取身份，原文件已保留")?)
            }
            Err(_) => Err("无法读取身份，原文件已保留"),
        }
    }
    fn from_bytes(bytes: Vec<u8>) -> Result<Self> {
        let bytes = Zeroizing::new(bytes);
        let seed = bytes
            .as_slice()
            .try_into()
            .map_err(|_| "身份文件无效，原文件已保留")?;
        Ok(Self {
            seed: Zeroizing::new(seed),
        })
    }
    pub fn seed(&self) -> &[u8; 32] {
        &self.seed
    }
    pub fn uid(&self) -> String {
        crate::wire::identity_uid(
            &SigningKey::from_bytes(&self.seed)
                .verifying_key()
                .to_bytes(),
        )
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Document {
    schema_version: u32,
    servers: Vec<ServerProfile>,
}

pub struct ProfileStore {
    path: PathBuf,
}
impl ProfileStore {
    pub fn new(path: PathBuf) -> Self {
        Self { path }
    }
    pub fn load(&self) -> Result<Vec<ServerProfile>> {
        self.read().map(|(p, _)| p)
    }
    fn read(&self) -> Result<(Vec<ServerProfile>, Option<Vec<u8>>)> {
        let bytes = match read(&self.path, MAX_BYTES) {
            Ok(b) => b,
            Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok((Vec::new(), None)),
            Err(_) => return Err("无法读取服务器书签，原文件已保留"),
        };
        let (profiles, legacy) = if bytes.iter().find(|b| !b.is_ascii_whitespace()) == Some(&b'[') {
            (
                serde_json::from_slice::<Vec<ServerProfile>>(&bytes)
                    .map_err(|_| "旧服务器书签格式无效")?,
                true,
            )
        } else {
            let d: Document =
                serde_json::from_slice(&bytes).map_err(|_| "服务器书签格式无效或含未知字段")?;
            if d.schema_version != 1 {
                return Err("服务器书签版本不受支持，原文件已保留");
            }
            (d.servers, false)
        };
        validate_profiles(&profiles)?;
        Ok((profiles, legacy.then_some(bytes)))
    }
    pub fn save(&self, profiles: &[ServerProfile]) -> Result<()> {
        validate_profiles(profiles)?;
        let (_, backup) = self.read()?;
        let mut bytes = serde_json::to_vec_pretty(&Document {
            schema_version: 1,
            servers: profiles.to_vec(),
        })
        .map_err(|_| "无法编码书签")?;
        bytes.push(b'\n');
        if bytes.len() as u64 > MAX_BYTES {
            return Err("服务器书签超过 1 MiB 上限");
        }
        if let Some(original) = backup {
            let backup = self.path.with_file_name(format!(
                "{}.pre-v1.bak",
                self.path
                    .file_name()
                    .ok_or("书签路径无效")?
                    .to_string_lossy()
            ));
            if let Err(e) = write(&backup, &original, false)
                && (e.kind() != io::ErrorKind::AlreadyExists
                    || read(&backup, MAX_BYTES).ok().as_deref() != Some(&original))
            {
                return Err("无法备份旧书签，原文件未覆盖");
            }
        }
        write(&self.path, &bytes, true).map_err(|_| "无法保存服务器书签")
    }
}

fn validate_profiles(profiles: &[ServerProfile]) -> Result<()> {
    let mut ids = HashSet::new();
    for profile in profiles {
        if profile.id.is_empty() || !ids.insert(&profile.id) {
            return Err("书签 ID 必须非空且唯一");
        }
        if profile.supports_connection() {
            profile.validate()?;
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn identity_is_preserved_and_bad_files_are_not_replaced() {
        let tmp = tempfile::tempdir().unwrap();
        let path = tmp.path().join("identity");
        let id = Identity::load_or_create(&path).unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&path, fs::Permissions::from_mode(0o644)).unwrap();
            Identity::load_or_create(&path).unwrap();
            assert_eq!(
                fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                0o600
            );
        }
        assert_eq!(id.uid(), Identity::load_or_create(&path).unwrap().uid());
        fs::write(&path, b"broken").unwrap();
        assert!(Identity::load_or_create(&path).is_err());
        assert_eq!(fs::read(&path).unwrap(), b"broken");
    }
    #[test]
    fn legacy_backup_and_future_unknown_files_are_preserved() {
        let tmp = tempfile::tempdir().unwrap();
        let path = tmp.path().join("servers.json");
        let legacy = br#"[{"id":"retired","protocol":"old","certificateFingerprint":"old"}]"#;
        fs::write(&path, legacy).unwrap();
        let store = ProfileStore::new(path.clone());
        let profiles = store.load().unwrap();
        store.save(&profiles).unwrap();
        assert_eq!(
            fs::read(tmp.path().join("servers.json.pre-v1.bak")).unwrap(),
            legacy
        );
        for data in [
            r#"{"schema_version":2,"servers":[]}"#,
            r#"{"schema_version":1,"servers":[],"unknown":true}"#,
        ] {
            fs::write(&path, data).unwrap();
            assert!(store.save(&[]).is_err());
            assert_eq!(fs::read_to_string(&path).unwrap(), data);
        }
    }
    #[test]
    fn addresses_and_destination_binding() {
        let mut p = ServerProfile {
            protocol: "resona-noise".into(),
            server_public_key: "07".repeat(32),
            name: "test".into(),
            nickname: "user".into(),
            address: "::1".into(),
            id: "bookmark".into(),
            ..Default::default()
        };
        assert_eq!(p.endpoint().unwrap(), "[::1]:9988");
        p.validate().unwrap();
        let key = p.credential_key();
        let account = p.account_credential_key();
        assert_ne!(key, account);
        p.server_public_key = "08".repeat(32);
        assert_ne!(account, p.account_credential_key());
        p.server_public_key = "07".repeat(32);
        p.id = "another-bookmark".into();
        assert_ne!(account, p.account_credential_key());
        p.id = "bookmark".into();
        p.address = "localhost:9988".into();
        assert_ne!(key, p.credential_key());
        assert_ne!(account, p.account_credential_key());
        p.address = "server:0".into();
        assert!(p.validate().is_err());
        p.server_public_key = "é".repeat(32);
        assert!(p.public_key().is_err());
    }
}
