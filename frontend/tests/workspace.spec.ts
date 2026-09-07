import { expect, test } from "@playwright/test";

test("bookmarks persist and preview messages stay local", async ({ page }) => {
  await page.goto("/");
  await expect(
    page.getByRole("textbox", { name: "消息", exact: true }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "新建服务器书签", exact: true })
    .click();
  await page.getByLabel("服务器名称").fill("我的 TS3");
  await page.getByLabel("服务器地址").fill("localhost:9987");
  await page.getByRole("button", { name: "保存书签" }).click();
  await expect(
    page.getByRole("button", { name: "我的 TS3 未连接" }),
  ).toBeVisible();
  await page.reload();
  await page.getByRole("button", { name: "我的 TS3 未连接" }).click();
  await expect(
    page.getByRole("button", { name: "连接 我的 TS3" }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "连接 我的 TS3" }),
  ).toHaveAttribute("title", "在桌面应用中连接");
  await page.getByRole("button", { name: "编辑 我的 TS3" }).click();
  await page.getByLabel("服务器名称").fill("更名后的服务器");
  await page.getByRole("button", { name: "保存书签" }).click();
  await expect(
    page.getByRole("button", { name: "更名后的服务器 未连接" }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "本地预览", exact: true })
    .last()
    .click();
  await expect(page.getByText("本地预览 · 未连接远程服务器")).toBeVisible();
  await page
    .getByRole("textbox", { name: "消息", exact: true })
    .fill("这是一条本地消息");
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  await expect(
    page.getByText("这是一条本地消息", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "音乐 0" }).click();
  await expect(page.getByText("这是一条本地消息", { exact: true })).toHaveCount(
    0,
  );
  await page.getByRole("button", { name: "结束预览", exact: true }).click();
  await expect(
    page.getByRole("textbox", { name: "消息", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "删除 更名后的服务器" }).click();
  await page.getByRole("button", { name: "删除书签", exact: true }).click();
  await expect(page.getByText("暂无服务器书签")).toBeVisible();
});

async function installDesktopBridge(
  page: import("@playwright/test").Page,
  shouldFail = false,
) {
  await page.addInitScript((fail) => {
    const server = {
      id: "desktop-server",
      name: "桌面测试",
      address: "test.invalid:9987",
      nickname: "DesktopUser",
    };
    const emptySession = () => ({
      mode: "offline",
      channelID: "",
      nickname: "DesktopUser",
      serverID: "",
      serverName: "",
      identityUID: "",
      selfID: "",
      error: "",
    });
    let state = {
      servers: [server],
      session: emptySession(),
      channels: [] as Array<Record<string, unknown>>,
      users: [] as Array<Record<string, unknown>>,
      messages: [] as Array<Record<string, unknown>>,
    };
    let connectingPolls = 0;
    const calls = {
      connect: [] as Array<[string, string]>,
      disconnect: 0,
    };
    const snapshot = () => structuredClone(state);
    const app = {
      async GetWorkspace() {
        if (state.session.mode === "connecting") {
          connectingPolls += 1;
          if (connectingPolls >= 2) {
            state = fail
              ? {
                  ...state,
                  session: {
                    ...state.session,
                    mode: "failed",
                    error: "测试服务器拒绝连接",
                  },
                }
              : {
                  ...state,
                  session: {
                    ...state.session,
                    mode: "connected",
                    channelID: "lobby",
                    identityUID: "identity-test-uid",
                    selfID: "42",
                  },
                  channels: [
                    {
                      id: "lobby",
                      name: "大厅",
                      description: "远程频道主题",
                      members: 99,
                      parentID: "",
                      order: "0",
                    },
                    {
                      id: "workshop",
                      name: "工作间",
                      description: "",
                      members: 99,
                      parentID: "",
                      order: "lobby",
                    },
                    {
                      id: "nested",
                      name: "子频道",
                      description: "",
                      members: 99,
                      parentID: "lobby",
                      order: "0",
                    },
                  ],
                  users: [
                    {
                      id: "42",
                      nickname: "DesktopUser",
                      channelID: "lobby",
                      self: true,
                    },
                    {
                      id: "84",
                      nickname: "RemoteUser",
                      channelID: "lobby",
                      self: false,
                    },
                  ],
                };
          }
        } else if (state.session.mode === "disconnecting") {
          state = {
            ...state,
            session: emptySession(),
            channels: [],
            users: [],
          };
        }
        return snapshot();
      },
      async ConnectServer(id: string, password: string) {
        calls.connect.push([id, password]);
        connectingPolls = 0;
        state = {
          ...state,
          session: {
            ...emptySession(),
            mode: "connecting",
            serverID: id,
            serverName: server.name,
          },
          channels: [],
          users: [],
        };
        return snapshot();
      },
      async DisconnectServer() {
        calls.disconnect += 1;
        state = {
          ...state,
          session: { ...state.session, mode: "disconnecting" },
        };
        return snapshot();
      },
      async SaveServer() {
        return snapshot();
      },
      async DeleteServer() {
        return snapshot();
      },
      async OpenPreview() {
        return snapshot();
      },
      async LeavePreview() {
        return snapshot();
      },
      async SelectChannel() {
        return snapshot();
      },
      async SendMessage() {
        return snapshot();
      },
    };
    Object.assign(window, {
      go: { main: { App: app } },
      desktopBridgeCalls: calls,
    });
  }, shouldFail);
}

