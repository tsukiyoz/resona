use super::{
    crypto::{Packet, ReceiveKeys, SendKeys},
    *,
};
use std::{collections::VecDeque, net::SocketAddr, time::Duration};
use tokio::{
    net::UdpSocket,
    sync::{mpsc, oneshot, watch},
    task::JoinHandle,
    time::{Instant, sleep_until, timeout},
};
use zeroize::Zeroize;

const HANDSHAKE_TIMEOUT: Duration = Duration::from_secs(5);
const RETRY: Duration = Duration::from_millis(200);
const WRITE_TIMEOUT: Duration = Duration::from_secs(8);
const SOCKET_TIMEOUT: Duration = Duration::from_millis(250);
const MAINTENANCE: Duration = Duration::from_secs(5);
const MAX_WRITE: usize = 65540;

#[derive(Clone)]
struct Life {
    cancel: CancellationToken,
    error: watch::Receiver<Option<Error>>,
}

impl Life {
    fn failure(&self) -> Error {
        self.error.borrow().clone().unwrap_or(Error::Closed)
    }
}

struct Write {
    data: Vec<u8>,
    deadline: Instant,
    reply: oneshot::Sender<Result<()>>,
}

/// Owned transport session. Keep this owner alive while using its handles.
/// Explicit `close` joins the worker; dropping it also requests cancellation.
pub struct Connection {
    pub control_tx: ControlSender,
    pub control_rx: ControlReceiver,
    pub voice_tx: VoiceSender,
    pub voice_rx: VoiceReceiver,
    binding: [u8; 32],
    life: Life,
    task: Option<JoinHandle<()>>,
}

impl Drop for Connection {
    fn drop(&mut self) {
        self.life.cancel.cancel();
    }
}

impl Connection {
    /// Authenticate a pinned server key. This is transport establishment, not
    /// application login: signed Hello / validated Welcome must still follow.
    pub async fn dial(
        address: SocketAddr,
        public_key: &[u8; 32],
        cancel: &CancellationToken,
    ) -> Result<Self> {
        tokio::select! {
            biased;
            _ = cancel.cancelled() => Err(Error::Cancelled),
            result = timeout(HANDSHAKE_TIMEOUT, Self::handshake(address, public_key, cancel)) => {
                result.map_err(|_| Error::Timeout)?
            }
        }
    }

    async fn handshake(
        address: SocketAddr,
        public_key: &[u8; 32],
        dial_cancel: &CancellationToken,
    ) -> Result<Self> {
        let bind = if address.is_ipv4() {
            "0.0.0.0:0"
        } else {
            "[::]:0"
        };
        let socket = UdpSocket::bind(bind).await?;
        socket.connect(address).await?;
        let mut handshake = snow::Builder::new(SUITE.parse().map_err(|_| Error::Crypto)?)
            .prologue(PROLOGUE)
            .map_err(|_| Error::Crypto)?
            .remote_public_key(public_key)
            .map_err(|_| Error::Crypto)?
            .build_initiator()
            .map_err(|_| Error::Crypto)?;
        let mut request = [0; 73];
        request[..4].copy_from_slice(MAGIC);
        request[4] = 1;
        if handshake
            .write_message(&[], &mut request[25..])
            .map_err(|_| Error::Crypto)?
            != 48
        {
            return Err(Error::Crypto);
        }
        let mut buffer = [0; MAX_PACKET + 1];
        let mut next = Instant::now();
        loop {
            tokio::select! {
                biased;
                _ = dial_cancel.cancelled() => return Err(Error::Cancelled),
                _ = sleep_until(next) => {
                    send_raw(&socket, &request).await?;
                    next = Instant::now() + RETRY;
                }
                received = socket.recv(&mut buffer) => {
                    let n = received?;
                    if n < 5 || &buffer[..4] != MAGIC { continue; }
                    if buffer[4] == 2 && n == 25 {
                        request[5..25].copy_from_slice(&buffer[5..25]);
                        next = Instant::now();
                        continue;
                    }
                    if buffer[4] != 3 || n != 53 { continue; }
                    let mut payload = [0; 48];
                    if handshake.read_message(&buffer[5..n], &mut payload).map_err(|_| Error::Authentication)? != 0 || !handshake.is_handshake_finished() {
                        return Err(Error::Authentication);
                    }
                    if dial_cancel.is_cancelled() { return Err(Error::Cancelled); }
                    let binding = handshake.get_handshake_hash().try_into().map_err(|_| Error::Crypto)?;
                    // Snow's high-level transport has no application-header AD.
                    // Extract only completed directional keys; all packet nonce,
                    // replay and rekey ownership stays in this one worker.
                    let (mut tx_key, mut rx_key) = handshake.dangerously_get_raw_split();
                    let tx = SendKeys::new(&tx_key, Instant::now());
                    let rx = ReceiveKeys::new(&rx_key);
                    tx_key.zeroize();
                    rx_key.zeroize();
                    return Ok(Self::start(socket, tx, rx, binding));
                }
            }
        }
    }

