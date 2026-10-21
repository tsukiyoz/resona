use resona_client::noise::{CancellationToken, Connection, Error};
use std::{
    io::{BufRead, BufReader},
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    sync::OnceLock,
    time::Duration,
};
use tokio::time::{Instant, timeout};

#[cfg(feature = "audio")]
#[test]
fn opus_decodes_existing_go_packets_and_go_decodes_rust_packets() {
    use resona_client::audio::{Decoder, Encoder, FRAME, RATE};
    use std::io::Write;
    fn reference(packets: &[Vec<u8>]) -> serde_json::Value {
        let mut child = Command::new(fixture())
            .arg("-codec")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .spawn()
            .unwrap();
        serde_json::to_writer(
            child.stdin.as_mut().unwrap(),
            &serde_json::json!({"packets":packets}),
        )
        .unwrap();
        child.stdin.take().unwrap().flush().unwrap();
        let output = child.wait_with_output().unwrap();
        assert!(output.status.success());
        serde_json::from_slice(&output.stdout).unwrap()
    }
    fn compare(data: &serde_json::Value) {
        let packets: Vec<Vec<u8>> = serde_json::from_value(data["packets"].clone()).unwrap();
        let expected: Vec<f32> = serde_json::from_value(data["decoded"].clone()).unwrap();
        let mut decoder = Decoder::new().unwrap();
        let mut pcm = [0.0; FRAME * 6];
        let mut actual = Vec::new();
        for p in packets {
            let n = decoder.decode(Some(&p), &mut pcm).unwrap();
            assert_eq!(n, FRAME);
            actual.extend_from_slice(&pcm[..n]);
        }
        assert_eq!(expected.len(), actual.len());
        let signal = expected.iter().map(|x| f64::from(*x).powi(2)).sum::<f64>();
        let error = expected
            .iter()
            .zip(actual)
            .map(|(a, b)| f64::from(a - b).powi(2))
            .sum::<f64>();
        assert!(signal > 1.0);
        assert!(
            (error / signal).sqrt() < 0.05,
            "cross-codec waveform NRMSE {}",
            (error / signal).sqrt()
        );
    }
    compare(&reference(&[]));
    let mut encoder = Encoder::new(48_000).unwrap();
    let mut out = [0; 1024];
    let mut packets = Vec::new();
    for frame in 0..10 {
        let pcm = std::array::from_fn(|i| {
            (2.0 * std::f32::consts::PI * 440.0 * (i + frame * FRAME) as f32 / RATE as f32).sin()
                * 0.25
        });
        let n = encoder.encode(&pcm, &mut out).unwrap();
        packets.push(out[..n].to_vec());
    }
    compare(&reference(&packets));
}

async fn app_session(
    address: std::net::SocketAddr,
    key: [u8; 32],
    name: &str,
) -> resona_client::session::Session {
    use resona_client::{
        config::{Identity, ServerProfile},
        session::Session,
    };
    let dir = tempfile::tempdir().unwrap();
    let identity = Identity::load_or_create(&dir.path().join("identity")).unwrap();
    let profile = ServerProfile {
        protocol: "resona-noise".into(),
        id: "fixture".into(),
        name: "fixture".into(),
        nickname: name.into(),
        address: address.to_string(),
        server_public_key: key.iter().map(|b| format!("{b:02x}")).collect(),
        ..Default::default()
    };
    Session::connect(
        &profile,
        &identity,
        "local-test-only",
        &CancellationToken::new(),
    )
    .await
    .unwrap()
}

async fn wait_state(
    handle: &mut resona_client::session::Handle,
    ready: impl Fn(&resona_client::session::Snapshot) -> bool,
) {
    timeout(Duration::from_secs(3), async {
        loop {
            if ready(&handle.state.borrow()) {
                return;
            }
            handle.state.changed().await.unwrap();
        }
    })
    .await
    .expect("application state did not converge");
}

