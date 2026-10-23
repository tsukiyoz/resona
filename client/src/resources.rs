//! Scoped, event-driven resource synchronization, independent of voice health.
use crate::{
    lifecycle::{self, Mode},
    noise::CancellationToken,
    session::{self, Command},
    wire::pb,
};
use serde::{Deserialize, Serialize};
use std::{future::Future, pin::Pin, time::Duration};
use tokio::{
    sync::watch,
    task::JoinHandle,
    time::{Instant, sleep, sleep_until},
};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Interest {
    pub active: bool,
    pub all_channels: bool,
    pub all_members: bool,
    pub generation: u64,
    pub retry: bool,
}
impl Default for Interest {
    fn default() -> Self {
        Self {
            active: true,
            all_channels: true,
            all_members: true,
            generation: 0,
            retry: false,
        }
    }
}
#[derive(Clone, Debug, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    pub generation: u64,
    #[serde(rename = "sessionID")]
    pub session_id: String,
    pub state: String,
    pub attempt: u32,
    pub error: String,
}
pub struct Observer {
    interest: watch::Sender<Interest>,
    pub status: watch::Receiver<Status>,
    cancel: CancellationToken,
    task: Option<JoinHandle<()>>,
}
impl Drop for Observer {
    fn drop(&mut self) {
        self.cancel.cancel();
    }
}
impl Observer {
    pub fn interest_handle(&self) -> watch::Sender<Interest> {
        self.interest.clone()
    }
    pub fn start(life: watch::Receiver<lifecycle::State>) -> Self {
        let (interest, interest_rx) = watch::channel(Interest::default());
        let (status_tx, status) = watch::channel(Status {
            state: "idle".into(),
            ..Default::default()
        });
        let cancel = CancellationToken::new();
        let token = cancel.clone();
        let task = tokio::spawn(async move { run(life, interest_rx, status_tx, &token).await });
        Self {
            interest,
            status,
            cancel,
            task: Some(task),
        }
    }
    pub fn set(&self, interest: Interest) -> std::result::Result<(), &'static str> {
        if interest.all_members && !interest.all_channels {
            return Err("成员关注需要频道范围");
        }
        self.interest.send_replace(interest);
        Ok(())
    }
    pub async fn close(mut self) {
        self.cancel.cancel();
        if let Some(task) = self.task.take() {
            let _ = task.await;
        }
    }
}
type Job = Pin<Box<dyn Future<Output = session::Result<()>> + Send>>;
const FALLBACK_DELAY: Duration = Duration::from_secs(300);

#[derive(Default)]
struct Fallback {
    revision: Option<u64>,
    deadline: Option<Instant>,
}
impl Fallback {
    fn received(&mut self, state: &pb::State, interest: Interest, now: Instant) {
        if interest.active
            && state.can_watch_resources
            && state.all_channels == interest.all_channels
            && state.all_members == interest.all_members
            && self
                .revision
                .is_none_or(|revision| state.revision > revision)
        {
            self.revision = Some(state.revision);
            self.synchronized(now);
        }
    }

    fn synchronized(&mut self, now: Instant) {
        self.deadline = Some(now + FALLBACK_DELAY);
    }
}

