use resona_client::{
    audio::{Decoder, FRAME},
    config::ServerProfile,
    session::Command,
};
use resona_listening_bot::connect;
use std::{collections::HashMap, time::Duration};
use tokio::time::{Instant, timeout_at};
use tokio_util::sync::CancellationToken;

#[tokio::test]
#[ignore = "explicit loopback address/key and PCM export required"]
async fn capture_local_agc_sources() {
    let profile = ServerProfile {
        protocol: "resona-noise".into(),
        address: std::env::var("RESONA_LISTENING_ADDRESS").expect("loopback address required"),
        server_public_key: std::env::var("RESONA_LISTENING_KEY").expect("public key required"),
        nickname: "本地音源诊断".into(),
        ..Default::default()
    };
    let mut session = connect(&profile, 4).await.unwrap();
    let cancel = CancellationToken::new();
    session
        .handle
        .command(
            Command::VoiceState {
                muted: true,
                deafened: false,
            },
            Instant::now() + Duration::from_secs(3),
            &cancel,
        )
        .await
        .unwrap();
    let until = Instant::now() + Duration::from_secs(25);
    let mut decoders = HashMap::new();
    let mut samples: HashMap<u16, Vec<f32>> = HashMap::new();
    while let Ok(Some(packet)) = timeout_at(until, session.voice_rx.recv()).await {
        if packet.end {
            decoders.remove(&packet.sender);
            continue;
        }
        let decoder = decoders
            .entry(packet.sender)
            .or_insert_with(|| Decoder::new().unwrap());
        let mut pcm = [0.0; FRAME * 6];
        let n = decoder.decode(Some(&packet.data), &mut pcm).unwrap();
        let output = samples.entry(packet.sender).or_default();
        assert!(output.len() + n <= 48_000 * 26, "capture budget exceeded");
        output.extend_from_slice(&pcm[..n]);
    }
    session.close().await;
    assert!(!samples.is_empty(), "no audio received");
    let directory = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("../../build/listening");
    std::fs::create_dir_all(&directory).unwrap();
    for (id, pcm) in samples {
        let bytes: Vec<_> = pcm.into_iter().flat_map(f32::to_le_bytes).collect();
        std::fs::write(directory.join(format!("received-{id}.f32")), bytes).unwrap();
    }
}
