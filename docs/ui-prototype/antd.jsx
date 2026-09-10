import React, { useState, useEffect, useRef } from "react";
import { createRoot } from "react-dom/client";
import {
  AudioLines,
  ChevronDown,
  ChevronRight,
  Hash,
  Headphones,
  Mic,
  MicOff,
  Settings,
  Search,
  Plus,
  X,
  Send,
  Volume2,
  LogOut,
  Users,
  MessageSquare,
  Info,
  Check,
  Lock,
  ArrowRight,
  SlidersHorizontal,
  Monitor,
  Bell,
  PanelLeft,
  Signal,
  ShieldCheck,
} from "lucide-react";
import logo from "../../build/appicon.png";
const channels = [
  { name: "大厅", desc: "集合与自由交流", members: 2 },
  {
    name: "五人开黑",
    desc: "队伍语音与战术沟通，准备好就一起出发。",
    members: 4,
  },
  { name: "战术讨论", desc: "赛后复盘与战术交流", members: 0 },
  { name: "比赛房间", desc: "比赛队伍专用", members: 0, locked: true },
  { name: "暂时离开", desc: "暂离休息区", members: 1 },
];
const people = [
  {
    name: "Tsukiyo",
    role: "管理员",
    desc: "保持沟通，专注比赛。",
    color: "blue",
  },
  { name: "Kaze", role: "普通成员", desc: "一起打配合。", color: "green" },
  { name: "Mori", role: "普通成员", desc: "通常晚上在线。", color: "pink" },
  { name: "Yuki", role: "普通成员", desc: "暂无简介", color: "orange" },
];
function Icon({ icon: I, label, ...props }) {
  return (
    <button className="icon" title={label} aria-label={label} {...props}>
      <I size={17} />
    </button>
  );
}
function Avatar({ p }) {
  return <span className={"avatar " + p.color}>{p.name[0]}</span>;
}
export function App({
  mineradio = typeof document !== "undefined" &&
    document.body.dataset.variant === "mineradio",
  dark = typeof document !== "undefined" &&
    ["dark", "mineradio"].includes(document.body.dataset.variant),
} = {}) {
  const [joined, setJoined] = useState(1),
    [selection, setSelection] = useState(1),
    [tab, setTab] = useState(dark ? "chat" : "members"),
    [drawer, setDrawer] = useState(null),
    [settings, setSettings] = useState(false),
    [settingsTab, setSettingsTab] = useState("audio"),
    [mode, setMode] = useState("ptt"),
    [muted, setMuted] = useState(true),
    [deaf, setDeaf] = useState(false),
    [volume, setVolume] = useState(75),
    [search, setSearch] = useState(""),
    [memberSearch, setMemberSearch] = useState(""),
    [test, setTest] = useState(false),
    [pending, setPending] = useState(null),
    [error, setError] = useState(""),
    [scenario, setScenario] = useState("normal"),
    [offline, setOffline] = useState(false),
    [mobileNav, setMobileNav] = useState(false),
    [drafts, setDrafts] = useState({}),
    [messages, setMessages] = useState({
      1: [
        { name: "Kaze", text: "都到齐了吗？准备开一局。" },
        { name: "Mori", text: "来了，我这边准备好了。" },
        { name: "Tsukiyo", text: "收到，还是和刚才一样的位置。" },
      ],
    });
  const timer = useRef(null),
    modal = useRef(null),
    focus = useRef(null);
  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(() => {
    if (!settings && !drawer) return;
    focus.current = document.activeElement;
    if (settings) modal.current?.querySelector("button")?.focus();
    function key(e) {
      if (e.key === "Escape") {
        setSettings(false);
        setDrawer(null);
        setTest(false);
        focus.current?.focus();
      }
      if (e.key === "Tab" && settings && modal.current) {
        const nodes = [
            ...modal.current.querySelectorAll(
              "button:not(:disabled),input:not(:disabled),select:not(:disabled)",
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
    }
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [settings, drawer]);
  function close() {
    setSettings(false);
    setDrawer(null);
    setTest(false);
    focus.current?.focus();
  }
  function join(id) {
    if (pending !== null || offline || id === joined) return;
    if (channels[id].locked) {
      setError("此频道需要密码，原型不提供真实加入。");
      return;
    }
    setPending(id);
    setError("");
    timer.current = setTimeout(() => {
      setPending(null);
      if (scenario === "denied") {
        setError("加入频道被拒绝：权限不足（演示）。");
        return;
      }
      setJoined(id);
    }, 700);
  }
  function inspect(id) {
    setSelection(id);
    setDrawer({ type: "channel", id });
    setMobileNav(false);
  }
  function send(e) {
    e.preventDefault();
    if (!draft.trim() || offline || pending !== null) return;
    setMessages((old) => ({
      ...old,
      [joined]: [...(old[joined] || []), { name: "Tsukiyo", text: draft }],
    }));
    setDraft("");
  }
  const draft = drafts[joined] || "";
  function setDraft(value) {
    setDrafts((old) => ({ ...old, [joined]: value }));
  }
  const members = offline ? [] : joined === 1 ? people : [people[0]],
    shown = members.filter((p) =>
      p.name.toLowerCase().includes(memberSearch.toLowerCase()),
    );
  const voiceLabel = offline
    ? "已离线"
    : test
      ? "试听中（演示）"
      : deaf
        ? "收听与发送已暂停"
        : muted
          ? "麦克风已静音"
          : mode === "ptt"
            ? "等待按键 · 演示"
            : mode === "vad"
              ? "语音检测 · 未采集"
              : "持续传输 · 演示";
  return (
    <div className={"app" + (dark ? " dark-voice" : "") + (mineradio ? " mineradio" : "")}>
      <header className="header">
        <div className="brand">
          <img src={logo} alt="" />
          <strong>Resona</strong>
        </div>
        <nav className="global-nav">
          <span className="active">语音空间</span>
          <span className="prototype-label">
            {mineradio ? "UI 04" : dark ? "VOICE / UI 03" : "Ant Design / UI 02"}
          </span>
        </nav>
        <div className="header-right">
          <span className="preview">本地预览 · 示例数据</span>
          <select
            aria-label="演示场景"
            value={scenario}
            onChange={(e) => setScenario(e.target.value)}
          >
            <option value="normal">正常场景</option>
            <option value="denied">加入失败</option>
            <option value="device">设备失败</option>
          </select>
          <Icon
            icon={Settings}
            label="打开设置"
            onClick={() => {
              setError("");
              setSettings(true);
            }}
          />
          <Avatar p={people[0]} />
        </div>
      </header>
      <div className="body">
        <aside className={"nav " + (mobileNav ? "open" : "")}>
          <div className="server">
            <span className="server-logo">
              <AudioLines size={24} />
            </span>
            <div>
              <b>周末开黑小队</b>
              <small>TeamSpeak 3</small>
            </div>
          </div>
          <label className="search">
            <Search size={14} />
            <input
              placeholder="搜索频道"
              aria-label="搜索频道"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </label>
          <div className="section-label">
            频道列表 <span>5</span>
          </div>
          <div className="channel-list">
            {channels.map(
              (c, id) =>
                c.name.includes(search) && (
                  <React.Fragment key={c.name}>
                    <button
                      className={
                        "channel " + (id === selection ? "selected" : "")
                      }
                      onClick={() => inspect(id)}
                      onDoubleClick={() => join(id)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          join(id);
                        }
                      }}
                    >
                      <Hash size={17} />
                      <span>{c.name}</span>
                      {c.locked ? (
                        <Lock size={12} />
                      ) : (
                        <small>
                          {c.members +
                            (joined !== 1
                              ? joined === id
                                ? 1
                                : id === 1
                                  ? -1
                                  : 0
                              : 0)}
                        </small>
                      )}
                      {joined === id && !offline && <span className="dot" />}
                    </button>
                    {id === joined && !offline && (
                      <div className="mini-members">
                        {members.map((p) => (
                          <button
                            key={p.name}
                            onClick={() =>
                              setDrawer({ type: "person", person: p })
                            }
                          >
                            <Avatar p={p} />
                            <span>{p.name}</span>
                            {p.name === "Tsukiyo" && <small>我</small>}
                            {p.name === "Tsukiyo" && muted ? (
                              <MicOff size={13} />
                            ) : (
                              <Mic size={13} />
                            )}
                          </button>
                        ))}
                      </div>
                    )}
                  </React.Fragment>
                ),
            )}
          </div>
          <div className="nav-bottom">
            <span className="dot" />
            <span>{offline ? "已离开预览" : "本地示例空间"}</span>
            <small>7 人</small>
          </div>
        </aside>
        <main>
          <div className="breadcrumb">
            <Icon
              icon={PanelLeft}
              label="频道导航"
              onClick={() => setMobileNav(!mobileNav)}
            />
            <span>语音空间</span>
            <ChevronRight size={13} />
            <span>周末开黑小队</span>
            <ChevronRight size={13} />
            <b>{channels[joined].name}</b>
          </div>
          <section className="page-heading">
            <div>
              <div className="title-row">
                <h1>{channels[joined].name}</h1>
                <span className="tag green">
                  {offline ? "历史记录" : "当前频道"}
                </span>
              </div>
              <p>{channels[joined].desc}</p>
            </div>
            <button aria-label="频道资料" title="频道资料" onClick={() => setDrawer({ type: "channel", id: joined })}>
              <Info size={15} />
              频道资料
            </button>
          </section>
          <div className="tabs">
            {!dark && (
              <button
                className={tab === "members" ? "active" : ""}
                onClick={() => setTab("members")}
              >
                <Users size={16} />
                频道成员 <span>{members.length}</span>
              </button>
            )}
            <button
              className={tab === "chat" ? "active" : ""}
              onClick={() => setTab("chat")}
            >
              <MessageSquare size={16} />
              频道聊天
            </button>
            <span className="tabs-right">
              {offline ? "已离线" : "Opus Voice"}
            </span>
          </div>
          {error && !settings && (
            <div className="alert" role="alert">
              <Info size={16} />
              {error}
              <Icon icon={X} label="关闭错误" onClick={() => setError("")} />
            </div>
          )}
          {pending !== null && (
            <div className="notice" role="status">
              正在加入 {channels[pending].name}（演示）
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
          {!dark && tab === "members" ? (
            <section className="members-view">
              <div className="table-toolbar">
                <h2>
                  {dark ? "房间成员" : "在线成员"} <span>{members.length}</span>
                </h2>
                <label className="search">
                  <Search size={14} />
                  <input
                    placeholder="搜索成员"
                    aria-label="搜索成员"
                    value={memberSearch}
                    onChange={(e) => setMemberSearch(e.target.value)}
                  />
                </label>
              </div>
              {dark ? (
                <div className="voice-members">
                  {shown.map((p) => (
                    <button
                      key={p.name}
                      className="voice-member"
                      onClick={() => setDrawer({ type: "person", person: p })}
                    >
                      <div className="member-top">
                        <span>
                          {p.name === "Tsukiyo" ? "本人" : "频道成员"}
                        </span>
                        {p.name === "Tsukiyo" && muted ? (
                          <MicOff size={15} />
                        ) : (
                          <Mic size={15} />
                        )}
                      </div>
                      <Avatar p={p} />
                      <strong>{p.name}</strong>
                      <small>
                        {p.name === "Tsukiyo" ? voiceLabel : "已在频道 · 示例"}
                      </small>
                    </button>
                  ))}
                  {!shown.length && (
                    <div className="empty">
                      <Users size={28} />
                      <p>{offline ? "已离开频道" : "没有匹配的成员"}</p>
                    </div>
                  )}
                </div>
              ) : (
                <div className="table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>成员</th>
                        <th>身份</th>
                        <th>麦克风</th>
                        <th>收听</th>
                        <th>操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {shown.map((p) => (
                        <tr key={p.name}>
                          <td>
                            <button
                              className="person-cell"
                              onClick={() =>
                                setDrawer({ type: "person", person: p })
                              }
                            >
                              <Avatar p={p} />
                              <span>{p.name}</span>
                              {p.name === "Tsukiyo" && <small>我</small>}
                            </button>
                          </td>
                          <td>
                            <span
                              className={
                                "tag " + (p.role === "管理员" ? "blue" : "")
                              }
                            >
                              {p.role}
                            </span>
                          </td>
                          <td>
                            <span className="cell-state">
                              {p.name === "Tsukiyo" && muted ? (
                                <>
                                  <MicOff size={14} />
                                  已静音
                                </>
                              ) : (
                                <>
                                  <span className="status-dot" />
                                  未静音
                                </>
                              )}
                            </span>
                          </td>
                          <td>
                            {p.name === "Tsukiyo" && deaf ? "已关闭" : "已开启"}
                          </td>
                          <td>
                            <button
                              className="link"
                              onClick={() =>
                                setDrawer({ type: "person", person: p })
                              }
                            >
                              查看资料
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  {!shown.length && (
                    <div className="empty">
                      <Users size={28} />
                      <p>{offline ? "已离开频道" : "没有匹配的成员"}</p>
                    </div>
                  )}
                </div>
              )}
              {!dark && (
                <div className="table-footer">
                  共 {shown.length} 位成员 <span>1</span>
                </div>
              )}
              <section className="channel-note">
                <Info size={17} />
                <div>
                  <h3>频道主题</h3>
                  <p>{channels[joined].desc}</p>
                </div>
              </section>
            </section>
          ) : (
            <section className="chat-view">
              <div className="chat-log">
                <div className="chat-date">今天 · 示例对话</div>
                {(messages[joined] || []).map((m, i) => (
                  <article key={i}>
                    <Avatar p={people.find((p) => p.name === m.name)} />
                    <div>
                      <b>{m.name}</b>
                      <small>{i < 3 ? "20:42" : "本地消息"}</small>
                      <p>{m.text}</p>
                    </div>
                  </article>
                ))}
              </div>
              <form className="composer" onSubmit={send}>
                <textarea
                  aria-label="本地消息"
                  value={draft}
                  maxLength={2000}
                  disabled={offline}
                  placeholder={
                    offline
                      ? "已离线，历史只读"
                      : "发送到 " + channels[joined].name
                  }
                  onChange={(e) => setDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (
                      e.key === "Enter" &&
                      !e.shiftKey &&
                      !e.nativeEvent.isComposing
                    ) {
                      e.preventDefault();
                      send(e);
                    }
                  }}
                />
                <div>
                  <small>本地预览</small>
                  <button
                    className="primary"
                    aria-label="发送消息"
                    title="发送消息"
                    disabled={offline || pending !== null || !draft.trim()}
                  >
                    <Send size={15} />
                    发送
                  </button>
                </div>
              </form>
            </section>
          )}
        </main>
      </div>
      <footer className="voicebar">
        <div className="self">
          <Avatar p={people[0]} />
          <div>
            <b>Tsukiyo</b>
            <small>{voiceLabel}</small>
          </div>
        </div>
        <div className="voice-controls">
          <button
            className={muted ? "" : "enabled"}
            aria-label={muted ? "取消静音" : "静音"}
            title={muted ? "取消静音" : "静音"}
            disabled={test || offline}
            onClick={() => setMuted(!muted)}
          >
            {muted ? <MicOff size={17} /> : <Mic size={17} />}
            <span>{muted ? "取消静音" : "静音"}</span>
          </button>
          <Icon
            icon={Headphones}
            label={deaf ? "恢复收听" : "停止收听"}
            aria-pressed={deaf}
            disabled={test || offline}
            onClick={() => setDeaf(!deaf)}
          />
          <span className="divider" />
          <Volume2 size={16} />
          <input
            type="range"
            min="0"
            max="100"
            aria-label="收听音量"
            value={volume}
            disabled={test}
            onChange={(e) => setVolume(+e.target.value)}
          />
          <small>{volume}%</small>
          <Icon
            icon={SlidersHorizontal}
            label="语音设置"
            onClick={() => {
              setError("");
              setSettingsTab("audio");
              setSettings(true);
            }}
          />
        </div>
        <div className="voice-right">
          <span>
            <Signal size={14} />
            本地预览
          </span>
          <button
            className="link"
            aria-label={offline ? "返回预览" : "离开预览"}
            title={offline ? "返回预览" : "离开预览"}
            onClick={() => {
              clearTimeout(timer.current);
              setPending(null);
              setOffline(!offline);
              setMuted(true);
              setTest(false);
            }}
          >
            <LogOut size={15} />
            {offline ? "返回预览" : "离开"}
          </button>
        </div>
      </footer>
      {drawer && !settings && (
        <div
          className="drawer-shade"
          onClick={(e) => {
            if (e.target === e.currentTarget) close();
          }}
        >
          <aside
            className="drawer"
            role="dialog"
            aria-modal="false"
            aria-label="资料"
            ref={modal}
          >
            <header>
              <h2>{drawer.type === "person" ? "成员资料" : "频道资料"}</h2>
              <Icon icon={X} label="关闭资料" onClick={close} />
            </header>
            <div className="drawer-content">
              {drawer.type === "person" ? (
                <>
                  <Avatar p={drawer.person} />
                  <h1>{drawer.person.name}</h1>
                  <span className="tag blue">{drawer.person.role}</span>
                  <h3>简介</h3>
                  <p>{drawer.person.desc}</p>
                  <h3>所在频道</h3>
                  <p>
                    {drawer.person.name === "Tsukiyo"
                      ? channels[joined].name
                      : "五人开黑"}
                  </p>
                </>
              ) : (
                <>
                  <span className="channel-icon">
                    <Hash size={30} />
                  </span>
                  <h1>{channels[drawer.id].name}</h1>
                  <span className="tag">永久频道</span>
                  <h3>频道主题</h3>
                  <p>{channels[drawer.id].desc}</p>
                  <dl>
                    <dt>音频编解码器</dt>
                    <dd>Opus Voice</dd>
                    <dt>音频质量</dt>
                    <dd>6</dd>
                    <dt>人数上限</dt>
                    <dd>无限</dd>
                    <dt>密码保护</dt>
                    <dd>{channels[drawer.id].locked ? "开启" : "关闭"}</dd>
                  </dl>
                  <div className="permission">
                    <ShieldCheck size={16} />
                    继承服务器权限
                  </div>
                  {drawer.id !== joined && (
                    <button
                      className="primary full"
                      disabled={
                        offline ||
                        pending !== null ||
                        channels[drawer.id].locked
                      }
                      onClick={() => {
                        join(drawer.id);
                        setDrawer(null);
                      }}
                    >
                      {channels[drawer.id].locked
                        ? "暂不支持密码频道"
                        : "加入频道"}
                      <ArrowRight size={16} />
                    </button>
                  )}
                </>
              )}
            </div>
            <footer>本地预览 · 示例资料</footer>
          </aside>
        </div>
      )}
      {settings && (
        <div
          className="modal-shade"
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) close();
          }}
        >
          <section
            className="settings"
            ref={modal}
            role="dialog"
            aria-modal="true"
            aria-label="设置"
          >
            <header>
              <h2>设置</h2>
              <Icon icon={X} label="关闭设置" onClick={close} />
            </header>
            <div className="settings-body">
              <nav>
                {[
                  ["audio", "语音与设备", AudioLines],
                  ["notice", "提示音", Bell],
                  ["appearance", "外观", Monitor],
                ].map(([id, name, I]) => (
                  <button
                    key={id}
                    className={settingsTab === id ? "active" : ""}
                    onClick={() => {
                      setTest(false);
                      setSettingsTab(id);
                    }}
                  >
                    <I size={16} />
                    {name}
                  </button>
                ))}
              </nav>
              <div className="form-content">
                {settingsTab === "audio" ? (
                  <>
                    <h2>语音与设备</h2>
                    <p className="form-subtitle">设备、发言方式与声音处理</p>
                    <fieldset disabled={test}>
                      <div className="form-row">
                        <label htmlFor="input">输入设备</label>
                        <select id="input">
                          <option>系统默认麦克风</option>
                          <option>USB 麦克风（示例）</option>
                        </select>
                      </div>
                      <div className="form-row">
                        <label htmlFor="output">输出设备</label>
                        <select id="output">
                          <option>系统默认扬声器</option>
                          <option>游戏耳机（示例）</option>
                        </select>
                      </div>
                      {mineradio && <div className="form-row">
                        <label htmlFor="settings-volume">收听音量</label>
                        <input id="settings-volume" type="range" min="0" max="100" value={volume} onChange={(e) => setVolume(+e.target.value)} />
                        <small>{volume}%</small>
                      </div>}
                      <div className="form-row">
                        <label>发言激活</label>
                        <div className="segmented">
                          {[
                            ["ptt", "按键发言"],
                            ["continuous", "持续传输"],
                            ["vad", "语音检测"],
                          ].map(([id, name]) => (
                            <button
                              type="button"
                              key={id}
                              className={mode === id ? "active" : ""}
                              aria-pressed={mode === id}
                              onClick={() => setMode(id)}
                            >
                              {name}
                            </button>
                          ))}
                        </div>
                      </div>
                      {mode === "ptt" ? (
                        <div className="form-row">
                          <label>发言按键</label>
                          <kbd>F8</kbd>
                        </div>
                      ) : mode === "vad" ? (
                        <div className="form-row">
                          <label htmlFor="threshold">声音阈值</label>
                          <input
                            id="threshold"
                            type="range"
                            min="-60"
                            max="-10"
                            defaultValue="-38"
                          />
                        </div>
                      ) : null}
                      <div className="form-row">
                        <label htmlFor="noise">背景噪声抑制</label>
                        <select id="noise" defaultValue="中">
                          <option>关闭</option>
                          <option>低</option>
                          <option>中</option>
                          <option>高</option>
                        </select>
                      </div>
                      {["回声消除", "残余回声抑制", "发言时降低频道音量"].map(
                        (name, i) => (
                          <div className="form-row" key={name}>
                            <label htmlFor={"check" + i}>{name}</label>
                            <input
                              className="switch"
                              type="checkbox"
                              id={"check" + i}
                              defaultChecked={i === 0}
                            />
                          </div>
                        ),
                      )}
                    </fieldset>
                    <section className="mic-test">
                      <div>
                        <h3>本地麦克风测试</h3>
                        <p>
                          {test
                            ? "试听中（演示），网络发送已暂停"
                            : "未采集麦克风 · 原型演示"}
                        </p>
                      </div>
                      <button
                        className={test ? "" : "primary"}
                        onClick={() => {
                          if (scenario === "device") {
                            setError(
                              "输入设备不可用（演示），请检查所选设备。",
                            );
                            return;
                          }
                          setTest(!test);
                        }}
                      >
                        {test ? <MicOff size={15} /> : <Mic size={15} />}
                        <span>{test ? "停止测试" : "开始测试"}</span>
                      </button>
                      <div className="meter" />
                      <small>— dB</small>
                    </section>
                    {error && (
                      <p className="form-error" role="alert">
                        {error}
                      </p>
                    )}
                  </>
                ) : settingsTab === "notice" ? (
                  <>
                    <h2>提示音</h2>
                    {[
                      "连接服务器",
                      "断开连接",
                      "成员进入频道",
                      "成员离开频道",
                    ].map((n) => (
                      <label className="notification" key={n}>
                        {n}
                        <input
                          type="checkbox"
                          className="switch"
                          defaultChecked
                        />
                      </label>
                    ))}
                    <p className="form-subtitle">本地预览，不播放声音。</p>
                  </>
                ) : (
                  <>
                    <h2>外观</h2>
                    <div className="theme-choice">
                      <Monitor size={28} />
                      <div>
                        <b>{dark ? "深色" : "浅色"}</b>
                        <p>{mineradio ? "曜黑 · 冰青" : dark ? "炭灰 · 青绿" : "Ant Design 主题"}</p>
                      </div>
                      <Check size={18} />
                    </div>
                  </>
                )}
              </div>
            </div>
            <footer>
              <small>本地预览 · 设置不写入客户端</small>
              <button className="primary" onClick={close}>
                完成
              </button>
            </footer>
          </section>
        </div>
      )}
    </div>
  );
}
if (typeof document !== "undefined")
  createRoot(document.getElementById("root")).render(<App />);
