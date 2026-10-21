use super::{
    Chain, Decoder, Encoder, FRAME, Factory, Limiter, Processing, Result, active, level_db,
};
use crate::{lifecycle, session};
use serde::{Deserialize, Serialize};
use std::{
    collections::{HashMap, VecDeque},
    time::Duration,
};
use tokio::time::Instant;

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default, deny_unknown_fields)]
pub struct Config {
    pub auto_unmute_on_connect: bool,
    pub enabled: bool,
    pub muted: bool,
    pub deafened: bool,
    #[serde(rename = "inputDeviceID")]
    pub input_device_id: String,
    #[serde(rename = "outputDeviceID")]
    pub output_device_id: String,
    pub volume: i32,
    pub input_gain: i32,
    pub activation_mode: String,
    #[serde(rename = "vadThresholdDB")]
    pub vad_threshold_db: i32,
    pub processing: Processing,
    pub ducking: bool,
}
impl Default for Config {
    fn default() -> Self {
        Self {
            auto_unmute_on_connect: false,
            enabled: false,
            muted: true,
            deafened: false,
            input_device_id: String::new(),
            output_device_id: String::new(),
            volume: 100,
            input_gain: 100,
            activation_mode: String::new(),
            vad_threshold_db: 0,
            processing: Processing::default(),
            ducking: false,
        }
    }
}
impl Config {
    pub fn validate(&self) -> Result<()> {
        if !(0..=794).contains(&self.volume) || !(0..=200).contains(&self.input_gain) {
            return Err("音量超出允许范围".into());
        }
        if !matches!(
            self.activation_mode.as_str(),
            "" | "continuous" | "ptt" | "vad"
        ) || !(-60..=0).contains(&self.vad_threshold_db)
        {
            return Err("语音激活设置无效".into());
        }
        for id in [&self.input_device_id, &self.output_device_id] {
            if id.len() > 1024 || id.contains('\0') {
                return Err("音频设备 ID 无效".into());
            }
        }
        Ok(())
    }
    pub fn same_devices(&self, other: &Self) -> bool {
        self.enabled == other.enabled
            && self.muted == other.muted
            && self.deafened == other.deafened
            && self.input_device_id == other.input_device_id
            && self.output_device_id == other.output_device_id
            && self.activation_mode == other.activation_mode
    }
}
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Peer {
    pub instance: String,
    pub epoch: u32,
    pub volume: i32,
    pub muted: bool,
}
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Route {
    pub generation: u64,
    pub epoch: u32,
    pub channel: u32,
    pub self_id: u16,
    pub bitrate: u32,
    pub peers: HashMap<u16, Peer>,
}
impl Route {
    pub fn from_snapshot(generation: u64, snapshot: &session::Snapshot) -> Self {
        let state = &snapshot.state;
        let channel = snapshot.channel();
        Self {
            generation,
            epoch: state.epoch,
            channel,
            self_id: state.self_ as u16,
            bitrate: snapshot.bitrate(),
            peers: state
                .members
                .iter()
                .filter(|m| m.channel == channel && m.id != state.self_)
                .map(|m| {
                    (
                        m.id as u16,
                        Peer {
                            instance: m.instance.clone(),
                            epoch: m.epoch,
                            volume: 100,
                            muted: false,
                        },
                    )
                })
                .collect(),
        }
    }
    fn accepts(&self, packet: &lifecycle::VoicePacket, now: Instant) -> bool {
        packet.generation == self.generation
            && packet.packet.epoch == self.epoch
            && now.saturating_duration_since(packet.packet.at) <= Duration::from_millis(100)
            && self.peers.get(&packet.packet.sender).is_some_and(|peer| {
                peer.instance == packet.packet.instance && peer.epoch == packet.packet.sender_epoch
            })
            && packet.packet.sender != self.self_id
    }
    pub(crate) fn same_scope(&self, other: &Self) -> bool {
        self.generation == other.generation
            && self.epoch == other.epoch
            && self.channel == other.channel
            && self.self_id == other.self_id
    }
}
#[derive(Clone, Debug, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Counters {
    pub received: u64,
    pub dropped: u64,
    pub decoded: u64,
    pub decode_errors: u64,
    pub sent: u64,
    pub send_errors: u64,
    pub capture_drops: u64,
}

