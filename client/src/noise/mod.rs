//! Resona RN06 transport; no UI, credentials, audio devices or NATS dependency.

mod crypto;
mod transport;

pub use transport::{Connection, ControlReceiver, ControlSender, VoiceReceiver, VoiceSender};

use std::{fmt, io};

pub use tokio_util::sync::CancellationToken;

pub(crate) const MAGIC: &[u8; 4] = b"RN06";
pub(crate) const PROLOGUE: &[u8] = b"resona-noise-exp-6";
pub(crate) const SUITE: &str = "Noise_NK_25519_ChaChaPoly_SHA256";
pub(crate) const MAX_PACKET: usize = 1200;
pub(crate) const MAX_CHUNK: usize = 1000;
pub(crate) const VOICE: u8 = 4;
pub(crate) const CONTROL: u8 = 5;
pub(crate) const ACK: u8 = 6;
pub(crate) const PING: u8 = 7;
pub(crate) const PONG: u8 = 8;
pub(crate) const CLOSE: u8 = 9;
pub(crate) const KEY_UPDATE: u8 = 10;
pub(crate) const KEY_ACK: u8 = 11;

/// Fixed messages only: callers must not attach passwords or payloads to diagnostics.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Error {
    Cancelled,
    Timeout,
    Authentication,
    Closed,
    RemoteClosed(u64),
    IdleTimeout,
    InvalidPacket,
    TooLarge,
    Busy,
    KeyExhausted,
    KeyConfirmationTimeout,
    Crypto,
    Io(io::ErrorKind),
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "Noise transport: {self:?}")
    }
}

impl std::error::Error for Error {}

impl From<io::Error> for Error {
    fn from(error: io::Error) -> Self {
        Self::Io(error.kind())
    }
}

pub type Result<T> = std::result::Result<T, Error>;
