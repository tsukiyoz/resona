//! Local listening sources using the same session, DSP and codec as the desktop.
use anyhow::{Context, Result, anyhow, bail, ensure};
use resona_client::{
    audio::{Encoder, FRAME, Factory, Params, RATE, Spec},
    config::{Identity, ServerProfile},
    session::{Command, Session},
};
use std::{
    io::{BufRead, BufReader, Read},
    net::SocketAddr,
    path::{Path, PathBuf},
    process::{Child, Stdio},
    time::Duration,
};
use tokio::time::{Instant, MissedTickBehavior};
use tokio_util::sync::CancellationToken;

fn pcm_bytes(data: &[u8]) -> Result<Vec<f32>> {
    ensure!(
        data.len() >= FRAME * 4
            && data.len() <= RATE as usize * 120 * 4
            && data.len().is_multiple_of(4),
        "input must be 48 kHz mono f32le PCM, 20 ms to 120 s"
    );
    let mut pcm = Vec::with_capacity((data.len() / 4).div_ceil(FRAME) * FRAME);
    for bytes in data.chunks_exact(4) {
        let sample = f32::from_le_bytes(bytes.try_into()?);
        ensure!(
            sample.is_finite() && (-1.0..=1.0).contains(&sample),
            "invalid PCM sample"
        );
        pcm.push(sample);
    }
    pcm.resize(pcm.len().div_ceil(FRAME) * FRAME, 0.0);
    Ok(pcm)
}

pub fn read_pcm(path: &Path) -> Result<Vec<f32>> {
    let mut bytes = Vec::new();
    std::fs::File::open(path)?
        .take((RATE as u64 * 120 * 4) + 1)
        .read_to_end(&mut bytes)?;
    pcm_bytes(&bytes)
}

pub fn agc_level(pcm: &[f32], db: f64) -> Result<Vec<f32>> {
    ensure!(
        db.is_finite() && pcm.iter().all(|x| x.is_finite()),
        "invalid AGC source"
    );
    let energy = pcm.iter().map(|x| f64::from(*x).powi(2)).sum::<f64>();
    ensure!(
        energy > 0.0 && !pcm.is_empty(),
        "AGC source must contain speech"
    );
    let gain = 10.0_f64.powf(db / 20.0) / (energy / pcm.len() as f64).sqrt();
    pcm.iter()
        .map(|x| {
            let sample = (f64::from(*x) * gain) as f32;
            ensure!(
                sample.is_finite() && sample.abs() <= 0.95,
                "AGC source would clip"
            );
            Ok(sample)
        })
        .collect()
}

pub fn encode(pcm: &[f32], backend: &str) -> Result<Vec<Vec<u8>>> {
    ensure!(
        !pcm.is_empty() && pcm.len().is_multiple_of(FRAME),
        "invalid source frames"
    );
    let spec = Spec {
        name: "ans".into(),
        backend: backend.into(),
        params: Params {
            level: if backend == "none" { 0 } else { 2 },
            ..Default::default()
        },
    };
    let mut chain = Factory::default()
        .build(&[spec], &["ans"])
        .map_err(|e| anyhow!(e))?;
    let mut encoder = Encoder::new(32_000).map_err(|e| anyhow!(e))?;
    let mut packet = [0; 1024];
    let mut packets = Vec::with_capacity(pcm.len() / FRAME);
    // Warm up on the same complete clip; retain the second pass only.
    for pass in 0..2 {
        for source in pcm.chunks_exact(FRAME) {
            let mut frame: [f32; FRAME] = source.try_into()?;
            chain.process(&mut frame, None).map_err(|e| anyhow!(e))?;
            let n = encoder
                .encode(&frame, &mut packet)
                .map_err(|e| anyhow!(e))?;
            if pass == 1 {
                packets.push(packet[..n].to_vec());
            }
        }
    }
    Ok(packets)
}