async fn run(
    mut life: watch::Receiver<lifecycle::State>,
    mut interest: watch::Receiver<Interest>,
    status: watch::Sender<Status>,
    cancel: &CancellationToken,
) {
    let mut session_id = String::new();
    let mut desired = Interest::default();
    let mut ready = false;
    let mut failed = false;
    let mut needed = true;
    let mut job: Option<Job> = None;
    let mut job_cancel = CancellationToken::new();
    let mut fallback = Fallback::default();
    let mut job_id = 0_u64;
    let mut job_interest = Interest::default();
    let (attempt_tx, mut attempts) = tokio::sync::mpsc::channel(1);
    loop {
        if cancel.is_cancelled() {
            break;
        }
        let current = life.borrow().clone();
        let next = *interest.borrow();
        let id = if current.mode == Mode::Connected {
            current.session_id.clone()
        } else {
            String::new()
        };
        if next != desired || id != session_id {
            fallback = Fallback::default();
            if id != session_id {
                job_cancel.cancel();
                job.take();
                ready = false;
                failed = false;
            }
            desired = next;
            session_id = id;
            needed = true;
        }
        let now = Instant::now();
        if ready
            && !failed
            && job.is_none()
            && let Some(handle) = &current.connection
        {
            fallback.received(&handle.state.borrow().state, desired, now);
        }
        // Read the latest accepted snapshot before deciding that silence expired.
        if fallback.deadline.is_some_and(|deadline| deadline <= now) {
            fallback.deadline = None;
            needed = true;
        }
        if needed && job.is_none() {
            needed = false;
            let initial = Status {
                generation: desired.generation,
                session_id: session_id.clone(),
                attempt: 0,
                ..Default::default()
            };
            if session_id.is_empty() {
                status.send_replace(Status {
                    state: "idle".into(),
                    ..initial
                });
            } else if failed && !desired.retry {
                status.send_modify(|s| {
                    s.generation = desired.generation;
                    s.session_id = session_id.clone();
                });
            } else if let Some(handle) = current.connection {
                fallback.deadline = None;
                let blocking = !ready || failed;
                failed = false;
                status.send_replace(Status {
                    state: if blocking { "syncing" } else { "refreshing" }.into(),
                    ..initial
                });
                job_cancel = CancellationToken::new();
                job_id = job_id.wrapping_add(1);
                job_interest = desired;
                job = Some(Box::pin(synchronize(
                    handle,
                    desired,
                    job_cancel.clone(),
                    attempt_tx.clone(),
                    job_id,
                )));
            }
        }
        tokio::select! {
            _=cancel.cancelled()=>break,
            r=life.changed()=>{if r.is_err(){break;}},
            r=interest.changed()=>{if r.is_err(){break;}},
            Some((id,attempt))=attempts.recv()=>{if job.is_some() && id==job_id && job_interest==desired{status.send_modify(|s|s.attempt=attempt);}},
            _=async{if let Some(at)=fallback.deadline{sleep_until(at).await}else{std::future::pending().await}}=>{},
            result=async {match job.as_mut(){Some(job)=>job.await,None=>std::future::pending().await}}=>{
                job.take();
                // A completion racing a new session/interest must not mark the
                // replacement scope ready or restart its fallback timer.
                let latest=life.borrow();
                if latest.mode!=Mode::Connected || latest.session_id!=session_id || *interest.borrow()!=job_interest{needed=true;continue;}
                drop(latest);
                if result.is_ok(){ready=true;status.send_modify(|s|{s.state="ready".into();s.error.clear();});if desired.active{fallback.synchronized(Instant::now());}}
                else{failed=true;status.send_modify(|s|{s.state="failed".into();s.error="资源同步失败，请重试；语音连接可能仍然正常".into();});}
            }
        }
    }
    job_cancel.cancel();
    drop(job);
}
async fn synchronize(
    handle: session::Handle,
    interest: Interest,
    cancel: CancellationToken,
    progress: tokio::sync::mpsc::Sender<(u64, u32)>,
    job_id: u64,
) -> session::Result<()> {
    for attempt in 1..=3 {
        if cancel.is_cancelled() {
            return Err(session::Error::Cancelled);
        }
        let _ = progress.try_send((job_id, attempt));
        let result = handle
            .command(
                Command::Watch {
                    channels: interest.active && interest.all_channels,
                    members: interest.active && interest.all_members,
                },
                Instant::now() + Duration::from_secs(2),
                &cancel,
            )
            .await;
        match result {
            Ok(()) => return Ok(()),
            Err(e) if attempt == 3 || matches!(e,session::Error::Rejected(code) if code!=3) => {
                return Err(e);
            }
            _ => {}
        }
        tokio::select! {biased;_=cancel.cancelled()=>return Err(session::Error::Cancelled),_=sleep(Duration::from_millis(300*attempt as u64))=>{}}
    }
    unreachable!()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn resource_state(revision: u64) -> pb::State {
        pb::State {
            revision,
            can_watch_resources: true,
            all_channels: true,
            all_members: true,
            ..Default::default()
        }
    }

    #[test]
    fn fallback_counts_silence_since_the_latest_resource_data() {
        let now = Instant::now();
        let mut fallback = Fallback::default();
        let interest = Interest::default();
        fallback.synchronized(now);
        assert_eq!(fallback.deadline, Some(now + FALLBACK_DELAY));

        let pushed_at = now + Duration::from_secs(240);
        fallback.received(&resource_state(7), interest, pushed_at);
        assert_eq!(fallback.deadline, Some(pushed_at + FALLBACK_DELAY));
        assert!(fallback.deadline.unwrap() > now + FALLBACK_DELAY);

        // Chat, credential and other snapshot publications keep the same revision.
        fallback.received(
            &resource_state(7),
            interest,
            pushed_at + Duration::from_secs(60),
        );
        fallback.received(
            &resource_state(6),
            interest,
            pushed_at + Duration::from_secs(80),
        );
        assert_eq!(fallback.deadline, Some(pushed_at + FALLBACK_DELAY));

        // A fresh push already received at expiry wins over a redundant fetch.
        let at_expiry = fallback.deadline.unwrap();
        fallback.received(&resource_state(8), interest, at_expiry);
        assert_eq!(fallback.deadline, Some(at_expiry + FALLBACK_DELAY));

        // A successful fetch also restarts the window even if nothing changed.
        let synced_at = at_expiry + Duration::from_secs(10);
        fallback.synchronized(synced_at);
        assert_eq!(fallback.deadline, Some(synced_at + FALLBACK_DELAY));
    }

    #[test]
    fn fallback_does_not_follow_background_or_another_resource_scope() {
        let now = Instant::now();
        let mut fallback = Fallback::default();
        let mut state = resource_state(20);
        let background = Interest {
            active: false,
            ..Default::default()
        };
        fallback.received(&state, background, now);
        assert!(fallback.deadline.is_none());

        state.all_members = false;
        fallback.received(&state, Interest::default(), now);
        assert!(fallback.deadline.is_none());
        state.all_members = true;
        state.all_channels = false;
        fallback.received(&state, Interest::default(), now);
        assert!(fallback.deadline.is_none());
        state.all_channels = true;
        fallback.received(&state, Interest::default(), now);
        assert_eq!(fallback.revision, Some(20));

        // Session/interest replacement discards the old clock and revision.
        fallback = Fallback::default();
        assert!(fallback.deadline.is_none());
        fallback.received(&resource_state(1), Interest::default(), now);
        assert_eq!(fallback.revision, Some(1));
        assert_eq!(fallback.deadline, Some(now + FALLBACK_DELAY));
    }
}
