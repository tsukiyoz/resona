//! Shared Protobuf schema and transport-bound application identity proof.
//! No product connection state is published by this low-level module.

use crate::noise::{CancellationToken, ControlReceiver, ControlSender, Error, Result};
use ed25519_dalek::{Signer, SigningKey};
use prost::Message;
use sha2::{Digest, Sha256};
use tokio::time::Instant;

pub mod pb {
    include!(concat!(env!("OUT_DIR"), "/resona.control.v1.rs"));
}

const MAX_FRAME: usize = 65536;

pub fn pack<M: Message>(kind: pb::Kind, request: u32, body: &M) -> Result<Vec<u8>> {
    if kind == pb::Kind::Unspecified {
        return Err(Error::InvalidPacket);
    }
    if body.encoded_len() > MAX_FRAME {
        return Err(Error::TooLarge);
    }
    let frame = pb::Frame {
        kind: kind as i32,
        request,
        body: body.encode_to_vec(),
    };
    let len = frame.encoded_len();
    if len == 0 || len > MAX_FRAME {
        return Err(Error::TooLarge);
    }
    let mut data = Vec::with_capacity(4 + len);
    data.extend_from_slice(&(len as u32).to_be_bytes());
    frame.encode(&mut data).map_err(|_| Error::InvalidPacket)?;
    Ok(data)
}

pub async fn send<M: Message>(
    sender: &ControlSender,
    kind: pb::Kind,
    request: u32,
    body: &M,
    deadline: Instant,
    cancel: &CancellationToken,
) -> Result<()> {
    sender
        .write(&pack(kind, request, body)?, deadline, cancel)
        .await
}

/// Keep partial frame bytes across a read timeout or cancellation. A caller
/// may cancel waiting for a reply without corrupting a healthy voice session.
#[derive(Default)]
pub struct FrameReader {
    bytes: Vec<u8>,
}

impl FrameReader {
    pub async fn read(
        &mut self,
        receiver: &mut ControlReceiver,
        deadline: Instant,
        cancel: &CancellationToken,
    ) -> Result<pb::Frame> {
        loop {
            if cancel.is_cancelled() {
                return Err(Error::Cancelled);
            }
            if Instant::now() >= deadline {
                return Err(Error::Timeout);
            }
            let wanted = if self.bytes.len() < 4 {
                4
            } else {
                let n = u32::from_be_bytes(self.bytes[..4].try_into().unwrap()) as usize;
                if n == 0 || n > MAX_FRAME {
                    return Err(Error::InvalidPacket);
                }
                4 + n
            };
            if self.bytes.len() == wanted && wanted != 4 {
                let frame =
                    pb::Frame::decode(&self.bytes[4..]).map_err(|_| Error::InvalidPacket)?;
                if !(1..=255).contains(&frame.kind) {
                    return Err(Error::InvalidPacket);
                }
                self.bytes.clear();
                return Ok(frame);
            }
            let mut buffer = [0; 1000];
            let take = buffer.len().min(wanted - self.bytes.len());
            let n = receiver.read(&mut buffer[..take], deadline, cancel).await?;
            self.bytes.extend_from_slice(&buffer[..n]);
        }
    }
}

/// A seed is supplied by the caller. This foundation deliberately does not
/// create or replace the product's existing persistent identity file.
pub fn sign_hello(
    seed: &[u8; 32],
    binding: &[u8; 32],
    nickname: String,
    password: String,
) -> Result<pb::Hello> {
    let identity = SigningKey::from_bytes(seed);
    let mut hello = pb::Hello {
        nickname,
        password,
        public_key: identity.verifying_key().to_bytes().to_vec(),
        signature: Vec::new(),
    };
    let unsigned = pack(pb::Kind::Hello, 0, &hello)?;
    let mut digest = Sha256::new();
    digest.update(b"resona-client-identity-v1\0");
    digest.update(binding);
    digest.update(&unsigned);
    hello.signature = identity.sign(&digest.finalize()).to_bytes().to_vec();
    Ok(hello)
}

pub fn identity_uid(public: &[u8; 32]) -> String {
    let digest = Sha256::digest(public);
    use std::fmt::Write;
    let mut uid = String::with_capacity(64);
    for byte in digest {
        write!(uid, "{byte:02x}").unwrap();
    }
    uid
}

