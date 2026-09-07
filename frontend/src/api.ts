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
}
export interface Message {
  id: string;
  channelID: string;
  author: string;
  text: string;
  createdAt: string;
}
export interface Workspace {
  servers: ServerProfile[];
  session: { mode: "offline" | "preview"; channelID: string; nickname: string };
  channels: Channel[];
  messages: Message[];
}
interface Bridge {
  GetWorkspace(): Promise<Workspace>;
  SaveServer(server: ServerProfile): Promise<Workspace>;
  DeleteServer(id: string): Promise<Workspace>;
  OpenPreview(): Promise<Workspace>;
  LeavePreview(): Promise<Workspace>;
  SelectChannel(id: string): Promise<Workspace>;
  SendMessage(text: string): Promise<Workspace>;
}
declare global {
  interface Window {
    go?: { main?: { App?: Bridge } };
  }
}

const storageKey = "resona.browser.workspace.v1";
export const browserPreview = !window.go?.main?.App;
const channels: Channel[] = [
  { id: "lobby", name: "大厅", description: "本地预览", members: 1 },
  { id: "music", name: "音乐", description: "本地预览", members: 0 },
  { id: "workshop", name: "工作间", description: "本地预览", members: 0 },
];
const emptyWorkspace = (): Workspace => ({
  servers: [],
  session: { mode: "offline", channelID: "", nickname: "Resona" },
  channels: [],
  messages: [],
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
    local.session = { mode: "preview", channelID: "lobby", nickname: "Resona" };
    local.channels = structuredClone(channels);
    local.messages = [];
    return result();
  },
  async LeavePreview() {
    local.session = { mode: "offline", channelID: "", nickname: "Resona" };
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
    });
    local.messages = local.messages.slice(-500);
    return result();
  },
};
export const api: Bridge = window.go?.main?.App ?? browserBridge;
export function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  return typeof error === "string" ? error : "操作失败，请重试。";
}
