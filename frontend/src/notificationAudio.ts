import type { Notification, Workspace } from "./api";

export interface SoundPreferences {
  enabled: boolean;
  volume: number;
}
const preferenceKey = "resona.notification-audio.v1";
const defaults: SoundPreferences = { enabled: true, volume: 35 };
const soundPaths: Record<Notification["kind"], string> = {
  connected: "/sounds/connected.wav",
  member_joined: "/sounds/member-joined.wav",
  member_left: "/sounds/member-left.wav",
  disconnected: "/sounds/disconnected.wav",
};

export function readSoundPreferences(): SoundPreferences {
  try {
    const value: unknown = JSON.parse(
      localStorage.getItem(preferenceKey) ?? "null",
    );
    if (
      value &&
      typeof value === "object" &&
      "enabled" in value &&
      typeof value.enabled === "boolean" &&
      "volume" in value &&
      typeof value.volume === "number" &&
      Number.isFinite(value.volume) &&
      value.volume >= 0 &&
      value.volume <= 100
    )
      return { enabled: value.enabled, volume: value.volume };
  } catch {
    /* Use defaults when preferences are unavailable. */
  }
  return { ...defaults };
}

export function saveSoundPreferences(value: SoundPreferences): void {
  try {
    localStorage.setItem(preferenceKey, JSON.stringify(value));
  } catch {
    /* The current session keeps its settings when storage is unavailable. */
  }
}

export class NotificationAudio {
  private context?: AudioContext;
  private gain?: GainNode;
  private buffers = new Map<Notification["kind"], Promise<AudioBuffer>>();
  private sources = new Set<AudioBufferSourceNode>();
  private seen = new Set<string>();
  private initialized = false;
  private disposed = false;
  private revision = 0;
  private pending = new Map<
    string,
    { token: symbol; notificationID?: string }
  >();
  private lastPlayed = new Map<Notification["kind"], number>();
  private workspace?: Workspace;

  constructor(
    private preferences: SoundPreferences,
    private report: (message: string) => void,
  ) {}

  setPreferences(value: SoundPreferences): void {
    this.preferences = value;
    if (this.gain) this.gain.gain.value = value.volume / 100;
    if (!value.enabled) {
      this.revision += 1;
      this.stopSources();
    }
  }

  // Create and resume during the click itself, before fetching or decoding audio.
  async unlock(): Promise<void> {
    if (this.disposed) return;
    try {
      if (!this.context) {
        const Constructor =
          window.AudioContext ??
          (window as unknown as { webkitAudioContext?: typeof AudioContext })
            .webkitAudioContext;
        if (!Constructor) throw new Error("Audio output unavailable");
        this.context = new Constructor();
        this.gain = this.context.createGain();
        this.gain.gain.value = this.preferences.volume / 100;
        this.gain.connect(this.context.destination);
      }
      if (this.context.state !== "running") await this.context.resume();
      if (!this.disposed) this.report("");
    } catch {
      if (!this.disposed) this.report("提示音输出不可用");
    }
  }

  consume(workspace: Workspace): void {
    this.workspace = workspace;
    const notifications = workspace.notifications;
    const currentIDs = new Set(
      notifications.map((notification) => notification.id),
    );
    for (const [kind, playback] of this.pending) {
      if (playback.notificationID && !currentIDs.has(playback.notificationID))
        this.pending.delete(kind);
    }
    if (!this.initialized) {
      this.initialized = true;
      for (const notification of notifications) this.seen.add(notification.id);
      return;
    }
    for (const notification of notifications) {
      if (this.seen.has(notification.id)) continue;
      this.seen.add(notification.id);
      if (this.eligible(notification))
        void this.play(notification.kind, notification);
    }
    // The backend retains 64 events; keep enough history for overlapping snapshots.
    while (this.seen.size > 256)
      this.seen.delete(this.seen.values().next().value!);
  }

  async preview(kind: Notification["kind"]): Promise<void> {
    await this.unlock();
    await this.play(kind);
  }

  private eligible(notification: Notification): boolean {
    const age = Date.now() - Date.parse(notification.createdAt);
    if (!Number.isFinite(age) || age < -1000 || age > 5000 || !this.workspace)
      return false;
    const session = this.workspace.session;
    if (
      !this.workspace.notifications.some(
        (current) => current.id === notification.id,
      )
    )
      return false;
    if (notification.kind === "connected") return session.mode === "connected";
    return notification.kind === "disconnected"
      ? session.mode === "offline" ||
          session.mode === "failed" ||
          session.mode === "disconnecting"
      : session.mode === "connected" &&
          notification.channelID === session.channelID;
  }

  private async play(
    kind: Notification["kind"],
    notification?: Notification,
  ): Promise<void> {
    const context = this.context;
    const sessionNotification =
      !!notification && (kind === "connected" || kind === "disconnected");
    const pendingKey = sessionNotification
      ? `${kind}:${notification.id}`
      : kind;
    if (
      this.disposed ||
      !this.preferences.enabled ||
      !context ||
      context.state !== "running" ||
      !soundPaths[kind] ||
      this.pending.has(pendingKey) ||
      (!sessionNotification &&
        Date.now() - (this.lastPlayed.get(kind) ?? 0) < 400)
    )
      return;
    const revision = this.revision;
    const token = Symbol(kind);
    this.pending.set(pendingKey, { token, notificationID: notification?.id });
    try {
      let buffer = this.buffers.get(kind);
      if (!buffer) {
        buffer = fetch(soundPaths[kind]).then(async (response) => {
          if (!response.ok) throw new Error("Sound asset unavailable");
          return context.decodeAudioData(await response.arrayBuffer());
        });
        this.buffers.set(kind, buffer);
      }
      const decoded = await buffer;
      if (
        this.disposed ||
        revision !== this.revision ||
        !this.preferences.enabled ||
        context.state !== "running" ||
        (notification && !this.eligible(notification))
      )
        return;
      const source = context.createBufferSource();
      source.buffer = decoded;
      source.connect(this.gain!);
      source.onended = () => {
        source.disconnect();
        this.sources.delete(source);
      };
      this.sources.add(source);
      source.start();
      if (!sessionNotification) this.lastPlayed.set(kind, Date.now());
      this.report("");
    } catch {
      this.buffers.delete(kind);
      if (!this.disposed) this.report("提示音播放失败");
    } finally {
      if (this.pending.get(pendingKey)?.token === token)
        this.pending.delete(pendingKey);
    }
  }

  private stopSources(): void {
    for (const source of this.sources) {
      try {
        source.stop();
        source.disconnect();
      } catch {
        /* Already ended. */
      }
    }
    this.sources.clear();
  }

  close(): void {
    this.disposed = true;
    this.revision += 1;
    this.stopSources();
    this.buffers.clear();
    this.gain?.disconnect();
    void this.context?.close().catch(() => {});
  }
}
