import { expect, test, type Page } from "@playwright/test";
import type { Workspace } from "../src/api";
import type { NotificationAudio } from "../src/notificationAudio";

interface DirectAudioDriver {
  audio: NotificationAudio;
  state: Workspace;
}

type SoundKind = "connected" | "member_joined" | "member_left" | "disconnected";
interface AudioTestControl {
  plays: string[];
  gains: Array<{ value: number }>;
  stops: number;
  setSession(
    mode: string,
    channelID?: string,
    clearNotifications?: boolean,
  ): void;
  emit(
    kind: SoundKind,
    options?: { age?: number; channelID?: string; mode?: string; id?: string },
  ): void;
}
const control = (page: Page) =>
  page.evaluate(() => {
    const state = (window as unknown as { soundTest: AudioTestControl })
      .soundTest;
    return {
      plays: state.plays,
      volumes: state.gains.map((gain) => gain.value),
      stops: state.stops,
    };
  });
async function emit(
  page: Page,
  kind: SoundKind,
  options: Parameters<AudioTestControl["emit"]>[1] = {},
) {
  await page.evaluate(
    ({ kind, options }) => {
      (window as unknown as { soundTest: AudioTestControl }).soundTest.emit(
        kind,
        options,
      );
    },
    { kind, options },
  );
}

async function setSession(
  page: Page,
  mode: string,
  channelID?: string,
  clearNotifications = false,
) {
  await page.evaluate(
    ({ mode, channelID, clearNotifications }) => {
      (
        window as unknown as { soundTest: AudioTestControl }
      ).soundTest.setSession(mode, channelID, clearNotifications);
    },
    { mode, channelID, clearNotifications },
  );
}

async function installDirectAudio(page: Page) {
  await page.evaluate(async () => {
    const modulePath = "/src/notificationAudio.ts";
    const { NotificationAudio } = await import(modulePath);
    const audio = new NotificationAudio(
      { enabled: true, volume: 35 },
      () => {},
    );
    const state = {
      session: { mode: "offline", channelID: "test-room" },
      notifications: [],
    } as unknown as Workspace;
    audio.consume(state);
    await audio.unlock();
    Object.assign(window, { directAudio: { audio, state } });
  });
}

async function directSessionEvent(
  page: Page,
  kind: "connected" | "disconnected",
) {
  await page.evaluate((kind) => {
    const { audio, state } = (
      window as unknown as { directAudio: DirectAudioDriver }
    ).directAudio;
    state.session.mode = kind === "connected" ? "connected" : "offline";
    state.notifications.push({
      id: `session-${kind}`,
      kind,
      channelID: "test-room",
      createdAt: new Date().toISOString(),
    });
    audio.consume(state);
  }, kind);
}