    fn start(socket: UdpSocket, tx: SendKeys, rx: ReceiveKeys, binding: [u8; 32]) -> Self {
        let cancel = CancellationToken::new();
        let (error_tx, error) = watch::channel(None);
        let life = Life { cancel, error };
        let (writes, requests) = mpsc::channel(1);
        let (control_out, control_in) = mpsc::channel(32);
        let (voice_out, voice_in) = mpsc::channel(4);
        let (voice_send, voice_requests) = mpsc::channel(4);
        let now = Instant::now();
        let mut worker = Worker {
            socket,
            tx,
            rx,
            requests,
            voice_requests,
            control_out,
            voice_out,
            pending: None,
            write_sequence: 0,
            expected: 1,
            last_receive: now,
            last_send: now,
            maintenance: now + MAINTENANCE,
            cancel: life.cancel.clone(),
        };
        let token = life.cancel.clone();
        let task = tokio::spawn(async move {
            let err = worker.run().await;
            error_tx.send_replace(Some(err));
            token.cancel();
        });
        Self {
            control_tx: ControlSender {
                writes,
                life: life.clone(),
            },
            control_rx: ControlReceiver {
                incoming: control_in,
                pending: VecDeque::new(),
                life: life.clone(),
            },
            voice_tx: VoiceSender {
                outgoing: voice_send,
                life: life.clone(),
            },
            voice_rx: VoiceReceiver {
                incoming: voice_in,
                life: life.clone(),
            },
            binding,
            life,
            task: Some(task),
        }
    }

    pub fn channel_binding(&self) -> &[u8; 32] {
        &self.binding
    }

    pub async fn closed(&self) -> Error {
        self.life.cancel.cancelled().await;
        self.life.failure()
    }

    pub async fn close(mut self) {
        self.life.cancel.cancel();
        if let Some(mut task) = self.task.take()
            && timeout(Duration::from_secs(1), &mut task).await.is_err()
        {
            task.abort();
            let _ = task.await;
        }
    }
}

#[derive(Clone)]
pub struct ControlSender {
    writes: mpsc::Sender<Write>,
    life: Life,
}

struct InFlight(CancellationToken, bool);
impl Drop for InFlight {
    fn drop(&mut self) {
        if self.1 {
            self.0.cancel();
        }
    }
}

impl ControlSender {
    /// Dropping/cancelling a submitted write closes the transport: a partially
    /// committed byte stream cannot be retried as a new command.
    pub async fn write(
        &self,
        data: &[u8],
        deadline: Instant,
        cancel: &CancellationToken,
    ) -> Result<()> {
        if data.len() > MAX_WRITE {
            return Err(Error::TooLarge);
        }
        if data.is_empty() {
            return Ok(());
        }
        let permit = tokio::select! {
            biased;
            _ = self.life.cancel.cancelled() => return Err(self.life.failure()),
            _ = cancel.cancelled() => return Err(Error::Cancelled),
            _ = sleep_until(deadline) => return Err(Error::Timeout),
            p = self.writes.reserve() => p.map_err(|_| self.life.failure())?,
        };
        let (reply, done) = oneshot::channel();
        let mut in_flight = InFlight(self.life.cancel.clone(), true);
        permit.send(Write {
            data: data.to_vec(),
            deadline,
            reply,
        });
        let result = tokio::select! {
            biased;
            _ = self.life.cancel.cancelled() => Err(self.life.failure()),
            _ = cancel.cancelled() => Err(Error::Cancelled),
            _ = sleep_until(deadline) => Err(Error::Timeout),
            result = done => result.unwrap_or_else(|_| Err(self.life.failure())),
        };
        if result.is_ok() {
            in_flight.1 = false;
        }
        result
    }
}

