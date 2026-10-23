use resona_client::{
    audio::{Decoder, FRAME},
    session::Command,
};
use resona_listening_bot::{Bots, LocalServer, agc_level, connect, encode, server_binary};
use std::{collections::HashSet, time::Duration};
use tokio::time::{Instant, timeout};
use tokio_util::sync::CancellationToken;

#[tokio::test]
async fn product_sources_are_decodable_and_channel_scoped() {
    let mut server = LocalServer::start(&server_binary()).await.unwrap();
    let mut listener = connect(&server.profile, 1).await.unwrap();
    let sender = connect(&server.profile, 1).await.unwrap();
    let outsider = connect(&server.profile, 2).await.unwrap();
    let cancel = CancellationToken::new();
    listener
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
    let pcm: Vec<_> = (0..FRAME * 5)
        .map(|i| 0.1 * (i as f32 * std::f32::consts::TAU / 48.0).sin())
        .collect();
    for backend in ["none", "speex"] {
        for db in [-34.0, -26.0, -18.0] {
            let mut decoder = Decoder::new().unwrap();
            for packet in encode(&agc_level(&pcm, db).unwrap(), backend).unwrap() {
                sender.voice_tx.send(&packet).unwrap();
                let received = timeout(Duration::from_secs(2), listener.voice_rx.recv())
                    .await
                    .unwrap()
                    .unwrap();
                assert_eq!(received.data, packet);
                assert_eq!(
                    decoder
                        .decode(Some(&received.data), &mut [0.0; FRAME * 6])
                        .unwrap(),
                    FRAME
                );
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        }
    }
    outsider.voice_tx.send(b"other-channel").unwrap();
    assert!(
        timeout(Duration::from_millis(150), listener.voice_rx.recv())
            .await
            .is_err()
    );
    outsider.close().await;
    sender.close().await;
    listener.close().await;
    server.stop().await.unwrap();
}

#[tokio::test]
async fn native_comparison_bots_cycle_three_agc_speakers_and_stop() {
    if std::env::var("RESONA_TEST_NATIVE_AUDIO").as_deref() != Ok("1") {
        return;
    }
    let mut server = LocalServer::start(&server_binary()).await.unwrap();
    let pcm: Vec<_> = (0..FRAME * 5)
        .map(|i| 0.1 * (i as f32 * std::f32::consts::TAU / 48.0).sin())
        .collect();
    let mut bots = Bots::start(&server.profile, &pcm, &pcm).await.unwrap();
    let mut listener = connect(&server.profile, 4).await.unwrap();
    let cancel = CancellationToken::new();
    listener
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
    let receive = async {
        let mut senders = HashSet::new();
        while senders.len() < 3 {
            let packet = listener.voice_rx.recv().await.unwrap();
            if !packet.end {
                senders.insert(packet.sender);
            }
        }
        cancel.cancel();
    };
    let result = timeout(Duration::from_secs(8), async {
        tokio::join!(bots.run(&cancel), receive).0
    })
    .await
    .unwrap();
    result.unwrap();
    listener.close().await;
    bots.close().await;
    server.stop().await.unwrap();
}
