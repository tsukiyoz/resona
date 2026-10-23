//! OS credential storage uses the existing service, account hash and payload.
//! All methods are blocking: callers run them away from UI and audio workers.
use serde::{Deserialize, Serialize};
use std::{
    collections::HashMap,
    sync::{Arc, Mutex},
};
use zeroize::Zeroizing;

#[cfg(any(target_os = "macos", target_os = "windows"))]
const SERVICE: &str = "io.github.tsukiyoz.resona.server-password";
#[cfg(any(target_os = "macos", target_os = "windows"))]
fn service(key: &str) -> &'static str {
    if key.starts_with("account-token:") {
        "io.github.tsukiyoz.resona.account-token"
    } else {
        SERVICE
    }
}
// Only the optional product Core starts the persistent credential worker.
#[cfg_attr(not(feature = "audio"), allow(dead_code))]
pub(crate) type SharedStore = Arc<Mutex<Store<Box<dyn Backend + Send>>>>;
pub(crate) const ACCOUNT_STORAGE_WARNING: &str =
    "无法保存或清除本地登录凭据；当前登录状态以服务器为准，下次连接可能需要重新登录";
pub(crate) type Receipt = tokio::sync::watch::Receiver<Option<Result<()>>>;
#[cfg_attr(not(feature = "audio"), allow(dead_code))]
enum Operation {
    Read(
        String,
        tokio::sync::oneshot::Sender<Result<Zeroizing<String>>>,
    ),
    Save(
        String,
        Zeroizing<String>,
        tokio::sync::watch::Sender<Option<Result<()>>>,
    ),
    Flush(tokio::sync::oneshot::Sender<()>),
}
#[derive(Clone)]
pub(crate) struct Accounts(tokio::sync::mpsc::Sender<Operation>);
#[cfg_attr(not(feature = "audio"), allow(dead_code))]
impl Accounts {
    pub fn new(store: SharedStore) -> Self {
        let (tx, mut rx) = tokio::sync::mpsc::channel(32);
        // Serialize low-frequency credential work independently of voice/control.
        tokio::spawn(async move {
            while let Some(operation) = rx.recv().await {
                let store = store.clone();
                let _ = tokio::task::spawn_blocking(move || match operation {
                    Operation::Read(key, reply) => {
                        let result = store
                            .lock()
                            .map_err(|_| Error::Unavailable)
                            .and_then(|mut s| s.get(&key));
                        let _ = reply.send(result);
                    }
                    Operation::Save(key, token, reply) => {
                        let result =
                            store
                                .lock()
                                .map_err(|_| Error::Unavailable)
                                .and_then(|mut s| {
                                    if token.is_empty() {
                                        s.delete(&key)
                                    } else {
                                        s.set(&key, &token)
                                    }
                                });
                        reply.send_replace(Some(result));
                    }
                    Operation::Flush(reply) => {
                        let _ = reply.send(());
                    }
                })
                .await;
            }
        });
        Self(tx)
    }
    pub fn account(&self, key: String) -> Account {
        Account {
            accounts: self.clone(),
            key,
        }
    }
    pub async fn flush(&self) {
        let (tx, rx) = tokio::sync::oneshot::channel();
        if self.0.send(Operation::Flush(tx)).await.is_ok() {
            let _ = rx.await;
        }
    }
}
pub(crate) async fn saved(mut receipt: Receipt) -> Result<()> {
    loop {
        if let Some(result) = *receipt.borrow_and_update() {
            return result;
        }
        receipt.changed().await.map_err(|_| Error::Unavailable)?;
    }
}
#[derive(Clone)]
pub(crate) struct Account {
    accounts: Accounts,
    key: String,
}
impl Account {
    pub async fn load(&self) -> Result<Zeroizing<String>> {
        let (tx, rx) = tokio::sync::oneshot::channel();
        self.accounts
            .0
            .send(Operation::Read(self.key.clone(), tx))
            .await
            .map_err(|_| Error::Unavailable)?;
        rx.await.map_err(|_| Error::Unavailable)?
    }
    pub fn save(&self, token: Zeroizing<String>) -> Result<Receipt> {
        let (tx, rx) = tokio::sync::watch::channel(None);
        self.accounts
            .0
            .try_send(Operation::Save(self.key.clone(), token, tx))
            .map_err(|_| Error::Unavailable)?;
        Ok(rx)
    }
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Error {
    NotFound,
    Unavailable,
    Denied,
    Invalid,
    System(i32),
}
impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::NotFound => f.write_str("凭据不存在"),
            Self::Unavailable => f.write_str("系统凭据存储不可用"),
            Self::Denied => f.write_str("未获系统授权"),
            Self::Invalid => f.write_str("凭据格式无效"),
            Self::System(code) => write!(f, "系统钥匙串错误 {code}"),
        }
    }
}
pub type Result<T> = std::result::Result<T, Error>;
pub trait Backend {
    fn has(&self, key: &str) -> Result<bool>;
    fn get(&self, key: &str) -> Result<Vec<u8>>;
    fn set(&self, key: &str, value: &[u8]) -> Result<()>;
    fn delete(&self, key: &str) -> Result<()>;
}
impl<B: Backend + ?Sized> Backend for Box<B> {
    fn has(&self, key: &str) -> Result<bool> {
        (**self).has(key)
    }
    fn get(&self, key: &str) -> Result<Vec<u8>> {
        (**self).get(key)
    }
    fn set(&self, key: &str, value: &[u8]) -> Result<()> {
        (**self).set(key, value)
    }
    fn delete(&self, key: &str) -> Result<()> {
        (**self).delete(key)
    }
}

