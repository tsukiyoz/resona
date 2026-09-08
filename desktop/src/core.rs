use anyhow::{Context, Result, anyhow};
use async_channel::{Receiver, Sender, TrySendError};
use serde::Serialize;
use serde_json::{Value, json};
use std::{
    env,
    io::{BufRead, BufReader, Write},
    path::PathBuf,
    process::{Child, ChildStdin, Command, Stdio},
    sync::{
        Arc, Mutex,
        atomic::{AtomicU64, Ordering},
    },
    thread,
    time::{Duration, Instant},
};

#[cfg(windows)]
use std::os::windows::process::CommandExt;

#[derive(Debug)]
pub enum Incoming {
    Response {
        id: u64,
        result: Result<Value, String>,
    },
    Workspace(Value),
    Voice(Value),
    ProtocolError(String),
    Exited(String),
}

struct Process {
    child: Mutex<Option<Child>>,
    requests: Mutex<Option<Sender<Vec<u8>>>>,
}

impl Drop for Process {
    fn drop(&mut self) {
        self.requests
            .lock()
            .ok()
            .and_then(|mut requests| requests.take());
        if let Ok(mut child) = self.child.lock()
            && let Some(mut child) = child.take()
        {
            // EOF lets the core disconnect cleanly before the desktop process exits.
            let deadline = Instant::now() + Duration::from_millis(750);
            loop {
                match child.try_wait() {
                    Ok(Some(_)) => return,
                    Ok(None) if Instant::now() < deadline => {
                        thread::sleep(Duration::from_millis(15))
                    }
                    _ => break,
                }
            }
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

#[derive(Clone)]
pub struct CoreClient {
    process: Arc<Process>,
    next_id: Arc<AtomicU64>,
}

impl CoreClient {
    pub fn spawn() -> Result<(Self, Receiver<Incoming>)> {
        let executable = core_executable()?;
        let mut command = Command::new(&executable);
        command
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        #[cfg(windows)]
        command.creation_flags(0x0800_0000);

        let mut child = command
            .spawn()
            .with_context(|| format!("无法启动核心进程 {}", executable.display()))?;
        let stdin = child.stdin.take().context("核心进程没有标准输入")?;
        let stdout = child.stdout.take().context("核心进程没有标准输出")?;
        let stderr = child.stderr.take().context("核心进程没有错误输出")?;
        let (tx, rx) = async_channel::bounded(16);
        let (request_tx, request_rx) = async_channel::bounded(32);

        spawn_stdin_writer(stdin, request_rx, tx.clone());
        spawn_stdout_reader(stdout, tx.clone());
        thread::Builder::new()
            .name("resona-core-stderr".into())
            .spawn(move || {
                for line in BufReader::new(stderr).lines().map_while(Result::ok) {
                    eprintln!("resona-core: {line}");
                }
            })
            .context("无法启动核心错误日志线程")?;

        Ok((
            Self {
                process: Arc::new(Process {
                    child: Mutex::new(Some(child)),
                    requests: Mutex::new(Some(request_tx)),
                }),
                next_id: Arc::new(AtomicU64::new(1)),
            },
            rx,
        ))
    }

    pub fn request<T: Serialize>(&self, method: &str, params: T) -> Result<u64> {
        let id = self.next_id.fetch_add(1, Ordering::Relaxed);
        let mut payload = serde_json::to_vec(&json!({
            "id": id,
            "method": method,
            "params": params,
        }))?;
        payload.push(b'\n');
        let guard = self
            .process
            .requests
            .lock()
            .map_err(|_| anyhow!("核心命令队列锁已损坏"))?;
        let requests = guard.as_ref().context("核心进程已关闭")?;
        match requests.try_send(payload) {
            Ok(()) => {}
            Err(TrySendError::Full(_)) => return Err(anyhow!("核心命令队列已满，请稍后重试")),
            Err(TrySendError::Closed(_)) => return Err(anyhow!("核心进程已关闭")),
        }
        Ok(id)
    }
}

fn spawn_stdin_writer(stdin: ChildStdin, requests: Receiver<Vec<u8>>, tx: Sender<Incoming>) {
    thread::Builder::new()
        .name("resona-core-stdin".into())
        .spawn(move || {
            let mut stdin = stdin;
            while let Ok(payload) = requests.recv_blocking() {
                if let Err(error) = stdin.write_all(&payload).and_then(|_| stdin.flush()) {
                    let _ =
                        tx.send_blocking(Incoming::Exited(format!("核心命令写入失败：{error}")));
                    return;
                }
            }
        })
        .expect("failed to start core stdin writer");
}

fn core_executable() -> Result<PathBuf> {
    if let Some(path) = env::var_os("RESONA_CORE") {
        return Ok(PathBuf::from(path));
    }
    let mut path = env::current_exe().context("无法定位 Resona 程序")?;
    path.pop();
    path.push(if cfg!(windows) {
        "resona-core.exe"
    } else {
        "resona-core"
    });
    Ok(path)
}

fn spawn_stdout_reader(stdout: impl std::io::Read + Send + 'static, tx: Sender<Incoming>) {
    thread::Builder::new()
        .name("resona-core-stdout".into())
        .spawn(move || {
            for line in BufReader::new(stdout).lines() {
                let line = match line {
                    Ok(line) => line,
                    Err(error) => {
                        let _ = tx
                            .send_blocking(Incoming::Exited(format!("核心输出读取失败：{error}")));
                        return;
                    }
                };
                if line.trim().is_empty() {
                    continue;
                }
                match parse_line(&line) {
                    Ok(incoming) => {
                        let _ = tx.send_blocking(incoming);
                    }
                    Err(error) => {
                        let _ = tx.send_blocking(Incoming::ProtocolError(error.to_string()));
                    }
                }
            }
            let _ = tx.send_blocking(Incoming::Exited("核心进程已退出".into()));
        })
        .expect("failed to start core stdout reader");
}

fn parse_line(line: &str) -> Result<Incoming> {
    let value: Value = serde_json::from_str(line).context("核心返回了无效 JSON")?;
    if let Some(event) = value.get("event").and_then(Value::as_str) {
        let result = value.get("result").cloned().unwrap_or(Value::Null);
        return match event {
            "workspace" => Ok(Incoming::Workspace(result)),
            "voice" => Ok(Incoming::Voice(result)),
            _ => Err(anyhow!("核心返回了未知事件：{event}")),
        };
    }
    let id = value
        .get("id")
        .and_then(Value::as_u64)
        .context("核心响应缺少 id")?;
    if let Some(error) = value.get("error").and_then(Value::as_str) {
        Ok(Incoming::Response {
            id,
            result: Err(error.to_owned()),
        })
    } else {
        Ok(Incoming::Response {
            id,
            result: Ok(value.get("result").cloned().unwrap_or(Value::Null)),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_response_and_events() {
        assert!(matches!(
            parse_line(r#"{"id":7,"result":{"saved":true}}"#).unwrap(),
            Incoming::Response { id: 7, .. }
        ));
        assert!(matches!(
            parse_line(r#"{"event":"voice","result":{"enabled":false}}"#).unwrap(),
            Incoming::Voice(_)
        ));
        assert!(parse_line(r#"{"event":"unknown","result":{}}"#).is_err());
    }
}