test("desktop connection passes an ephemeral password and polls until disconnect", async ({
  page,
}) => {
  await installDesktopBridge(page);
  await page.goto("/");
  await page.getByRole("button", { name: "桌面测试 未连接" }).click();
  await page.getByRole("button", { name: "连接 桌面测试" }).click();
  await page.getByLabel("服务器密码（可选）").fill("ephemeral-secret");
  await page.getByRole("button", { name: "连接服务器" }).click();

  await expect(page.getByRole("heading", { name: "正在连接" })).toBeVisible();
  await expect(page.locator(".connection-label")).toHaveText(
    "桌面测试 · 在线",
    {
      timeout: 4000,
    },
  );
  await expect(page.getByText("RemoteUser", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("textbox", { name: "消息", exact: true }),
  ).toHaveAttribute("placeholder", "文字发送尚未开放");
  await expect(
    page.getByRole("textbox", { name: "消息", exact: true }),
  ).toBeDisabled();
  await expect(page.getByRole("button", { name: "大厅" })).toHaveAttribute(
    "title",
    "频道切换暂未开放",
  );
  await expect(page.locator(".channel-row")).toHaveText([
    "大厅",
    "子频道",
    "工作间",
  ]);
  await expect(page.locator(".channel-list")).not.toContainText("99");

  const passwordHandling = await page.evaluate(() => ({
    calls: (
      window as unknown as {
        desktopBridgeCalls: { connect: Array<[string, string]> };
      }
    ).desktopBridgeCalls.connect,
    storage: Object.keys(localStorage).map((key) => localStorage.getItem(key)),
  }));
  expect(passwordHandling.calls).toEqual([
    ["desktop-server", "ephemeral-secret"],
  ]);
  expect(JSON.stringify(passwordHandling.storage)).not.toContain(
    "ephemeral-secret",
  );

  await page.getByRole("button", { name: "断开 桌面测试" }).click();
  await expect(page.getByRole("heading", { name: "正在断开" })).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "还没有加入会话" }),
  ).toBeVisible({
    timeout: 3000,
  });
  expect(
    await page.evaluate(
      () =>
        (
          window as unknown as {
            desktopBridgeCalls: { disconnect: number };
          }
        ).desktopBridgeCalls.disconnect,
    ),
  ).toBe(1);
});