#[tokio::test]
async fn application_session_watches_moves_chats_and_routes_voice() {
    use resona_client::session::Command as AppCommand;
    let (_peer, address, key) = start_peer(&["-server"]);
    let mut a = app_session(address, key, "alpha").await;
    let mut b = app_session(address, key, "beta").await;
    let cancel = CancellationToken::new();
    wait_state(&mut a.handle, |s| {
        s.state.members.len() == 2 && s.chat_status.status == 1 && s.chat_status.epoch != 0
    })
    .await;
    a.handle
        .command(
            AppCommand::Watch {
                channels: true,
                members: true,
            },
            Instant::now() + Duration::from_secs(3),
            &cancel,
        )
        .await
        .unwrap();
    assert_eq!(a.handle.state.borrow().state.channels.len(), 2);
    b.handle
        .command(
            AppCommand::VoiceState {
                muted: false,
                deafened: false,
            },
            Instant::now() + Duration::from_secs(3),
            &cancel,
        )
        .await
        .unwrap();
    b.voice_tx.send(b"opaque-opus").unwrap();
    let voice = timeout(Duration::from_secs(2), a.voice_rx.recv())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(voice.data, b"opaque-opus");
    assert!(!voice.instance.is_empty());
    a.handle
        .command(
            AppCommand::Chat {
                channel: 1,
                text: "test message".into(),
                id: "session-chat-1".into(),
            },
            Instant::now() + Duration::from_secs(3),
            &cancel,
        )
        .await
        .unwrap();
    wait_state(&mut a.handle, |s| {
        s.messages.iter().any(|m| m.client_id == "session-chat-1")
    })
    .await;
    wait_state(&mut b.handle, |s| {
        s.messages.iter().any(|m| m.client_id == "session-chat-1")
    })
    .await;
    a.handle
        .command(
            AppCommand::Move(2),
            Instant::now() + Duration::from_secs(3),
            &cancel,
        )
        .await
        .unwrap();
    wait_state(&mut a.handle, |s| s.channel() == 2).await;
    assert!(a.handle.state.borrow().messages.is_empty());
    while a.voice_rx.try_recv().is_ok() {}
    b.voice_tx.send(b"other-channel").unwrap();
    assert!(
        timeout(Duration::from_millis(150), a.voice_rx.recv())
            .await
            .is_err()
    );
    let late = a.handle.clone();
    a.close().await;
    assert!(
        late.command(
            AppCommand::Watch {
                channels: true,
                members: true
            },
            Instant::now() + Duration::from_secs(1),
            &cancel
        )
        .await
        .is_err()
    );
    b.close().await;
}

#[tokio::test]
async fn application_session_owner_drop_stops_cloned_handles() {
    use resona_client::session::Command as AppCommand;
    let (_peer, address, key) = start_peer(&["-server"]);
    let session = app_session(address, key, "drop-owner").await;
    let mut handle = session.handle.clone();
    drop(session);
    wait_state(&mut handle, |s| s.closed.is_some()).await;
    assert!(
        handle
            .command(
                AppCommand::Move(2),
                Instant::now() + Duration::from_secs(1),
                &CancellationToken::new()
            )
            .await
            .is_err()
    );
}

