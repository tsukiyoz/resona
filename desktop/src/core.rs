use anyhow::{Result, anyhow};
use async_channel::{Receiver, Sender};
use resona_client::{
    core::{Core, Options, View},
    noise::CancellationToken,
};
use serde::Serialize;
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, AtomicU64, Ordering},
    },
    thread,
    time::Duration,
};

#[derive(Debug)]
pub enum Incoming {
    Response {
        id: u64,
        result: Result<Value, String>,
    },
    Workspace(Value),
    ResourceSync(Value),
    Update(Value),
    Notifications(Value),
    Voice(Value),
    MicrophoneTest(Value),
    Exited(String),
}
struct Request {
    id: u64,
    method: String,
    params: Value,
    intent: Option<u64>,
}
#[derive(Default)]
struct Intents {
    connection: AtomicU64,
    ptt: AtomicU64,
}
impl Intents {
    fn counter(&self, method: &str) -> Option<&AtomicU64> {
        match method {
            "ConnectSavedServer"
            | "ConnectServerWithPassword"
            | "RetryConnection"
            | "DisconnectServer" => Some(&self.connection),
            "SetPushToTalk" => Some(&self.ptt),
            _ => None,
        }
    }
    fn register(&self, method: &str) -> Option<u64> {
        if matches!(method, "PrepareShutdown" | "Shutdown") {
            self.connection.fetch_add(1, Ordering::AcqRel);
            self.ptt.fetch_add(1, Ordering::AcqRel);
        }
        self.counter(method)
            .map(|counter| counter.fetch_add(1, Ordering::AcqRel) + 1)
    }
    fn current(&self, request: &Request) -> bool {
        self.counter(&request.method)
            .is_none_or(|counter| request.intent == Some(counter.load(Ordering::Acquire)))
    }
}
struct Runtime {
    requests: Sender<Request>,
    urgent: Sender<Request>,
    cancel: CancellationToken,
    intents: Arc<Intents>,
    submission: Mutex<()>,
}
impl Drop for Runtime {
    fn drop(&mut self) {
        self.cancel.cancel();
    }
}
type Waiters = Arc<Mutex<HashMap<u64, Sender<Result<Value, String>>>>>;
#[derive(Clone)]
pub struct CoreClient {
    runtime: Arc<Runtime>,
    next_id: Arc<AtomicU64>,
    waiters: Waiters,
    shutting_down: Arc<AtomicBool>,
}
impl CoreClient {
    pub fn spawn() -> Result<(Self, Receiver<Incoming>)> {
        let (requests, rx) = async_channel::bounded::<Request>(32);
        let (urgent, priority) = async_channel::bounded::<Request>(4);
        let (tx, incoming) = async_channel::bounded(64);
        let cancel = CancellationToken::new();
        let token = cancel.clone();
        let waiters: Waiters = Arc::new(Mutex::new(HashMap::new()));
        let replies = waiters.clone();
        let shutting_down = Arc::new(AtomicBool::new(false));
        let closing = shutting_down.clone();
        let intents = Arc::new(Intents::default());
        let latest = intents.clone();
        thread::Builder::new().name("resona-client".into()).spawn(move||{
            let runtime=match tokio::runtime::Builder::new_current_thread().enable_all().build(){Ok(runtime)=>runtime,Err(_)=>{let _=tx.try_send(Incoming::Exited("无法启动客户端运行时".into()));return;}};
            runtime.block_on(async move{
                let result=async {
                    let core=Core::start(Options::local()?).await?;
                    let handle=core.handle.clone();let events=tx.clone();let stop=token.clone();let state=handle.view.clone();
                    let publisher=tokio::spawn(publish(state,events,stop));
                    let mut jobs=tokio::task::JoinSet::new();
                    loop {
                        let request=tokio::select! {biased;
                            _=token.cancelled()=>break,
                            request=priority.recv(),if jobs.len()<12=>request.ok(),
                            request=rx.recv(),if jobs.len()<8=>request.ok(),
                            Some(_)=jobs.join_next(),if !jobs.is_empty()=>continue,
                        };
                        let Some(request)=request else{break;};
                        let handle=handle.clone();let output=tx.clone();let waiters=replies.clone();let closing=closing.clone();let stop=token.clone();let latest=latest.clone();
                        jobs.spawn(async move{
                            let result=if latest.current(&request){handle.request(&request.method,request.params).await}else{Err("操作已被后续操作取消".into())};
                            let waiter=waiters.lock().ok().and_then(|mut w|w.remove(&request.id));
                            if let Some(waiter)=waiter{let _=waiter.try_send(result);}
                            else if !closing.load(Ordering::Acquire){tokio::select!{biased;_=stop.cancelled()=>{},_=output.send(Incoming::Response{id:request.id,result})=>{}}}
                        });
                    }
                    token.cancel();jobs.abort_all();while jobs.join_next().await.is_some(){}
                    let _=publisher.await;core.close().await;
                    Ok::<(),String>(())
                }.await;
                if let Ok(mut pending)=replies.lock(){pending.clear();}
                let message=result.err().unwrap_or_else(||"客户端已关闭".into());
                let _=tx.send(Incoming::Exited(message)).await;
            });
            // A system authorization dialog may outlive cancellation. Its worker
            // retains its data without holding application exit indefinitely.
            runtime.shutdown_timeout(Duration::from_millis(250));
        })?;
        Ok((
            Self {
                runtime: Arc::new(Runtime {
                    requests,
                    urgent,
                    cancel,
                    intents,
                    submission: Mutex::new(()),
                }),
                next_id: Arc::new(AtomicU64::new(1)),
                waiters,
                shutting_down,
            },
            incoming,
        ))
    }
    pub fn request<T: Serialize>(&self, method: &str, params: T) -> Result<u64> {
        let id = self.next_id.fetch_add(1, Ordering::Relaxed);
        let params = serde_json::to_value(params)?;
        let priority = matches!(
            method,
            "PrepareShutdown" | "Shutdown" | "DisconnectServer" | "StopNotifications"
        ) || (method == "SetPushToTalk" && params["pressed"] == false);
        let queue = if priority {
            &self.runtime.urgent
        } else {
            &self.runtime.requests
        };
        let _submission = self
            .runtime
            .submission
            .lock()
            .map_err(|_| anyhow!("客户端命令队列不可用"))?;
        if queue.is_full() || queue.is_closed() {
            return Err(anyhow!("客户端命令队列不可用，请稍后重试"));
        }
        let request = Request {
            id,
            method: method.into(),
            params,
            intent: self.runtime.intents.register(method),
        };
        queue
            .try_send(request)
            .map_err(|_| anyhow!("客户端命令队列不可用，请稍后重试"))?;
        Ok(id)
    }
    pub fn finish_shutdown(&self) {
        self.runtime.cancel.cancel();
    }
    pub async fn prepare_shutdown(&self, enabled: bool, volume: u8) -> Result<Value> {
        self.shutting_down.store(true, Ordering::Release);
        let (tx, rx) = async_channel::bounded(1);
        {
            let mut waiters = self.waiters.lock().map_err(|_| anyhow!("退出状态不可用"))?;
            let id = self.request(
                "PrepareShutdown",
                json!({"notificationEnabled":enabled,"notificationVolume":volume}),
            )?;
            waiters.insert(id, tx);
        }
        rx.recv()
            .await
            .map_err(|_| anyhow!("客户端在退出确认前关闭"))?
            .map_err(|e| anyhow!(e))
    }
}
fn field_equal(previous: &View, next: &View, field: usize) -> bool {
    match field {
        0 => Arc::ptr_eq(&previous.workspace, &next.workspace),
        1 => previous.resources == next.resources,
        2 => previous.update == next.update,
        3 => previous.notifications == next.notifications,
        4 => previous.voice == next.voice,
        _ => previous.microphone_test == next.microphone_test,
    }
}
fn field_event(next: &View, field: usize) -> Incoming {
    match field {
        0 => Incoming::Workspace(json!(*next.workspace)),
        1 => Incoming::ResourceSync(next.resources.clone()),
        2 => Incoming::Update(next.update.clone()),
        3 => Incoming::Notifications(next.notifications.clone()),
        4 => Incoming::Voice(next.voice.clone()),
        _ => Incoming::MicrophoneTest(next.microphone_test.clone()),
    }
}
fn delivered(previous: &mut View, next: &View, field: usize) {
    match field {
        0 => previous.workspace = next.workspace.clone(),
        1 => previous.resources = next.resources.clone(),
        2 => previous.update = next.update.clone(),
        3 => previous.notifications = next.notifications.clone(),
        4 => previous.voice = next.voice.clone(),
        _ => previous.microphone_test = next.microphone_test.clone(),
    }
}
async fn publish(
    mut view: tokio::sync::watch::Receiver<View>,
    output: Sender<Incoming>,
    cancel: CancellationToken,
) {
    let mut previous = view.borrow().clone();
    let mut initialized = 0u8;
    let mut cursor = 0;
    loop {
        let next = view.borrow_and_update().clone();
        let field = (0..6).map(|offset| (cursor + offset) % 6).find(|&field| {
            initialized & (1 << field) == 0 || !field_equal(&previous, &next, field)
        });
        if let Some(field) = field {
            tokio::select! {biased;
                _=cancel.cancelled()=>return,
                result=output.send(field_event(&next,field))=>{
                    if result.is_err(){return;}
                    delivered(&mut previous,&next,field);
                    initialized |= 1 << field;
                    cursor=(field+1)%6;
                },
                result=view.changed()=>if result.is_err(){return;},
            }
        } else {
            tokio::select! {biased;_=cancel.cancelled()=>return,result=view.changed()=>if result.is_err(){return;}}
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn rejected_submission_preserves_accepted_disconnect() {
        let (requests, _normal) = async_channel::bounded(1);
        let (urgent, _priority) = async_channel::bounded(4);
        let core = CoreClient {
            runtime: Arc::new(Runtime {
                requests,
                urgent,
                cancel: CancellationToken::new(),
                intents: Arc::new(Intents::default()),
                submission: Mutex::new(()),
            }),
            next_id: Arc::new(AtomicU64::new(1)),
            waiters: Arc::new(Mutex::new(HashMap::new())),
            shutting_down: Arc::new(AtomicBool::new(false)),
        };
        core.request("GetWorkspace", json!({})).unwrap();
        core.request("DisconnectServer", json!({})).unwrap();
        let generation = core.runtime.intents.connection.load(Ordering::Acquire);
        assert!(
            core.request("ConnectSavedServer", json!({"id":"late"}))
                .is_err()
        );
        assert_eq!(
            core.runtime.intents.connection.load(Ordering::Acquire),
            generation
        );
    }
    #[test]
    fn urgent_actions_invalidate_older_queued_intents() {
        let intents = Intents::default();
        for (first, last) in [
            ("SetPushToTalk", "SetPushToTalk"),
            ("ConnectSavedServer", "DisconnectServer"),
            ("ConnectServerWithPassword", "PrepareShutdown"),
        ] {
            let queued = Request {
                id: 1,
                method: first.into(),
                params: Value::Null,
                intent: intents.register(first),
            };
            assert!(intents.current(&queued));
            intents.register(last);
            assert!(!intents.current(&queued));
        }
    }
    #[tokio::test]
    async fn presentation_backpressure_delivers_each_field_without_workspace_flooding() {
        let initial = View {
            workspace: Arc::new(Default::default()),
            voice: json!(0),
            microphone_test: Value::Null,
            resources: Value::Null,
            update: Value::Null,
            notifications: Value::Null,
        };
        let (state, rx) = tokio::sync::watch::channel(initial);
        let (tx, out) = async_channel::bounded(1);
        let cancel = CancellationToken::new();
        let task = tokio::spawn(publish(rx, tx, cancel.clone()));
        let mut workspace = 0;
        let mut voice = false;
        for step in 1..=8 {
            let event = tokio::time::timeout(Duration::from_secs(1), out.recv())
                .await
                .unwrap()
                .unwrap();
            workspace += usize::from(matches!(event, Incoming::Workspace(_)));
            voice |= matches!(event, Incoming::Voice(_));
            state.send_modify(|view| view.voice = json!(step));
            if voice {
                break;
            }
        }
        assert!(voice);
        assert_eq!(workspace, 1);
        cancel.cancel();
        task.await.unwrap();
    }
    #[tokio::test]
    async fn blocked_presentation_queue_remains_cancellable() {
        let (tx, _rx) = async_channel::bounded(1);
        tx.send(Incoming::Voice(Value::Null)).await.unwrap();
        let view = View {
            workspace: Arc::new(Default::default()),
            voice: Value::Null,
            microphone_test: Value::Null,
            resources: Value::Null,
            update: Value::Null,
            notifications: Value::Null,
        };
        let (_state, rx) = tokio::sync::watch::channel(view);
        let cancel = CancellationToken::new();
        let token = cancel.clone();
        let task = tokio::spawn(publish(rx, tx, token));
        cancel.cancel();
        tokio::time::timeout(Duration::from_secs(1), task)
            .await
            .unwrap()
            .unwrap();
    }
}