pub struct ControlReceiver {
    incoming: mpsc::Receiver<Vec<u8>>,
    pending: VecDeque<u8>,
    life: Life,
}

impl ControlReceiver {
    /// Cancellation of this future does not consume a partial read.
    pub async fn read(
        &mut self,
        out: &mut [u8],
        deadline: Instant,
        cancel: &CancellationToken,
    ) -> Result<usize> {
        if out.is_empty() {
            return Ok(0);
        }
        if self.life.cancel.is_cancelled() {
            return Err(self.life.failure());
        }
        if cancel.is_cancelled() {
            return Err(Error::Cancelled);
        }
        if Instant::now() >= deadline {
            return Err(Error::Timeout);
        }
        if self.pending.is_empty() {
            let chunk = tokio::select! {
                biased;
                _ = self.life.cancel.cancelled() => return Err(self.life.failure()),
                _ = cancel.cancelled() => return Err(Error::Cancelled),
                _ = sleep_until(deadline) => return Err(Error::Timeout),
                chunk = self.incoming.recv() => chunk.ok_or_else(|| self.life.failure())?,
            };
            self.pending.extend(chunk);
        }
        let n = out.len().min(self.pending.len());
        for byte in &mut out[..n] {
            *byte = self.pending.pop_front().unwrap();
        }
        Ok(n)
    }
}

struct OutgoingVoice {
    data: Vec<u8>,
    at: Instant,
}

#[derive(Clone)]
pub struct VoiceSender {
    outgoing: mpsc::Sender<OutgoingVoice>,
    life: Life,
}

impl VoiceSender {
    /// Nonblocking, bounded handoff. This confirms queue admission, not delivery.
    pub fn try_send(&self, data: &[u8]) -> Result<()> {
        if self.life.cancel.is_cancelled() {
            return Err(self.life.failure());
        }
        if data.len() + 21 > MAX_PACKET {
            return Err(Error::TooLarge);
        }
        let slot = self.outgoing.try_reserve().map_err(|e| match e {
            mpsc::error::TrySendError::Full(_) => Error::Busy,
            mpsc::error::TrySendError::Closed(_) => self.life.failure(),
        })?;
        slot.send(OutgoingVoice {
            data: data.to_vec(),
            at: Instant::now(),
        });
        Ok(())
    }
}

pub struct VoiceReceiver {
    incoming: mpsc::Receiver<Vec<u8>>,
    life: Life,
}

impl VoiceReceiver {
    pub async fn recv(&mut self, cancel: &CancellationToken) -> Result<Vec<u8>> {
        tokio::select! {
            biased;
            _ = self.life.cancel.cancelled() => Err(self.life.failure()),
            _ = cancel.cancelled() => Err(Error::Cancelled),
            p = self.incoming.recv() => p.ok_or_else(|| self.life.failure()),
        }
    }
}

struct PendingWrite {
    request: Write,
    offset: usize,
    length: usize,
    sequence: u32,
    next: Instant,
    limit: Instant,
    retry: Duration,
}

struct Worker {
    socket: UdpSocket,
    tx: SendKeys,
    rx: ReceiveKeys,
    requests: mpsc::Receiver<Write>,
    voice_requests: mpsc::Receiver<OutgoingVoice>,
    control_out: mpsc::Sender<Vec<u8>>,
    voice_out: mpsc::Sender<Vec<u8>>,
    pending: Option<PendingWrite>,
    write_sequence: u32,
    expected: u32,
    last_receive: Instant,
    last_send: Instant,
    maintenance: Instant,
    cancel: CancellationToken,
}

async fn send_raw(socket: &UdpSocket, bytes: &[u8]) -> Result<()> {
    let n = timeout(SOCKET_TIMEOUT, socket.send(bytes))
        .await
        .map_err(|_| Error::Timeout)??;
    if n != bytes.len() {
        return Err(Error::Io(io::ErrorKind::WriteZero));
    }
    Ok(())
}

impl Worker {
    async fn run(&mut self) -> Error {
        let result = self.run_loop().await;
        if self.cancel.is_cancelled() {
            let _ = self.send(CLOSE, &0_u64.to_be_bytes()).await;
        }
        result.err().unwrap_or(Error::Closed)
    }