#[derive(Serialize, Deserialize)]
struct Payload {
    version: u32,
    password: String,
}
pub struct Store<B: Backend = Native> {
    backend: B,
    cache: HashMap<String, Zeroizing<String>>,
}
impl Default for Store {
    fn default() -> Self {
        Self::new(Native)
    }
}
impl<B: Backend> Store<B> {
    pub fn new(backend: B) -> Self {
        Self {
            backend,
            cache: HashMap::new(),
        }
    }
    fn validate(key: &str) -> Result<()> {
        if key.is_empty() || key.len() > 256 || key.contains('\0') {
            Err(Error::Invalid)
        } else {
            Ok(())
        }
    }
    pub fn has(&self, key: &str) -> Result<bool> {
        Self::validate(key)?;
        if self.cache.contains_key(key) {
            return Ok(true);
        }
        self.backend.has(key)
    }
    pub fn get(&mut self, key: &str) -> Result<Zeroizing<String>> {
        Self::validate(key)?;
        if let Some(value) = self.cache.get(key) {
            return Ok(value.clone());
        }
        let bytes = Zeroizing::new(self.backend.get(key)?);
        if bytes.len() > 1 << 20 {
            return Err(Error::Invalid);
        }
        let p: Payload = serde_json::from_slice(&bytes).map_err(|_| Error::Invalid)?;
        if p.version != 1 {
            return Err(Error::Invalid);
        }
        let value = Zeroizing::new(p.password);
        self.cache(key, value.clone());
        Ok(value)
    }
    fn cache(&mut self, key: &str, value: Zeroizing<String>) {
        if self.cache.len() >= 128 && !self.cache.contains_key(key) {
            self.cache.clear();
        }
        self.cache.insert(key.into(), value);
    }
    pub fn set(&mut self, key: &str, password: &str) -> Result<()> {
        Self::validate(key)?;
        if self.cache.get(key).is_some_and(|v| v.as_str() == password) {
            return Ok(());
        }
        self.cache.remove(key);
        let bytes = Zeroizing::new(
            serde_json::to_vec(&Payload {
                version: 1,
                password: password.into(),
            })
            .map_err(|_| Error::Invalid)?,
        );
        self.backend.set(key, &bytes)?;
        self.cache(key, Zeroizing::new(password.into()));
        Ok(())
    }
    pub fn delete(&mut self, key: &str) -> Result<()> {
        Self::validate(key)?;
        self.cache.remove(key);
        self.backend.delete(key)
    }
    pub fn available() -> bool {
        cfg!(any(target_os = "macos", target_os = "windows"))
    }
}