/// Validate application ranges before narrowing generated u32 fields. Admission
/// additionally requires a Welcome frame and the expected authenticated UID.
pub fn valid_state(state: &pb::State, expected_uid: &str) -> bool {
    let account_uid = |uid: &str| {
        uid.is_empty() || (uid.len() == 64 && uid.bytes().all(|b| b.is_ascii_hexdigit()))
    };
    if state.self_ == 0
        || state.self_ > u16::MAX.into()
        || state.epoch == 0
        || state.identity_uid != expected_uid
        || state.channels.is_empty()
        || state.channels.len() > 64
        || state.members.len() > 64
        || !account_uid(&state.account_uid)
        || state.account_name.len() > 32
        || (!state.account_uid.is_empty()
            && !matches!(state.server_role.as_str(), "member" | "admin" | "owner"))
    {
        return false;
    }
    let mut channels = Vec::with_capacity(state.channels.len());
    for ch in &state.channels {
        if ch.id == 0
            || ch.id > u16::MAX.into()
            || channels.contains(&ch.id)
            || (ch.bitrate != 0
                && (!(16000..=64000).contains(&ch.bitrate) || ch.bitrate % 1000 != 0))
        {
            return false;
        }
        channels.push(ch.id);
    }
    let mut members = Vec::with_capacity(state.members.len());
    for member in &state.members {
        if member.id == 0
            || member.id > u16::MAX.into()
            || member.epoch == 0
            || member.instance.is_empty()
            || !channels.contains(&member.channel)
            || members.contains(&member.id)
            || !account_uid(&member.account_uid)
            || (member.id == state.self_ && member.account_uid != state.account_uid)
        {
            return false;
        }
        members.push(member.id);
    }
    members.contains(&state.self_)
}

#[cfg(test)]
mod tests {
    use super::*;
    use ed25519_dalek::Verifier;

    #[test]
    fn identity_proof_binds_transport_and_login() {
        let seed = [19; 32];
        let hello = sign_hello(&seed, &[5; 32], "tester".into(), "test-only".into()).unwrap();
        let mut unsigned = hello.clone();
        unsigned.signature.clear();
        let mut hash = Sha256::new();
        hash.update(b"resona-client-identity-v1\0");
        hash.update([5; 32]);
        hash.update(pack(pb::Kind::Hello, 0, &unsigned).unwrap());
        let signature = ed25519_dalek::Signature::from_slice(&hello.signature).unwrap();
        let key = SigningKey::from_bytes(&seed).verifying_key();
        key.verify(&hash.finalize(), &signature).unwrap();
        assert_ne!(
            hello.signature,
            sign_hello(&seed, &[6; 32], "tester".into(), "test-only".into())
                .unwrap()
                .signature
        );
        assert_ne!(
            hello.signature,
            sign_hello(&seed, &[5; 32], "other".into(), "test-only".into())
                .unwrap()
                .signature
        );
    }

    #[test]
    fn frame_size_is_checked_before_transport() {
        assert_eq!(
            pack(
                pb::Kind::Chat,
                1,
                &pb::Command {
                    text: "x".repeat(MAX_FRAME),
                    ..Default::default()
                }
            ),
            Err(Error::TooLarge)
        );
    }

    #[test]
    fn welcome_validation_rejects_identity_ranges_and_duplicate_members() {
        let base = pb::State {
            self_: 1,
            epoch: 1,
            identity_uid: "known".into(),
            channels: vec![pb::Channel {
                id: 1,
                bitrate: 32000,
                ..Default::default()
            }],
            members: vec![pb::Member {
                id: 1,
                channel: 1,
                epoch: 1,
                instance: "session-1".into(),
                ..Default::default()
            }],
            ..Default::default()
        };
        assert!(valid_state(&base, "known"));
        assert!(!valid_state(&base, "different"));
        let mut invalid = base.clone();
        invalid.members.push(invalid.members[0].clone());
        assert!(!valid_state(&invalid, "known"));
        let mut invalid = base.clone();
        invalid.self_ = 65536;
        assert!(!valid_state(&invalid, "known"));
        let mut invalid = base;
        invalid.channels[0].bitrate = 32001;
        assert!(!valid_state(&invalid, "known"));
    }
}
