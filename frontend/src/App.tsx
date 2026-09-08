import {
  useEffect,
  useLayoutEffect,
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
  LogIn,
  LogOut,
  LockKeyhole,
  LoaderCircle,
  Menu,
  MessageSquare,
  MicOff,
  Moon,
  Music2,
  Pencil,
  Plus,
  Radio,
  RefreshCw,
  RotateCcw,
  Send,
  Server,
  Settings,
  Signal,
  Sun,
  Trash2,
  Users,
  UserRound,
  Volume2,
  X,
} from "lucide-react";
import {
  api,
  browserPreview,
  errorMessage,
  type Channel,
  type ServerProfile,
  type User,
  type Message,
  type Workspace,
} from "./api";
import {
  NotificationAudio,
  readSoundPreferences,
  saveSoundPreferences,
  type SoundPreferences,
} from "./notificationAudio";
import { ChannelIcon } from "./ChannelIcon";

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

const remoteModes = new Set([
  "connecting",
  "connected",
  "failed",
  "disconnecting",
]);
const activeModes = new Set(["connecting", "connected", "disconnecting"]);
const sessionLabels: Record<Workspace["session"]["mode"], string> = {
  offline: "离线",
  preview: "本地预览",
  connecting: "正在连接",
  connected: "在线",
  failed: "连接失败",
  disconnecting: "正在断开",
};

function orderedChannels(
  channels: Channel[],
): { channel: Channel; depth: number }[] {
  const ids = new Set(channels.map((channel) => channel.id));
  const groups = new Map<string, Channel[]>();
  for (const channel of channels) {
    const parent =
      channel.parentID && ids.has(channel.parentID) ? channel.parentID : "";
    groups.set(parent, [...(groups.get(parent) ?? []), channel]);
  }
  const orderSiblings = (siblings: Channel[]): Channel[] => {
    const byPredecessor = new Map<string, Channel[]>();
    for (const channel of siblings) {
      const predecessor = channel.order || "0";
      byPredecessor.set(predecessor, [
        ...(byPredecessor.get(predecessor) ?? []),
        channel,
      ]);
    }
    const result: Channel[] = [];
    const seen = new Set<string>();
    const appendAfter = (predecessor: string) => {
      for (const channel of byPredecessor.get(predecessor) ?? []) {
        if (seen.has(channel.id)) continue;
        seen.add(channel.id);
        result.push(channel);
        appendAfter(channel.id);
      }
    };
    appendAfter("0");
    for (const channel of siblings) {
      if (seen.has(channel.id)) continue;
      seen.add(channel.id);
      result.push(channel);
      appendAfter(channel.id);
    }
    return result;
  };
  const result: { channel: Channel; depth: number }[] = [];
  const seen = new Set<string>();
  const visit = (parentID: string, depth: number) => {
    for (const channel of orderSiblings(groups.get(parentID) ?? [])) {
      if (seen.has(channel.id)) continue;
      seen.add(channel.id);
      result.push({ channel, depth });
      visit(channel.id, depth + 1);
    }
  };
  visit("", 0);
  for (const channel of channels) {
    if (!seen.has(channel.id)) result.push({ channel, depth: 0 });
  }
  return result;
}