pub struct Native;

#[cfg(target_os = "macos")]
#[allow(unsafe_code)]
mod macos {
    use super::*;
    use core_foundation::{base::TCFType, data::CFData};
    use security_framework::{
        base::Error as OsError,
        item::{
            ItemAddOptions, ItemAddValue, ItemClass, ItemSearchOptions, ItemUpdateOptions,
            ItemUpdateValue, update_item,
        },
        os::macos::{keychain_item::SecKeychainItem, passwords::find_generic_password},
    };
    use security_framework_sys::{
        keychain::{
            SecKeychainFindGenericPassword, SecKeychainGetUserInteractionAllowed,
            SecKeychainSetUserInteractionAllowed,
        },
        keychain_item::SecKeychainItemDelete,
    };
    use std::sync::Mutex;
    static KEYCHAIN: Mutex<()> = Mutex::new(());
    struct Silent {
        previous: u8,
        restored: bool,
    }
    impl Silent {
        fn begin() -> Result<Self> {
            let mut previous = 0;
            if unsafe { SecKeychainGetUserInteractionAllowed(&mut previous) } != 0
                || unsafe { SecKeychainSetUserInteractionAllowed(0) } != 0
            {
                return Err(Error::Denied);
            }
            Ok(Self {
                previous,
                restored: false,
            })
        }
        fn restore(mut self) -> Result<()> {
            let status = unsafe { SecKeychainSetUserInteractionAllowed(self.previous) };
            self.restored = status == 0;
            if status == 0 {
                Ok(())
            } else {
                Err(Error::Denied)
            }
        }
    }
    impl Drop for Silent {
        fn drop(&mut self) {
            if !self.restored {
                unsafe { SecKeychainSetUserInteractionAllowed(self.previous) };
            }
        }
    }
    fn error(e: OsError) -> Error {
        if e.code() == -25300 {
            Error::NotFound
        } else {
            Error::Denied
        }
    }
    impl Backend for Native {
        fn has(&self, key: &str) -> Result<bool> {
            let _lock = KEYCHAIN.lock().map_err(|_| Error::Denied)?;
            let silent = Silent::begin()?;
            let result = match ItemSearchOptions::new()
                .class(ItemClass::generic_password())
                .service(service(key))
                .account(key)
                .load_attributes(true)
                .search()
            {
                Ok(items) => Ok(!items.is_empty()),
                Err(e) if e.code() == -25300 => Ok(false),
                Err(e) if e.code() == -25308 => Ok(true),
                Err(e) => Err(error(e)),
            };
            silent.restore()?;
            result
        }
        fn get(&self, key: &str) -> Result<Vec<u8>> {
            let _lock = KEYCHAIN.lock().map_err(|_| Error::Denied)?;
            // Read legacy items directly once; do not use SecItem's compatibility
            // read path, which previously caused duplicate authorization dialogs.
            let (data, _item) = find_generic_password(None, service(key), key).map_err(error)?;
            Ok(data.as_ref().to_vec())
        }
        fn set(&self, key: &str, value: &[u8]) -> Result<()> {
            let _lock = KEYCHAIN.lock().map_err(|_| Error::Denied)?;
            let mut search = ItemSearchOptions::new();
            search
                .class(ItemClass::generic_password())
                .service(service(key))
                .account(key);
            let mut changes = ItemUpdateOptions::new();
            changes.set_value(ItemUpdateValue::Data(CFData::from_buffer(value)));
            match update_item(&search, &changes) {
                Ok(()) => return Ok(()),
                Err(e) if e.code() == -25300 => {}
                Err(e) => return Err(error(e)),
            }
            let mut add = ItemAddOptions::new(ItemAddValue::Data {
                class: ItemClass::generic_password(),
                data: CFData::from_buffer(value),
            });
            add.set_account_name(key)
                .set_service(service(key))
                .set_label(if key.starts_with("account-token:") {
                    "Resona account login"
                } else {
                    "Resona server password"
                });
            match add.add() {
                Ok(()) => Ok(()),
                Err(e) if e.code() == -25299 => update_item(&search, &changes).map_err(error),
                Err(e) => Err(error(e)),
            }
        }
        fn delete(&self, key: &str) -> Result<()> {
            let _lock = KEYCHAIN.lock().map_err(|_| Error::Denied)?;
            let service = service(key);
            let mut item = std::ptr::null_mut();
            // Match the legacy read path, but find only the reference, never password data.
            let status = unsafe {
                SecKeychainFindGenericPassword(
                    std::ptr::null(),
                    service.len() as u32,
                    service.as_ptr().cast(),
                    key.len() as u32,
                    key.as_ptr().cast(),
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    &mut item,
                )
            };
            if status == -25300 {
                return Ok(());
            }
            if status != 0 {
                return Err(Error::System(status));
            }
            if item.is_null() {
                return Err(Error::Unavailable);
            }
            let item = unsafe { SecKeychainItem::wrap_under_create_rule(item) };
            match unsafe { SecKeychainItemDelete(item.as_concrete_TypeRef()) } {
                0 | -25300 => Ok(()),
                status => Err(Error::System(status)),
            }
        }
    }
}