async function installAudioBridge(page: Page, initialMode = "connected") {
  await page.route("**/sounds/*.wav", (route) =>
    route.fulfill({ body: route.request().url() }),
  );
  await page.addInitScript((initialMode) => {
    const state = {
      servers: [],
      session: {
        mode: initialMode,
        channelID: "test-room",
        nickname: "Tester",
        serverID: "test",
        serverName: "Test server",
      },
      channels: [{ id: "test-room", name: "Test room", members: 1 }],
      users: [],
      messages: [],
      notifications: [
        {
          id: "initial-connected",
          kind: "connected",
          channelID: "test-room",
          createdAt: new Date().toISOString(),
        },
        {
          id: "initial",
          kind: "member_joined",
          channelID: "test-room",
          createdAt: new Date().toISOString(),
        },
      ],
    };
    let sequence = 0;
    const soundTest = {
      plays: [] as string[],
      gains: [] as Array<{ value: number }>,
      stops: 0,
      setSession(mode: string, channelID?: string, clearNotifications = false) {
        state.session.mode = mode;
        if (channelID) state.session.channelID = channelID;
        if (clearNotifications) state.notifications = [];
      },
      emit(
        kind: string,
        options: {
          age?: number;
          channelID?: string;
          mode?: string;
          id?: string;
        } = {},
      ) {
        if (options.mode) state.session.mode = options.mode;
        state.notifications.push({
          id: options.id ?? `event-${++sequence}`,
          kind,
          channelID: options.channelID ?? "test-room",
          createdAt: new Date(Date.now() - (options.age ?? 0)).toISOString(),
        });
      },
    };
    class FakeAudioContext {
      state = "suspended";
      destination = {};
      async resume() {
        this.state = "running";
      }
      async close() {
        this.state = "closed";
      }
      createGain() {
        const gain = { value: 1 };
        soundTest.gains.push(gain);
        return { gain, connect() {}, disconnect() {} };
      }
      async decodeAudioData(bytes: ArrayBuffer) {
        return new TextDecoder().decode(bytes);
      }
      createBufferSource() {
        return {
          buffer: "",
          onended: null,
          connect() {},
          disconnect() {},
          start() {
            soundTest.plays.push(this.buffer);
          },
          stop() {
            soundTest.stops += 1;
          },
        };
      }
    }
    Object.assign(window, {
      AudioContext: FakeAudioContext,
      soundTest,
      go: {
        main: { App: { GetWorkspace: async () => structuredClone(state) } },
      },
    });
  }, initialMode);
  await page.goto("/");
  if (initialMode === "connected") {
    await expect(page.locator(".connection-label")).toHaveText(
      "Test server · 在线",
    );
  } else {
    await expect(page.locator(".connection-label")).toHaveText("离线");
  }
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("checkbox", { name: "启用提示音" }).uncheck();
  await page.getByRole("checkbox", { name: "启用提示音" }).check();
}

test("initial queues are silent; rapid join and leave play once with same-kind throttling", async ({
  page,
}) => {
  await installAudioBridge(page);
  expect((await control(page)).plays).toEqual([]);
  await emit(page, "member_joined", { id: "join" });
  await emit(page, "member_joined");
  await emit(page, "member_left");
  await expect.poll(async () => (await control(page)).plays.length).toBe(2);
  expect(
    (await control(page)).plays.map((path) => path.split("/").pop()),
  ).toEqual(["member-joined.wav", "member-left.wav"]);
  await emit(page, "member_joined", { id: "join" });
  await emit(page, "member_left", { age: 9000 });
  await emit(page, "member_joined", { channelID: "other-room" });
  await page.waitForTimeout(1200);
  expect((await control(page)).plays).toHaveLength(2);
  await emit(page, "disconnected", { mode: "offline" });
  await expect.poll(async () => (await control(page)).plays.length).toBe(3);
  expect((await control(page)).plays[2]).toContain("disconnected.wav");
});

test("disabled events are consumed, volume updates live, and preferences survive reload", async ({
  page,
}) => {
  await installAudioBridge(page);
  await page.getByRole("button", { name: "试听成员进入当前频道" }).click();
  await expect.poll(async () => (await control(page)).plays.length).toBe(1);
  await page.getByRole("slider", { name: "提示音音量" }).fill("70");
  expect((await control(page)).volumes.at(-1)).toBe(0.7);
  await page.getByRole("checkbox", { name: "启用提示音" }).uncheck();
  expect((await control(page)).stops).toBe(1);
  await emit(page, "member_left");
  await page.waitForTimeout(1200);
  await page.getByRole("checkbox", { name: "启用提示音" }).check();
  await page.waitForTimeout(1200);
  expect((await control(page)).plays).toHaveLength(1);
  await emit(page, "member_left");
  await expect.poll(async () => (await control(page)).plays.length).toBe(2);
  await page.reload();
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await expect(page.getByRole("slider", { name: "提示音音量" })).toHaveValue(
    "70",
  );
  expect((await control(page)).plays).toHaveLength(0);
});