    async fn run_loop(&mut self) -> Result<()> {
        let mut buffer = [0; MAX_PACKET + 1];
        loop {
            let mut wake = self.maintenance;
            if let Some(p) = &self.pending {
                wake = wake.min(p.next).min(p.limit);
            }
            if self.cancel.is_cancelled() {
                return Ok(());
            }
            if Instant::now() >= wake {
                self.tick().await?;
                continue;
            }
            tokio::select! {
                _ = self.cancel.cancelled() => return Ok(()),
                _ = sleep_until(wake) => self.tick().await?,
                request = self.requests.recv(), if self.pending.is_none() => {
                    let request = request.ok_or(Error::Closed)?;
                    self.pending = Some(PendingWrite { limit: request.deadline.min(Instant::now() + WRITE_TIMEOUT),
                        request, offset: 0, length: 0, sequence: 0, next: Instant::now(), retry: RETRY });
                    self.start_chunk().await?;
                }
                received = self.socket.recv(&mut buffer) => {
                    let n = received?;
                    if let Some(packet) = self.rx.decode(&buffer[..n], Instant::now()) {
                        self.last_receive = Instant::now();
                        if packet.advanced { self.send(KEY_ACK, &packet.epoch.to_be_bytes()).await?; }
                        self.receive(packet.kind, packet.epoch, packet.advanced, packet.body()).await?;
                    }
                }
                voice = self.voice_requests.recv() => {
                    let p = voice.ok_or(Error::Closed)?;
                    if p.at.elapsed() <= Duration::from_millis(100) { self.send(VOICE, &p.data).await?; }
                }
            }
        }
    }

    async fn send_packet(&mut self, packet: Packet) -> Result<()> {
        send_raw(&self.socket, packet.as_slice()).await?;
        self.last_send = Instant::now();
        Ok(())
    }

    async fn send(&mut self, kind: u8, body: &[u8]) -> Result<()> {
        if let Some(p) = self.tx.refresh(Instant::now())? {
            self.send_packet(p).await?;
        }
        let p = self.tx.encode(kind, body)?;
        self.send_packet(p).await
    }

    async fn tick(&mut self) -> Result<()> {
        let now = Instant::now();
        if let Some(p) = &self.pending {
            if now >= p.limit {
                return Err(Error::Timeout);
            }
            if now >= p.next {
                self.send_chunk().await?;
            }
        }
        if now >= self.maintenance {
            if now.duration_since(self.last_receive) >= Duration::from_secs(30) {
                return Err(Error::IdleTimeout);
            }
            if let Some(p) = self.tx.refresh(now)? {
                self.send_packet(p).await?;
            }
            if now.duration_since(self.last_send) >= Duration::from_secs(10) {
                self.send(PING, &[]).await?;
            }
            self.maintenance = now + MAINTENANCE;
        }
        Ok(())
    }

    async fn start_chunk(&mut self) -> Result<()> {
        self.write_sequence = self
            .write_sequence
            .checked_add(1)
            .ok_or(Error::KeyExhausted)?;
        let p = self.pending.as_mut().ok_or(Error::InvalidPacket)?;
        p.sequence = self.write_sequence;
        p.length = (p.request.data.len() - p.offset).min(MAX_CHUNK);
        p.retry = RETRY;
        p.limit = p.request.deadline.min(Instant::now() + WRITE_TIMEOUT);
        if Instant::now() >= p.limit {
            return Err(Error::Timeout);
        }
        self.send_chunk().await
    }

    async fn send_chunk(&mut self) -> Result<()> {
        let p = self.pending.as_mut().ok_or(Error::InvalidPacket)?;
        let mut bytes = [0; 4 + MAX_CHUNK];
        bytes[..4].copy_from_slice(&p.sequence.to_be_bytes());
        bytes[4..4 + p.length].copy_from_slice(&p.request.data[p.offset..p.offset + p.length]);
        let n = p.length + 4;
        p.next = Instant::now() + p.retry;
        p.retry = (p.retry * 2).min(Duration::from_secs(1));
        self.send(CONTROL, &bytes[..n]).await
    }

