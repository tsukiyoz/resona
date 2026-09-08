export interface ServerProfile {
  id: string;
  name: string;
  address: string;
  nickname: string;
}
export interface Channel {
  id: string;
  name: string;
  description: string;
  members: number;
  parentID: string;
  order: string;
  passwordRequired: boolean;
  kind: "channel" | "separator";
  align: "left" | "center" | "right";
  repeat: boolean;
  iconID: string;
  iconRef: string;
}
export interface User {
  id: string;
  nickname: string;
  channelID: string;
  self: boolean;
}
export interface Message {
  id: string;
  channelID: string;
  author: string;
  text: string;
  createdAt: string;
  authorID: string;
  status: "sending" | "sent" | "received" | "failed" | "unconfirmed";
  error: string;
}
export interface Notification {
  id: string;
  kind: "connected" | "member_joined" | "member_left" | "disconnected";
  channelID: string;
  createdAt: string;
}
export interface Workspace {
  servers: ServerProfile[];
  session: {
    id: string;
    sendingMessageID: string;
    mode:
      | "offline"
      | "preview"
      | "connecting"
      | "connected"
      | "failed"
      | "disconnecting";
    channelID: string;
    switchingChannelID: string;
    nickname: string;
    serverID: string;
    serverName: string;
    identityUID: string;
    selfID: string;
    error: string;
    credentialError: string;
    memberSyncState: "pending" | "ready" | "limited";
    memberSyncError: string;
  };
  channels: Channel[];
  users: User[];
  messages: Message[];
  notifications: Notification[];
}
interface Bridge {
  GetIconResource(sessionID: string, ref: string): Promise<{ref: string; dataURL: string}>;
  GetWorkspace(): Promise<Workspace>;
  SaveServer(server: ServerProfile): Promise<Workspace>;
  DeleteServer(id: string): Promise<Workspace>;
  OpenPreview(): Promise<Workspace>;
  LeavePreview(): Promise<Workspace>;
  SelectChannel(id: string): Promise<Workspace>;
  SendMessage(text: string): Promise<Workspace>;
  SendChannelMessage(
    sessionID: string,
    channelID: string,
    text: string,
  ): Promise<Workspace>;
  RetryMessage(id: string, allowDuplicate: boolean): Promise<Workspace>;
  ConnectServer(id: string, password: string): Promise<Workspace>;
  GetServerCredentialStatus(
    id: string,
  ): Promise<{ saved: boolean; remember: boolean }>;
  ConnectSavedServer(id: string): Promise<Workspace>;
  ConnectServerWithPassword(
    id: string,
    password: string,
    remember: boolean,
  ): Promise<Workspace>;
  ForgetServerPassword(id: string): Promise<Workspace>;
  DisconnectServer(): Promise<Workspace>;
}
declare global {
  interface Window {
    go?: { main?: { App?: Bridge } };
  }
}