struct Jitter {
    packets: HashMap<u16, session::VoicePacket>,
    expected: Option<u16>,
    started: bool,
    ended: bool,
}
enum Pop {
    Waiting,
    Lost,
    Packet(session::VoicePacket),
}
impl Default for Jitter {
    fn default() -> Self {
        Self {
            packets: HashMap::with_capacity(32),
            expected: None,
            started: false,
            ended: false,
        }
    }
}
impl Jitter {
    fn push(&mut self, packet: session::VoicePacket) -> bool {
        let expected = *self.expected.get_or_insert(packet.sequence);
        if (packet.sequence.wrapping_sub(expected) as i16) < 0
            || self.packets.contains_key(&packet.sequence)
            || self.packets.len() >= 32
        {
            return false;
        }
        self.ended |= packet.end;
        self.packets.insert(packet.sequence, packet);
        true
    }
    fn pop(&mut self) -> Pop {
        if !self.started {
            if self.packets.len() < 3 && !self.ended {
                return Pop::Waiting;
            }
            self.started = true;
        }
        let Some(expected) = self.expected else {
            return Pop::Waiting;
        };
        self.expected = Some(expected.wrapping_add(1));
        self.packets
            .remove(&expected)
            .map_or(Pop::Lost, Pop::Packet)
    }
}
struct Speaker {
    instance: String,
    epoch: u32,
    decoder: Decoder,
    jitter: Jitter,
    pcm: VecDeque<f32>,
    last: Instant,
    ended: bool,
    buffer: Box<[f32; FRAME * 6]>,
}
struct Receive {
    instance: String,
    chain: Option<Chain>,
    last: Instant,
    error: String,
}
struct DecodeFailure {
    first: Instant,
    count: u32,
}
pub struct Pipeline {
    pub config: Config,
    pub route: Route,
    pub counters: Counters,
    pub receive_error: String,
    factory: Factory,
    encoder: Encoder,
    pre: Chain,
    speakers: HashMap<u16, Speaker>,
    receivers: HashMap<u16, Receive>,
    speaking: HashMap<u16, Instant>,
    failures: HashMap<u16, DecodeFailure>,
    limiter: Limiter,
    vad_until: Instant,
    local_until: Instant,
}
impl Pipeline {
    pub fn new(config: Config, route: Route, factory: Factory) -> Result<Self> {
        config.validate()?;
        let pre = factory.build(&config.processing.preprocess, &["aec", "ans"])?;
        factory.build(&config.processing.postprocess, &["agc"])?;
        let encoder = Encoder::new(if route.bitrate == 0 {
            48_000
        } else {
            route.bitrate
        })?;
        Ok(Self {
            config,
            route,
            counters: Counters::default(),
            receive_error: String::new(),
            factory,
            encoder,
            pre,
            speakers: HashMap::with_capacity(64),
            receivers: HashMap::with_capacity(64),
            speaking: HashMap::with_capacity(64),
            failures: HashMap::with_capacity(64),
            limiter: Limiter::default(),
            vad_until: Instant::now(),
            local_until: Instant::now(),
        })
    }
    pub fn configure(&mut self, config: Config) -> Result<()> {
        config.validate()?;
        let replacement = if config.processing.preprocess != self.config.processing.preprocess {
            Some(
                self.factory
                    .build(&config.processing.preprocess, &["aec", "ans"])?,
            )
        } else {
            None
        };
        let post_changed = config.processing.postprocess != self.config.processing.postprocess;
        if post_changed {
            self.factory
                .build(&config.processing.postprocess, &["agc"])?;
        }
        if let Some(replacement) = replacement {
            self.pre = replacement;
        }
        if post_changed || !self.receive_error.is_empty() {
            self.receivers.clear();
            self.receive_error.clear();
        }
        if config.activation_mode != self.config.activation_mode || config.muted || !config.enabled
        {
            self.local_until = Instant::now();
            self.vad_until = Instant::now();
        }
        self.config = config;
        Ok(())
    }
    pub fn route(&mut self, route: Route) -> Result<bool> {
        let changed = !self.route.same_scope(&route);
        let pre = if changed {
            Some(
                self.factory
                    .build(&self.config.processing.preprocess, &["aec", "ans"])?,
            )
        } else {
            None
        };
        self.encoder.bitrate(if route.bitrate == 0 {
            48_000
        } else {
            route.bitrate
        })?;
        if let Some(pre) = pre {
            self.pre = pre;
            self.speakers.clear();
            self.receivers.clear();
            self.speaking.clear();
            self.failures.clear();
            self.local_until = Instant::now();
            self.vad_until = Instant::now();
            self.limiter = Limiter::default();
        } else {
            self.speaking.retain(|id, _| {
                self.route
                    .peers
                    .get(id)
                    .zip(route.peers.get(id))
                    .is_some_and(|(old, new)| {
                        old.instance == new.instance && old.epoch == new.epoch
                    })
            });
            self.failures.retain(|id, _| {
                self.route
                    .peers
                    .get(id)
                    .zip(route.peers.get(id))
                    .is_some_and(|(old, new)| {
                        old.instance == new.instance && old.epoch == new.epoch
                    })
            });
            self.speakers.retain(|id, s| {
                route
                    .peers
                    .get(id)
                    .is_some_and(|p| p.instance == s.instance && p.epoch == s.epoch)
            });
            self.receivers.retain(|id, s| {
                route
                    .peers
                    .get(id)
                    .is_some_and(|p| p.instance == s.instance)
            });
        }
        self.route = route;
        Ok(changed)
    }
    pub fn release_ptt(&mut self) {
        if self.config.activation_mode == "ptt" {
            self.local_until = Instant::now();
        }
    }
    pub fn capture(
        &mut self,
        pcm: &mut [f32; FRAME],
        reference: &[f32; FRAME],
        ptt: bool,
        monitor: bool,
        now: Instant,
        out: &mut [u8; 1024],
    ) -> Result<(Option<usize>, i32)> {
        self.pre.process(pcm, Some(reference))?;
        let level = level_db(pcm);
        let gain = self.config.input_gain as f32 / 100.0;
        for sample in pcm.iter_mut() {
            *sample = (*sample * gain).clamp(-1.0, 1.0);
        }
        if monitor {
            return Ok((None, level_db(pcm)));
        }
        if !self.config.enabled || self.config.muted || self.config.deafened {
            return Ok((None, level));
        }
        let open = match self.config.activation_mode.as_str() {
            "ptt" => ptt,
            "vad" => {
                if level >= self.config.vad_threshold_db {
                    self.vad_until = now + Duration::from_millis(250);
                }
                self.vad_until > now
            }
            _ => true,
        };
        if !open {
            return Ok((None, level));
        }
        Ok((Some(self.encoder.encode(pcm, out)?), level))
    }
    pub fn sent(&mut self, pcm: &[f32; FRAME], now: Instant) {
        self.counters.sent += 1;
        if active(pcm) {
            self.local_until = now + Duration::from_millis(300);
        }
    }
    fn decode_failed(&mut self, id: u16, now: Instant) {
        self.counters.decode_errors += 1;
        if self.failures.len() >= 64 && !self.failures.contains_key(&id) {
            return;
        }
        let failure = self.failures.entry(id).or_insert(DecodeFailure {
            first: now,
            count: 0,
        });
        if now.saturating_duration_since(failure.first) > Duration::from_secs(2) {
            failure.first = now;
            failure.count = 0;
        }
        failure.count += 1;
    }
    pub fn error(&self) -> String {
        if !self.receive_error.is_empty() {
            return self.receive_error.clone();
        }
        if self.failures.values().any(|failure| failure.count >= 3) {
            return "部分成员的语音解码失败，请尝试重新进入频道".into();
        }
        String::new()
    }
    pub fn push(&mut self, packet: lifecycle::VoicePacket, now: Instant) {
        self.counters.received += 1;
        if self.config.deafened || !self.route.accepts(&packet, now) {
            self.counters.dropped += 1;
            return;
        }
        let packet = packet.packet;
        if !packet.end && Decoder::packet_samples(&packet.data).is_none() {
            self.decode_failed(packet.sender, now);
            return;
        }
        let id = packet.sender;
        if self
            .speakers
            .get(&id)
            .is_some_and(|s| s.instance != packet.instance || s.epoch != packet.sender_epoch)
        {
            self.speakers.remove(&id);
        }
        if !self.speakers.contains_key(&id) {
            if packet.end {
                return;
            }
            if self.speakers.len() >= 64 {
                self.counters.dropped += 1;
                return;
            }
            let decoder = match Decoder::new() {
                Ok(d) => d,
                Err(_) => {
                    self.counters.decode_errors += 1;
                    return;
                }
            };
            self.speakers.insert(
                id,
                Speaker {
                    instance: packet.instance.clone(),
                    epoch: packet.sender_epoch,
                    decoder,
                    jitter: Jitter::default(),
                    pcm: VecDeque::with_capacity(FRAME * 7),
                    last: packet.at,
                    ended: false,
                    buffer: Box::new([0.0; FRAME * 6]),
                },
            );
        }
        let speaker = self.speakers.get_mut(&id).unwrap();
        let at = packet.at;
        if speaker.jitter.push(packet) {
            speaker.last = at;
        } else {
            self.counters.dropped += 1;
        }
    }
    pub fn render(&mut self, now: Instant, out: &mut [f32; FRAME * 2]) -> Result<bool> {
        out.fill(0.0);
        self.receivers.retain(|id, r| {
            now.saturating_duration_since(r.last) <= Duration::from_secs(30)
                && self
                    .route
                    .peers
                    .get(id)
                    .is_some_and(|p| p.instance == r.instance)
        });
        self.speaking
            .retain(|id, until| *until > now && self.route.peers.contains_key(id));
        let mut removed = [0_u16; 64];
        let mut remove_count = 0;
        let spec = &self.config.processing.postprocess[0];
        let post_enabled = spec.backend != "none" && !spec.backend.is_empty();
        let mut mixed = false;
        for (&id, speaker) in &mut self.speakers {
            let Some(peer) = self
                .route
                .peers
                .get(&id)
                .filter(|p| p.instance == speaker.instance && p.epoch == speaker.epoch)
            else {
                removed[remove_count] = id;
                remove_count += 1;
                continue;
            };
            if now.saturating_duration_since(speaker.last) > Duration::from_millis(500) {
                removed[remove_count] = id;
                remove_count += 1;
                continue;
            }
            let gain = if peer.muted || self.config.deafened {
                0.0
            } else {
                peer.volume.clamp(0, 794) as f32 / 100.0 * self.config.volume as f32 / 100.0
            };
            if post_enabled && gain != 0.0 && !self.receivers.contains_key(&id) {
                if self.receivers.len() >= 64
                    && let Some((&oldest, _)) = self.receivers.iter().min_by_key(|(_, r)| r.last)
                {
                    self.receivers.remove(&oldest);
                }
                let chain = match self.factory.build(std::slice::from_ref(spec), &["agc"]) {
                    Ok(chain) => Some(chain),
                    Err(error) => {
                        self.receive_error = error;
                        None
                    }
                };
                self.receivers.insert(
                    id,
                    Receive {
                        instance: peer.instance.clone(),
                        chain,
                        last: now,
                        error: String::new(),
                    },
                );
            }
            let receive = if post_enabled && gain != 0.0 {
                self.receivers.get_mut(&id)
            } else {
                None
            };
            let decoded_before = self.counters.decoded;
            match speaker.render(out, gain, receive, now, &mut self.counters) {
                Ok((has_frame, active, ended)) => {
                    if self.counters.decoded > decoded_before {
                        self.failures.remove(&id);
                    }
                    mixed |= has_frame;
                    if active {
                        self.speaking.insert(id, now + Duration::from_millis(300));
                    }
                    if ended {
                        removed[remove_count] = id;
                        remove_count += 1;
                    }
                }
                Err(_) => {
                    self.counters.decode_errors += 1;
                    if self.failures.len() < 64 || self.failures.contains_key(&id) {
                        let failure = self.failures.entry(id).or_insert(DecodeFailure {
                            first: now,
                            count: 0,
                        });
                        if now.saturating_duration_since(failure.first) > Duration::from_secs(2) {
                            failure.first = now;
                            failure.count = 0;
                        }
                        failure.count += 1;
                    }
                    removed[remove_count] = id;
                    remove_count += 1;
                }
            }
            if let Some(receive) = self.receivers.get(&id)
                && !receive.error.is_empty()
            {
                self.receive_error.clone_from(&receive.error);
            }
        }
        for id in &removed[..remove_count] {
            self.speakers.remove(id);
        }
        let duck = if self.config.ducking && self.local_until > now {
            0.25
        } else {
            1.0
        };
        self.limiter.apply(out, duck);
        Ok(mixed)
    }
    pub fn speaking(&self, now: Instant) -> (bool, Vec<u16>) {
        let mut ids: Vec<_> = self
            .speaking
            .iter()
            .filter(|(_, until)| **until > now)
            .map(|(&id, _)| id)
            .collect();
        ids.sort_unstable();
        (self.local_until > now, ids)
    }
}
impl Speaker {
    fn render(
        &mut self,
        out: &mut [f32; FRAME * 2],
        gain: f32,
        mut receive: Option<&mut Receive>,
        now: Instant,
        counters: &mut Counters,
    ) -> Result<(bool, bool, bool)> {
        let mut audible = false;
        while self.pcm.len() < FRAME && !self.ended {
            let packet = self.jitter.pop();
            let (data, lost) = match &packet {
                Pop::Waiting => break,
                Pop::Lost => {
                    if now.saturating_duration_since(self.last) > Duration::from_millis(120) {
                        break;
                    }
                    (None, true)
                }
                Pop::Packet(p) => {
                    if p.end {
                        self.ended = true;
                        break;
                    }
                    (Some(p.data.as_slice()), false)
                }
            };
            let n = self.decoder.decode(data, &mut self.buffer)?;
            if !lost {
                counters.decoded += 1;
                audible |= active(&self.buffer[..n]);
            }
            if !lost && let Some(receive) = receive.as_mut() {
                receive.last = now;
                for chunk in self.buffer[..n].chunks_exact_mut(FRAME) {
                    if active(chunk) {
                        let original: [f32; FRAME] = chunk.try_into().unwrap();
                        if let Some(chain) = receive.chain.as_mut()
                            && let Err(error) = chain.process(chunk.try_into().unwrap(), None)
                        {
                            receive.chain = None;
                            receive.error = error;
                            chunk.copy_from_slice(&original);
                        }
                    }
                }
            }
            self.pcm.extend(&self.buffer[..n]);
        }
        if self.pcm.len() < FRAME && (!self.ended || self.pcm.is_empty()) {
            return Ok((false, audible, self.ended));
        }
        for frame in out.chunks_exact_mut(2) {
            let sample = self.pcm.pop_front().unwrap_or(0.0) * gain;
            frame[0] += sample;
            frame[1] += sample;
        }
        Ok((true, audible, self.ended && self.pcm.is_empty()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::audio::Spec;
    fn packet(sequence: u16, end: bool) -> session::VoicePacket {
        session::VoicePacket {
            epoch: 10,
            sender_epoch: 20,
            sender: 2,
            instance: "speaker".into(),
            sequence,
            end,
            data: Vec::new(),
            at: Instant::now(),
        }
    }
    fn config() -> Config {
        Config {
            enabled: true,
            muted: false,
            activation_mode: "continuous".into(),
            ..Default::default()
        }
    }
    fn pipeline() -> Pipeline {
        let route = Route {
            generation: 3,
            epoch: 10,
            channel: 1,
            self_id: 1,
            bitrate: 48_000,
            peers: HashMap::from([(
                2,
                Peer {
                    instance: "speaker".into(),
                    epoch: 20,
                    volume: 100,
                    muted: false,
                },
            )]),
        };
        Pipeline::new(
            config(),
            route,
            Factory::new(std::path::Path::new("/no-such-native-library")),
        )
        .unwrap()
    }
    #[test]
    fn jitter_reorders_wraps_rejects_duplicates_and_plays_short_end() {
        let mut jitter = Jitter::default();
        assert!(jitter.push(packet(65535, false)));
        assert!(jitter.push(packet(1, false)));
        assert!(jitter.push(packet(0, false)));
        for sequence in [65535, 0, 1] {
            assert!(matches!(jitter.pop(),Pop::Packet(p) if p.sequence==sequence));
        }
        assert!(!jitter.push(packet(0, false)));
        assert!(matches!(jitter.pop(), Pop::Lost));
        let mut short = Jitter::default();
        assert!(short.push(packet(1, false)));
        assert!(matches!(short.pop(), Pop::Waiting));
        assert!(short.push(packet(2, true)));
        assert!(matches!(short.pop(),Pop::Packet(p) if !p.end));
        assert!(matches!(short.pop(),Pop::Packet(p) if p.end));
    }
    #[test]
    fn consume_rechecks_generation_epoch_instance_and_age() {
        let mut p = pipeline();
        let now = Instant::now();
        for (generation, epoch, sender_epoch, instance, age) in [
            (2, 10, 20, "speaker", 0),
            (3, 9, 20, "speaker", 0),
            (3, 10, 19, "speaker", 0),
            (3, 10, 20, "previous", 0),
            (3, 10, 20, "speaker", 101),
        ] {
            let mut packet = packet(0, false);
            packet.epoch = epoch;
            packet.sender_epoch = sender_epoch;
            packet.instance = instance.into();
            packet.at = now - Duration::from_millis(age);
            p.push(lifecycle::VoicePacket { generation, packet }, now);
        }
        assert_eq!(p.counters.dropped, 5);
        assert!(p.speakers.is_empty());
    }
    #[test]
    fn mixer_mutes_and_scope_change_clears_audio() {
        let mut p = pipeline();
        let now = Instant::now();
        let mut encoder = Encoder::new(48_000).unwrap();
        let mut bytes = [0; 1024];
        for seq in 0..4 {
            let pcm = std::array::from_fn(|i| (i as f32 * 0.08).sin() * 0.05);
            let n = encoder.encode(&pcm, &mut bytes).unwrap();
            let mut packet = packet(seq, false);
            packet.data = bytes[..n].to_vec();
            packet.at = now;
            p.push(
                lifecycle::VoicePacket {
                    generation: 3,
                    packet,
                },
                now,
            );
        }
        let mut out = [0.0; FRAME * 2];
        assert!(p.render(now, &mut out).unwrap());
        assert!(out.iter().any(|x| x.abs() > 0.001));
        p.route.peers.get_mut(&2).unwrap().muted = true;
        assert!(p.render(now + Duration::from_millis(20), &mut out).unwrap());
        assert!(out.iter().all(|x| *x == 0.0));
        let mut next = p.route.clone();
        next.epoch += 1;
        assert!(p.route(next).unwrap());
        assert!(p.speakers.is_empty());
        assert!(p.receivers.is_empty());
        assert!(!p.render(now, &mut out).unwrap());
        assert!(out.iter().all(|x| *x == 0.0));
    }
    fn feed(p: &mut Pipeline, packets: &[Vec<u8>], id: u16, now: Instant) {
        for (seq, data) in packets.iter().enumerate() {
            let mut packet = packet(seq as u16, false);
            packet.sender = id;
            packet.data = data.clone();
            packet.at = now;
            p.push(
                lifecycle::VoicePacket {
                    generation: 3,
                    packet,
                },
                now,
            );
        }
    }
    fn encoded() -> Vec<Vec<u8>> {
        let mut encoder = Encoder::new(48_000).unwrap();
        let mut bytes = [0; 1024];
        (0..6)
            .map(|_| {
                let pcm = std::array::from_fn(|i| (i as f32 * 0.08).sin() * 0.05);
                let n = encoder.encode(&pcm, &mut bytes).unwrap();
                bytes[..n].to_vec()
            })
            .collect()
    }
    #[test]
    fn gains_multiply_and_bad_packets_recover_without_affecting_other_audio() {
        let now = Instant::now();
        let packets = encoded();
        let mut normal = pipeline();
        let mut amplified = pipeline();
        amplified.config.volume = 200;
        amplified.route.peers.get_mut(&2).unwrap().volume = 150;
        feed(&mut normal, &packets, 2, now);
        feed(&mut amplified, &packets, 2, now);
        let mut a = [0.0; FRAME * 2];
        let mut b = a;
        normal.render(now, &mut a).unwrap();
        amplified.render(now, &mut b).unwrap();
        for (a, b) in a.into_iter().zip(b) {
            assert!((b - a * 3.0).abs() < 0.00001);
        }
        for _ in 0..3 {
            normal.push(
                lifecycle::VoicePacket {
                    generation: 3,
                    packet: packet(100, false),
                },
                now,
            );
        }
        assert!(!normal.error().is_empty());
        assert_eq!(normal.counters.decode_errors, 3);
        normal
            .render(now + Duration::from_millis(20), &mut a)
            .unwrap();
        assert!(normal.error().is_empty());
        assert!(a.iter().any(|v| v.abs() > 0.001));
    }
    #[test]
    fn failed_agc_preserves_voice_reports_error_and_retries_on_confirmation() {
        let now = Instant::now();
        let packets = encoded();
        let mut p = pipeline();
        let mut expected = pipeline();
        p.config.processing.postprocess[0] = Spec {
            name: "agc".into(),
            backend: "speex".into(),
            ..Default::default()
        };
        p.receivers.insert(
            2,
            Receive {
                instance: "speaker".into(),
                chain: Some(Chain::failing()),
                last: now,
                error: String::new(),
            },
        );
        feed(&mut p, &packets, 2, now);
        feed(&mut expected, &packets, 2, now);
        let mut actual = [0.0; FRAME * 2];
        let mut original = actual;
        p.render(now, &mut actual).unwrap();
        expected.render(now, &mut original).unwrap();
        assert_eq!(actual, original);
        assert!(p.error().contains("test processor failure"));
        assert_eq!(p.counters.decode_errors, 0);
        p.configure(p.config.clone()).unwrap();
        assert!(p.error().is_empty());
        assert!(p.receivers.is_empty());
        p.render(now + Duration::from_millis(20), &mut actual)
            .unwrap();
        assert!(p.receivers[&2].chain.is_some());
    }
    #[test]
    fn local_speaking_requires_successful_send_and_ptt_release_clears_it() {
        let mut p = pipeline();
        p.config.activation_mode = "ptt".into();
        let now = Instant::now();
        let mut pcm = [0.1; FRAME];
        p.capture(&mut pcm, &[0.0; FRAME], true, false, now, &mut [0; 1024])
            .unwrap();
        assert!(!p.speaking(now).0);
        p.sent(&pcm, now);
        assert!(p.speaking(now).0);
        p.release_ptt();
        assert!(!p.speaking(Instant::now()).0);
    }
    #[test]
    fn ptt_vad_and_invalid_reconfiguration_preserve_intent() {
        let mut p = pipeline();
        p.config.activation_mode = "ptt".into();
        let mut out = [0; 1024];
        assert!(
            p.capture(
                &mut [0.1; FRAME],
                &[0.0; FRAME],
                false,
                false,
                Instant::now(),
                &mut out
            )
            .unwrap()
            .0
            .is_none()
        );
        assert!(
            p.capture(
                &mut [0.1; FRAME],
                &[0.0; FRAME],
                true,
                false,
                Instant::now(),
                &mut out
            )
            .unwrap()
            .0
            .is_some()
        );
        let original = p.config.clone();
        let mut invalid = original.clone();
        invalid.processing.preprocess[0] = Spec {
            name: "ans".into(),
            backend: "speex".into(),
            ..Default::default()
        };
        assert!(p.configure(invalid).is_err());
        assert_eq!(p.config, original);
        let mut vad = original;
        vad.activation_mode = "vad".into();
        vad.vad_threshold_db = -30;
        p.configure(vad).unwrap();
        let now = Instant::now();
        assert!(
            p.capture(
                &mut [0.0; FRAME],
                &[0.0; FRAME],
                false,
                false,
                now,
                &mut out
            )
            .unwrap()
            .0
            .is_none()
        );
        assert!(
            p.capture(
                &mut [0.1; FRAME],
                &[0.0; FRAME],
                false,
                false,
                now,
                &mut out
            )
            .unwrap()
            .0
            .is_some()
        );
        assert!(
            p.capture(
                &mut [0.0; FRAME],
                &[0.0; FRAME],
                false,
                false,
                now + Duration::from_millis(100),
                &mut out
            )
            .unwrap()
            .0
            .is_some()
        );
        assert!(
            p.capture(
                &mut [0.0; FRAME],
                &[0.0; FRAME],
                false,
                false,
                now + Duration::from_millis(300),
                &mut out
            )
            .unwrap()
            .0
            .is_none()
        );
    }
}
