//! OS credential storage uses the existing service, account hash and payload.
//! All methods are blocking: callers run them away from UI and audio workers.
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use zeroize::Zeroizing;

const SERVICE: &str = "io.github.tsukiyoz.resona.server-password";
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Error {
    NotFound,
    Unavailable,
    Denied,
    Invalid,
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
    use core_foundation::data::CFData;
    use security_framework::{
        base::Error as OsError,
        item::{
            ItemAddOptions, ItemAddValue, ItemClass, ItemSearchOptions, ItemUpdateOptions,
            ItemUpdateValue, update_item,
        },
        os::macos::passwords::find_generic_password,
        passwords::delete_generic_password,
    };
    use security_framework_sys::keychain::{
        SecKeychainGetUserInteractionAllowed, SecKeychainSetUserInteractionAllowed,
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
                .service(SERVICE)
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
            let (data, _item) = find_generic_password(None, SERVICE, key).map_err(error)?;
            Ok(data.as_ref().to_vec())
        }
        fn set(&self, key: &str, value: &[u8]) -> Result<()> {
            let _lock = KEYCHAIN.lock().map_err(|_| Error::Denied)?;
            let mut search = ItemSearchOptions::new();
            search
                .class(ItemClass::generic_password())
                .service(SERVICE)
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
                .set_service(SERVICE)
                .set_label("Resona server password");
            match add.add() {
                Ok(()) => Ok(()),
                Err(e) if e.code() == -25299 => update_item(&search, &changes).map_err(error),
                Err(e) => Err(error(e)),
            }
        }
        fn delete(&self, key: &str) -> Result<()> {
            let _lock = KEYCHAIN.lock().map_err(|_| Error::Denied)?;
            match delete_generic_password(SERVICE, key) {
                Ok(()) => Ok(()),
                Err(e) if e.code() == -25300 => Ok(()),
                Err(e) => Err(error(e)),
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
        format!("{SERVICE}/{key}\0").encode_utf16().collect()
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