    async fn receive(&mut self, kind: u8, epoch: u64, advanced: bool, body: &[u8]) -> Result<()> {
        match kind {
            KEY_UPDATE => {
                if body != epoch.to_be_bytes() {
                    return Err(Error::InvalidPacket);
                }
                if !advanced {
                    self.send(KEY_ACK, &epoch.to_be_bytes()).await?;
                }
            }
            KEY_ACK => {
                let bytes = body.try_into().map_err(|_| Error::InvalidPacket)?;
                self.tx.confirm(u64::from_be_bytes(bytes));
            }
            VOICE => {
                if let Ok(slot) = self.voice_out.try_reserve() {
                    slot.send(body.to_vec());
                }
            }
            CONTROL => {
                if !(5..=4 + MAX_CHUNK).contains(&body.len()) {
                    return Err(Error::InvalidPacket);
                }
                let sequence = u32::from_be_bytes(body[..4].try_into().unwrap());
                if sequence == self.expected {
                    match self.control_out.try_reserve() {
                        Ok(slot) => {
                            slot.send(body[4..].to_vec());
                        }
                        Err(mpsc::error::TrySendError::Full(_)) => return Ok(()),
                        Err(mpsc::error::TrySendError::Closed(_)) => return Err(Error::Closed),
                    }
                    self.expected = self.expected.checked_add(1).ok_or(Error::KeyExhausted)?;
                } else if sequence == 0 || sequence >= self.expected {
                    return Ok(());
                }
                self.send(ACK, &body[..4]).await?;
            }
            ACK if body.len() == 4 => {
                let sequence = u32::from_be_bytes(body.try_into().unwrap());
                if self
                    .pending
                    .as_ref()
                    .is_some_and(|p| sequence == p.sequence)
                {
                    let p = self.pending.as_mut().unwrap();
                    p.offset += p.length;
                    if p.offset == p.request.data.len() {
                        let done = self.pending.take().unwrap();
                        let _ = done.request.reply.send(Ok(()));
                    } else {
                        self.start_chunk().await?;
                    }
                }
            }
            ACK | PONG => {}
            PING if body.is_empty() => self.send(PONG, &[]).await?,
            PING => {}
            CLOSE if body.len() == 8 => {
                return Err(Error::RemoteClosed(u64::from_be_bytes(
                    body.try_into().unwrap(),
                )));
            }
            CLOSE => {}
            _ => return Err(Error::InvalidPacket),
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn simultaneous_rekey_keeps_control_and_voice_alive() {
        let a = UdpSocket::bind("127.0.0.1:0").await.unwrap();
        let b = UdpSocket::bind("127.0.0.1:0").await.unwrap();
        a.connect(b.local_addr().unwrap()).await.unwrap();
        b.connect(a.local_addr().unwrap()).await.unwrap();
        let aged = Instant::now() - Duration::from_secs(12 * 60 * 60);
        let mut left = Connection::start(
            a,
            SendKeys::new(&[7; 32], aged),
            ReceiveKeys::new(&[9; 32]),
            [0; 32],
        );
        let mut right = Connection::start(
            b,
            SendKeys::new(&[9; 32], aged),
            ReceiveKeys::new(&[7; 32]),
            [0; 32],
        );
        left.voice_tx.try_send(b"left").unwrap();
        right.voice_tx.try_send(b"right").unwrap();
        let cancel = CancellationToken::new();
        assert_eq!(
            timeout(Duration::from_secs(1), right.voice_rx.recv(&cancel))
                .await
                .unwrap()
                .unwrap(),
            b"left"
        );
        assert_eq!(
            timeout(Duration::from_secs(1), left.voice_rx.recv(&cancel))
                .await
                .unwrap()
                .unwrap(),
            b"right"
        );
        let deadline = Instant::now() + Duration::from_secs(1);
        left.control_tx
            .write(b"still authenticated", deadline, &cancel)
            .await
            .unwrap();
        let mut data = [0; 19];
        assert_eq!(
            right
                .control_rx
                .read(&mut data, deadline, &cancel)
                .await
                .unwrap(),
            data.len()
        );
        assert_eq!(&data, b"still authenticated");
        right
            .control_tx
            .write(b"confirmed", deadline, &cancel)
            .await
            .unwrap();
        let mut back = [0; 9];
        assert_eq!(
            left.control_rx
                .read(&mut back, deadline, &cancel)
                .await
                .unwrap(),
            back.len()
        );
        assert_eq!(&back, b"confirmed");
        left.close().await;
        right.close().await;
    }
}
