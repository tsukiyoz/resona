import { expect, test, type Page } from "@playwright/test";

interface ChatControl {
  calls: {
    send: [string, string, string][];
    retry: [string, boolean][];
    select: string[];
    disconnect: number;
  };
  finish(status: "sent" | "failed" | "unconfirmed"): void;
  receive(text: string, channelID?: string): void;
  history(count: number): void;
  holdSend(): void;
  releaseSend(): void;
  reconnect(): void;
  drop(): void;
  fastAck(): void;
}
async function control(
  page: Page,
  operation:
    | "finish"
    | "receive"
    | "history"
    | "holdSend"
    | "releaseSend"
    | "reconnect"
    | "drop"
    | "fastAck",
  argument?: string | number,
) {
  await page.evaluate(
    ({ operation, argument }) => {
      const target = (window as unknown as { chatControl: ChatControl })
        .chatControl;
      if (operation === "finish")
        target.finish(argument as "sent" | "failed" | "unconfirmed");
      else if (operation === "receive") target.receive(String(argument));
      else if (operation === "history") target.history(Number(argument));
      else target[operation]();
    },
    { operation, argument },
  );
}
async function calls(page: Page) {
  return page.evaluate(
    () => (window as unknown as { chatControl: ChatControl }).chatControl.calls,
  );
}
async function installChat(page: Page) {
  await page.addInitScript(() => {
    const canvas = document.createElement("canvas");
    canvas.width = 16;
    canvas.height = 16;
    const context = canvas.getContext("2d")!;
    context.fillStyle = "#bc4864";
    context.fillRect(1, 1, 14, 14);
    context.fillStyle = "#ffffff";
    context.fillRect(5, 5, 6, 6);
    const makeChannel = (id: string, name: string, order: string) => ({
      id,
      name,
      order,
      parentID: "",
      kind: "channel",
      align: "left",
      repeat: false,
      iconID: "",
      iconRef: "",
      description: "频道讨论",
      members: 1,
      passwordRequired: false,
    });
    const state = {
      servers: [
        {
          id: "server",
          name: "聊天测试",
          address: "test.invalid",
          nickname: "Resona",
        },
      ],
      session: {
        id: "session-1",
        mode: "connected",
        serverID: "server",
        serverName: "聊天测试",
        channelID: "lobby",
        selfID: "self",
        nickname: "Resona",
        sendingMessageID: "",
        memberSyncState: "ready",
        error: "",
      },
      channels: [
        {
          ...makeChannel("heading", "交流区", "0"),
          kind: "separator",
          align: "center",
        },
        {
          ...makeChannel("lobby", "大厅", "heading"),
          iconID: "1001",
          iconRef: "valid-icon",
        },
        {
          ...makeChannel("line", "=", "lobby"),
          kind: "separator",
          repeat: true,
        },
        makeChannel("workshop", "工作间", "line"),
        { ...makeChannel("blank", "", "workshop"), kind: "separator" },
        {
          ...makeChannel("broken", "图标缺失", "blank"),
          iconID: "1002",
          iconRef: "broken-icon",
        },
        {
          ...makeChannel("unsafe", "外部图标", "broken"),
          iconRef: "unsafe-icon",
        },
      ],
      users: [
        { id: "self", nickname: "Resona", channelID: "lobby", self: true },
        { id: "peer", nickname: "朋友", channelID: "lobby", self: false },
      ],
      messages: [] as {
        id: string;
        channelID: string;
        author: string;
        authorID: string;
        text: string;
        createdAt: string;
        status: string;
        error: string;
      }[],
      notifications: [],
    };
    let sequence = 0;
    let held = false;
    let fastAck = false;
    let release: (() => void) | undefined;
    const snapshot = () => structuredClone(state);
    const chatControl: ChatControl = {
      calls: { send: [], retry: [], select: [], disconnect: 0 },
      fastAck() {
        fastAck = true;
      },
      finish(status) {
        const message = state.messages.find(
          (m) => m.id === state.session.sendingMessageID,
        )!;
        message.status = status;
        message.error =
          status === "failed"
            ? "没有发送权限"
            : status === "unconfirmed"
              ? "等待服务器确认超时"
              : "";
        state.session.sendingMessageID = "";
      },
      receive(text, channelID = state.session.channelID) {
        state.messages.push({
          id: `received-${++sequence}`,
          channelID,
          author: "朋友",
          authorID: "peer",
          text,
          createdAt: new Date().toISOString(),
          status: "received",
          error: "",
        });
      },
      history(count) {
        for (let i = 0; i < count; i++) this.receive(`历史消息 ${i + 1}`);
      },
      holdSend() {
        held = true;
      },
      releaseSend() {
        release?.();
      },
      reconnect() {
        state.session = {
          ...state.session,
          id: "session-2",
          mode: "connected",
          channelID: "lobby",
          sendingMessageID: "",
        };
        state.messages = [];
      },
      drop() {
        if (state.session.sendingMessageID) this.finish("unconfirmed");
        state.session.mode = "failed";
        state.session.error = "连接意外断开";
        state.users = [];
      },
    };
    const app = {
      async GetIconResource(sessionID: string, ref: string) {
        if (sessionID !== state.session.id) throw new Error("会话已改变");
        return {ref, dataURL: ref === "valid-icon" ? canvas.toDataURL() : ref === "broken-icon" ? "data:image/png;base64,AAAA" : "https://untrusted.invalid/icon.png"};
      },
      async GetWorkspace() {
        return snapshot();
      },
      async SendChannelMessage(
        sessionID: string,
        channelID: string,
        text: string,
      ) {
        chatControl.calls.send.push([sessionID, channelID, text]);
        if (
          sessionID !== state.session.id ||
          channelID !== state.session.channelID ||
          state.session.mode !== "connected"
        )
          throw new Error("会话已改变");
        const id = `sent-${++sequence}`;
        if (fastAck) chatControl.receive("同时到达的消息");
        state.messages.push({
          id,
          channelID,
          author: "Resona",
          authorID: "self",
          text,
          createdAt: new Date().toISOString(),
          status: "sending",
          error: "",
        });
        state.session.sendingMessageID = id;
        if (fastAck) {
          fastAck = false;
          chatControl.finish("sent");
        }
        const response = snapshot();
        if (held) {
          held = false;
          return new Promise<typeof state>((resolve) => {
            release = () => resolve(response);
          });
        }
        return response;
      },
      async RetryMessage(id: string, allowDuplicate: boolean) {
        chatControl.calls.retry.push([id, allowDuplicate]);
        const message = state.messages.find((m) => m.id === id)!;
        message.status = "sending";
        message.error = "";
        state.session.sendingMessageID = id;
        return snapshot();
      },
      async SelectChannel(id: string) {
        chatControl.calls.select.push(id);
        if (state.session.sendingMessageID) throw new Error("消息正在发送");
        state.session.channelID = id;
        return snapshot();
      },
      async DisconnectServer() {
        chatControl.calls.disconnect++;
        if (state.session.sendingMessageID) chatControl.finish("unconfirmed");
        state.session.mode = "offline";
        state.users = [];
        return snapshot();
      },
    };
    Object.assign(window, { chatControl, go: { main: { App: app } } });
  });
  await page.goto("/");
  await expect(
    page.getByRole("textbox", { name: "消息", exact: true }),
  ).toBeEnabled();
}

