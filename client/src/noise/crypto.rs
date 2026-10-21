use super::*;
use snow::{
    params::CipherChoice,
    resolvers::{CryptoResolver, DefaultResolver},
    types::Cipher,
};
use std::time::Duration;
use tokio::time::Instant;
use zeroize::Zeroize;

const PHASE: u8 = 0x80;
const MAX_COUNTER: u32 = 1 << 24;
const MAX_EPOCH: u64 = (1 << 40) - 2;
const REKEY_AFTER: Duration = Duration::from_secs(12 * 60 * 60);
const REKEY_RETRY: Duration = Duration::from_secs(5);
const CONFIRM_TIMEOUT: Duration = Duration::from_secs(30);
const OLD_GRACE: Duration = Duration::from_secs(3);

pub(super) struct Packet {
    bytes: [u8; MAX_PACKET],
    len: usize,
}

impl Packet {
    pub fn as_slice(&self) -> &[u8] {
        &self.bytes[..self.len]
    }
}

fn cipher(key: &[u8; 32]) -> Box<dyn Cipher> {
    let mut c = DefaultResolver
        .resolve_cipher(&CipherChoice::ChaChaPoly)
        .expect("ChaChaPoly is a required build feature");
    c.set(key);
    c
}

fn next_cipher(c: &dyn Cipher) -> Box<dyn Cipher> {
    let mut material = [0; 48];
    c.encrypt(u64::MAX, &[], &[0; 32], &mut material);
    let mut key = [0; 32];
    key.copy_from_slice(&material[..32]);
    let next = cipher(&key);
    key.zeroize();
    material.zeroize();
    next
}

#[derive(Default)]
struct Replay {
    highest: u32,
    bits: u64,
}

impl Replay {
    fn seen(&self, n: u32) -> bool {
        n == 0
            || n >= MAX_COUNTER
            || (n <= self.highest
                && (self.highest - n >= 64 || self.bits & (1 << (self.highest - n)) != 0))
    }

    fn add(&mut self, n: u32) {
        if n > self.highest {
            self.bits = self.bits.checked_shl(n - self.highest).unwrap_or(0);
            self.highest = n;
        }
        self.bits |= 1 << (self.highest - n);
    }
}

pub(super) struct SendKeys {
    cipher: Box<dyn Cipher>,
    epoch: u64,
    counter: u32,
    confirmed: bool,
    since: Instant,
    update_sent: Option<Instant>,
}

impl SendKeys {
    pub fn new(key: &[u8; 32], now: Instant) -> Self {
        Self {
            cipher: cipher(key),
            epoch: 0,
            counter: 0,
            confirmed: true,
            since: now,
            update_sent: None,
        }
    }

    pub fn refresh(&mut self, now: Instant) -> Result<Option<Packet>> {
        if !self.confirmed && now.duration_since(self.since) >= CONFIRM_TIMEOUT {
            return Err(Error::KeyConfirmationTimeout);
        }
        if self.confirmed
            && (self.counter >= MAX_COUNTER / 2 || now.duration_since(self.since) >= REKEY_AFTER)
        {
            if self.epoch >= MAX_EPOCH {
                return Err(Error::KeyExhausted);
            }
            self.cipher.rekey();
            self.epoch += 1;
            self.counter = 0;
            self.confirmed = false;
            self.since = now;
            self.update_sent = None;
        }
        if !self.confirmed
            && self
                .update_sent
                .is_none_or(|sent| now.duration_since(sent) >= REKEY_RETRY)
        {
            let p = self.encode(KEY_UPDATE, &self.epoch.to_be_bytes())?;
            self.update_sent = Some(now);
            return Ok(Some(p));
        }
        Ok(None)
    }

    pub fn confirm(&mut self, epoch: u64) {
        if epoch == self.epoch {
            self.confirmed = true;
        }
    }

