import { expect, test, type Page } from "@playwright/test";

async function installLongWorkspace(page: Page) {
  await page.addInitScript(() => {
    const channels = Array.from({ length: 80 }, (_, index) => ({
      id: `channel-${index}`,
      name: `频道 ${index + 1}`,
      description: "",
      parentID: index > 0 && index < 5 ? "channel-0" : "",
      order: index === 0 || index === 1 ? "0" : `channel-${index - 1}`,
      members: 0,
      passwordRequired: false,
    }));
    channels.push({
      id: "orphan",
      name: "独立频道",
      description: "",
      parentID: "unknown",
      order: "0",
      members: 0,
      passwordRequired: false,
    });
    const state = {
      servers: [],
      session: {
        mode: "connected",
        serverID: "test",
        serverName: "Long server",
        channelID: "channel-0",
        nickname: "本人",
        selfID: "self",
        memberSyncState: "ready",
        memberSyncError: "",
      },
      channels,
      users: [
        { id: "self", nickname: "本人", channelID: "channel-0", self: true },
        {
          id: "peer",
          nickname: "同频道用户",
          channelID: "channel-0",
          self: false,
        },
        {
          id: "other",
          nickname: "其他频道用户",
          channelID: "channel-1",
          self: false,
        },
        {
          id: "long",
          nickname: "很长的成员昵称用于检查窄窗口展示是否溢出",
          channelID: "channel-1",
          self: false,
        },
        {
          id: "last",
          nickname: "末尾频道用户",
          channelID: "channel-79",
          self: false,
        },
      ],
      messages: [],
      notifications: [],
    };
    Object.assign(window, {
      sidebarControl: {
        move(channelID: string) {
          state.session.channelID = channelID;
        },
        setSync(mode: string, error = "") {
          state.session.memberSyncState = mode;
          state.session.memberSyncError = error;
          state.users = [];
        },
      },
      go: {
        main: { App: { GetWorkspace: async () => structuredClone(state) } },
      },
    });
  });
  await page.goto("/");
  await expect(page.locator(".channel-row")).toHaveCount(81);
}

test("all channel rows and their visible members render, while details stay scoped to the current channel", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await installLongWorkspace(page);
  const current = page.getByRole("list", {
    name: "频道 1的可见成员",
    exact: true,
  });
  await expect(current.getByRole("listitem")).toHaveCount(2);
  await expect(current.getByText("本人", { exact: true })).toBeVisible();
  await expect(
    page
      .getByRole("list", { name: "频道 2的可见成员", exact: true })
      .getByText("其他频道用户", { exact: true }),
  ).toBeVisible();
  await expect(
    page.locator(".details-panel").getByText("同频道用户", { exact: true }),
  ).toBeVisible();
  await expect(
    page.locator(".details-panel").getByText("其他频道用户", { exact: true }),
  ).toHaveCount(0);
  await expect(page.locator('[data-channel-id="orphan"]')).toHaveCount(1);
  const sidebar = page.locator(".sidebar");
  expect((await sidebar.boundingBox())!.height).toBe(720);
  expect((await page.locator(".identity").boundingBox())!.y).toBeLessThan(720);
  await page.screenshot({
    path: testInfo.outputPath("channel-members-desktop.png"),
  });
  await page.setViewportSize({ width: 1000, height: 650 });
  await expect(page.locator(".details-panel")).toBeHidden();
  await expect(current.getByText("同频道用户", { exact: true })).toBeVisible();
});

test("a confirmed channel move reveals the active row without stealing later manual scrolling", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await installLongWorkspace(page);
  const scroller = page.locator(".sidebar-content");
  await page.evaluate(() =>
    (
      window as unknown as { sidebarControl: { move(channelID: string): void } }
    ).sidebarControl.move("channel-79"),
  );
  await expect(
    page.locator('[data-channel-id="channel-79"] .channel-row'),
  ).toHaveAttribute("aria-current", "true");
  const bounds = (await scroller.boundingBox())!;
  const row = (await page
    .locator('[data-channel-id="channel-79"] .channel-row')
    .boundingBox())!;
  expect(
    row.y >= bounds.y && row.y + row.height <= bounds.y + bounds.height + 1,
  ).toBe(true);
  await scroller.hover();
  await page.mouse.wheel(0, -10000);
  await expect
    .poll(() => scroller.evaluate((element) => element.scrollTop))
    .toBe(0);
  await page.waitForTimeout(1200);
  expect(await scroller.evaluate((element) => element.scrollTop)).toBe(0);
});

test("pending and permission-limited member snapshots do not claim an empty channel", async ({
  page,
}) => {
  await installLongWorkspace(page);
  await page.evaluate(() =>
    (
      window as unknown as {
        sidebarControl: { setSync(mode: string, error?: string): void };
      }
    ).sidebarControl.setSync("pending"),
  );
  await expect(page.locator(".members-heading")).toContainText("同步中");
  await expect(
    page.getByText("正在同步可见成员…", { exact: true }),
  ).toBeVisible();
  await page.evaluate(() =>
    (
      window as unknown as {
        sidebarControl: { setSync(mode: string, error?: string): void };
      }
    ).sidebarControl.setSync("limited", "成员同步权限不足"),
  );
  await expect(page.locator(".members-heading")).toContainText("受限");
  await expect(page.locator(".sync-banner")).toHaveText("成员同步权限不足");
  await expect(
    page.getByText("当前频道暂无可见成员", { exact: true }),
  ).toHaveCount(0);
});

for (const viewport of [
  { width: 1280, height: 720 },
  { width: 390, height: 844 },
]) {
  test(`long channel tree scrolls inside its sidebar at ${viewport.width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize(viewport);
    await installLongWorkspace(page);
    if (viewport.width < 680)
      await page.getByRole("button", { name: "展开侧栏" }).click();
    const scroller = page.locator(".sidebar-content");
    const bounds = (await scroller.boundingBox())!;
    expect(
      await scroller.evaluate(
        (element) => element.scrollHeight > element.clientHeight,
      ),
    ).toBe(true);
    await scroller.hover();
    await page.mouse.wheel(0, 10000);
    await expect
      .poll(() => scroller.evaluate((element) => element.scrollTop))
      .toBeGreaterThan(0);
    const last = page.locator('[data-channel-id="channel-79"]');
    await expect
      .poll(async () => {
        const row = (await last.boundingBox())!;
        return (
          row.y >= bounds.y && row.y + row.height <= bounds.y + bounds.height
        );
      })
      .toBe(true);
    await expect(last.getByText("末尾频道用户", { exact: true })).toBeVisible();
    expect(
      await page.evaluate(
        () =>
          document.documentElement.scrollHeight <= innerHeight &&
          document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`channel-list-bottom-${viewport.width}.png`),
    });
    await page.mouse.wheel(0, -10000);
    await expect
      .poll(() => scroller.evaluate((element) => element.scrollTop))
      .toBe(0);
    await expect(
      page.getByRole("list", { name: "频道 1的可见成员", exact: true }),
    ).toBeVisible();
  });
}