export default function App() {
  const [workspace, setWorkspace] = useState<Workspace>();
  const [pending, setPending] = useState(false);
  const [requestedChannelID, setRequestedChannelID] = useState("");
  const [error, setError] = useState("");
  const [page, setPage] = useState<"chat" | "settings">("chat");
  const [editing, setEditing] = useState<ServerProfile | null>(null);
  const [deleting, setDeleting] = useState<ServerProfile | null>(null);
  const [connectingTo, setConnectingTo] = useState<ServerProfile | null>(null);
  const [password, setPassword] = useState("");
  const [rememberPassword, setRememberPassword] = useState(true);
  const [connectError, setConnectError] = useState("");
  const [selectedServer, setSelectedServer] = useState("");
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [messageRequest, setMessageRequest] = useState(false);
  const [retrying, setRetrying] = useState<Message | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(
    () => window.innerWidth > 1120,
  );
  const detailsToggle = useRef<HTMLButtonElement>(null);
  const [theme, setTheme] = useState(() =>
    readPreference("resona.theme.v2", "dark"),
  );
  const [compact, setCompact] = useState(
    () => readPreference("resona.compact", "false") === "true",
  );
  const [soundPreferences, setSoundPreferences] =
    useState(readSoundPreferences);
  const [soundError, setSoundError] = useState("");
  const notificationAudio = useRef<NotificationAudio>();
  const messageHistory = useRef<HTMLDivElement>(null);
  const nearBottom = useRef(true);
  const forceMessageScroll = useRef(false);
  const messageRequestRef = useRef(0);
  const sentDrafts = useRef(
    new Map<string, { key: string; text: string; sessionID: string }>(),
  );
  const sidebarContent = useRef<HTMLDivElement>(null);
  const channelRows = useRef(new Map<string, HTMLButtonElement>());
  const pendingRef = useRef(false);
  const channelRequestRef = useRef(0);
  const workspaceRef = useRef<Workspace>();
  const actionRevision = useRef(0);
  const pollError = useRef("");
  const mode = workspace?.session.mode ?? "offline";
  const isPreview = mode === "preview";
  const isConnected = mode === "connected";
  const isRemote = remoteModes.has(mode);
  const isHistory =
    (mode === "offline" || mode === "failed") &&
    !!workspace?.session.serverID &&
    !!workspace.channels.length;
  const switchingChannelID = isConnected
    ? requestedChannelID || workspace?.session.switchingChannelID || ""
    : "";
  const switchingChannel = workspace?.channels.find(
    (c) => c.id === switchingChannelID,
  );
  const channel = workspace?.channels.find(
    (c) => c.id === workspace.session.channelID,
  );
  const messages =
    workspace?.messages.filter((m) => m.channelID === channel?.id) ?? [];
  const draftKey = `${workspace?.session.serverID || "preview"}\0${channel?.id || ""}`;
  const draft = drafts[draftKey] ?? "";
  const setDraft = (value: string) =>
    setDrafts((current) => ({ ...current, [draftKey]: value }));
  const draftLength = isPreview
    ? [...draft.trim()].length
    : new TextEncoder().encode(draft).length;
  const draftLimit = isPreview ? 2000 : 8192;
  const sendingMessage =
    messageRequest || !!workspace?.session.sendingMessageID;
  const writable =
    (isPreview || (isConnected && !!workspace?.session.id)) &&
    channel?.kind !== "separator" &&
    !!channel;
  const canCompose =
    writable && !pending && !switchingChannelID && !sendingMessage;
  const server = workspace?.servers.find((s) => s.id === selectedServer);
  const serverIsCurrent =
    !!server && workspace?.session.serverID === server.id && isRemote;
  const usersByChannel = new Map<string, User[]>();
  for (const user of workspace?.users ?? []) {
    const members = usersByChannel.get(user.channelID) ?? [];
    members.push(user);
    usersByChannel.set(user.channelID, members);
  }
  const channelUsers = usersByChannel.get(channel?.id ?? "") ?? [];
  const channels = orderedChannels(workspace?.channels ?? []);
  const sessionLabel = sessionLabels[mode];
  const statusClass =
    mode === "connected"
      ? "online"
      : mode === "preview"
        ? "preview"
        : mode === "failed"
          ? "failed"
          : mode === "connecting" || mode === "disconnecting"
            ? "working"
            : "";

  useEffect(() => {
    const audio = new NotificationAudio(readSoundPreferences(), setSoundError);
    notificationAudio.current = audio;
    const close = () => audio.close();
    window.addEventListener("pagehide", close);
    return () => {
      window.removeEventListener("pagehide", close);
      audio.close();
      notificationAudio.current = undefined;
    };
  }, []);
  useEffect(() => {
    if (workspace) notificationAudio.current?.consume(workspace);
  }, [workspace]);

  function updateSoundPreferences(value: SoundPreferences) {
    notificationAudio.current?.setPreferences(value);
    setSoundPreferences(value);
    saveSoundPreferences(value);
  }

  useEffect(() => {
    let cancelled = false;
    let timer = 0;
    const refresh = async () => {
      if (
        !pendingRef.current &&
        !channelRequestRef.current &&
        !messageRequestRef.current
      ) {
        const revision = actionRevision.current;
        try {
          const next = await api.GetWorkspace();
          if (
            !cancelled &&
            revision === actionRevision.current &&
            !pendingRef.current &&
            !channelRequestRef.current &&
            !messageRequestRef.current
          ) {
            workspaceRef.current = next;
            setWorkspace(next);
            pollError.current = "";
            if (next.session.serverID)
              setSelectedServer((current) => current || next.session.serverID);
          }
        } catch (e) {
          const message = errorMessage(e);
          if (
            !cancelled &&
            !workspaceRef.current &&
            pollError.current !== message
          ) {
            pollError.current = message;
            setError(message);
          }
        }
      }
      if (!cancelled) timer = window.setTimeout(refresh, 1000);
    };
    void refresh();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, []);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.dataset.compact = String(compact);
    try {
      localStorage.setItem("resona.theme.v2", theme);
      localStorage.setItem("resona.compact", String(compact));
    } catch {
      /* Preferences remain active for this session. */
    }
  }, [theme, compact]);
  useEffect(() => {
    if (!workspace) return;
    for (const [id, submitted] of sentDrafts.current) {
      if (submitted.sessionID !== workspace.session.id) {
        sentDrafts.current.delete(id);
        continue;
      }
      const message = workspace.messages.find((entry) => entry.id === id);
      if (!message || message.status === "sending") continue;
      if (message.status === "sent")
        setDrafts((current) =>
          current[submitted.key] === submitted.text
            ? { ...current, [submitted.key]: "" }
            : current,
        );
      sentDrafts.current.delete(id);
    }
  }, [workspace]);
  const historyKey = `${workspace?.session.id || workspace?.session.mode}\0${channel?.id || ""}`;
  useLayoutEffect(() => {
    nearBottom.current = true;
    forceMessageScroll.current = true;
  }, [historyKey]);
  useLayoutEffect(() => {
    if (nearBottom.current || forceMessageScroll.current) {
      const pane = messageHistory.current;
      if (pane) pane.scrollTop = pane.scrollHeight;
      forceMessageScroll.current = false;
    }
  }, [
    historyKey,
    messages.length,
    messages.at(-1)?.id,
    messages.at(-1)?.status,
  ]);
  useEffect(() => {
    if (mode !== "connected" && mode !== "preview" && !isHistory) return;
    const scroller = sidebarContent.current;
    const row = channelRows.current.get(channel?.id ?? "");
    if (!scroller?.clientHeight || !row) return;
    const viewport = scroller.getBoundingClientRect();
    const target = row.getBoundingClientRect();
    // Scroll only this pane; document-level scrolling can move the fixed shell.
    if (target.top < viewport.top)
      scroller.scrollTop += target.top - viewport.top;
    else if (target.bottom > viewport.bottom)
      scroller.scrollTop += target.bottom - viewport.bottom;
  }, [mode, workspace?.session.serverID, channel?.id, sidebarOpen, isHistory]);
  useEffect(() => {
    const narrow = window.matchMedia("(max-width: 1120px)");
    const resize = () => {
      if (narrow.matches) setDetailsOpen(false);
    };
    narrow.addEventListener("change", resize);
    return () => narrow.removeEventListener("change", resize);
  }, []);
  function closeDetails() {
    setDetailsOpen(false);
    detailsToggle.current?.focus();
  }
  useEffect(() => {
    const close = (event: KeyboardEvent) => {
      if (
        event.key === "Escape" &&
        detailsOpen &&
        !editing &&
        !deleting &&
        !connectingTo &&
        !retrying
      )
        closeDetails();
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [detailsOpen, editing, deleting, connectingTo, retrying]);
  useEffect(() => {
    const close = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !pending) {
        setEditing(null);
        setDeleting(null);
        setConnectingTo(null);
        setPassword("");
        setConnectError("");
        setRetrying(null);
        setSidebarOpen(false);
      }
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [pending]);

  async function run(action: () => Promise<Workspace>, after?: () => void) {
    if (pendingRef.current) return;
    actionRevision.current += 1;
    pendingRef.current = true;
    setPending(true);
    setError("");
    try {
      const next = await action();
      workspaceRef.current = next;
      setWorkspace(next);
      after?.();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  }
  function send(event?: FormEvent) {
    event?.preventDefault();
    if (
      !draft.trim() ||
      !canCompose ||
      draftLength > draftLimit ||
      messageRequestRef.current
    )
      return;
    const unconfirmed = isConnected
      ? messages.find(
          (message) =>
            message.status === "unconfirmed" &&
            message.text === draft &&
            message.authorID === workspace?.session.selfID,
        )
      : undefined;
    if (unconfirmed) {
      retryMessage(unconfirmed);
      return;
    }
    forceMessageScroll.current = true;
    if (isPreview)
      void run(
        () => api.SendMessage(draft),
        () => setDraft(""),
      );
    else if (workspace && channel)
      void sendRemote(
        () => api.SendChannelMessage(workspace.session.id, channel.id, draft),
        draft,
      );
  }
  async function sendRemote(
    action: () => Promise<Workspace>,
    submittedText: string,
    retryID?: string,
  ) {
    if (
      messageRequestRef.current ||
      pendingRef.current ||
      sendingMessage ||
      !workspace
    )
      return;
    const originalSession = workspace.session.id;
    const originalKey = draftKey;
    const beforeIDs = new Set(workspace.messages.map((message) => message.id));
    const revision = ++actionRevision.current;
    messageRequestRef.current = revision;
    setMessageRequest(true);
    setError("");
    forceMessageScroll.current = true;
    try {
      const next = await action();
      if (revision !== actionRevision.current) return;
      const submitted =
        next.messages.find(
          (message) => message.id === next.session.sendingMessageID,
        ) ??
        next.messages.find(
          (message) =>
            !beforeIDs.has(message.id) &&
            message.authorID === workspace.session.selfID &&
            message.channelID === channel?.id &&
            message.text === submittedText,
        ) ??
        next.messages.find((message) => message.id === retryID);
      if (submitted)
        sentDrafts.current.set(submitted.id, {
          key: originalKey,
          text: submittedText,
          sessionID: originalSession,
        });
      workspaceRef.current = next;
      setWorkspace(next);
      setRetrying(null);
    } catch (error) {
      if (revision === actionRevision.current) setError(errorMessage(error));
    } finally {
      if (messageRequestRef.current === revision) {
        messageRequestRef.current = 0;
        setMessageRequest(false);
      }
    }
  }
  function retryMessage(message: Message, allowDuplicate = false) {
    if (!canCompose || !isConnected || message.channelID !== channel?.id)
      return;
    if (message.status === "unconfirmed" && !allowDuplicate) {
      setRetrying(message);
      return;
    }
    void sendRemote(
      () => api.RetryMessage(message.id, allowDuplicate),
      message.text,
      message.id,
    );
  }
  function changePreview() {
    void run(
      () => (isPreview ? api.LeavePreview() : api.OpenPreview()),
      () => {
        setPage("chat");
      },
    );
  }
  async function openConnect(target: ServerProfile, forcePassword = false) {
    if (browserPreview || pendingRef.current) return;
    if (
      !forcePassword &&
      workspaceRef.current?.session.serverID === target.id &&
      activeModes.has(workspaceRef.current.session.mode)
    ) {
      setPage("chat");
      return;
    }
    void notificationAudio.current?.unlock();
    setPassword("");
    setConnectError("");
    pendingRef.current = true;
    setPending(true);
    let status = { saved: false, remember: true };
    try {
      status = await api.GetServerCredentialStatus(target.id);
    } catch (error) {
      setConnectError(errorMessage(error));
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
    setRememberPassword(status.remember);
    if (status.saved && !forcePassword) {
      await connectTarget(target, () => api.ConnectSavedServer(target.id));
      return;
    }
    setConnectingTo(target);
  }
  function closeConnect() {
    if (pendingRef.current) return;
    setPassword("");
    setConnectError("");
    setConnectingTo(null);
  }
  async function connect(event: FormEvent) {
    event.preventDefault();
    if (!connectingTo || pendingRef.current) return;
    void notificationAudio.current?.unlock();
    const target = connectingTo;
    const submittedPassword = password;
    await connectTarget(target, () =>
      api.ConnectServerWithPassword(
        target.id,
        submittedPassword,
        rememberPassword,
      ),
    );
  }
  async function connectTarget(
    target: ServerProfile,
    action: () => Promise<Workspace>,
  ) {
    if (pendingRef.current) return;
    setConnectError("");
    setError("");
    actionRevision.current += 1;
    messageRequestRef.current = 0;
    setMessageRequest(false);
    channelRequestRef.current = 0;
    setRequestedChannelID("");
    pendingRef.current = true;
    setPending(true);
    try {
      const next = await action();
      workspaceRef.current = next;
      setWorkspace(next);
      setPassword("");
      setConnectingTo(null);
      setSelectedServer(target.id);
      setPage("chat");
      setSidebarOpen(false);
    } catch (e) {
      setConnectingTo(target);
      setConnectError(errorMessage(e));
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  }
  function disconnect() {
    messageRequestRef.current = 0;
    setMessageRequest(false);
    channelRequestRef.current = 0;
    setRequestedChannelID("");
    void run(() => api.DisconnectServer());
  }
  async function selectChannel(target: Channel) {
    if (
      pendingRef.current ||
      channelRequestRef.current ||
      sendingMessage ||
      switchingChannelID ||
      (!isPreview && !isConnected && !isHistory) ||
      target.kind === "separator" ||
      (isConnected && target.passwordRequired)
    )
      return;
    if (isPreview || isHistory) {
      void run(
        () => api.SelectChannel(target.id),
        () => {
          setPage("chat");
          setSidebarOpen(false);
        },
      );
      return;
    }
    if (target.id === workspaceRef.current?.session.channelID) return;
    const revision = ++actionRevision.current;
    channelRequestRef.current = revision;
    setRequestedChannelID(target.id);
    setError("");
    setPage("chat");
    try {
      const next = await api.SelectChannel(target.id);
      if (revision !== actionRevision.current) return;
      workspaceRef.current = next;
      setWorkspace(next);
    } catch (e) {
      if (revision === actionRevision.current) setError(errorMessage(e));
    } finally {
      if (channelRequestRef.current === revision) {
        channelRequestRef.current = 0;
        setRequestedChannelID("");
      }
    }
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
        <div className="rail-servers" aria-label="服务器快捷入口">
          {workspace?.servers.map((server) => (
            <button
              key={server.id}
              className={`rail-server ${selectedServer === server.id ? "selected" : ""}`}
              title={server.name}
              aria-label={`选择服务器 ${server.name}`}
              aria-pressed={selectedServer === server.id}
              onClick={() => {
                setSelectedServer(server.id);
                setPage("chat");
                setSidebarOpen(true);
              }}
              onDoubleClick={() => void openConnect(server)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  void openConnect(server);
                }
              }}
            >
              <span>{Array.from(server.name)[0]}</span>
              {workspace?.session.serverID === server.id && (
                <i className={`status-dot ${statusClass}`} />
              )}
            </button>
          ))}
        </div>
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
        <div className="sidebar-content" ref={sidebarContent}>
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
                  onDoubleClick={() => void openConnect(s)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") {
                      event.preventDefault();
                      setSelectedServer(s.id);
                      void openConnect(s);
                    }
                  }}
                >
                  <span className="server-icon">
                    <Server size={17} />
                  </span>
                  <span className="row-copy">
                    <strong>{s.name}</strong>
                    <small>
                      {workspace?.session.serverID === s.id && isRemote
                        ? sessionLabel
                        : "未连接"}
                    </small>
                  </span>
                  <span
                    className={`status-dot ${workspace?.session.serverID === s.id ? statusClass : ""}`}
                  />
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
            <>
              <div className="bookmark-actions">
                <span title={server.address}>{server.address}</span>
                <IconButton
                  label={`编辑 ${server.name}`}
                  disabled={
                    pending || (serverIsCurrent && activeModes.has(mode))
                  }
                  onClick={() => setEditing({ ...server })}
                >
                  <Pencil size={14} />
                </IconButton>
                <IconButton
                  label={`删除 ${server.name}`}
                  disabled={
                    pending || (serverIsCurrent && activeModes.has(mode))
                  }
                  onClick={() => setDeleting(server)}
                >
                  <Trash2 size={14} />
                </IconButton>
              </div>
              <div className="server-session-actions">
                {serverIsCurrent && mode === "connected" ? (
                  <button
                    className="secondary-button compact-button"
                    aria-label={`断开 ${server.name}`}
                    disabled={pending}
                    onClick={disconnect}
                  >
                    <LogOut size={14} />
                    断开
                  </button>
                ) : serverIsCurrent && mode === "connecting" ? (
                  <button
                    className="secondary-button compact-button"
                    aria-label={`取消连接 ${server.name}`}
                    disabled={pending}
                    onClick={disconnect}
                  >
                    <X size={14} />
                    取消连接
                  </button>
                ) : serverIsCurrent && mode === "disconnecting" ? (
                  <button className="secondary-button compact-button" disabled>
                    <RefreshCw size={14} />
                    断开中…
                  </button>
                ) : (
                  <button
                    className="primary-button compact-button"
                    aria-label={`${mode === "failed" && serverIsCurrent ? "重新连接" : "连接"} ${server.name}`}
                    title={browserPreview ? "在桌面应用中连接" : undefined}
                    disabled={browserPreview || pending}
                    onClick={() => void openConnect(server)}
                  >
                    <LogIn size={14} />
                    {mode === "failed" && serverIsCurrent ? "重新连接" : "连接"}
                  </button>
                )}
                {serverIsCurrent && mode === "failed" && (
                  <button
                    className="secondary-button compact-button"
                    disabled={pending}
                    onClick={() => void openConnect(server, true)}
                  >
                    <Pencil size={14} />
                    修改密码
                  </button>
                )}
              </div>
            </>
          )}

          <div className="section-label channel-section">
            <span>频道</span>
            <ChevronDown size={14} />
          </div>
          {(isPreview || isConnected || isHistory) &&
          workspace?.channels.length ? (
            <div className="channel-list">
              {channels.map(({ channel: c, depth }) =>
                c.kind === "separator" ? (
                  <div
                    className={`channel-separator align-${c.align} ${c.repeat ? "is-repeated" : ""}`}
                    key={c.id}
                    data-channel-id={c.id}
                    role="separator"
                    aria-label={c.name || undefined}
                  >
                    <span aria-hidden={c.repeat || undefined}>
                      {c.repeat && c.name
                        ? c.name.repeat(Math.ceil(200 / c.name.length))
                        : c.name}
                    </span>
                  </div>
                ) : (
                  <div
                    className="channel-group"
                    key={c.id}
                    data-channel-id={c.id}
                  >
                    <button
                      ref={(node) => {
                        if (node) channelRows.current.set(c.id, node);
                        else channelRows.current.delete(c.id);
                      }}
                      className={`channel-row ${channel?.id === c.id ? "selected" : ""}`}
                      aria-current={channel?.id === c.id ? "true" : undefined}
                      disabled={
                        pending ||
                        sendingMessage ||
                        !!switchingChannelID ||
                        (isConnected && c.passwordRequired)
                      }
                      title={
                        isConnected && c.passwordRequired
                          ? "此频道需要密码，暂不支持加入"
                          : c.name
                      }
                      onClick={() => void selectChannel(c)}
                    >
                      <span
                        className="channel-main"
                        style={{ paddingLeft: `${Math.min(depth, 8) * 14}px` }}
                      >
                        {c.id === "music" && isPreview ? (
                          <Music2 size={17} />
                        ) : (
                          <ChannelIcon channel={c} sessionID={workspace.session.id} />
                        )}
                        <span>{c.name}</span>
                      </span>
                      {isPreview && <small>{c.members}</small>}
                      {isConnected &&
                        workspace.session.memberSyncState !== "pending" &&
                        !!usersByChannel.get(c.id)?.length && (
                          <small aria-hidden="true" title="可见成员">
                            {usersByChannel.get(c.id)!.length}
                          </small>
                        )}
                      {isConnected && (
                        <span className="channel-indicator" aria-hidden="true">
                          {switchingChannelID === c.id ? (
                            <LoaderCircle
                              size={15}
                              className="channel-spinner"
                            />
                          ) : c.passwordRequired ? (
                            <LockKeyhole size={14} />
                          ) : null}
                        </span>
                      )}
                    </button>
                    {isConnected && !!usersByChannel.get(c.id)?.length && (
                      <ul
                        className="channel-members"
                        aria-label={`${c.name}的可见成员`}
                      >
                        {usersByChannel.get(c.id)!.map((user) => (
                          <li
                            className={`channel-member ${user.self ? "is-self" : ""}`}
                            key={user.id}
                            style={{
                              paddingLeft: `${36 + Math.min(depth, 8) * 14}px`,
                            }}
                            title={user.nickname}
                          >
                            <UserRound size={14} aria-hidden="true" />
                            <span>{user.nickname}</span>
                            {user.self && <small>我</small>}
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                ),
              )}
            </div>
          ) : (
            <div className="channels-empty">
              {mode === "connecting" || mode === "connected"
                ? "正在同步频道…"
                : isRemote
                  ? "暂无可见频道"
                  : "未连接"}
            </div>
          )}
        </div>
        <div className="preview-control">
          <span className={`status-dot ${statusClass}`} />
          <span>{sessionLabel}</span>
          {mode === "connecting" || mode === "connected" ? (
            <button
              className="text-button"
              disabled={pending}
              onClick={disconnect}
            >
              {mode === "connecting" ? "取消连接" : "断开"}
              <LogOut size={14} />
            </button>
          ) : mode === "disconnecting" ? (
            <button className="text-button" disabled>
              正在断开
              <RefreshCw size={14} />
            </button>
          ) : !isRemote ? (
            <button
              className="text-button"
              disabled={pending || !workspace}
              onClick={changePreview}
            >
              {isPreview ? "结束预览" : "本地预览"}
              <ArrowUpRight size={14} />
            </button>
          ) : null}
        </div>
        <div className="identity">
          <div className="avatar">我</div>
          <div className="identity-copy">
            <strong>{workspace?.session.nickname || "我"}</strong>
            <small>
              {isRemote && workspace?.session.serverName
                ? workspace.session.serverName
                : sessionLabel}
            </small>
          </div>
          <IconButton label="语音通话设备尚未接入" disabled>
            <MicOff size={18} />
          </IconButton>
          <IconButton label="语音通话播放尚未接入" disabled>
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
            ) : channel ? (
              <ChannelIcon channel={channel} sessionID={workspace?.session.id ?? ""} size={21} />
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
            <span className={`connection-label ${statusClass}`}>
              <span className={`status-dot ${statusClass}`} />
              {isConnected && workspace?.session.serverName
                ? `${workspace.session.serverName} · 在线`
                : sessionLabel}
            </span>
          </div>
          {page === "chat" && (
            <button
              className={`icon-button details-toggle ${detailsOpen ? "active" : ""}`}
              ref={detailsToggle}
              aria-label={detailsOpen ? "收起频道详情" : "展开频道详情"}
              title={detailsOpen ? "收起频道详情" : "展开频道详情"}
              aria-expanded={detailsOpen}
              aria-controls="channel-details"
              onClick={() => setDetailsOpen(!detailsOpen)}
            >
              <Users size={19} />
            </button>
          )}
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
        {(isConnected || isHistory) && workspace?.session.error && (
          <div className="error-banner" role="alert">
            <Info size={17} />
            <span>{workspace.session.error}</span>
          </div>
        )}
        {isConnected && workspace?.session.credentialError && (
          <div className="error-banner" role="alert">
            <Info size={17} />
            <span>{workspace.session.credentialError}</span>
          </div>
        )}
        {isConnected && workspace?.session.memberSyncError && (
          <div className="sync-banner" role="status">
            <Info size={15} />
            <span>{workspace.session.memberSyncError}</span>
          </div>
        )}
        {switchingChannelID && (
          <div className="channel-switch-status" role="status">
            <LoaderCircle size={15} className="channel-spinner" />
            <span>
              正在加入 {switchingChannel?.name || "目标频道"}，等待服务器确认
            </span>
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
            <h2 className="settings-section-heading">提示音</h2>
            <div className="setting-row">
              <div>
                <strong>启用提示音</strong>
              </div>
              <label className="switch">
                <input
                  type="checkbox"
                  aria-label="启用提示音"
                  checked={soundPreferences.enabled}
                  onChange={(event) => {
                    void notificationAudio.current?.unlock();
                    updateSoundPreferences({
                      ...soundPreferences,
                      enabled: event.target.checked,
                    });
                  }}
                />
                <span />
              </label>
            </div>
            <div className="setting-row">
              <div>
                <strong>音量</strong>
              </div>
              <div className="sound-volume">
                <input
                  type="range"
                  min="0"
                  max="100"
                  step="1"
                  aria-label="提示音音量"
                  value={soundPreferences.volume}
                  disabled={!soundPreferences.enabled}
                  onChange={(event) =>
                    updateSoundPreferences({
                      ...soundPreferences,
                      volume: Number(event.target.value),
                    })
                  }
                />
                <output>{soundPreferences.volume}%</output>
              </div>
            </div>
            {(
              [
                ["connected", "连接服务器"],
                ["disconnected", "断开连接"],
                ["member_joined", "成员进入当前频道"],
                ["member_left", "成员离开当前频道"],
              ] as const
            ).map(([kind, label]) => (
              <div className="setting-row sound-event-row" key={kind}>
                <div>
                  <strong>{label}</strong>
                </div>
                <IconButton
                  label={`试听${label}`}
                  disabled={!soundPreferences.enabled}
                  onClick={() => void notificationAudio.current?.preview(kind)}
                >
                  <Volume2 size={18} />
                </IconButton>
              </div>
            ))}
            {soundError && (
              <p className="sound-error" role="status">
                {soundError}
              </p>
            )}
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
                ref={messageHistory}
                className={`message-history ${!messages.length ? "is-empty" : ""}`}
                aria-live="polite"
                onScroll={(event) => {
                  const pane = event.currentTarget;
                  nearBottom.current =
                    pane.scrollHeight - pane.clientHeight - pane.scrollTop < 80;
                }}
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
                ) : !isPreview && !isRemote && !isHistory ? (
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
                ) : mode === "connecting" || mode === "disconnecting" ? (
                  <div className="empty-state">
                    <div className="empty-symbol working">
                      <RefreshCw size={30} />
                    </div>
                    <h1>{sessionLabel}</h1>
                    <p>{workspace.session.serverName || server?.name}</p>
                    {mode === "connecting" && (
                      <button
                        className="secondary-button"
                        disabled={pending}
                        onClick={disconnect}
                      >
                        <X size={15} />
                        取消连接
                      </button>
                    )}
                  </div>
                ) : mode === "failed" && !isHistory ? (
                  <div className="empty-state failure-state">
                    <div className="empty-symbol failed">
                      <Info size={30} />
                    </div>
                    <h1>连接失败</h1>
                    <p role="alert">
                      {workspace.session.error || "无法连接服务器，请重试。"}
                    </p>
                    {server && (
                      <button
                        className="primary-button"
                        disabled={pending || browserPreview}
                        onClick={() => openConnect(server)}
                      >
                        <RefreshCw size={15} />
                        重新连接
                      </button>
                    )}
                  </div>
                ) : isConnected && !channel ? (
                  <div className="empty-state">
                    <div className="empty-symbol">
                      <Radio size={31} />
                    </div>
                    <h1>
                      {workspace.channels.length
                        ? "正在同步频道"
                        : "暂无可见频道"}
                    </h1>
                    <p>{workspace.session.serverName}</p>
                  </div>
                ) : isConnected && !messages.length ? (
                  <div className="empty-state channel-empty">
                    <div className="empty-symbol online">
                      <Volume2 size={29} />
                    </div>
                    <h1>{channel?.name}</h1>
                    <p>
                      {channelUsers.length
                        ? `${channelUsers.length} 位可见成员`
                        : workspace?.session.memberSyncState === "pending"
                          ? "正在同步可见成员…"
                          : workspace?.session.memberSyncState === "limited"
                            ? "成员信息受限"
                            : "当前频道暂无可见成员"}
                    </p>
                    <span className="online-badge">
                      <span className="status-dot online" />
                      {workspace.session.serverName || "远程服务器"}
                    </span>
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
                    <span
                      className={isPreview ? "preview-badge" : "browser-tag"}
                    >
                      <span
                        className={`status-dot ${isPreview ? "preview" : ""}`}
                      />
                      {isPreview ? "本地预览" : "已断开"}
                    </span>
                  </div>
                ) : (
                  <>
                    <div className="history-heading">
                      <span>
                        {isPreview
                          ? "本地预览"
                          : isHistory
                            ? "会话记录 · 已断开"
                            : channel?.name}
                      </span>
                      <span>
                        {new Date().toLocaleDateString("zh-CN", {
                          month: "long",
                          day: "numeric",
                        })}
                      </span>
                    </div>
                    {messages.map((m) => (
                      <article
                        className={`message message-${m.status}`}
                        key={m.id}
                        data-message-id={m.id}
                      >
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
                            <span
                              className="message-status"
                              title={m.error || undefined}
                            >
                              {isPreview
                                ? "本地"
                                : (
                                    {
                                      sending: "发送中",
                                      sent: "已发送",
                                      received: "",
                                      failed: "发送失败",
                                      unconfirmed: "未确认送达",
                                    } as const
                                  )[m.status]}
                            </span>
                          </div>
                          <p>{m.text}</p>
                          {!isPreview &&
                            (m.status === "failed" ||
                              m.status === "unconfirmed") && (
                              <div className="message-delivery">
                                <span>{m.error}</span>
                                <IconButton
                                  label="重试消息"
                                  disabled={
                                    !canCompose ||
                                    !isConnected ||
                                    m.channelID !== channel?.id
                                  }
                                  onClick={() => retryMessage(m)}
                                >
                                  <RotateCcw size={14} />
                                </IconButton>
                              </div>
                            )}
                        </div>
                      </article>
                    ))}
                  </>
                )}
              </div>
              <form
                className={`composer ${!canCompose ? "disabled" : ""}`}
                onSubmit={send}
              >
                <textarea
                  aria-label="消息"
                  placeholder={
                    writable
                      ? `发送到 ${channel?.name || "频道"}`
                      : isHistory
                        ? "已断开连接"
                        : isConnected
                          ? "等待会话信息"
                          : "未连接"
                  }
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  disabled={!canCompose}
                  rows={2}
                  onKeyDown={(e) => {
                    if (
                      e.key === "Enter" &&
                      !e.shiftKey &&
                      !e.nativeEvent.isComposing &&
                      e.nativeEvent.keyCode !== 229
                    ) {
                      e.preventDefault();
                      send();
                    }
                  }}
                />
                <div className="composer-footer">
                  <span>
                    {isPreview
                      ? "本地预览"
                      : sendingMessage
                        ? "正在发送…"
                        : isConnected
                          ? "频道消息"
                          : "离线"}
                    {draftLength > draftLimit * 0.9 && (
                      <span
                        className={draftLength > draftLimit ? "text-error" : ""}
                      >{` · ${draftLength}/${draftLimit}${isPreview ? " 字符" : " 字节"}`}</span>
                    )}
                  </span>
                  <button
                    className="send-button"
                    type="submit"
                    title="发送消息"
                    aria-label="发送消息"
                    disabled={
                      !canCompose || !draft.trim() || draftLength > draftLimit
                    }
                  >
                    <Send size={16} />
                  </button>
                </div>
              </form>
              <footer className="conversation-footer">
                <Signal size={12} />
                <span>
                  {isPreview
                    ? "本地预览 · 未连接远程服务器"
                    : isConnected
                      ? `${workspace?.session.serverName || "远程服务器"} · 在线`
                      : isRemote
                        ? sessionLabel
                        : "未连接服务器"}
                </span>
              </footer>
            </section>
            {detailsOpen && (
              <aside
                className="details-panel"
                id="channel-details"
                aria-label="频道详情"
              >
                <div className="details-heading">
                  <span>频道详情</span>
                  <IconButton label="关闭频道详情" onClick={closeDetails}>
                    <X size={15} />
                  </IconButton>
                </div>
                <div className="detail-channel-icon">
                  {channel ? (
                    <ChannelIcon channel={channel} sessionID={workspace?.session.id ?? ""} size={23} />
                  ) : (
                    <Volume2 size={23} />
                  )}
                </div>
                <h2>{channel?.name || "暂无频道"}</h2>
                <p className="detail-description">
                  {channel ? channel.description || "暂无频道主题" : "未连接"}
                </p>
                <dl>
                  <div>
                    <dt>状态</dt>
                    <dd>
                      <span className={`status-dot ${statusClass}`} />
                      {sessionLabel}
                    </dd>
                  </div>
                  {isRemote && (
                    <div>
                      <dt>服务器</dt>
                      <dd className="detail-value">
                        {workspace?.session.serverName || "-"}
                      </dd>
                    </div>
                  )}
                  {isRemote && (
                    <div>
                      <dt>身份 UID</dt>
                      <dd className="detail-value">
                        {workspace?.session.identityUID || "-"}
                      </dd>
                    </div>
                  )}
                  <div>
                    <dt>语音</dt>
                    <dd>未接入</dd>
                  </div>
                </dl>
                <div className="members-heading">
                  <Users size={15} />
                  <span>可见成员</span>
                  <span>
                    {isConnected
                      ? workspace?.session.memberSyncState === "pending"
                        ? "同步中"
                        : workspace?.session.memberSyncState === "limited"
                          ? "受限"
                          : channelUsers.length
                      : (channel?.members ?? 0)}
                  </span>
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
                ) : isConnected && channelUsers.length ? (
                  channelUsers.map((user) => (
                    <div className="member" key={user.id}>
                      <div className="avatar">{user.nickname.slice(0, 1)}</div>
                      <div>
                        <strong>{user.nickname}</strong>
                        <small>{user.self ? "当前用户" : "远程成员"}</small>
                      </div>
                    </div>
                  ))
                ) : (
                  <p className="no-members">
                    {isConnected
                      ? workspace?.session.memberSyncState === "pending"
                        ? "正在同步成员…"
                        : workspace?.session.memberSyncState === "limited"
                          ? "成员信息受限"
                          : "暂无可见成员"
                      : "暂无成员"}
                  </p>
                )}
              </aside>
            )}
          </div>
        )}
      </main>

      {retrying && (
        <Modal title="重新发送消息" close={() => setRetrying(null)}>
          <p className="delete-copy">
            这条消息可能已经送达，重新发送可能出现重复消息。
          </p>
          <div className="modal-actions">
            <button
              className="secondary-button"
              onClick={() => setRetrying(null)}
            >
              取消
            </button>
            <button
              className="primary-button"
              disabled={!canCompose || !isConnected}
              onClick={() => retryMessage(retrying, true)}
            >
              <RotateCcw size={15} />
              仍然发送
            </button>
          </div>
        </Modal>
      )}
      {connectingTo && (
        <Modal title={`连接 ${connectingTo.name}`} close={closeConnect}>
          <p className="connect-copy">{connectingTo.address}</p>
          <form onSubmit={connect}>
            <label>
              服务器密码（可选）
              <input
                autoFocus
                type="password"
                autoComplete="off"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </label>
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={rememberPassword}
                onChange={(event) => setRememberPassword(event.target.checked)}
              />
              记住密码
            </label>
            {connectError && (
              <p className="form-error" role="alert">
                {connectError}
              </p>
            )}
            <div className="modal-actions">
              <button
                type="button"
                className="secondary-button"
                disabled={pending}
                onClick={closeConnect}
              >
                取消
              </button>
              <button className="primary-button" disabled={pending}>
                <LogIn size={15} />
                {pending ? "连接中…" : "连接服务器"}
              </button>
            </div>
          </form>
        </Modal>
      )}
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
            {editing.id && !browserPreview && (
              <button
                type="button"
                className="text-button"
                disabled={pending}
                onClick={() =>
                  void run(() => api.ForgetServerPassword(editing.id))
                }
              >
                <Trash2 size={14} />
                忘记已保存密码
              </button>
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