    pub fn encode(&mut self, kind: u8, body: &[u8]) -> Result<Packet> {
        if body.len() + 21 > MAX_PACKET {
            return Err(Error::TooLarge);
        }
        if self.counter + 1 >= MAX_COUNTER {
            return Err(Error::KeyExhausted);
        }
        self.counter += 1;
        let mut p = Packet {
            bytes: [0; MAX_PACKET],
            len: body.len() + 21,
        };
        p.bytes[0] = kind | (((self.epoch & 1) as u8) * PHASE);
        p.bytes[1..5].copy_from_slice(&self.counter.to_be_bytes());
        let (head, tail) = p.bytes.split_at_mut(5);
        self.cipher.encrypt(
            (self.epoch << 24) | u64::from(self.counter),
            head,
            body,
            tail,
        );
        Ok(p)
    }
}

struct Previous {
    cipher: Box<dyn Cipher>,
    replay: Replay,
    until: Instant,
}

pub(super) struct ReceiveKeys {
    cipher: Box<dyn Cipher>,
    next: Box<dyn Cipher>,
    previous: Option<Previous>,
    epoch: u64,
    replay: Replay,
}

pub(super) struct Plaintext {
    pub kind: u8,
    pub epoch: u64,
    pub advanced: bool,
    bytes: [u8; MAX_PACKET],
    len: usize,
}

impl Plaintext {
    pub fn body(&self) -> &[u8] {
        &self.bytes[..self.len]
    }
}

impl ReceiveKeys {
    pub fn new(key: &[u8; 32]) -> Self {
        let c = cipher(key);
        Self {
            next: next_cipher(&*c),
            cipher: c,
            previous: None,
            epoch: 0,
            replay: Replay::default(),
        }
    }