test("desktop connection failure stays visible and can be retried", async ({
  page,
}) => {
  await installDesktopBridge(page, true);
  await page.goto("/");
  await page.getByRole("button", { name: "桌面测试 未连接" }).click();
  await page.getByRole("button", { name: "连接 桌面测试" }).click();
  await page.getByRole("button", { name: "连接服务器" }).click();

  await expect(page.getByRole("heading", { name: "连接失败" })).toBeVisible({
    timeout: 4000,
  });
  await expect(page.getByRole("alert")).toContainText("测试服务器拒绝连接");
  await expect(
    page.getByRole("button", { name: "重新连接", exact: true }),
  ).toBeEnabled();
});

test("appearance persists and mobile navigation opens", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("button", { name: "深色", exact: true }).click();
  await page.getByRole("checkbox", { name: "紧凑文字" }).check();
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator("html")).toHaveAttribute("data-compact", "true");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "展开侧栏" }).click();
  await expect(
    page.getByRole("button", { name: "新建服务器书签" }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
});

test("corrupt bookmarks are retained and loading can be retried", async ({
  page,
}) => {
  await page.addInitScript(() =>
    localStorage.setItem("resona.browser.workspace.v1", "{broken"),
  );
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "加载失败" })).toBeVisible();
  expect(
    await page.evaluate(() =>
      localStorage.getItem("resona.browser.workspace.v1"),
    ),
  ).toBe("{broken");
  await page.evaluate(() =>
    localStorage.setItem("resona.browser.workspace.v1", '{"servers":[]}'),
  );
  await page.getByRole("button", { name: "重试", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "还没有加入会话" }),
  ).toBeVisible();
});

test("failed bookmark writes do not partially commit or block preview", async ({
  page,
}) => {
  await page.addInitScript(() => {
    const original = Storage.prototype.setItem;
    Object.assign(window, { bookmarkWritesFail: true });
    Storage.prototype.setItem = function (key, value) {
      if (
        key === "resona.browser.workspace.v1" &&
        (window as unknown as { bookmarkWritesFail: boolean })
          .bookmarkWritesFail
      )
        throw new DOMException("Storage is full", "QuotaExceededError");
      return original.call(this, key, value);
    };
  });
  await page.goto("/");
  await page
    .getByRole("button", { name: "新建服务器书签", exact: true })
    .click();
  await page.getByLabel("服务器名称").fill("保存重试");
  await page.getByLabel("服务器地址").fill("localhost:9987");
  await page.getByRole("button", { name: "保存书签" }).click();
  await expect(page.getByRole("dialog").getByRole("alert")).toContainText(
    "Storage is full",
  );
  await page.evaluate(() =>
    Object.assign(window, { bookmarkWritesFail: false }),
  );
  await page.getByRole("button", { name: "保存书签" }).click();
  await expect(
    page.getByRole("button", { name: "保存重试 未连接" }),
  ).toHaveCount(1);
  await page.getByRole("button", { name: "保存重试 未连接" }).click();
  await page.evaluate(() =>
    Object.assign(window, { bookmarkWritesFail: true }),
  );
  await page.getByRole("button", { name: "删除 保存重试" }).click();
  await page.getByRole("button", { name: "删除书签", exact: true }).click();
  await expect(page.getByRole("dialog").getByRole("alert")).toContainText(
    "Storage is full",
  );
  await page.getByRole("button", { name: "取消", exact: true }).click();
  await page
    .getByRole("button", { name: "本地预览", exact: true })
    .last()
    .click();
  await expect(
    page.getByRole("button", { name: "保存重试 未连接" }),
  ).toHaveCount(1);
  await page
    .getByRole("textbox", { name: "消息", exact: true })
    .fill("存储不可写时的本地消息");
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  await expect(
    page.getByText("存储不可写时的本地消息", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "结束预览", exact: true }).click();
  await page.evaluate(() =>
    Object.assign(window, { bookmarkWritesFail: false }),
  );
  await page.getByRole("button", { name: "删除 保存重试" }).click();
  await page.getByRole("button", { name: "删除书签", exact: true }).click();
  await expect(page.getByText("暂无服务器书签")).toBeVisible();
});