#[cfg(target_os = "windows")]
#[allow(unsafe_code)]
mod windows {
    use super::*;
    use windows_sys::Win32::{
        Foundation::{ERROR_NOT_FOUND, GetLastError},
        Security::Credentials::*,
    };
    fn target(key: &str) -> Vec<u16> {
        format!("{}/{key}\0", service(key)).encode_utf16().collect()
    }
    fn error() -> Error {
        if unsafe { GetLastError() } == ERROR_NOT_FOUND {
            Error::NotFound
        } else {
            Error::Denied
        }
    }
    impl Backend for Native {
        fn has(&self, key: &str) -> Result<bool> {
            match self.get(key) {
                Ok(mut b) => {
                    use zeroize::Zeroize;
                    b.zeroize();
                    Ok(true)
                }
                Err(Error::NotFound) => Ok(false),
                Err(e) => Err(e),
            }
        }
        fn get(&self, key: &str) -> Result<Vec<u8>> {
            let name = target(key);
            let mut item = std::ptr::null_mut();
            if unsafe { CredReadW(name.as_ptr(), CRED_TYPE_GENERIC, 0, &mut item) } == 0 {
                return Err(error());
            }
            if item.is_null() {
                return Err(Error::Invalid);
            }
            let result = unsafe {
                let credential = &*item;
                if credential.Type != CRED_TYPE_GENERIC
                    || credential.CredentialBlobSize > 2560
                    || (credential.CredentialBlobSize > 0 && credential.CredentialBlob.is_null())
                {
                    Err(Error::Invalid)
                } else if credential.CredentialBlobSize == 0 {
                    Ok(Vec::new())
                } else {
                    let bytes = std::slice::from_raw_parts_mut(
                        credential.CredentialBlob,
                        credential.CredentialBlobSize as usize,
                    );
                    let copy = bytes.to_vec();
                    use zeroize::Zeroize;
                    bytes.zeroize();
                    Ok(copy)
                }
            };
            unsafe { CredFree(item.cast()) };
            result
        }
        fn set(&self, key: &str, value: &[u8]) -> Result<()> {
            if value.is_empty() || value.len() > 2560 {
                return Err(Error::Invalid);
            }
            let mut name = target(key);
            let c = CREDENTIALW {
                Type: CRED_TYPE_GENERIC,
                TargetName: name.as_mut_ptr(),
                CredentialBlobSize: value.len() as u32,
                CredentialBlob: value.as_ptr().cast_mut(),
                Persist: CRED_PERSIST_LOCAL_MACHINE,
                ..Default::default()
            };
            if unsafe { CredWriteW(&c, 0) } == 0 {
                Err(error())
            } else {
                Ok(())
            }
        }
        fn delete(&self, key: &str) -> Result<()> {
            let name = target(key);
            if unsafe { CredDeleteW(name.as_ptr(), CRED_TYPE_GENERIC, 0) } == 0
                && error() != Error::NotFound
            {
                Err(Error::Denied)
            } else {
                Ok(())
            }
        }
    }
}