const storageKey = "resona.browser.workspace.v1";
export const browserPreview = !window.go?.main?.App;
const channels: Channel[] = [
  {
    id: "lobby",
    name: "大厅",
    description: "本地预览",
    members: 1,
    parentID: "",
    order: "0",
    passwordRequired: false,
    kind: "channel",
    align: "left",
    repeat: false,
    iconID: "",
    iconRef: "",
  },
  {
    id: "music",
    name: "音乐",
    description: "本地预览",
    members: 0,
    parentID: "",
    order: "lobby",
    passwordRequired: false,
    kind: "channel",
    align: "left",
    repeat: false,
    iconID: "",
    iconRef: "",
  },
  {
    id: "workshop",
    name: "工作间",
    description: "本地预览",
    members: 0,
    parentID: "",
    order: "music",
    passwordRequired: false,
    kind: "channel",
    align: "left",
    repeat: false,
    iconID: "",
    iconRef: "",
  },
];
const emptyWorkspace = (): Workspace => ({
  servers: [],
  session: {
    id: "",
    sendingMessageID: "",
    mode: "offline",
    channelID: "",
    switchingChannelID: "",
    nickname: "Resona",
    serverID: "",
    serverName: "",
    identityUID: "",
    selfID: "",
    error: "",
    credentialError: "",
    memberSyncState: "pending",
    memberSyncError: "",
  },
  channels: [],
  users: [],
  messages: [],
  notifications: [],
});
let local = emptyWorkspace();
let loaded = false;
function loadBookmarks(): void {
  if (loaded) return;
  const raw = localStorage.getItem(storageKey);
  if (raw !== null) {
    let saved: unknown;
    try {
      saved = JSON.parse(raw);
    } catch {
      throw new Error("服务器书签数据损坏，原始数据已保留。修复后重试。");
    }
    if (
      !saved ||
      typeof saved !== "object" ||
      !("servers" in saved) ||
      !Array.isArray(saved.servers) ||
      !saved.servers.every(
        (s: unknown): s is ServerProfile =>
          !!s &&
          typeof s === "object" &&
          ["id", "name", "address", "nickname"].every(
            (k) => typeof (s as Record<string, unknown>)[k] === "string",
          ),
      )
    )
      throw new Error("服务器书签格式无效，原始数据已保留。修复后重试。");
    local.servers = saved.servers;
  }
  loaded = true;
}
function saveBookmarks(servers: ServerProfile[]): Workspace {
  localStorage.setItem(storageKey, JSON.stringify({ servers }));
  local.servers = servers;
  return result();
}
function result(): Workspace {
  return structuredClone(local);
}
function validAddress(address: string): boolean {
  if (!address || address.length > 253 || /[\s\p{Cc}/\\@?#]/u.test(address))
    return false;
  let host = address;
  let port = "";
  if (address.includes(":")) {
    if (!address.startsWith("[") && address.split(":").length > 2) {
      try {
        return new URL(`http://[${address}]`).hostname.startsWith("[");
      } catch {
        return false;
      }
    }
    const match = address.match(/^(\[[^\]]+\]|[^:]+):([0-9]+)$/);
    if (!match) return false;
    [, host, port] = match;
    if (Number(port) < 1 || Number(port) > 65535) return false;
    if (host.startsWith("[")) {
      try {
        return new URL(`http://${host}`).hostname.startsWith("[");
      } catch {
        return false;
      }
    }
  }
  return host
    .replace(/\.$/, "")
    .split(".")
    .every(
      (label) =>
        label.length > 0 &&
        label.length <= 63 &&
        /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/i.test(label),
    );
}
const browserBridge: Bridge = {
  async GetIconResource() { throw new Error("本地预览没有远程图标资源"); },
  async GetWorkspace() {
    loadBookmarks();
    return result();
  },
  async SaveServer(server) {
    loadBookmarks();
    const clean = {
      ...server,
      name: server.name.trim(),
      address: server.address.trim(),
      nickname: server.nickname.trim(),
    };
    if (!clean.name || !clean.address || !clean.nickname)
      throw new Error("请填写服务器名称、地址和昵称。");
    if ([...clean.name].length > 100 || /\p{Cc}/u.test(clean.name))
      throw new Error("服务器名称需为 1–100 个字符，不能包含控制字符。");
    if ([...clean.nickname].length > 30 || /\p{Cc}/u.test(clean.nickname))
      throw new Error("昵称需为 1–30 个字符，不能包含控制字符。");
    if (!validAddress(clean.address))
      throw new Error("请输入有效的域名或 IP 地址，端口范围为 1–65535。");
    if (clean.id && !local.servers.some((s) => s.id === clean.id))
      throw new Error("服务器书签不存在。");
    const servers = clean.id
      ? local.servers.map((s) => (s.id === clean.id ? clean : s))
      : [...local.servers, { ...clean, id: crypto.randomUUID() }];
    return saveBookmarks(servers);
  },
  async DeleteServer(id) {
    loadBookmarks();
    if (!local.servers.some((s) => s.id === id))
      throw new Error("服务器书签不存在。");
    return saveBookmarks(local.servers.filter((s) => s.id !== id));
  },
  async OpenPreview() {
    if (local.session.mode === "preview") return result();
    local.session = {
      ...emptyWorkspace().session,
      mode: "preview",
      channelID: "lobby",
      nickname: "Resona",
    };
    local.channels = structuredClone(channels);
    local.messages = [];
    return result();
  },
  async LeavePreview() {
    local.session = emptyWorkspace().session;
    local.channels = [];
    local.messages = [];
    return result();
  },
  async SelectChannel(id) {
    if (
      local.session.mode !== "preview" ||
      !local.channels.some((c) => c.id === id)
    )
      throw new Error("频道不可用。");
    local.session.channelID = id;
    local.channels = local.channels.map((c) => ({
      ...c,
      members: c.id === id ? 1 : 0,
    }));
    return result();
  },
  async SendMessage(text) {
    if (local.session.mode !== "preview") throw new Error("当前未连接。");
    if (!text.trim()) throw new Error("消息不能为空。");
    if ([...text.trim()].length > 2000)
      throw new Error("消息不能超过 2000 个字符。");
    local.messages.push({
      id: crypto.randomUUID(),
      channelID: local.session.channelID,
      author: local.session.nickname,
      text: text.trim(),
      createdAt: new Date().toISOString(),
      authorID: "preview-self",
      status: "sent",
      error: "",
    });
    local.messages = local.messages.slice(-500);
    return result();
  },
  async ConnectServer() {
    throw new Error("真实连接仅在桌面应用中可用");
  },
  async SendChannelMessage() {
    throw new Error("真实消息仅在桌面应用中可用");
  },
  async RetryMessage() {
    throw new Error("真实消息仅在桌面应用中可用");
  },
  async GetServerCredentialStatus() {
    return { saved: false, remember: true };
  },
  async ConnectSavedServer() {
    throw new Error("真实连接仅在桌面应用中可用");
  },
  async ConnectServerWithPassword() {
    throw new Error("真实连接仅在桌面应用中可用");
  },
  async ForgetServerPassword() {
    throw new Error("真实连接仅在桌面应用中可用");
  },
  async DisconnectServer() {
    throw new Error("真实连接仅在桌面应用中可用");
  },
};

function normalizeWorkspace(value: Workspace): Workspace {
  const fallback = emptyWorkspace();
  const session = value?.session ?? fallback.session;
  return {
    servers: value?.servers ?? [],
    session: {
      id: session.id ?? "",
      sendingMessageID: session.sendingMessageID ?? "",
      mode: session.mode ?? "offline",
      channelID: session.channelID ?? "",
      switchingChannelID: session.switchingChannelID ?? "",
      nickname: session.nickname ?? "",
      serverID: session.serverID ?? "",
      serverName: session.serverName ?? "",
      identityUID: session.identityUID ?? "",
      selfID: session.selfID ?? "",
      error: session.error ?? "",
      credentialError: session.credentialError ?? "",
      memberSyncState: session.memberSyncState || "pending",
      memberSyncError: session.memberSyncError ?? "",
    },
    channels: (value?.channels ?? []).map((channel) => ({
      ...channel,
      description: channel.description ?? "",
      members: channel.members ?? 0,
      parentID: channel.parentID ?? "",
      order: channel.order ?? "",
      passwordRequired: channel.passwordRequired ?? false,
      kind: channel.kind ?? "channel",
      align: channel.align ?? "left",
      repeat: channel.repeat ?? false,
      iconID: channel.iconID ?? "",
      iconRef: channel.iconRef ?? "",
    })),
    users: value?.users ?? [],
    messages: (value?.messages ?? []).map((message) => ({
      ...message,
      authorID: message.authorID ?? "",
      status: message.status ?? "received",
      error: message.error ?? "",
    })),
    notifications: value?.notifications ?? [],
  };
}

function normalizeBridge(bridge: Bridge): Bridge {
  return {
    GetIconResource: (sessionID, ref) => bridge.GetIconResource(sessionID, ref),
    GetWorkspace: async () => normalizeWorkspace(await bridge.GetWorkspace()),
    SaveServer: async (server) =>
      normalizeWorkspace(await bridge.SaveServer(server)),
    DeleteServer: async (id) =>
      normalizeWorkspace(await bridge.DeleteServer(id)),
    OpenPreview: async () => normalizeWorkspace(await bridge.OpenPreview()),
    LeavePreview: async () => normalizeWorkspace(await bridge.LeavePreview()),
    SelectChannel: async (id) =>
      normalizeWorkspace(await bridge.SelectChannel(id)),
    SendMessage: async (message) =>
      normalizeWorkspace(await bridge.SendMessage(message)),
    SendChannelMessage: async (sessionID, channelID, text) =>
      normalizeWorkspace(
        await bridge.SendChannelMessage(sessionID, channelID, text),
      ),
    RetryMessage: async (id, allowDuplicate) =>
      normalizeWorkspace(await bridge.RetryMessage(id, allowDuplicate)),
    ConnectServer: async (id, password) =>
      normalizeWorkspace(await bridge.ConnectServer(id, password)),
    GetServerCredentialStatus: (id) => bridge.GetServerCredentialStatus(id),
    ConnectSavedServer: async (id) =>
      normalizeWorkspace(await bridge.ConnectSavedServer(id)),
    ConnectServerWithPassword: async (id, password, remember) =>
      normalizeWorkspace(
        await bridge.ConnectServerWithPassword(id, password, remember),
      ),
    ForgetServerPassword: async (id) =>
      normalizeWorkspace(await bridge.ForgetServerPassword(id)),
    DisconnectServer: async () =>
      normalizeWorkspace(await bridge.DisconnectServer()),
  };
}

export const api: Bridge = normalizeBridge(
  window.go?.main?.App ?? browserBridge,
);
export function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  return typeof error === "string" ? error : "操作失败，请重试。";
}
