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