test("redesigned workspace remains usable across themes and viewport sizes", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await installChat(page);
  await control(page, "receive", "今晚先在大厅集合，频道已经准备好了。");
  const input = page.getByRole("textbox", { name: "消息", exact: true });
  await input.fill("收到，我在这里。先确认文字连接，再继续讨论。");
  await input.press("Enter");
  await control(page, "finish", "sent");
  await control(
    page,
    "receive",
    "可以看到你的消息。工作间也可以随时切换过去。",
  );
  await expect(page.locator(".message")).toHaveCount(3);
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.screenshot({
    path: "/private/tmp/resona-dark-1440.png",
    animations: "disabled",
  });
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.screenshot({
    path: "/private/tmp/resona-dark-1280.png",
    animations: "disabled",
  });
  await page.getByRole("button", { name: "收起频道详情" }).click();
  await expect(page.locator(".details-panel")).toHaveCount(0);
  await page.getByRole("button", { name: "展开频道详情" }).click();
  await expect(page.locator(".details-panel")).toBeVisible();
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("button", { name: "浅色" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(page.locator(".server-row.selected")).toHaveCSS(
    "background-color",
    "rgb(225, 230, 235)",
  );
  await expect(page.locator(".channel-row.selected")).toHaveCSS(
    "color",
    "rgb(50, 107, 156)",
  );
  await page.screenshot({
    path: "/private/tmp/resona-light-settings.png",
    animations: "disabled",
  });
  await page.getByRole("button", { name: "聊天", exact: true }).click();
  await page.screenshot({
    path: "/private/tmp/resona-light-chat.png",
    animations: "disabled",
  });
  await page.getByRole("button", { name: "添加服务器", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.screenshot({
    path: "/private/tmp/resona-light-modal.png",
    animations: "disabled",
  });
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("button", { name: "深色" }).click();
  await page.getByRole("button", { name: "聊天", exact: true }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator(".details-panel")).toHaveCount(0);
  await page.getByRole("button", { name: "展开频道详情" }).click();
  await expect(page.locator(".details-panel")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(
    page.getByRole("button", { name: "展开频道详情" }),
  ).toBeFocused();
  await expect(input).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: "/private/tmp/resona-dark-mobile.png",
    animations: "disabled",
  });
  await page.getByRole("button", { name: "展开侧栏" }).click();
  await expect(page.locator(".sidebar")).toBeVisible();
  await page.screenshot({
    path: "/private/tmp/resona-dark-mobile-navigation.png",
    animations: "disabled",
  });
  await page.getByRole("button", { name: "关闭侧栏" }).click();
  await control(page, "drop");
  await expect(input).toBeDisabled();
  await expect(page.locator(".message")).toHaveCount(3);
  await page.screenshot({
    path: "/private/tmp/resona-dark-mobile-disconnected.png",
    animations: "disabled",
  });
});

test("channel messages wait for acknowledgement and incoming text remains plain text", async ({
  page,
}) => {
  await installChat(page);
  const input = page.getByRole("textbox", { name: "消息", exact: true });
  await input.fill("你好 TS3");
  await input.press("Enter");
  await expect(page.locator(".message-sending")).toContainText("你好 TS3");
  await expect(input).toHaveValue("你好 TS3");
  await expect(input).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "工作间", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "断开 聊天测试" }),
  ).toBeEnabled();
  expect((await calls(page)).send).toEqual([
    ["session-1", "lobby", "你好 TS3"],
  ]);
  await control(page, "finish", "sent");
  await expect(page.locator(".message-sent")).toContainText("已发送");
  await expect(input).toHaveValue("");
  await control(
    page,
    "receive",
    "<img src=x onerror=alert(1)> [url=https://test.invalid]文字[/url]",
  );
  await expect(page.locator(".message-received")).toContainText("<img src=x");
  await expect(
    page.locator(".message-received img, .message-received a"),
  ).toHaveCount(0);
});