#[tokio::test]
async fn lost_command_acks_do_not_pause_incoming_voice() {
    use resona_client::session::Command as AppCommand;
    let (peer, address, key) = start_peer(&["-server", "-slow-command-acks"]);
    let mut listener = app_session(address, key, "listener").await;
    let source = app_session(peer.1.unwrap(), key, "speaker").await;
    source
        .handle
        .command(
            AppCommand::VoiceState {
                muted: false,
                deafened: false,
            },
            Instant::now() + Duration::from_secs(3),
            &CancellationToken::new(),
        )
        .await
        .unwrap();
    wait_state(&mut listener.handle, |s| s.state.members.len() == 2).await;
    let handle = listener.handle.clone();
    let command = tokio::spawn(async move {
        handle
            .command(
                AppCommand::Watch {
                    channels: true,
                    members: true,
                },
                Instant::now() + Duration::from_secs(3),
                &CancellationToken::new(),
            )
            .await
    });
    let voice = source.voice_tx.clone();
    let sending = tokio::spawn(async move {
        for _ in 0..40 {
            voice.send(b"test-opus").unwrap();
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    });
    let mut received = 0;
    while let Ok(Some(_)) = timeout(Duration::from_millis(150), listener.voice_rx.recv()).await {
        received += 1;
        if received == 40 {
            break;
        }
    }
    sending.await.unwrap();
    command.await.unwrap().unwrap();
    assert!(
        received >= 32,
        "control ACK retransmit starved voice: received {received}/40"
    );
    listener.close().await;
    source.close().await;
}

async fn wait_lifecycle(
    handle: &mut resona_client::lifecycle::Handle,
    mode: resona_client::lifecycle::Mode,
) {
    timeout(Duration::from_secs(4), async {
        loop {
            if handle.state.borrow().mode == mode {
                return;
            }
            handle.state.changed().await.unwrap();
        }
    })
    .await
    .expect("lifecycle state did not converge");
}

#[tokio::test]
async fn resource_observer_changes_scope_and_clears_on_disconnect() {
    use resona_client::{
        config::{Identity, ServerProfile},
        lifecycle::{Client, Mode},
        resources::{Interest, Observer},
    };
    let (_peer, address, key) = start_peer(&["-server"]);
    let dir = tempfile::tempdir().unwrap();
    let client = Client::start(std::sync::Arc::new(
        Identity::load_or_create(&dir.path().join("identity")).unwrap(),
    ));
    let mut life = client.handle.clone();
    let observer = Observer::start(life.state.clone());
    let mut status = observer.status.clone();
    let profile = ServerProfile {
        protocol: "resona-noise".into(),
        id: "resources".into(),
        name: "fixture".into(),
        nickname: "resources".into(),
        address: address.to_string(),
        server_public_key: key.iter().map(|b| format!("{b:02x}")).collect(),
        ..Default::default()
    };
    life.connect(profile, zeroize::Zeroizing::new("local-test-only".into()))
        .await
        .unwrap();
    wait_lifecycle(&mut life, Mode::Connected).await;
    for generation in 1..=3 {
        observer
            .set(Interest {
                active: generation != 2,
                generation,
                ..Default::default()
            })
            .unwrap();
        timeout(Duration::from_secs(4), async {
            loop {
                let s = status.borrow().clone();
                if s.generation == generation && s.state == "ready" {
                    break;
                }
                status.changed().await.unwrap();
            }
        })
        .await
        .unwrap();
        assert_eq!(status.borrow().session_id, life.state.borrow().session_id);
    }
    life.disconnect().await.unwrap();
    timeout(Duration::from_secs(2), async {
        loop {
            if status.borrow().state == "idle" {
                break;
            }
            status.changed().await.unwrap();
        }
    })
    .await
    .unwrap();
    assert!(status.borrow().session_id.is_empty());
    observer.close().await;
    client.close().await;
}

#[tokio::test]
async fn lifecycle_preserves_identity_rejects_stale_session_and_stops_recovery() {
    use resona_client::{
        config::{Identity, ServerProfile},
        lifecycle::{Client, Mode},
    };
    let (peer, address, key) = start_peer(&["-server"]);
    let dir = tempfile::tempdir().unwrap();
    let identity =
        std::sync::Arc::new(Identity::load_or_create(&dir.path().join("identity")).unwrap());
    let uid = identity.uid();
    let client = Client::start(identity);
    let mut handle = client.handle.clone();
    let profile = ServerProfile {
        protocol: "resona-noise".into(),
        id: "bookmark".into(),
        name: "fixture".into(),
        nickname: "recover".into(),
        address: address.to_string(),
        server_public_key: key.iter().map(|b| format!("{b:02x}")).collect(),
        ..Default::default()
    };
    handle
        .connect(
            profile.clone(),
            zeroize::Zeroizing::new("local-test-only".into()),
        )
        .await
        .unwrap();
    wait_lifecycle(&mut handle, Mode::Connected).await;
    let first = handle.state.borrow().session_id.clone();
    assert_eq!(
        handle
            .session(&first)
            .unwrap()
            .state
            .borrow()
            .state
            .identity_uid,
        uid
    );
    handle.disconnect().await.unwrap();
    wait_lifecycle(&mut handle, Mode::Offline).await;
    handle
        .connect(
            profile.clone(),
            zeroize::Zeroizing::new("incorrect-test-only".into()),
        )
        .await
        .unwrap();
    wait_lifecycle(&mut handle, Mode::Failed).await;
    assert_eq!(
        handle.state.borrow().attempt,
        1,
        "authentication failure must not retry"
    );
    handle
        .connect(profile, zeroize::Zeroizing::new("local-test-only".into()))
        .await
        .unwrap();
    wait_lifecycle(&mut handle, Mode::Connected).await;
    let second = handle.state.borrow().session_id.clone();
    assert_ne!(first, second);
    assert!(handle.session(&first).is_err());
    assert_eq!(
        handle
            .session(&second)
            .unwrap()
            .state
            .borrow()
            .state
            .identity_uid,
        uid
    );
    drop(peer);
    wait_lifecycle(&mut handle, Mode::Reconnecting).await;
    handle.disconnect().await.unwrap();
    wait_lifecycle(&mut handle, Mode::Offline).await;
    tokio::time::sleep(Duration::from_millis(350)).await;
    assert_eq!(handle.state.borrow().mode, Mode::Offline);
    client.close().await;
}

#[tokio::test]
async fn lifecycle_disconnect_cancels_pending_handshake() {
    use resona_client::{
        config::{Identity, ServerProfile},
        lifecycle::{Client, Mode},
    };
    let socket = tokio::net::UdpSocket::bind("127.0.0.1:0").await.unwrap();
    let dir = tempfile::tempdir().unwrap();
    let identity =
        std::sync::Arc::new(Identity::load_or_create(&dir.path().join("identity")).unwrap());
    let client = Client::start(identity);
    let mut handle = client.handle.clone();
    handle
        .connect(
            ServerProfile {
                protocol: "resona-noise".into(),
                id: "dead".into(),
                name: "offline".into(),
                nickname: "test".into(),
                address: socket.local_addr().unwrap().to_string(),
                server_public_key: "07".repeat(32),
                ..Default::default()
            },
            zeroize::Zeroizing::new(String::new()),
        )
        .await
        .unwrap();
    wait_lifecycle(&mut handle, Mode::Connecting).await;
    let start = Instant::now();
    handle.disconnect().await.unwrap();
    wait_lifecycle(&mut handle, Mode::Offline).await;
    timeout(Duration::from_secs(1), client.close())
        .await
        .unwrap();
    assert!(start.elapsed() < Duration::from_secs(1));
}

struct Peer(Child, Option<std::net::SocketAddr>);
impl Drop for Peer {
    fn drop(&mut self) {
        self.0.stdin.take();
        let until = std::time::Instant::now() + Duration::from_secs(2);
        while std::time::Instant::now() < until {
            if self.0.try_wait().unwrap().is_some() {
                return;
            }
            std::thread::sleep(Duration::from_millis(10));
        }
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

fn fixture() -> &'static Path {
    static BINARY: OnceLock<PathBuf> = OnceLock::new();
    BINARY.get_or_init(|| {
        let root = Path::new(env!("CARGO_MANIFEST_DIR")).parent().unwrap();
        let dir = root.join("client/target/interop");
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join(format!("go-peer{}", std::env::consts::EXE_SUFFIX));
        let status = Command::new("go")
            .args(["build", "-o"])
            .arg(&path)
            .arg("./client/tests/peer")
            .env("CGO_ENABLED", "0")
            .current_dir(root)
            .status()
            .expect("Go toolchain required for interoperability tests");
        assert!(status.success(), "Go fixture build failed");
        path
    })
}

fn peer(loss: bool) -> (Peer, std::net::SocketAddr, [u8; 32]) {
    start_peer(if loss { &["-loss"] } else { &[] })
}

fn start_peer(args: &[&str]) -> (Peer, std::net::SocketAddr, [u8; 32]) {
    let mut command = Command::new(fixture());
    command.args(args);
    let child = command
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::inherit())
        .spawn()
        .unwrap();
    let mut peer = Peer(child, None);
    let stdout = peer.0.stdout.take().unwrap();
    let (tx, rx) = std::sync::mpsc::channel();
    let reader = std::thread::spawn(move || {
        let mut line = String::new();
        BufReader::new(stdout).read_line(&mut line).unwrap();
        let _ = tx.send(line);
    });
    let line = rx
        .recv_timeout(Duration::from_secs(8))
        .expect("Go peer startup timed out");
    reader.join().unwrap();
    let info: serde_json::Value = serde_json::from_str(&line).unwrap();
    let address = info["address"].as_str().unwrap().parse().unwrap();
    peer.1 = info["backend"].as_str().map(|s| s.parse().unwrap());
    let key = info["public_key"].as_str().unwrap();
    let mut public = [0; 32];
    for (i, byte) in public.iter_mut().enumerate() {
        *byte = u8::from_str_radix(&key[2 * i..2 * i + 2], 16).unwrap();
    }
    (peer, address, public)
}

async fn read_exact(connection: &mut Connection, out: &mut [u8]) {
    let cancel = CancellationToken::new();
    let mut at = 0;
    while at < out.len() {
        at += connection
            .control_rx
            .read(
                &mut out[at..],
                Instant::now() + Duration::from_secs(4),
                &cancel,
            )
            .await
            .unwrap();
    }
}

async fn exchange(loss: bool) {
    let (_peer, address, key) = peer(loss);
    let dial = CancellationToken::new();
    let mut connection = Connection::dial(address, &key, &dial).await.unwrap();
    dial.cancel(); // The established session must outlive its setup token.
    let mut binding = [0; 32];
    read_exact(&mut connection, &mut binding).await;
    assert_eq!(&binding, connection.channel_binding());
    let payload = (0..4097).map(|i| (i % 251) as u8).collect::<Vec<_>>();
    let sender = connection.control_tx.clone();
    let sent = payload.clone();
    let task = tokio::spawn(async move {
        sender
            .write(
                &sent,
                Instant::now() + Duration::from_secs(5),
                &CancellationToken::new(),
            )
            .await
    });
    let mut received = vec![0; payload.len()];
    read_exact(&mut connection, &mut received).await;
    task.await.unwrap().unwrap();
    assert_eq!(received, payload, "retries must not duplicate stream bytes");
    connection
        .voice_tx
        .try_send(b"opaque opus test payload")
        .unwrap();
    let voice = timeout(
        Duration::from_secs(2),
        connection.voice_rx.recv(&CancellationToken::new()),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(voice, b"opaque opus test payload");
    let waiting = connection.control_tx.clone();
    let closed = tokio::spawn(async move {
        // This read-only handle must stop accepting commands after owner close.
        tokio::time::sleep(Duration::from_millis(50)).await;
        waiting
            .write(
                b"late",
                Instant::now() + Duration::from_secs(1),
                &CancellationToken::new(),
            )
            .await
    });
    connection.close().await;
    assert!(closed.await.unwrap().is_err());
}

#[tokio::test]
async fn go_noise_bidirectional_stream_voice_and_close() {
    exchange(false).await;
}

#[tokio::test]
async fn go_noise_lost_handshake_and_ack_retries() {
    exchange(true).await;
}

#[tokio::test]
async fn incorrect_server_key_never_establishes_a_session() {
    let (_peer, address, _) = peer(false);
    let result = timeout(
        Duration::from_secs(6),
        Connection::dial(address, &[23; 32], &CancellationToken::new()),
    )
    .await
    .unwrap();
    assert!(matches!(
        result.err(),
        Some(Error::Timeout | Error::Authentication)
    ));
}

#[tokio::test]
async fn cancel_pending_handshake_and_pending_read() {
    let socket = tokio::net::UdpSocket::bind("127.0.0.1:0").await.unwrap();
    let address = socket.local_addr().unwrap();
    let cancel = CancellationToken::new();
    let trigger = cancel.clone();
    tokio::spawn(async move {
        tokio::time::sleep(Duration::from_millis(30)).await;
        trigger.cancel();
    });
    let result = timeout(
        Duration::from_secs(1),
        Connection::dial(address, &[1; 32], &cancel),
    )
    .await
    .unwrap();
    assert_eq!(result.err(), Some(Error::Cancelled));
    let (_peer, address, key) = peer(false);
    let mut connection = Connection::dial(address, &key, &CancellationToken::new())
        .await
        .unwrap();
    read_exact(&mut connection, &mut [0; 32]).await;
    let cancel = CancellationToken::new();
    cancel.cancel();
    assert_eq!(
        connection
            .control_rx
            .read(
                &mut [0; 1],
                Instant::now() + Duration::from_secs(1),
                &cancel
            )
            .await,
        Err(Error::Cancelled)
    );
    connection.voice_tx.try_send(b"still connected").unwrap();
    assert_eq!(
        timeout(
            Duration::from_secs(1),
            connection.voice_rx.recv(&CancellationToken::new())
        )
        .await
        .unwrap()
        .unwrap(),
        b"still connected"
    );
    connection.close().await;
}

#[tokio::test]
async fn timed_out_partial_write_closes_the_session() {
    let (_peer, address, key) = start_peer(&["-blackhole-acks"]);
    let mut connection = Connection::dial(address, &key, &CancellationToken::new())
        .await
        .unwrap();
    read_exact(&mut connection, &mut [0; 32]).await;
    let error = connection
        .control_tx
        .write(
            b"cannot safely retry",
            Instant::now() + Duration::from_millis(80),
            &CancellationToken::new(),
        )
        .await;
    assert!(matches!(error, Err(Error::Timeout)));
    timeout(Duration::from_secs(1), connection.closed())
        .await
        .unwrap();
    assert!(connection.voice_tx.try_send(b"must stop").is_err());
    connection.close().await;
}

#[tokio::test]
async fn full_control_receive_queue_resumes_without_loss() {
    let (_peer, address, key) = peer(false);
    let mut connection = Connection::dial(address, &key, &CancellationToken::new())
        .await
        .unwrap();
    read_exact(&mut connection, &mut [0; 32]).await;
    let payload = (0..50000).map(|n| (n % 239) as u8).collect::<Vec<_>>();
    let sender = connection.control_tx.clone();
    let sent = payload.clone();
    let task = tokio::spawn(async move {
        sender
            .write(
                &sent,
                Instant::now() + Duration::from_secs(6),
                &CancellationToken::new(),
            )
            .await
    });
    tokio::time::sleep(Duration::from_millis(400)).await;
    let mut received = vec![0; payload.len()];
    read_exact(&mut connection, &mut received).await;
    task.await.unwrap().unwrap();
    assert_eq!(received, payload);
    connection.close().await;
}

#[tokio::test]
async fn partial_application_frame_survives_read_timeout() {
    use resona_client::wire::{self, pb};
    let (_peer, address, key) = peer(false);
    let mut connection = Connection::dial(address, &key, &CancellationToken::new())
        .await
        .unwrap();
    read_exact(&mut connection, &mut [0; 32]).await;
    let cancel = CancellationToken::new();
    let frame = wire::pack(
        pb::Kind::Chat,
        8,
        &pb::Command {
            text: "partial".into(),
            ..Default::default()
        },
    )
    .unwrap();
    connection
        .control_tx
        .write(
            &frame[..2],
            Instant::now() + Duration::from_secs(1),
            &cancel,
        )
        .await
        .unwrap();
    let mut reader = wire::FrameReader::default();
    assert_eq!(
        reader
            .read(
                &mut connection.control_rx,
                Instant::now() + Duration::from_millis(30),
                &cancel
            )
            .await
            .err(),
        Some(Error::Timeout)
    );
    connection
        .control_tx
        .write(
            &frame[2..],
            Instant::now() + Duration::from_secs(1),
            &cancel,
        )
        .await
        .unwrap();
    let decoded = reader
        .read(
            &mut connection.control_rx,
            Instant::now() + Duration::from_secs(1),
            &cancel,
        )
        .await
        .unwrap();
    assert_eq!(decoded.request, 8);
    connection.close().await;
}

#[tokio::test]
async fn buffered_read_honors_cancel_and_deadline_without_consuming_bytes() {
    let (_peer, address, key) = peer(false);
    let mut connection = Connection::dial(address, &key, &CancellationToken::new())
        .await
        .unwrap();
    read_exact(&mut connection, &mut [0; 32]).await;
    let cancel = CancellationToken::new();
    connection
        .control_tx
        .write(
            b"buffered",
            Instant::now() + Duration::from_secs(1),
            &cancel,
        )
        .await
        .unwrap();
    let mut first = [0; 1];
    assert_eq!(
        connection
            .control_rx
            .read(&mut first, Instant::now() + Duration::from_secs(1), &cancel)
            .await
            .unwrap(),
        1
    );
    assert_eq!(&first, b"b");
    let cancelled = CancellationToken::new();
    cancelled.cancel();
    assert_eq!(
        connection
            .control_rx
            .read(
                &mut first,
                Instant::now() + Duration::from_secs(1),
                &cancelled
            )
            .await,
        Err(Error::Cancelled)
    );
    assert_eq!(
        connection
            .control_rx
            .read(&mut first, Instant::now(), &cancel)
            .await,
        Err(Error::Timeout)
    );
    let mut rest = [0; 7];
    read_exact(&mut connection, &mut rest).await;
    assert_eq!(&rest, b"uffered");
    connection.close().await;
}

#[tokio::test]
async fn dropping_submitted_write_closes_all_handles() {
    let (_peer, address, key) = start_peer(&["-blackhole-acks"]);
    let mut connection = Connection::dial(address, &key, &CancellationToken::new())
        .await
        .unwrap();
    read_exact(&mut connection, &mut [0; 32]).await;
    let sender = connection.control_tx.clone();
    let task = tokio::spawn(async move {
        sender
            .write(
                b"submitted",
                Instant::now() + Duration::from_secs(3),
                &CancellationToken::new(),
            )
            .await
    });
    let mut echo = [0; 9];
    read_exact(&mut connection, &mut echo).await; // Peer retained the write; ACK is deliberately lost.
    assert_eq!(&echo, b"submitted");
    task.abort();
    let _ = task.await;
    timeout(Duration::from_secs(1), connection.closed())
        .await
        .unwrap();
    assert!(connection.voice_tx.try_send(b"late").is_err());
    connection.close().await;
}

#[tokio::test]
async fn owner_drop_cancels_a_pending_write() {
    let (_peer, address, key) = start_peer(&["-blackhole-acks"]);
    let mut connection = Connection::dial(address, &key, &CancellationToken::new())
        .await
        .unwrap();
    read_exact(&mut connection, &mut [0; 32]).await;
    let sender = connection.control_tx.clone();
    let task = tokio::spawn(async move {
        sender
            .write(
                b"pending",
                Instant::now() + Duration::from_secs(3),
                &CancellationToken::new(),
            )
            .await
    });
    read_exact(&mut connection, &mut [0; 7]).await;
    let voice = connection.voice_tx.clone();
    drop(connection);
    assert!(
        timeout(Duration::from_secs(1), task)
            .await
            .unwrap()
            .unwrap()
            .is_err()
    );
    assert!(voice.try_send(b"late").is_err());
}

#[tokio::test]
async fn go_server_authentication_and_jetstream_chat() {
    use prost::Message;
    use resona_client::wire::{self, pb};
    let (_peer, address, key) = start_peer(&["-server"]);
    let cancel = CancellationToken::new();
    let mut connection = Connection::dial(address, &key, &cancel).await.unwrap();
    let hello = wire::sign_hello(
        &[11; 32],
        connection.channel_binding(),
        "Rust tester".into(),
        "local-test-only".into(),
    )
    .unwrap();
    let expected_uid = wire::identity_uid(hello.public_key.as_slice().try_into().unwrap());
    let deadline = Instant::now() + Duration::from_secs(5);
    wire::send(
        &connection.control_tx,
        pb::Kind::Hello,
        0,
        &hello,
        deadline,
        &cancel,
    )
    .await
    .unwrap();
    let mut reader = wire::FrameReader::default();
    let welcome = reader
        .read(&mut connection.control_rx, deadline, &cancel)
        .await
        .unwrap();
    assert_eq!(welcome.kind, pb::Kind::Welcome as i32);
    assert_eq!(welcome.request, 0);
    let state = pb::State::decode(welcome.body.as_slice()).unwrap();
    assert!(wire::valid_state(&state, &expected_uid));
    let command = pb::Command {
        channel: 1,
        text: "Rust to Go bounded chat".into(),
        message_id: "rust-interop-0001".into(),
        ..Default::default()
    };
    wire::send(
        &connection.control_tx,
        pb::Kind::Chat,
        1,
        &command,
        deadline,
        &cancel,
    )
    .await
    .unwrap();
    let (mut reply, mut echo) = (false, false);
    while !reply || !echo {
        let frame = reader
            .read(&mut connection.control_rx, deadline, &cancel)
            .await
            .unwrap();
        if frame.kind == pb::Kind::Reply as i32 && frame.request == 1 {
            assert_eq!(pb::Reply::decode(frame.body.as_slice()).unwrap().code, 0);
            reply = true;
        }
        if frame.kind == pb::Kind::Message as i32 {
            let message = pb::Message::decode(frame.body.as_slice()).unwrap();
            assert_eq!(message.text, command.text);
            assert_eq!(message.client_id, command.message_id);
            assert_eq!(message.author_uid, expected_uid);
            assert!(message.sequence > 0 && message.timestamp > 0 && message.self_);
            echo = true;
        }
    }
    connection.close().await;
}

#[tokio::test]
async fn go_server_rejects_wrong_password_and_replayed_identity_proof() {
    use resona_client::wire::{self, pb};
    let (_peer, address, key) = start_peer(&["-server"]);
    for wrong_password in [true, false] {
        let cancel = CancellationToken::new();
        let mut connection = Connection::dial(address, &key, &cancel).await.unwrap();
        let binding = if wrong_password {
            *connection.channel_binding()
        } else {
            [0; 32]
        };
        let password = if wrong_password {
            "incorrect"
        } else {
            "local-test-only"
        };
        let hello =
            wire::sign_hello(&[13; 32], &binding, "Rust rejected".into(), password.into()).unwrap();
        let deadline = Instant::now() + Duration::from_secs(2);
        let sent = wire::send(
            &connection.control_tx,
            pb::Kind::Hello,
            0,
            &hello,
            deadline,
            &cancel,
        )
        .await;
        let error = match sent {
            Err(error) => error,
            Ok(()) => wire::FrameReader::default()
                .read(&mut connection.control_rx, deadline, &cancel)
                .await
                .expect_err("rejected login must not receive Welcome"),
        };
        assert_eq!(error, Error::RemoteClosed(2));
        connection.close().await;
    }
}