    pub fn decode(&mut self, packet: &[u8], now: Instant) -> Option<Plaintext> {
        if !(21..=MAX_PACKET).contains(&packet.len()) {
            return None;
        }
        let n = u32::from_be_bytes(packet[1..5].try_into().ok()?);
        if n == 0 || n >= MAX_COUNTER {
            return None;
        }
        if self.previous.as_ref().is_some_and(|p| now >= p.until) {
            self.previous = None;
        }
        let mut p = Plaintext {
            kind: packet[0] & !PHASE,
            epoch: self.epoch,
            advanced: false,
            bytes: [0; MAX_PACKET],
            len: 0,
        };
        if packet[0] & PHASE == ((self.epoch & 1) as u8) * PHASE {
            if self.replay.seen(n) {
                return None;
            }
            p.len = self
                .cipher
                .decrypt(
                    (self.epoch << 24) | u64::from(n),
                    &packet[..5],
                    &packet[5..],
                    &mut p.bytes,
                )
                .ok()?;
            self.replay.add(n);
            return Some(p);
        }
        if let Some(old) = &mut self.previous
            && !old.replay.seen(n)
            && let Ok(len) = old.cipher.decrypt(
                ((self.epoch - 1) << 24) | u64::from(n),
                &packet[..5],
                &packet[5..],
                &mut p.bytes,
            )
        {
            old.replay.add(n);
            p.len = len;
            p.epoch -= 1;
            return Some(p);
        }
        if self.epoch >= MAX_EPOCH {
            return None;
        }
        p.len = self
            .next
            .decrypt(
                ((self.epoch + 1) << 24) | u64::from(n),
                &packet[..5],
                &packet[5..],
                &mut p.bytes,
            )
            .ok()?;
        let following = next_cipher(&*self.next);
        let current = std::mem::replace(
            &mut self.cipher,
            std::mem::replace(&mut self.next, following),
        );
        self.previous = Some(Previous {
            cipher: current,
            replay: std::mem::take(&mut self.replay),
            until: now + OLD_GRACE,
        });
        self.epoch += 1;
        self.replay.add(n);
        p.epoch = self.epoch;
        p.advanced = true;
        Some(p)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn go_reference_vectors_match_rekey_and_full_nonce() {
        // Generated by: go run ./client/tests/peer -vectors (fixed test key 0x07).
        let expected = [
            "0400000001dd365e055717c533dab0540b9525bea5314c1f3a538ecf133b7e",
            "84000000023d0f03d9940e28c1e28274bd737734288e5de909bac551e9bbea",
            "04000000027aeb435c90c16a191e39d52bd45bf958e96646c48153591e855e",
        ];
        let now = Instant::now();
        let mut tx = SendKeys::new(&[7; 32], now);
        let mut rx = ReceiveKeys::new(&[7; 32]);
        for (epoch, reference) in expected.iter().enumerate() {
            if epoch != 0 {
                tx.counter = MAX_COUNTER / 2;
                tx.confirm((epoch - 1) as u64);
                tx.refresh(now).unwrap();
            }
            let packet = tx.encode(VOICE, b"generation").unwrap();
            let hex = packet
                .as_slice()
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect::<String>();
            assert_eq!(&hex, reference);
            assert_eq!(
                rx.decode(packet.as_slice(), now).unwrap().body(),
                b"generation"
            );
        }
    }

    #[test]
    fn rekey_loss_replay_tampering_and_phase_wrap() {
        let now = Instant::now();
        let mut tx = SendKeys::new(&[7; 32], now);
        let mut rx = ReceiveKeys::new(&[7; 32]);
        let old = tx.encode(VOICE, b"old").unwrap();
        let expired = tx.encode(VOICE, b"expired").unwrap();
        tx.counter = MAX_COUNTER / 2;
        let update = tx.refresh(now).unwrap().unwrap();
        let fresh = tx.encode(VOICE, b"new").unwrap();
        let mut corrupt = fresh.as_slice().to_vec();
        *corrupt.last_mut().unwrap() ^= 1;
        assert!(rx.decode(&corrupt, now).is_none());
        assert_eq!(rx.epoch, 0);
        assert_eq!(rx.replay.highest, 0);
        let p = rx.decode(fresh.as_slice(), now).unwrap();
        assert!(p.advanced);
        assert_eq!(p.body(), b"new");
        assert_eq!(rx.decode(old.as_slice(), now).unwrap().body(), b"old");
        assert!(rx.decode(old.as_slice(), now).is_none());
        assert!(rx.decode(expired.as_slice(), now + OLD_GRACE).is_none());
        let retry = tx.refresh(now + REKEY_RETRY).unwrap().unwrap();
        assert_ne!(&retry.as_slice()[1..5], &update.as_slice()[1..5]);
        assert_eq!(rx.decode(retry.as_slice(), now).unwrap().kind, KEY_UPDATE);
        tx.confirm(1);
        tx.counter = MAX_COUNTER / 2;
        tx.refresh(now).unwrap();
        tx.confirm(1);
        assert!(!tx.confirmed);
        let third = tx.encode(VOICE, b"third").unwrap();
        assert_eq!(rx.decode(third.as_slice(), now).unwrap().epoch, 2);
        assert!(rx.decode(old.as_slice(), now).is_none());
        assert!(rx.decode(fresh.as_slice(), now).is_none());
        tx.confirm(2);
        assert!(tx.confirmed);
    }

    #[test]
    fn limits_age_and_unconfirmed_keys_fail_closed() {
        let now = Instant::now();
        let mut tx = SendKeys::new(&[9; 32], now);
        assert!(tx.refresh(now + REKEY_AFTER).unwrap().is_some());
        assert_eq!(
            tx.refresh(now + REKEY_AFTER + CONFIRM_TIMEOUT).err(),
            Some(Error::KeyConfirmationTimeout)
        );
        tx.counter = MAX_COUNTER - 1;
        assert_eq!(tx.encode(VOICE, b"x").err(), Some(Error::KeyExhausted));
        tx.confirmed = true;
        tx.epoch = MAX_EPOCH;
        assert_eq!(
            tx.refresh(now + REKEY_AFTER).err(),
            Some(Error::KeyExhausted)
        );
        assert_eq!(
            tx.encode(VOICE, &[0; MAX_PACKET]).err(),
            Some(Error::TooLarge)
        );
    }

    #[test]
    fn authenticated_header_and_large_replay_gap() {
        let now = Instant::now();
        let mut tx = SendKeys::new(&[1; 32], now);
        let mut rx = ReceiveKeys::new(&[1; 32]);
        let packet = tx.encode(VOICE, b"sample").unwrap();
        let mut bad = packet.as_slice().to_vec();
        bad[0] = CONTROL;
        assert!(rx.decode(&bad, now).is_none());
        assert!(rx.decode(packet.as_slice(), now).is_some());
        tx.counter = 100;
        let far = tx.encode(VOICE, b"far").unwrap();
        assert!(rx.decode(far.as_slice(), now).is_some());
        assert!(rx.decode(packet.as_slice(), now).is_none());
    }
}