pub struct LocalServer {
    child: Child,
    pub profile: ServerProfile,
}
impl LocalServer {
    pub async fn start(binary: &Path) -> Result<Self> {
        let child = std::process::Command::new(binary)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit())
            .spawn()
            .context("build the isolated listening server first (make build-listening-bot)")?;
        let mut server = Self {
            child,
            profile: ServerProfile::default(),
        };
        let stdout = server
            .child
            .stdout
            .take()
            .context("server stdout unavailable")?;
        let (tx, rx) = tokio::sync::oneshot::channel();
        std::thread::spawn(move || {
            let mut line = String::new();
            let result = BufReader::new(stdout)
                .take(4096)
                .read_line(&mut line)
                .map(|_| line);
            let _ = tx.send(result);
        });
        let line = tokio::time::timeout(Duration::from_secs(8), rx).await???;
        let info: serde_json::Value = serde_json::from_str(&line)?;
        let address = info["address"].as_str().context("missing server address")?;
        ensure_loopback(address)?;
        server.profile = ServerProfile {
            protocol: "resona-noise".into(),
            id: "local-listening".into(),
            name: "Resona 本地音质试听".into(),
            nickname: "试听用户".into(),
            address: address.into(),
            server_public_key: info["publicKey"]
                .as_str()
                .context("missing server public key")?
                .into(),
            ..Default::default()
        };
        server.profile.validate().map_err(|e| anyhow!(e))?;
        Ok(server)
    }
    pub async fn stop(&mut self) -> Result<()> {
        self.child.stdin.take();
        let until = Instant::now() + Duration::from_secs(3);
        while Instant::now() < until {
            if let Some(status) = self.child.try_wait()? {
                ensure!(status.success(), "local server exited unsuccessfully");
                return Ok(());
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
        self.child.kill()?;
        self.child.wait()?;
        bail!("local server did not stop gracefully")
    }
}
impl Drop for LocalServer {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

pub fn ensure_loopback(address: &str) -> Result<()> {
    let address: SocketAddr = address.parse()?;
    ensure!(
        address.ip().is_loopback() && address.port() != 0,
        "loopback address required"
    );
    Ok(())
}

pub async fn connect(profile: &ServerProfile, channel: u16) -> Result<Session> {
    ensure_loopback(&profile.address)?;
    let dir = tempfile::tempdir()?;
    let identity =
        Identity::load_or_create(&dir.path().join("identity")).map_err(|e| anyhow!(e))?;
    let cancel = CancellationToken::new();
    let session = Session::connect(profile, &identity, "", &cancel)
        .await
        .map_err(|e| anyhow!(e.message()))?;
    for command in [
        Command::Move(channel),
        Command::VoiceState {
            muted: false,
            deafened: true,
        },
    ] {
        if let Err(error) = session
            .handle
            .command(command, Instant::now() + Duration::from_secs(5), &cancel)
            .await
        {
            session.close().await;
            bail!(error.message());
        }
    }
    Ok(session)
}

pub struct Bots {
    sessions: Vec<Session>,
    packets: Vec<Vec<Vec<u8>>>,
}
impl Bots {
    pub async fn start(profile: &ServerProfile, pcm: &[f32], agc: &[f32]) -> Result<Self> {
        ensure!(
            !agc.is_empty() && agc.len() <= RATE as usize * 10,
            "AGC phrase must be at most ten seconds"
        );
        let mut packets = Vec::new();
        for backend in ["none", "speex", "webrtc"] {
            packets.push(encode(pcm, backend)?);
        }
        for db in [-34.0, -26.0, -18.0] {
            packets.push(encode(&agc_level(agc, db)?, "none")?);
        }
        let mut bots = Self {
            sessions: Vec::new(),
            packets,
        };
        for (i, name) in [
            "原始",
            "SpeexDSP",
            "WebRTC",
            "轻声 -34 dBFS",
            "正常 -26 dBFS",
            "较响 -18 dBFS",
        ]
        .iter()
        .enumerate()
        {
            let mut profile = profile.clone();
            profile.nickname = format!("试听机器人 / {name}");
            match connect(&profile, (i + 1).min(4) as u16).await {
                Ok(session) => bots.sessions.push(session),
                Err(error) => {
                    bots.close().await;
                    return Err(error);
                }
            }
        }
        Ok(bots)
    }
    pub async fn run(&self, cancel: &CancellationToken) -> Result<()> {
        let mut ticker = tokio::time::interval_at(
            Instant::now() + Duration::from_millis(20),
            Duration::from_millis(20),
        );
        ticker.set_missed_tick_behavior(MissedTickBehavior::Skip);
        let (mut frame, mut speaker, mut agc_frame) = (0, 0, 0);
        loop {
            tokio::select! {
                biased;
                _ = cancel.cancelled() => return Ok(()),
                _ = ticker.tick() => {
                    for (session, packets) in self.sessions[..3].iter().zip(&self.packets) {
                        ensure!(session.handle.state.borrow().closed.is_none(), "listening session closed");
                        session.voice_tx.send(&packets[frame]).map_err(|_| anyhow!("voice send failed"))?;
                    }
                    let session = &self.sessions[3 + speaker];
                    ensure!(session.handle.state.borrow().closed.is_none(), "AGC session closed");
                    let packets = &self.packets[3 + speaker];
                    if agc_frame <= packets.len() {
                        let packet = packets.get(agc_frame).map_or(&[][..], Vec::as_slice);
                        session.voice_tx.send(packet).map_err(|_| anyhow!("voice send failed"))?;
                    }
                    frame = (frame + 1) % self.packets[0].len();
                    agc_frame += 1;
                    if agc_frame >= packets.len() + 25 {
                        speaker = (speaker + 1) % 3;
                        agc_frame = 0;
                    }
                }
            }
        }
    }
    pub async fn close(&mut self) {
        for session in self.sessions.drain(..) {
            session.close().await;
        }
    }
}

pub fn server_binary() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../build/bin")
        .join(format!("listening-server{}", std::env::consts::EXE_SUFFIX))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn pcm_bounds_padding_and_invalid_samples() {
        assert!(pcm_bytes(&[0; 10]).is_err());
        let mut bytes = vec![0; (FRAME + 1) * 4];
        assert_eq!(pcm_bytes(&bytes).unwrap().len(), FRAME * 2);
        bytes[..4].copy_from_slice(&f32::NAN.to_le_bytes());
        assert!(pcm_bytes(&bytes).is_err());
        assert!(ensure_loopback("example.com:9988").is_err());
        assert!(ensure_loopback("192.0.2.1:9988").is_err());
        assert!(ensure_loopback("127.0.0.1:0").is_err());
    }
    #[test]
    fn agc_rms_targets_and_clipping() {
        let pcm: Vec<_> = (0..FRAME)
            .map(|i| 0.2 * (i as f32 * std::f32::consts::TAU / 48.0).sin())
            .collect();
        for db in [-34.0, -26.0, -18.0] {
            let out = agc_level(&pcm, db).unwrap();
            let energy = out.iter().map(|x| f64::from(*x).powi(2)).sum::<f64>() / out.len() as f64;
            assert!((10.0 * energy.log10() - db).abs() < 0.001);
        }
        assert!(agc_level(&[0.0; FRAME], -18.0).is_err());
        let mut spike = [0.0; FRAME];
        spike[0] = 1.0;
        assert!(agc_level(&spike, -18.0).is_err());
    }
    #[test]
    fn product_codec_and_speex_produce_decodable_frames() {
        let pcm: Vec<_> = (0..FRAME * 10)
            .map(|i| 0.2 * (i as f32 * std::f32::consts::TAU / 48.0).sin())
            .collect();
        for backend in ["none", "speex"] {
            let mut decoder = resona_client::audio::Decoder::new().unwrap();
            for packet in encode(&pcm, backend).unwrap() {
                assert_eq!(
                    decoder
                        .decode(Some(&packet), &mut [0.0; FRAME * 6])
                        .unwrap(),
                    FRAME
                );
            }
        }
    }
}