test("failures retain drafts and an unconfirmed retry requires duplicate confirmation", async ({
  page,
}) => {
  await installChat(page);
  const input = page.getByRole("textbox", { name: "消息", exact: true });
  await input.fill("保留原文");
  await input.press("Enter");
  await control(page, "finish", "failed");
  await expect(page.locator(".message-failed")).toContainText("没有发送权限");
  await expect(input).toHaveValue("保留原文");
  await page.getByRole("button", { name: "重试消息", exact: true }).click();
  await control(page, "finish", "unconfirmed");
  await expect(page.locator(".message-unconfirmed")).toContainText(
    "未确认送达",
  );
  await page.getByRole("button", { name: "重试消息", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("重复消息");
  await page.getByRole("button", { name: "取消", exact: true }).click();
  expect((await calls(page)).retry).toHaveLength(1);
  await page.getByRole("button", { name: "重试消息", exact: true }).click();
  await page.getByRole("button", { name: "仍然发送" }).click();
  expect((await calls(page)).retry.map((call) => call[1])).toEqual([
    false,
    true,
  ]);
  await control(page, "finish", "sent");
  await expect(input).toHaveValue("");
});

test("immediate acknowledgement clears only the submitted draft when incoming messages share the response", async ({
  page,
}) => {
  await installChat(page);
  await control(page, "fastAck");
  const input = page.getByRole("textbox", { name: "消息", exact: true });
  await input.fill("  本次发送  ");
  await input.press("Enter");
  expect((await calls(page)).send).toEqual([
    ["session-1", "lobby", "  本次发送  "],
  ]);
  await expect(page.locator(".message-sent")).toContainText("本次发送");
  await expect(page.locator(".message-received")).toContainText(
    "同时到达的消息",
  );
  await expect(input).toHaveValue("");
});

test("submitting an unconfirmed draft requires confirmation, while confirmed identical text is a new message", async ({
  page,
}) => {
  await installChat(page);
  const input = page.getByRole("textbox", { name: "消息", exact: true });
  await input.fill("未确认的原文");
  await input.press("Enter");
  await control(page, "finish", "unconfirmed");
  await expect(page.locator(".message-unconfirmed")).toBeVisible();
  await input.press("Enter");
  await expect(page.getByRole("dialog")).toContainText("重复消息");
  expect((await calls(page)).send).toHaveLength(1);
  expect((await calls(page)).retry).toHaveLength(0);
  await page.getByRole("button", { name: "取消", exact: true }).click();
  await expect(input).toHaveValue("未确认的原文");
  await input.press("Enter");
  await page.getByRole("button", { name: "仍然发送" }).click();
  expect((await calls(page)).send).toHaveLength(1);
  expect((await calls(page)).retry).toEqual([["sent-1", true]]);
  await control(page, "finish", "sent");
  await expect(input).toHaveValue("");
  await input.fill("未确认的原文");
  await input.press("Enter");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect((await calls(page)).send).toHaveLength(2);
});

test("editing an unconfirmed draft into new content sends a new message without duplicate confirmation", async ({
  page,
}) => {
  await installChat(page);
  const input = page.getByRole("textbox", { name: "消息", exact: true });
  await input.fill("旧内容");
  await input.press("Enter");
  await control(page, "finish", "unconfirmed");
  await expect(page.locator(".message-unconfirmed")).toBeVisible();
  await input.fill("修改后的新内容");
  await input.press("Enter");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect((await calls(page)).send).toEqual([
    ["session-1", "lobby", "旧内容"],
    ["session-1", "lobby", "修改后的新内容"],
  ]);
  expect((await calls(page)).retry).toHaveLength(0);
});

test("channel drafts and history survive channel changes and disconnect, but a new session clears history", async ({
  page,
}) => {
  await installChat(page);
  const input = page.getByRole("textbox", { name: "消息", exact: true });
  await input.fill("大厅草稿");
  await page.getByRole("button", { name: "工作间", exact: true }).click();
  await expect(input).toHaveValue("");
  await input.fill("工作间草稿");
  await control(page, "receive", "工作间记录");
  await expect(page.getByText("工作间记录", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "大厅", exact: true }).click();
  await expect(input).toHaveValue("大厅草稿");
  await expect(page.getByText("工作间记录", { exact: true })).toHaveCount(0);
  await input.press("Enter");
  await control(page, "finish", "sent");
  await expect(input).toHaveValue("");
  await page.getByRole("button", { name: "工作间", exact: true }).click();
  await expect(input).toHaveValue("工作间草稿");
  await page.getByRole("button", { name: "断开 聊天测试" }).click();
  await expect(input).toBeDisabled();
  await expect(page.getByText("工作间记录", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "大厅", exact: true }).click();
  await expect(page.getByText("大厅草稿", { exact: true })).toBeVisible();
  await expect(page.getByText("会话记录 · 已断开")).toBeVisible();
  await control(page, "reconnect");
  await expect(page.locator(".message")).toHaveCount(0);
});

test("IME enter does not send and the remote limit counts UTF-8 bytes", async ({
  page,
}) => {
  await installChat(page);
  const input = page.getByRole("textbox", { name: "消息", exact: true });
  await input.fill("输入法");
  await input.dispatchEvent("keydown", {
    key: "Enter",
    code: "Enter",
    isComposing: true,
    keyCode: 229,
  });
  expect((await calls(page)).send).toHaveLength(0);
  await input.press("Shift+Enter");
  expect((await calls(page)).send).toHaveLength(0);
  await input.fill("汉".repeat(2731));
  await expect(
    page.getByRole("button", { name: "发送消息", exact: true }),
  ).toBeDisabled();
  await expect(page.locator(".composer-footer")).toContainText(
    "8193/8192 字节",
  );
  await input.fill("a".repeat(8192));
  await expect(
    page.getByRole("button", { name: "发送消息", exact: true }),
  ).toBeEnabled();
  await input.fill(" " + "a".repeat(8192));
  await expect(
    page.getByRole("button", { name: "发送消息", exact: true }),
  ).toBeDisabled();
});

test("unexpected disconnect retains read-only history alongside its error and reconnect action", async ({
  page,
}) => {
  await installChat(page);
  await control(page, "receive", "掉线前的频道记录");
  await expect(
    page.getByText("掉线前的频道记录", { exact: true }),
  ).toBeVisible();
  await control(page, "drop");
  await expect(page.getByRole("alert")).toHaveText("连接意外断开");
  await expect(
    page.getByText("掉线前的频道记录", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("会话记录 · 已断开")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "重新连接 聊天测试" }),
  ).toBeEnabled();
  await expect(
    page.getByRole("textbox", { name: "消息", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "工作间", exact: true }).click();
  await expect(page.getByText("掉线前的频道记录", { exact: true })).toHaveCount(
    0,
  );
  await page.getByRole("button", { name: "大厅", exact: true }).click();
  await expect(
    page.getByText("掉线前的频道记录", { exact: true }),
  ).toBeVisible();
});

test("incoming messages do not steal history scrolling and own sends reveal the new message", async ({
  page,
}) => {
  await installChat(page);
  await control(page, "history", 60);
  await expect(page.locator(".message")).toHaveCount(60);
  const pane = page.locator(".message-history");
  await pane.evaluate((element) => {
    element.scrollTop = 0;
    element.dispatchEvent(new Event("scroll", { bubbles: true }));
  });
  await control(page, "receive", "新到的消息");
  await expect(page.locator(".message")).toHaveCount(61);
  expect(await pane.evaluate((element) => element.scrollTop)).toBe(0);
  await page
    .getByRole("textbox", { name: "消息", exact: true })
    .fill("我的新消息");
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  await expect
    .poll(() =>
      pane.evaluate(
        (element) =>
          element.scrollHeight - element.clientHeight - element.scrollTop,
      ),
    )
    .toBeLessThan(80);
});

test("disconnect remains available while a send bridge response is delayed", async ({
  page,
}) => {
  await installChat(page);
  await control(page, "holdSend");
  const input = page.getByRole("textbox", { name: "消息", exact: true });
  await input.fill("断开前消息");
  await input.press("Enter");
  await page.getByRole("button", { name: "断开 聊天测试" }).click();
  await expect(page.locator(".connection-label")).toContainText("离线");
  await control(page, "releaseSend");
  await expect(input).toBeDisabled();
  await expect(page.locator(".message-unconfirmed")).toContainText(
    "断开前消息",
  );
  await expect(page.getByRole("button", { name: "重试消息" })).toBeDisabled();
});

test("spacers are decorative and custom icons stay bounded with safe fallback", async ({
  page,
}, testInfo) => {
  const unsafeRequests: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("untrusted.invalid"))
      unsafeRequests.push(request.url());
  });
  await page.setViewportSize({ width: 1280, height: 1000 });
  await installChat(page);
  await expect(page.getByRole("separator")).toHaveCount(3);
  await expect(page.locator(".channel-separator button")).toHaveCount(0);
  await expect(page.getByText("[spacer", { exact: false })).toHaveCount(0);
  const icon = page.locator('[data-channel-id="lobby"] img');
  await expect
    .poll(() =>
      icon.evaluate((image) => (image as HTMLImageElement).naturalWidth),
    )
    .toBe(16);
  await expect(icon).toHaveAttribute("width", "18");
  await expect(
    page.locator(
      '[data-channel-id="broken"] img, [data-channel-id="unsafe"] img',
    ),
  ).toHaveCount(0);
  expect(unsafeRequests).toEqual([]);
  await control(page, "receive", "频道讨论已经开始。");
  await expect(page.locator(".message-received")).toBeVisible();
  await page.screenshot({
    path: testInfo.outputPath("chat-icons-desktop.png"),
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: testInfo.outputPath("chat-mobile.png") });
  await page.getByRole("button", { name: "展开侧栏" }).click();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("chat-icons-mobile-sidebar.png"),
  });
});