#[cfg(not(any(target_os = "macos", target_os = "windows")))]
impl Backend for Native {
    fn has(&self, _: &str) -> Result<bool> {
        Ok(false)
    }
    fn get(&self, _: &str) -> Result<Vec<u8>> {
        Err(Error::Unavailable)
    }
    fn set(&self, _: &str, _: &[u8]) -> Result<()> {
        Err(Error::Unavailable)
    }
    fn delete(&self, _: &str) -> Result<()> {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[cfg(any(target_os = "macos", target_os = "windows"))]
    #[test]
    fn native_storage_preserves_format_across_fresh_stores() {
        if std::env::var("RESONA_CREDENTIAL_INTEGRATION").as_deref() != Ok("1") {
            return;
        }
        let mut random = [0u8; 16];
        getrandom::fill(&mut random).unwrap();
        let key = format!(
            "rust-integration-{}",
            random
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect::<String>()
        );
        struct Cleanup(String);
        impl Drop for Cleanup {
            fn drop(&mut self) {
                let _ = Native.delete(&self.0);
            }
        }
        for key in [key.clone(), format!("account-token:{key}")] {
            let _cleanup = Cleanup(key.clone());
            assert!(!Native.has(&key).unwrap());
            Store::new(Native).set(&key, "test-only-first").unwrap();
            let mut store = Store::new(Native);
            assert_eq!(store.get(&key).unwrap().as_str(), "test-only-first");
            store.set(&key, "test-only-updated").unwrap();
            assert_eq!(
                Store::new(Native).get(&key).unwrap().as_str(),
                "test-only-updated"
            );
            store.delete(&key).unwrap();
            assert!(!Native.has(&key).unwrap());
            store.delete(&key).unwrap();
        }
    }
    use std::cell::Cell;
    struct Mock {
        reads: Cell<u32>,
        writes: Cell<u32>,
    }
    impl Backend for Mock {
        fn has(&self, _: &str) -> Result<bool> {
            Ok(true)
        }
        fn get(&self, _: &str) -> Result<Vec<u8>> {
            self.reads.set(self.reads.get() + 1);
            Ok(br#"{"version":1,"password":"test-only"}"#.to_vec())
        }
        fn set(&self, _: &str, _: &[u8]) -> Result<()> {
            self.writes.set(self.writes.get() + 1);
            Ok(())
        }
        fn delete(&self, _: &str) -> Result<()> {
            Ok(())
        }
    }
    #[test]
    fn successful_reads_and_same_password_saves_do_not_reauthorize() {
        let mut s = Store::new(Mock {
            reads: Cell::new(0),
            writes: Cell::new(0),
        });
        assert!(s.has("key").unwrap());
        assert_eq!(s.backend.reads.get(), 0);
        s.get("key").unwrap();
        s.get("key").unwrap();
        s.set("key", "test-only").unwrap();
        assert_eq!(s.backend.reads.get(), 1);
        assert_eq!(s.backend.writes.get(), 0);
        s.delete("key").unwrap();
        s.get("key").unwrap();
        assert_eq!(s.backend.reads.get(), 2);
    }
}
