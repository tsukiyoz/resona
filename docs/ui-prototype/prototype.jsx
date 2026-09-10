import React, { useState, useEffect, useRef } from "react";
import { createRoot } from "react-dom/client";
import {
  AudioLines,
  ChevronDown,
  Hash,
  Headphones,
  HeadphoneOff,
  Mic,
  MicOff,
  Settings2,
  Sun,
  Moon,
  PanelRight,
  PanelLeft,
  Plus,
  Search,
  Send,
  X,
  LogOut,
  Volume2,
  LockKeyhole,
  Check,
  ArrowRight,
  Signal,
  ShieldCheck,
  Bell,
  Monitor,
  CircleHelp,
  RotateCcw,
  Power,
} from "lucide-react";
import logo from "../../build/appicon.png";
const people = [
  {
    name: "Tsukiyo",
    initial: "T",
    color: "teal",
    self: true,
    desc: "保持沟通，专注比赛。",
  },
  { name: "Kaze", initial: "K", color: "blue", desc: "一起打配合。" },
  { name: "Mori", initial: "M", color: "rose", desc: "通常晚上在线。" },
  { name: "Yuki", initial: "Y", color: "gold", desc: "" },
];
const channels = [
  {
    id: "lobby",
    name: "大厅",
    group: "公共频道",
    topic: "集合与自由交流",
    count: 2,
  },
  {
    id: "squad",
    name: "五人开黑",
    group: "游戏频道",
    topic: "保持交流，下一局一起。",
    count: 4,
  },
  {
    id: "practice",
    name: "战术讨论",
    group: "游戏频道",
    topic: "复盘与战术交流",
    count: 0,
  },
  {
    id: "private",
    name: "比赛房间",
    group: "游戏频道",
    topic: "队伍比赛专用",
    count: 0,
    locked: true,
  },
  {
    id: "away",
    name: "暂时离开",
    group: "休息区",
    topic: "离开片刻，稍后回来。",
    count: 1,
  },
];
const initialMessages = [
  { id: 1, name: "Kaze", time: "20:41", text: "都到齐了吗？准备开一局。" },
  { id: 2, name: "Mori", time: "20:42", text: "来了，我这边准备好了。" },
  {
    id: 3,
    name: "Tsukiyo",
    time: "20:42",
    text: "我也好了，还是和刚才一样的位置。",
  },
  { id: 4, name: "Yuki", time: "20:43", text: "收到，这局我们先打配合。" },
];
function IB({ icon: Icon, label, active = false, ...props }) {
  return (
    <button
      className={"icon-button " + (active ? "active" : "")}
      title={label}
      aria-label={label}
      aria-pressed={active}
      {...props}
    >
      <Icon size={17} />
    </button>
  );
}
function Avatar({ p, large = false }) {
  return (
    <span className={"avatar " + p.color + (large ? " large" : "")}>
      {p.initial}
    </span>
  );
}
function Toggle({ label, on, change, disabled = false }) {
  return (
    <button
      role="switch"
      aria-label={label}
      aria-checked={on}
      disabled={disabled}
      className={"switch " + (on ? "on" : "")}
      onClick={() => change(!on)}
    >
      <span />
    </button>
  );
}
function App() {
  const [theme, setTheme] = useState("dark"),
    [joined, setJoined] = useState("squad"),
    [selected, setSelected] = useState({ type: "channel", id: "squad" }),
    [details, setDetails] = useState(() => window.innerWidth > 760),
    [nav, setNav] = useState(false),
    [search, setSearch] = useState("");
  const [modal, setModal] = useState(null),
    [tab, setTab] = useState("audio"),
    [mode, setMode] = useState("ptt"),
    [noise, setNoise] = useState("中"),
    [threshold, setThreshold] = useState(-38),
    [volume, setVolume] = useState(75),
    [muted, setMuted] = useState(true),
    [deaf, setDeaf] = useState(false),
    [ptt, setPtt] = useState(false),
    [aec, setAec] = useState(true),
    [duck, setDuck] = useState(false),
    [echo, setEcho] = useState(false),
    [notes, setNotes] = useState(true);
  const [test, setTest] = useState("stopped"),
    [testError, setTestError] = useState(""),
    [pending, setPending] = useState(null),
    [joinError, setJoinError] = useState(""),
    [scenario, setScenario] = useState("normal"),
    [offline, setOffline] = useState(false),
    [ended, setEnded] = useState(false),
    [closing, setClosing] = useState(false),
    [drafts, setDrafts] = useState({}),
    [messages, setMessages] = useState({ squad: initialMessages });
  const timer = useRef(null),
    testTimer = useRef(null),
    dialog = useRef(null),
    previousFocus = useRef(null),
    log = useRef(null),
    count = useRef(5);
  const current = channels.find((c) => c.id === joined),
    channel = channels.find((c) => c.id === selected.id) || current,
    person = people.find((p) => p.name === selected.id) || people[0],
    running = test === "starting" || test === "running",
    draft = drafts[joined] || "";
  function members(id) {
    return offline ? [] : people.filter((p) => p.self ? id === joined : id === "squad");
  }
  useEffect(() => {
    const release = () => setPtt(false);
    window.addEventListener("blur", release);
    return () => window.removeEventListener("blur", release);
  }, []);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);
  useEffect(() => {
    if (log.current) log.current.scrollTop = log.current.scrollHeight;
  }, [messages, joined]);
  useEffect(
    () => () => {
      clearTimeout(timer.current);
      clearTimeout(testTimer.current);
    },
    [],
  );
  useEffect(() => {
    if (!modal) return;
    previousFocus.current = document.activeElement;
    dialog.current?.querySelector("button")?.focus();
    const key = (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        closeModal();
      }
      if (e.key === "Tab") {
        const nodes = [
            ...dialog.current.querySelectorAll(
              "button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled)",
            ),
          ],
          first = nodes[0],
          last = nodes.at(-1);
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, [modal]);
  function stopTest() {
    clearTimeout(testTimer.current);
    setTest("stopped");
    setPtt(false);
  }
  function closeModal() {
    stopTest();
    setModal(null);
    previousFocus.current?.focus();
  }
  function settings() {
    setTab("audio");
    setTestError("");
    setModal("settings");
  }
  function inspect(type, id) {
    setSelected({ type, id });
    setDetails(true);
    setNav(false);
  }
  function join(id) {
    if (pending || offline || id === joined) return;
    const target = channels.find((c) => c.id === id);
    if (target.locked) {
      setJoinError("此频道需要密码，当前尚不支持加入。");
      return;
    }
    stopTest();
    setJoinError("");
    setPending(id);
    timer.current = setTimeout(() => {
      setPending(null);
      if (scenario === "denied") {
        setJoinError("加入频道被拒绝：权限不足（演示）。");
        return;
      }
      setJoined(id);
      setPtt(false);
    }, 700);
  }
  function disconnect() {
    clearTimeout(timer.current);
    setPending(null);
    stopTest();
    setMuted(true);
    setOffline(true);
    setJoinError("");
  }
  function exit() {
    disconnect();
    setModal(null);
    setClosing(true);
    timer.current = setTimeout(() => {
      setClosing(false);
      setEnded(true);
    }, 600);
  }
  function startTest() {
    setTestError("");
    setPtt(false);
    setTest("starting");
    testTimer.current = setTimeout(() => {
      if (scenario === "device") {
        setTest("stopped");
        setTestError("输入设备不可用（演示），已恢复原语音设置。");
      } else setTest("running");
    }, 600);
  }
  function send(e) {
    e?.preventDefault();
    if (!draft.trim() || offline || pending) return;
    setMessages((old) => ({
      ...old,
      [joined]: [
        ...(old[joined] || []),
        {
          id: count.current++,
          name: "Tsukiyo",
          time: "现在",
          text: draft.trim(),
        },
      ],
    }));
    setDrafts((old) => ({ ...old, [joined]: "" }));
  }
  function total(c) {
    return (
      c.count +
      (joined !== "squad"
        ? c.id === joined
          ? 1
          : c.id === "squad"
            ? -1
            : 0
        : 0)
    );
  }
  const status = offline
    ? "已离线"
    : running
      ? "本地试听（演示）"
      : deaf
        ? "已停止收听"
        : muted
          ? "麦克风已静音"
          : mode === "ptt"
            ? ptt
              ? "按键已按下（演示）"
              : "按键发言 · F8"
            : mode === "vad"
              ? "语音检测"
              : "持续传输";
  if (ended)
    return (
      <div className="end-screen">
        <img src={logo} />
        <h1>预览已结束</h1>
        <button
          className="primary"
          onClick={() => {
            setEnded(false);
            setOffline(false);
          }}
        >
          重新打开预览
          <RotateCcw size={16} />
        </button>
      </div>
    );
  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <img src={logo} alt="Resona" />
          <strong>Resona</strong>
          <span className="version">UI 01</span>
        </div>
        <span className="preview">
          <span />
          本地预览 · 示例数据
        </span>
        <div className="top-actions">
          <select
            aria-label="演示场景"
            value={scenario}
            onChange={(e) => setScenario(e.target.value)}
          >
            <option value="normal">正常场景</option>
            <option value="denied">频道拒绝</option>
            <option value="device">设备失败</option>
          </select>
          <IB
            icon={theme === "dark" ? Sun : Moon}
            label={theme === "dark" ? "切换浅色" : "切换深色"}
            onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
          />
          <IB icon={Power} label="退出预览" onClick={exit} />
        </div>
      </header>
      <div className={"workspace " + (!details ? "no-details" : "")}>
        <aside className={"sidebar " + (nav ? "mobile-open" : "")}>
          <div className="server-heading">
            <span className="server-mark">
              <AudioLines size={21} />
            </span>
            <div>
              <strong>周末开黑小队</strong>
              <small>TeamSpeak 3 · 本地示例</small>
            </div>
            <IB
              icon={Plus}
              label="添加服务器"
              onClick={() => {
                setTestError("");
                setModal("server");
              }}
            />
          </div>
          <label className="search">
            <Search size={15} />
            <input
              aria-label="搜索频道或成员"
              placeholder="搜索频道或成员"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </label>
          <div className="tree" aria-label="频道列表">
            {["公共频道", "游戏频道", "休息区"].map((group) => (
              <section key={group}>
                <h3>
                  <ChevronDown size={12} />
                  {group}
                </h3>
                {channels
                  .filter(
                    (c) =>
                      c.group === group &&
                      (c.name.includes(search) ||
                        (c.id === "squad" &&
                          people.some((p) =>
                            p.name.toLowerCase().includes(search.toLowerCase()),
                          ))),
                  )
                  .map((c) => (
                    <React.Fragment key={c.id}>
                      <button
                        className={
                          "channel-row " +
                          (selected.type === "channel" && selected.id === c.id
                            ? "selected "
                            : "") +
                          (joined === c.id && !offline ? "joined" : "")
                        }
                        onClick={() => inspect("channel", c.id)}
                        onDoubleClick={() => join(c.id)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") {
                            e.preventDefault();
                            join(c.id);
                          }
                        }}
                      >
                        <Hash size={17} />
                        <span>{c.name}</span>
                        {c.locked && <LockKeyhole size={12} />}
                        <small>{pending === c.id ? "…" : total(c)}</small>
                        {joined === c.id && !offline && (
                          <span className="joined-dot" title="当前频道" />
                        )}
                      </button>
                      {members(c.id).length > 0 && (
                        <div className="tree-members">
                          {members(c.id).map((p) => (
                            <button
                              key={p.name}
                              className={
                                "member-row " +
                                (selected.type === "person" &&
                                selected.id === p.name
                                  ? "selected"
                                  : "")
                              }
                              onClick={() => inspect("person", p.name)}
                            >
                              <Avatar p={p} />
                              <span>{p.name}</span>
                              {p.self && <small>我</small>}
                              {p.self && muted ? (
                                <MicOff size={14} />
                              ) : (
                                <Mic size={14} />
                              )}
                            </button>
                          ))}
                        </div>
                      )}
                    </React.Fragment>
                  ))}
              </section>
            ))}
            {search &&
              !channels.some((c) => c.name.includes(search)) &&
              !people.some((p) =>
                p.name.toLowerCase().includes(search.toLowerCase()),
              ) && <p className="empty-text">没有匹配的频道或成员</p>}
          </div>
          <div className="server-foot">
            <span className={"dot " + (offline ? "gray" : "")} />
            <span>{offline ? "未连接" : "本地预览"}</span>
            <span className="spacer" />
            <span className="tiny">{offline ? "—" : "7 人 · 示例"}</span>
          </div>
        </aside>
        <main className="main">
          <header className="channel-header">
            <IB
              icon={PanelLeft}
              label="展开频道"
              onClick={() => setNav(!nav)}
            />
            <div className="channel-title">
              <div>
                <Hash size={23} />
                <h1>{current.name}</h1>
                <span className="count">{offline ? 0 : total(current)}</span>
              </div>
              <p>{current.topic}</p>
            </div>
            <IB
              icon={PanelRight}
              label={details ? "收起资料" : "展开资料"}
              active={details}
              onClick={() => setDetails(!details)}
            />
          </header>
          <div className="chat-tabs">
            <span className="tab-current">频道聊天</span>
            <span className="tiny">{offline ? "只读历史" : "本地消息"}</span>
          </div>
          {joinError && (
            <div className="inline-error" role="alert">
              <CircleHelp size={16} />
              <span>{joinError}</span>
              <IB
                icon={X}
                label="关闭频道错误"
                onClick={() => setJoinError("")}
              />
            </div>
          )}
          {pending && (
            <div className="pending" role="status">
              正在加入 {channels.find((c) => c.id === pending).name}（演示）
              <button
                onClick={() => {
                  clearTimeout(timer.current);
                  setPending(null);
                }}
              >
                取消
              </button>
            </div>
          )}
          <div className="chat-log" ref={log}>
            <div className="conversation-heading">
              <span className="channel-emblem">
                <Hash size={30} />
              </span>
              <h2>{current.name}</h2>
              <p>{current.topic}</p>
            </div>
            <div className="date">
              <span />9 月 10 日 · 示例对话
              <span />
            </div>
            {(messages[joined] || []).map((m) => (
              <article className="message" key={m.id}>
                <button
                  className="avatar-button"
                  aria-label={"查看 " + m.name}
                  onClick={() => inspect("person", m.name)}
                >
                  <Avatar p={people.find((p) => p.name === m.name)} />
                </button>
                <div>
                  <header>
                    <button onClick={() => inspect("person", m.name)}>
                      {m.name}
                    </button>
                    {m.name === "Tsukiyo" && (
                      <small className="self-label">我</small>
                    )}
                    <time>{m.time}</time>
                  </header>
                  <p>{m.text}</p>
                </div>
              </article>
            ))}
            {!(messages[joined] || []).length && (
              <p className="empty-text">此频道暂无本地消息</p>
            )}
          </div>
          <form className="composer" onSubmit={send}>
            <textarea
              aria-label="频道消息"
              placeholder={
                offline ? "已离线，消息仅供查看" : "发送到 " + current.name
              }
              disabled={offline}
              maxLength={2000}
              value={draft}
              onChange={(e) =>
                setDrafts((old) => ({ ...old, [joined]: e.target.value }))
              }
              onKeyDown={(e) => {
                if (
                  e.key === "Enter" &&
                  !e.shiftKey &&
                  !e.nativeEvent.isComposing
                ) {
                  e.preventDefault();
                  send();
                }
              }}
            />
            <div>
              <span className="tiny">
                {draft.length ? draft.length + " / 2000" : "本地预览"}
              </span>
              <button
                className="primary send"
                aria-label="发送本地消息"
                disabled={!draft.trim() || offline || !!pending}
              >
                <Send size={16} />
              </button>
            </div>
          </form>
        </main>
        {details && (
          <aside className="details">
            <header>
              <span>
                {selected.type === "channel" ? "频道资料" : "成员资料"}
              </span>
              <IB icon={X} label="关闭资料" onClick={() => setDetails(false)} />
            </header>
            <div className="detail-scroll">
              {selected.type === "channel" ? (
                <>
                  <span className="detail-emblem">
                    <Hash size={28} />
                  </span>
                  <h2>{channel.name}</h2>
                  <p className="detail-sub">{channel.topic}</p>
                  <div className="tags">
                    <span>{channel.locked ? "密码频道" : "永久频道"}</span>
                    {channel.id === joined && !offline && (
                      <span className="positive">当前频道</span>
                    )}
                  </div>
                  {channel.id !== joined && !offline && (
                    <button
                      className="primary wide"
                      disabled={!!pending || channel.locked}
                      onClick={() => join(channel.id)}
                    >
                      {channel.locked ? "暂不支持密码频道" : "加入频道"}
                      <ArrowRight size={15} />
                    </button>
                  )}
                  <section>
                    <h3>简介</h3>
                    <p>
                      {channel.id === "squad"
                        ? "队伍语音与战术沟通。\n游戏中保持频道安静，有事直接说。"
                        : channel.topic}
                    </p>
                  </section>
                  <section>
                    <h3>频道属性</h3>
                    <dl>
                      <dt>当前用户</dt>
                      <dd>{total(channel)} / 无限</dd>
                      <dt>音频编解码器</dt>
                      <dd>Opus Voice</dd>
                      <dt>音频质量</dt>
                      <dd>6</dd>
                      <dt>频道类型</dt>
                      <dd>永久</dd>
                      <dt>密码保护</dt>
                      <dd>{channel.locked ? "开启" : "关闭"}</dd>
                    </dl>
                  </section>
                  <section>
                    <h3>权限</h3>
                    <div className="permission">
                      <ShieldCheck size={16} />
                      <span>继承服务器权限</span>
                    </div>
                  </section>
                </>
              ) : (
                <>
                  <Avatar p={person} large />
                  <h2>
                    {person.name}
                    {person.self && <small className="self-label">我</small>}
                  </h2>
                  <p className="detail-sub">
                    {person.self ? status : "频道成员 · 示例"}
                  </p>
                  <div className="tags">
                    <span>{person.self ? "本人" : "普通成员"}</span>
                  </div>
                  <section>
                    <h3>简介</h3>
                    <p>{person.desc || "暂无简介"}</p>
                  </section>
                  <section>
                    <h3>成员信息</h3>
                    <dl>
                      <dt>所在频道</dt>
                      <dd>{offline ? "已离线" : person.self ? current.name : "五人开黑"}</dd>
                      <dt>麦克风</dt>
                      <dd>{person.self && muted ? "已静音" : "未静音"}</dd>
                      <dt>收听状态</dt>
                      <dd>{person.self && deaf ? "已关闭" : "已开启"}</dd>
                    </dl>
                  </section>
                </>
              )}
            </div>
          </aside>
        )}
      </div>
      <footer className="voicebar">
        <div className="identity">
          <Avatar p={people[0]} />
          <div>
            <strong>Tsukiyo</strong>
            <small>{status}</small>
          </div>
        </div>
        <div className="voice-actions">
          <IB
            icon={muted ? MicOff : Mic}
            label={muted ? "取消麦克风静音" : "麦克风静音"}
            active={!muted}
            disabled={offline || running}
            onClick={() => {
              setMuted(!muted);
              setPtt(false);
            }}
          />
          <IB
            icon={deaf ? HeadphoneOff : Headphones}
            label={deaf ? "恢复收听" : "停止收听"}
            active={deaf}
            disabled={offline || running}
            onClick={() => {
              setDeaf(!deaf);
              setPtt(false);
            }}
          />
          <span className="separator" />
          <Volume2 size={16} />
          <input
            type="range"
            min="0"
            max="100"
            aria-label="收听音量"
            value={volume}
            disabled={running}
            onChange={(e) => setVolume(+e.target.value)}
          />
          <span className="numeric">{volume}%</span>
          <IB icon={Settings2} label="语音与设备设置" onClick={settings} />
        </div>
        <div className="connection">
          <span className="tiny">
            <Signal size={14} />
            {offline ? "已离线" : "本地预览"}
          </span>
          {offline ? (
            <button
              className="text-button"
              onClick={() => {
                setOffline(false);
                setMuted(true);
              }}
            >
              返回预览
              <ArrowRight size={14} />
            </button>
          ) : (
            <IB icon={LogOut} label="离开预览" onClick={disconnect} />
          )}
        </div>
      </footer>
      {closing && (
        <div className="overlay">
          <div className="exit-message" role="status">
            正在结束预览…
          </div>
        </div>
      )}
      {modal && (
        <div
          className="overlay"
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) closeModal();
          }}
        >
          <div
            ref={dialog}
            className={"modal " + (modal === "server" ? "small-modal" : "")}
            role="dialog"
            aria-modal="true"
            aria-label={modal === "server" ? "添加服务器" : "设置"}
          >
            {modal === "server" ? (
              <>
                <header className="modal-title">
                  <h2>添加服务器</h2>
                  <IB icon={X} label="关闭添加服务器" onClick={closeModal} />
                </header>
                <form
                  className="server-form"
                  onSubmit={(e) => {
                    e.preventDefault();
                    setTestError("当前为 UI 原型，不保存服务器或发起连接。");
                  }}
                >
                  {["名称", "地址", "昵称"].map((label, i) => (
                    <label key={label}>
                      {label}
                      <input
                        required={i < 2}
                        placeholder={
                          ["服务器名称", "voice.example.com:9987", "Tsukiyo"][i]
                        }
                      />
                    </label>
                  ))}
                  <p role="status" className="tiny">
                    {testError || "本地预览 · 不连接服务器"}
                  </p>
                  <button className="primary">保存</button>
                </form>
              </>
            ) : (
              <>
                <header className="modal-title">
                  <h2>设置</h2>
                  <span className="tiny">Resona</span>
                  <IB icon={X} label="关闭设置" onClick={closeModal} />
                </header>
                <div className="settings-body">
                  <nav className="settings-nav">
                    {[
                      { id: "audio", name: "语音与设备", Icon: AudioLines },
                      { id: "notifications", name: "提示音", Icon: Bell },
                      { id: "appearance", name: "外观", Icon: Monitor },
                    ].map(({ id, name, Icon }) => (
                      <button
                        key={id}
                        className={tab === id ? "selected" : ""}
                        onClick={() => {
                          stopTest();
                          setTab(id);
                        }}
                      >
                        <Icon size={16} />
                        {name}
                      </button>
                    ))}
                  </nav>
                  <div className="settings-main">
                    {tab === "audio" ? (
                      <>
                        <h2>语音与设备</h2>
                        <section className="setting-section">
                          <h3>音频设备</h3>
                          <div className="device-grid">
                            <label>
                              输入设备
                              <select
                                disabled={running}
                                defaultValue="系统默认"
                              >
                                <option>系统默认</option>
                                <option>USB 麦克风（示例）</option>
                              </select>
                            </label>
                            <label>
                              输出设备
                              <select
                                disabled={running}
                                defaultValue="系统默认"
                              >
                                <option>系统默认</option>
                                <option>游戏耳机（示例）</option>
                              </select>
                            </label>
                          </div>
                        </section>
                        <section className="setting-section">
                          <h3>发言激活</h3>
                          <div className="segmented" aria-label="激活方式">
                            {[
                              ["ptt", "按键发言"],
                              ["continuous", "持续传输"],
                              ["vad", "语音检测"],
                            ].map(([id, label]) => (
                              <button
                                key={id}
                                aria-pressed={mode === id}
                                className={mode === id ? "chosen" : ""}
                                disabled={running}
                                onClick={() => {
                                  setMode(id);
                                  setPtt(false);
                                }}
                              >
                                {label}
                              </button>
                            ))}
                          </div>
                          {mode === "ptt" ? (
                            <div className="setting-row">
                              <span>发言按键</span>
                              <button
                                className={
                                  "key-button " + (ptt ? "selected" : "")
                                }
                                disabled={muted || deaf || offline || running}
                                onPointerDown={() => setPtt(true)}
                                onPointerUp={() => setPtt(false)}
                                onPointerLeave={() => setPtt(false)}
                                onKeyDown={(e) => {
                                  if (
                                    (e.key === " " || e.key === "Enter") &&
                                    !e.repeat
                                  )
                                    setPtt(true);
                                }}
                                onKeyUp={() => setPtt(false)}
                              >
                                F8
                              </button>
                            </div>
                          ) : mode === "vad" ? (
                            <div className="setting-row">
                              <label htmlFor="threshold">声音阈值</label>
                              <input
                                id="threshold"
                                type="range"
                                min="-60"
                                max="-10"
                                value={threshold}
                                disabled={running}
                                onChange={(e) => setThreshold(+e.target.value)}
                              />
                              <span className="numeric">{threshold} dB</span>
                            </div>
                          ) : (
                            <div className="setting-row">
                              <span>麦克风状态</span>
                              <span className="muted">
                                {muted ? "已静音" : "已启用（演示）"}
                              </span>
                            </div>
                          )}
                        </section>
                        <section className="setting-section">
                          <h3>声音处理</h3>
                          <div className="setting-row">
                            <span>背景噪声抑制</span>
                            <select
                              aria-label="背景噪声抑制"
                              value={noise}
                              disabled={running}
                              onChange={(e) => setNoise(e.target.value)}
                            >
                              {["关闭", "低", "中", "高"].map((n) => (
                                <option key={n}>{n}</option>
                              ))}
                            </select>
                          </div>
                          {[
                            ["回声消除", aec, setAec],
                            ["残余回声抑制", echo, setEcho],
                            ["发言时降低频道音量", duck, setDuck],
                          ].map(([name, on, fn]) => (
                            <div className="setting-row" key={name}>
                              <span>{name}</span>
                              <Toggle
                                label={name}
                                on={on}
                                change={fn}
                                disabled={running}
                              />
                            </div>
                          ))}
                        </section>
                        <section className="test-section">
                          <div>
                            <h3>本地麦克风测试</h3>
                            <span className="tiny">
                              {test === "starting"
                                ? "正在准备（演示）"
                                : test === "running"
                                  ? "试听中（演示） · 暂停网络发送"
                                  : "未采集麦克风 · 原型预览"}
                            </span>
                          </div>
                          <button
                            className={running ? "" : "primary"}
                            onClick={running ? stopTest : startTest}
                          >
                            {running ? (
                              <>
                                <MicOff size={15} />
                                停止测试
                              </>
                            ) : (
                              <>
                                <Mic size={15} />
                                开始测试
                              </>
                            )}
                          </button>
                          <div className="meter" />
                          <span className="tiny">— dB</span>
                          {testError && (
                            <p role="alert" className="test-error">
                              {testError}
                            </p>
                          )}
                        </section>
                      </>
                    ) : tab === "appearance" ? (
                      <>
                        <h2>外观</h2>
                        <section className="setting-section">
                          <h3>主题</h3>
                          <div className="theme-options">
                            <button
                              className={theme === "dark" ? "selected" : ""}
                              onClick={() => setTheme("dark")}
                            >
                              <Moon />
                              深色{theme === "dark" && <Check size={16} />}
                            </button>
                            <button
                              className={theme === "light" ? "selected" : ""}
                              onClick={() => setTheme("light")}
                            >
                              <Sun />
                              浅色{theme === "light" && <Check size={16} />}
                            </button>
                          </div>
                        </section>
                      </>
                    ) : (
                      <>
                        <h2>提示音</h2>
                        <div className="setting-row">
                          <span>启用提示音</span>
                          <Toggle
                            label="启用提示音"
                            on={notes}
                            change={setNotes}
                          />
                        </div>
                        {[
                          "连接服务器",
                          "断开连接",
                          "成员进入频道",
                          "成员离开频道",
                        ].map((n) => (
                          <div className="setting-row" key={n}>
                            <span>{n}</span>
                            <span className="tiny">
                              {notes ? "已启用" : "已关闭"}
                            </span>
                          </div>
                        ))}
                        <p className="tiny">本地预览 · 不播放声音</p>
                      </>
                    )}
                  </div>
                </div>
                <footer className="settings-foot">
                  <span className="tiny">本地预览 · 设置不写入客户端</span>
                  <button className="primary" onClick={closeModal}>
                    完成
                  </button>
                </footer>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
createRoot(document.getElementById("root")).render(<App />);
