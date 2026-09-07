import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import {
  ArrowLeft,
  ArrowUpRight,
  Check,
  ChevronDown,
  Headphones,
  Info,
  Menu,
  MessageSquare,
  MicOff,
  Moon,
  Music2,
  Pencil,
  Plus,
  Radio,
  Send,
  Server,
  Settings,
  Signal,
  Sun,
  Trash2,
  Users,
  Volume2,
  X,
} from "lucide-react";
import {
  api,
  browserPreview,
  errorMessage,
  type ServerProfile,
  type Workspace,
} from "./api";

function IconButton({
  label,
  children,
  onClick,
  disabled = false,
  className = "",
}: {
  label: string;
  children: ReactNode;
  onClick?: () => void;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <button
      className={`icon-button ${className}`}
      aria-label={label}
      title={label}
      onClick={onClick}
      disabled={disabled}
    >
      {children}
    </button>
  );
}
function readPreference(key: string, fallback: string): string {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}
const newServer = (): ServerProfile => ({
  id: "",
  name: "",
  address: "",
  nickname: "Resona",
});

export default function App() {
  const [workspace, setWorkspace] = useState<Workspace>();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [page, setPage] = useState<"chat" | "settings">("chat");
  const [editing, setEditing] = useState<ServerProfile | null>(null);
  const [deleting, setDeleting] = useState<ServerProfile | null>(null);
  const [selectedServer, setSelectedServer] = useState("");
  const [draft, setDraft] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [theme, setTheme] = useState(() =>
    readPreference("resona.theme", "light"),
  );
  const [compact, setCompact] = useState(
    () => readPreference("resona.compact", "false") === "true",
  );
  const messagesEnd = useRef<HTMLDivElement>(null);
  const isPreview = workspace?.session.mode === "preview";
  const channel = workspace?.channels.find(
    (c) => c.id === workspace.session.channelID,
  );
  const messages =
    workspace?.messages.filter((m) => m.channelID === channel?.id) ?? [];
  const server = workspace?.servers.find((s) => s.id === selectedServer);

  useEffect(() => {
    api
      .GetWorkspace()
      .then(setWorkspace)
      .catch((e) => setError(errorMessage(e)));
  }, []);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.dataset.compact = String(compact);
    try {
      localStorage.setItem("resona.theme", theme);
      localStorage.setItem("resona.compact", String(compact));
    } catch {
      /* Preferences remain active for this session. */
    }
  }, [theme, compact]);
  useEffect(() => {
    messagesEnd.current?.scrollIntoView({ block: "end" });
  }, [workspace?.messages.length, channel?.id]);
  useEffect(() => {
    const close = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !pending) {
        setEditing(null);
        setDeleting(null);
        setSidebarOpen(false);
      }
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [pending]);

  async function run(action: () => Promise<Workspace>, after?: () => void) {
    if (pending) return;
    setPending(true);
    setError("");
    try {
      setWorkspace(await action());
      after?.();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setPending(false);
    }
  }
  function send(event?: FormEvent) {
    event?.preventDefault();
    if (draft.trim() && isPreview)
      void run(
        () => api.SendMessage(draft),
        () => setDraft(""),
      );
  }
  function changePreview() {
    void run(
      () => (isPreview ? api.LeavePreview() : api.OpenPreview()),
      () => {
        setPage("chat");
        setDraft("");
      },
    );
  }

  return (
    <div className="app-shell">
      <nav className="app-rail" aria-label="主导航">
        <button
          className="brand-mark"
          title="Resona"
          aria-label="Resona 聊天"
          onClick={() => setPage("chat")}
        >
          <img src="/resona.svg" alt="" />
        </button>
        <div className="rail-divider" />
        <IconButton
          label="聊天"
          className={page === "chat" ? "active" : ""}
          onClick={() => setPage("chat")}
        >
          <MessageSquare size={21} />
        </IconButton>
        <IconButton label="添加服务器" onClick={() => setEditing(newServer())}>
          <Plus size={22} />
        </IconButton>
        <div className="rail-spacer" />
        <IconButton
          label="设置"
          className={page === "settings" ? "active" : ""}
          onClick={() => setPage("settings")}
        >
          <Settings size={21} />
        </IconButton>
        <div
          className="rail-avatar"
          title={workspace?.session.nickname || "我"}
        >
          我
        </div>
      </nav>

      <aside className={`sidebar ${sidebarOpen ? "mobile-open" : ""}`}>
        <header className="brand-heading">
          <div>
            <span className="wordmark">
              Resona<span>.</span>
            </span>
            <span className="brand-subtitle">共鸣</span>
          </div>
          <IconButton
            label="关闭侧栏"
            className="mobile-only"
            onClick={() => setSidebarOpen(false)}
          >
            <X size={18} />
          </IconButton>
        </header>
        <div className="sidebar-content">
          <div className="section-label">
            <span>服务器</span>
            <IconButton
              label="新建服务器书签"
              onClick={() => setEditing(newServer())}
            >
              <Plus size={16} />
            </IconButton>
          </div>
          <div className="server-list">
            {workspace?.servers.length ? (
              workspace.servers.map((s) => (
                <button
                  key={s.id}
                  className={`server-row ${selectedServer === s.id ? "selected" : ""}`}
                  onClick={() => {
                    setSelectedServer(s.id);
                    setPage("chat");
                  }}
                >
                  <span className="server-icon">
                    <Server size={17} />
                  </span>
                  <span className="row-copy">
                    <strong>{s.name}</strong>
                    <small>未连接</small>
                  </span>
                  <span className="status-dot" />
                </button>
              ))
            ) : (
              <div className="bookmark-empty">
                <Server size={20} />
                <span>暂无服务器书签</span>
                <button
                  className="text-button"
                  onClick={() => setEditing(newServer())}
                >
                  添加服务器 <Plus size={13} />
                </button>
              </div>
            )}
          </div>
          {server && (
            <div className="bookmark-actions">
              <span title={server.address}>{server.address}</span>
              <IconButton
                label={`编辑 ${server.name}`}
                onClick={() => setEditing({ ...server })}
              >
                <Pencil size={14} />
              </IconButton>
              <IconButton
                label={`删除 ${server.name}`}
                onClick={() => setDeleting(server)}
              >
                <Trash2 size={14} />
              </IconButton>
            </div>
          )}

          <div className="section-label channel-section">
            <span>频道</span>
            <ChevronDown size={14} />
          </div>
          {isPreview ? (
            <div className="channel-list">
              {workspace?.channels.map((c) => (
                <button
                  key={c.id}
                  className={`channel-row ${channel?.id === c.id ? "selected" : ""}`}
                  onClick={() =>
                    void run(
                      () => api.SelectChannel(c.id),
                      () => {
                        setPage("chat");
                        setSidebarOpen(false);
                        setDraft("");
                      },
                    )
                  }
                  disabled={pending}
                >
                  {c.id === "music" ? (
                    <Music2 size={17} />
                  ) : (
                    <Volume2 size={17} />
                  )}
                  <span>{c.name}</span>
                  <small>{c.members}</small>
                </button>
              ))}
            </div>
          ) : (
            <div className="channels-empty">未连接</div>
          )}
        </div>
        <div className="preview-control">
          <span className={`status-dot ${isPreview ? "preview" : ""}`} />
          <span>{isPreview ? "本地预览" : "离线"}</span>
          <button
            className="text-button"
            disabled={pending || !workspace}
            onClick={changePreview}
          >
            {isPreview ? "结束预览" : "本地预览"}
            <ArrowUpRight size={14} />
          </button>
        </div>
        <div className="identity">
          <div className="avatar">我</div>
          <div className="identity-copy">
            <strong>{workspace?.session.nickname || "我"}</strong>
            <small>{isPreview ? "本地预览" : "离线"}</small>
          </div>
          <IconButton label="语音设备尚未接入" disabled>
            <MicOff size={18} />
          </IconButton>
          <IconButton label="音频播放尚未接入" disabled>
            <Headphones size={18} />
          </IconButton>
        </div>
      </aside>
      {sidebarOpen && (
        <button
          className="sidebar-scrim"
          aria-label="收起侧栏"
          onClick={() => setSidebarOpen(false)}
        />
      )}

      <main className="main-view">
        <header className="workspace-header">
          <div className="workspace-title">
            <IconButton
              label="展开侧栏"
              className="mobile-only"
              onClick={() => setSidebarOpen(true)}
            >
              <Menu size={20} />
            </IconButton>
            {page === "settings" ? (
              <Settings size={20} />
            ) : (
              <Volume2 size={21} />
            )}
            <strong>
              {page === "settings" ? "设置" : channel?.name || "会话"}
            </strong>
            {page === "chat" && channel && (
              <span className="channel-subtitle">{channel.description}</span>
            )}
          </div>
          <div className="header-status">
            {browserPreview && <span className="browser-tag">浏览器预览</span>}
            <span className={`connection-label ${isPreview ? "preview" : ""}`}>
              <span className={`status-dot ${isPreview ? "preview" : ""}`} />
              {isPreview ? "本地预览" : "离线"}
            </span>
          </div>
        </header>
        {error && (
          <div className="error-banner" role="alert">
            <Info size={17} />
            <span>{error}</span>
            <IconButton label="关闭错误提示" onClick={() => setError("")}>
              <X size={16} />
            </IconButton>
          </div>
        )}
        {page === "settings" ? (
          <div className="settings-view">
            <button
              className="text-button back-button"
              onClick={() => setPage("chat")}
            >
              <ArrowLeft size={15} />
              返回会话
            </button>
            <h1>外观</h1>
            <div className="setting-row">
              <div>
                <strong>主题</strong>
                <span>界面颜色</span>
              </div>
              <div className="segmented" aria-label="主题">
                {(["light", "dark"] as const).map((value) => (
                  <button
                    key={value}
                    aria-pressed={theme === value}
                    className={theme === value ? "selected" : ""}
                    onClick={() => setTheme(value)}
                  >
                    {value === "light" ? <Sun size={16} /> : <Moon size={16} />}
                    {value === "light" ? "浅色" : "深色"}
                    {theme === value && <Check size={14} />}
                  </button>
                ))}
              </div>
            </div>
            <div className="setting-row">
              <div>
                <strong>紧凑文字</strong>
                <span>消息与导航字号</span>
              </div>
              <label className="switch">
                <input
                  type="checkbox"
                  checked={compact}
                  onChange={(e) => setCompact(e.target.checked)}
                  aria-label="紧凑文字"
                />
                <span />
              </label>
            </div>
            <div className="settings-about">
              <img src="/resona.svg" alt="" />
              <div>
                <strong>Resona</strong>
                <span>0.1.0 · 共鸣</span>
              </div>
            </div>
          </div>
        ) : (
          <div className="conversation-layout">
            <section className="conversation" aria-label="频道聊天">
              <div
                className={`message-history ${!messages.length ? "is-empty" : ""}`}
                aria-live="polite"
              >
                {!workspace ? (
                  <div className="empty-state">
                    <Radio size={28} />
                    <h1>{error ? "加载失败" : "正在载入"}</h1>
                    {error && (
                      <button
                        className="primary-button"
                        disabled={pending}
                        onClick={() => void run(() => api.GetWorkspace())}
                      >
                        重试
                      </button>
                    )}
                  </div>
                ) : !isPreview ? (
                  <div className="empty-state">
                    <div className="empty-symbol">
                      <Radio size={31} />
                    </div>
                    <span className="empty-eyebrow">RESONA</span>
                    <h1>还没有加入会话</h1>
                    <p>{server ? server.name : "离线"}</p>
                    <button
                      className="primary-button"
                      disabled={pending}
                      onClick={changePreview}
                    >
                      <Radio size={16} />
                      本地预览
                    </button>
                  </div>
                ) : !messages.length ? (
                  <div className="empty-state channel-empty">
                    <div className="empty-symbol">
                      {channel?.id === "music" ? (
                        <Music2 size={29} />
                      ) : (
                        <MessageSquare size={29} />
                      )}
                    </div>
                    <h1>{channel?.name}</h1>
                    <p>暂无消息</p>
                    <span className="preview-badge">
                      <span className="status-dot preview" />
                      本地预览
                    </span>
                  </div>
                ) : (
                  <>
                    <div className="history-heading">
                      <span>本地预览</span>
                      <span>
                        {new Date().toLocaleDateString("zh-CN", {
                          month: "long",
                          day: "numeric",
                        })}
                      </span>
                    </div>
                    {messages.map((m) => (
                      <article className="message" key={m.id}>
                        <div className="avatar">{m.author.slice(0, 1)}</div>
                        <div className="message-copy">
                          <div className="message-meta">
                            <strong>{m.author}</strong>
                            <time dateTime={m.createdAt}>
                              {new Date(m.createdAt).toLocaleTimeString(
                                "zh-CN",
                                { hour: "2-digit", minute: "2-digit" },
                              )}
                            </time>
                            <span>本地</span>
                          </div>
                          <p>{m.text}</p>
                        </div>
                      </article>
                    ))}
                  </>
                )}
                <div ref={messagesEnd} />
              </div>
              <form
                className={`composer ${!isPreview ? "disabled" : ""}`}
                onSubmit={send}
              >
                <textarea
                  aria-label="消息"
                  placeholder={
                    isPreview ? `发送到 ${channel?.name || "频道"}` : "未连接"
                  }
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  maxLength={2000}
                  disabled={!isPreview || pending}
                  rows={2}
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
                <div className="composer-footer">
                  <span>
                    {isPreview ? "本地预览" : "离线"}
                    {draft.length > 1800 && ` · ${draft.length}/2000`}
                  </span>
                  <button
                    className="send-button"
                    type="submit"
                    title="发送消息"
                    aria-label="发送消息"
                    disabled={!isPreview || pending || !draft.trim()}
                  >
                    <Send size={16} />
                  </button>
                </div>
              </form>
              <footer className="conversation-footer">
                <Signal size={12} />
                <span>
                  {isPreview ? "本地预览 · 未连接远程服务器" : "未连接服务器"}
                </span>
              </footer>
            </section>
            <aside className="details-panel">
              <div className="details-heading">
                <span>频道详情</span>
                <Info size={15} />
              </div>
              <div className="detail-channel-icon">
                <Volume2 size={23} />
              </div>
              <h2>{channel?.name || "暂无频道"}</h2>
              <p className="detail-description">
                {channel?.description || "未连接"}
              </p>
              <dl>
                <div>
                  <dt>状态</dt>
                  <dd>
                    <span
                      className={`status-dot ${isPreview ? "preview" : ""}`}
                    />
                    {isPreview ? "本地预览" : "离线"}
                  </dd>
                </div>
                <div>
                  <dt>语音</dt>
                  <dd>未接入</dd>
                </div>
              </dl>
              <div className="members-heading">
                <Users size={15} />
                <span>成员</span>
                <span>{channel?.members ?? 0}</span>
              </div>
              {isPreview ? (
                <div className="member">
                  <div className="avatar">我</div>
                  <div>
                    <strong>{workspace?.session.nickname}</strong>
                    <small>本地预览</small>
                  </div>
                  <MicOff size={15} />
                </div>
              ) : (
                <p className="no-members">暂无成员</p>
              )}
            </aside>
          </div>
        )}
      </main>

      {editing && (
        <Modal
          title={editing.id ? "编辑服务器" : "添加服务器"}
          close={() => !pending && setEditing(null)}
        >
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void run(
                () => api.SaveServer(editing),
                () => setEditing(null),
              );
            }}
          >
            <label>
              服务器名称
              <input
                autoFocus
                required
                maxLength={100}
                value={editing.name}
                onChange={(e) =>
                  setEditing({ ...editing, name: e.target.value })
                }
                placeholder="我的服务器"
              />
            </label>
            <label>
              服务器地址
              <input
                required
                maxLength={253}
                value={editing.address}
                onChange={(e) =>
                  setEditing({ ...editing, address: e.target.value })
                }
                placeholder="voice.example.com:9987"
                spellCheck={false}
              />
            </label>
            <label>
              昵称
              <input
                required
                maxLength={30}
                value={editing.nickname}
                onChange={(e) =>
                  setEditing({ ...editing, nickname: e.target.value })
                }
              />
            </label>
            {error && (
              <p className="form-error" role="alert">
                {error}
              </p>
            )}
            <div className="modal-actions">
              <button
                type="button"
                className="secondary-button"
                disabled={pending}
                onClick={() => setEditing(null)}
              >
                取消
              </button>
              <button className="primary-button" disabled={pending}>
                {pending ? "保存中…" : "保存书签"}
              </button>
            </div>
          </form>
        </Modal>
      )}
      {deleting && (
        <Modal
          title="删除服务器书签"
          close={() => !pending && setDeleting(null)}
        >
          <p className="delete-copy">删除「{deleting.name}」？</p>
          {error && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
          <div className="modal-actions">
            <button
              className="secondary-button"
              disabled={pending}
              onClick={() => setDeleting(null)}
            >
              取消
            </button>
            <button
              className="danger-button"
              disabled={pending}
              onClick={() =>
                void run(
                  () => api.DeleteServer(deleting.id),
                  () => {
                    setDeleting(null);
                    setSelectedServer("");
                  },
                )
              }
            >
              {pending ? "删除中…" : "删除书签"}
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}

function Modal({
  title,
  close,
  children,
}: {
  title: string;
  close: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const el = ref.current;
    (el?.querySelector("input") ?? el?.querySelector("button"))?.focus();
    function trap(event: KeyboardEvent) {
      if (event.key !== "Tab") return;
      const items = el?.querySelectorAll<HTMLElement>(
        "button:not(:disabled),input:not(:disabled)",
      );
      if (!items?.length) return;
      const first = items[0],
        last = items[items.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    }
    el?.addEventListener("keydown", trap);
    return () => {
      el?.removeEventListener("keydown", trap);
      previous?.focus();
    };
  }, []);
  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) close();
      }}
    >
      <div
        className="modal"
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
      >
        <header>
          <h2>{title}</h2>
          <IconButton label="关闭弹窗" onClick={close}>
            <X size={19} />
          </IconButton>
        </header>
        {children}
      </div>
    </div>
  );
}
