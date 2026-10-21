//! Scoped, event-driven resource synchronization, independent of voice health.
use crate::{
    lifecycle::{self, Mode},
    noise::CancellationToken,
    session::{self, Command},
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
    let mut timer = None;
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
            timer = None;
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
            _=async{if let Some(at)=timer{sleep_until(at).await}else{std::future::pending().await}}=>{timer=None;needed=true;},
            result=async {match job.as_mut(){Some(job)=>job.await,None=>std::future::pending().await}}=>{
                job.take();
                // A completion racing a new session/interest must not mark the
                // replacement scope ready or restart its fallback timer.
                let latest=life.borrow();
                if latest.mode!=Mode::Connected || latest.session_id!=session_id || *interest.borrow()!=job_interest{needed=true;continue;}
                drop(latest);
                if result.is_ok(){ready=true;status.send_modify(|s|{s.state="ready".into();s.error.clear();});if desired.active{timer=Some(Instant::now()+Duration::from_secs(300));}}
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