test("connection success plays once, failures stay silent, and a new connection can play again", async ({
  page,
}) => {
  await installAudioBridge(page, "offline");
  expect((await control(page)).plays).toHaveLength(0);
  await setSession(page, "connecting", undefined, true);
  await expect(page.locator(".connection-label")).toContainText("正在连接");
  await setSession(page, "failed");
  await expect(page.locator(".connection-label")).toContainText("连接失败");
  expect((await control(page)).plays).toHaveLength(0);
  await emit(page, "connected", {
    mode: "connected",
    channelID: "previous-room",
    id: "success-one",
  });
  await expect.poll(async () => (await control(page)).plays.length).toBe(1);
  expect((await control(page)).plays[0]).toContain("connected.wav");
  await setSession(page, "connected", "another-room");
  await page.waitForTimeout(1200);
  expect((await control(page)).plays).toHaveLength(1);
  await emit(page, "disconnected", { mode: "offline" });
  await expect.poll(async () => (await control(page)).plays.length).toBe(2);
  await setSession(page, "connecting", undefined, true);
  await expect(page.locator(".connection-label")).toContainText("正在连接");
  await emit(page, "connected", { mode: "connected", id: "success-two" });
  await expect.poll(async () => (await control(page)).plays.length).toBe(3);
  expect(
    (await control(page)).plays.filter((path) =>
      path.endsWith("/connected.wav"),
    ),
  ).toHaveLength(2);
});

test("success followed by disconnection before a snapshot only plays disconnection", async ({
  page,
}) => {
  await installAudioBridge(page, "offline");
  await page.evaluate(() => {
    const test = (window as unknown as { soundTest: AudioTestControl })
      .soundTest;
    test.emit("connected", { mode: "connected" });
    test.emit("disconnected", { mode: "failed" });
  });
  await expect.poll(async () => (await control(page)).plays.length).toBe(1);
  expect((await control(page)).plays[0]).toContain("disconnected.wav");
});

test("a new connection supersedes pending success playback from the previous session", async ({
  page,
}) => {
  await installAudioBridge(page, "offline");
  let pendingRoute: import("@playwright/test").Route | undefined;
  await page.route("**/sounds/connected.wav", (route) => {
    pendingRoute = route;
  });
  await emit(page, "connected", { mode: "connected", id: "old-session" });
  await expect.poll(() => !!pendingRoute).toBe(true);
  await setSession(page, "connecting", undefined, true);
  await expect(page.locator(".connection-label")).toContainText("正在连接");
  await emit(page, "connected", { mode: "connected", id: "new-session" });
  await expect(page.locator(".connection-label")).toContainText("在线");
  await pendingRoute!.fulfill({ body: "connected.wav" });
  await expect.poll(async () => (await control(page)).plays.length).toBe(1);
  await page.waitForTimeout(1200);
  expect((await control(page)).plays).toEqual(["connected.wav"]);
});

test("invalid sound settings and unavailable storage preserve usable session controls", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.addInitScript(() => {
    localStorage.setItem(
      "resona.notification-audio.v1",
      '{"enabled":"invalid","volume":900}',
    );
    const original = Storage.prototype.setItem;
    Storage.prototype.setItem = function (key, value) {
      if (key === "resona.notification-audio.v1")
        throw new DOMException("full", "QuotaExceededError");
      original.call(this, key, value);
    };
    Object.assign(window, {
      AudioContext: class {
        constructor() {
          throw new Error("device unavailable");
        }
      },
    });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await expect(
    page.getByRole("checkbox", { name: "启用提示音" }),
  ).toBeChecked();
  await expect(page.getByRole("slider", { name: "提示音音量" })).toHaveValue(
    "35",
  );
  await page.getByRole("slider", { name: "提示音音量" }).fill("50");
  await page.getByRole("button", { name: "试听成员进入当前频道" }).click();
  await expect(page.locator(".sound-error")).toHaveText("提示音输出不可用");
  await expect(page.getByRole("slider", { name: "提示音音量" })).toHaveValue(
    "50",
  );
  expect(errors).toEqual([]);
});

test("bundled sounds decode and play through real Web Audio; settings fit desktop and mobile", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.addInitScript(() => {
    const original = AudioBufferSourceNode.prototype.start;
    Object.assign(window, { realSoundStarts: 0 });
    AudioBufferSourceNode.prototype.start = function (
      ...args: Parameters<typeof original>
    ) {
      (window as unknown as { realSoundStarts: number }).realSoundStarts += 1;
      return original.apply(this, args);
    };
  });
  await page.goto("/");
  await page.getByRole("button", { name: "设置", exact: true }).click();
  for (const label of [
    "连接服务器",
    "断开连接",
    "成员进入当前频道",
    "成员离开当前频道",
  ]) {
    await page.getByRole("button", { name: `试听${label}` }).click();
  }
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as unknown as { realSoundStarts: number }).realSoundStarts,
      ),
    )
    .toBe(4);
  await expect(page.locator(".sound-error")).toHaveCount(0);
  await page.screenshot({
    path: testInfo.outputPath("notification-settings-desktop.png"),
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole("button", { name: "试听成员离开当前频道" }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
  await page.screenshot({
    path: testInfo.outputPath("notification-settings-mobile.png"),
  });
  expect(errors).toEqual([]);
});

test("disabling cancels pending playback and closing releases active sources", async ({
  page,
}) => {
  await installAudioBridge(page);
  let pendingRoute: import("@playwright/test").Route | undefined;
  await page.route("**/sounds/member-joined.wav", (route) => {
    pendingRoute = route;
  });
  await page.getByRole("button", { name: "试听成员进入当前频道" }).click();
  await expect.poll(() => !!pendingRoute).toBe(true);
  await page.getByRole("checkbox", { name: "启用提示音" }).uncheck();
  await pendingRoute!.fulfill({ body: "member-joined.wav" });
  await page.waitForTimeout(100);
  expect((await control(page)).plays).toHaveLength(0);
  await page.getByRole("checkbox", { name: "启用提示音" }).check();
  await page.getByRole("button", { name: "试听成员进入当前频道" }).click();
  await expect.poll(async () => (await control(page)).plays.length).toBe(1);
  await page.evaluate(() => window.dispatchEvent(new Event("pagehide")));
  expect((await control(page)).stops).toBe(1);
  await emit(page, "member_left");
  await page.waitForTimeout(1200);
  expect((await control(page)).plays).toHaveLength(1);
});

for (const kind of ["connected", "disconnected"] as const) {
  test(`real ${kind} plays while its preview is still loading`, async ({
    page,
  }) => {
    await installAudioBridge(page);
    await installDirectAudio(page);
    let pendingRoute: import("@playwright/test").Route | undefined;
    await page.route(`**/sounds/${kind}.wav`, (route) => {
      pendingRoute = route;
    });
    await page.evaluate((kind) => {
      void (
        window as unknown as { directAudio: DirectAudioDriver }
      ).directAudio.audio.preview(kind);
    }, kind);
    await expect.poll(() => !!pendingRoute).toBe(true);
    await directSessionEvent(page, kind);
    await pendingRoute!.fulfill({ body: `${kind}.wav` });
    await expect.poll(async () => (await control(page)).plays.length).toBe(2);
    expect((await control(page)).plays).toEqual([`${kind}.wav`, `${kind}.wav`]);
  });

  test(`real ${kind} is not throttled by a preview played less than 400ms earlier`, async ({
    page,
  }) => {
    await installAudioBridge(page);
    await page.clock.setFixedTime(new Date());
    await installDirectAudio(page);
    await page.evaluate(async (kind) => {
      await (
        window as unknown as { directAudio: DirectAudioDriver }
      ).directAudio.audio.preview(kind);
    }, kind);
    expect((await control(page)).plays).toHaveLength(1);
    await directSessionEvent(page, kind);
    await expect.poll(async () => (await control(page)).plays.length).toBe(2);
  });
}
