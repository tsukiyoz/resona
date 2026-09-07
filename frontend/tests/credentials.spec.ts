import { expect, test, type Page } from "@playwright/test";

interface CredentialControl {
  calls: {
    status: string[];
    saved: string[];
    password: [string, string, boolean][];
    forget: string[];
    disconnect: number;
  };
  statusError: boolean;
  preflightError: boolean;
  fail(): void;
  saveError(): void;
}
async function installCredentialBridge(page: Page) {
  await page.addInitScript(() => {
    const servers = [
      { id: "a", name: "已记住服务器", address: "a.invalid", nickname: "Test" },
      { id: "b", name: "新服务器", address: "b.invalid", nickname: "Test" },
      { id: "c", name: "无密码服务器", address: "c.invalid", nickname: "Test" },
    ];
    const credentials = new Map<string, string>([
      ["a", "test-saved-secret"],
      ["c", ""],
    ]);
    const preferences = new Map<string, boolean>();
    const state = {
      servers,
      session: {
        mode: "offline",
        serverID: "",
        serverName: "",
        channelID: "",
        error: "",
        credentialError: "",
      },
      channels: [],
      users: [],
      messages: [],
      notifications: [],
    };
    const control: CredentialControl = {
      calls: { status: [], saved: [], password: [], forget: [], disconnect: 0 },
      statusError: false,
      preflightError: false,
      fail() {
        state.session.mode = "failed";
        state.session.error = "服务器密码错误";
      },
      saveError() {
        state.session.credentialError = "密码未能保存到钥匙串";
      },
    };
    const snapshot = () => structuredClone(state);
    const connect = (id: string) => {
      state.session = {
        ...state.session,
        mode: "connected",
        serverID: id,
        serverName: servers.find((s) => s.id === id)!.name,
        error: "",
        credentialError: "",
      };
      return snapshot();
    };
    const app = {
      async GetWorkspace() {
        return snapshot();
      },
      async GetServerCredentialStatus(id: string) {
        control.calls.status.push(id);
        if (control.statusError) throw new Error("读取钥匙串失败");
        return {
          saved: credentials.has(id),
          remember: preferences.get(id) ?? true,
        };
      },
      async ConnectSavedServer(id: string) {
        control.calls.saved.push(id);
        return connect(id);
      },
      async ConnectServerWithPassword(
        id: string,
        password: string,
        remember: boolean,
      ) {
        control.calls.password.push([id, password, remember]);
        if (control.preflightError && remember)
          throw new Error("保存密码偏好失败");
        preferences.set(id, remember);
        if (remember) credentials.set(id, password);
        else credentials.delete(id);
        return connect(id);
      },
      async ForgetServerPassword(id: string) {
        control.calls.forget.push(id);
        credentials.delete(id);
        return snapshot();
      },
      async DisconnectServer() {
        control.calls.disconnect += 1;
        state.session.mode = "offline";
        return snapshot();
      },
    };
    Object.assign(window, {
      credentialControl: control,
      go: { main: { App: app } },
    });
  });
  await page.goto("/");
  await expect(
    page.getByRole("button", { name: "已记住服务器 未连接" }),
  ).toBeVisible();
}
async function calls(page: Page) {
  return page.evaluate(
    () =>
      (window as unknown as { credentialControl: CredentialControl })
        .credentialControl.calls,
  );
}

test("single click selects, double click reuses credentials, and cancelling a switch preserves the current connection", async ({
  page,
}) => {
  await installCredentialBridge(page);
  const saved = page.getByRole("button", { name: "已记住服务器 未连接" });
  await saved.click();
  expect((await calls(page)).saved).toEqual([]);
  await saved.dblclick();
  await expect(page.locator(".connection-label")).toHaveText(
    "已记住服务器 · 在线",
  );
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("button", { name: "已记住服务器 在线" }).dblclick();
  expect((await calls(page)).saved).toEqual(["a"]);
  await page.getByRole("button", { name: "新服务器 未连接" }).dblclick();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page.getByLabel("记住密码", { exact: true })).toBeChecked();
  await expect(page.getByLabel("服务器密码（可选）")).toHaveValue("");
  await page.getByRole("button", { name: "取消", exact: true }).click();
  await expect(page.locator(".connection-label")).toHaveText(
    "已记住服务器 · 在线",
  );
  expect((await calls(page)).disconnect).toBe(0);
  await page.getByRole("button", { name: "新服务器 未连接" }).press("Enter");
  await page.getByLabel("服务器密码（可选）").fill("single-use-secret");
  await page.getByLabel("记住密码", { exact: true }).uncheck();
  await page.getByRole("button", { name: "连接服务器", exact: true }).click();
  await expect(page.locator(".connection-label")).toHaveText("新服务器 · 在线");
  expect((await calls(page)).password).toEqual([
    ["b", "single-use-secret", false],
  ]);
  expect(await page.evaluate(() => JSON.stringify(localStorage))).not.toContain(
    "secret",
  );
});

test("remembered empty passwords connect directly and failed authentication offers password replacement", async ({
  page,
}) => {
  await installCredentialBridge(page);
  await page.getByRole("button", { name: "无密码服务器 未连接" }).dblclick();
  await expect(page.locator(".connection-label")).toHaveText(
    "无密码服务器 · 在线",
  );
  expect((await calls(page)).saved).toEqual(["c"]);
  await page.evaluate(() =>
    (
      window as unknown as { credentialControl: CredentialControl }
    ).credentialControl.fail(),
  );
  await page.getByRole("button", { name: "修改密码", exact: true }).click();
  await expect(page.getByLabel("服务器密码（可选）")).toHaveValue("");
  await page.getByLabel("服务器密码（可选）").fill("replacement");
  await page.getByRole("button", { name: "连接服务器", exact: true }).click();
  expect((await calls(page)).password).toEqual([["c", "replacement", true]]);
  await page.evaluate(() =>
    (
      window as unknown as { credentialControl: CredentialControl }
    ).credentialControl.saveError(),
  );
  await expect(page.getByRole("alert")).toHaveText("密码未能保存到钥匙串");
  await expect(page.locator(".connection-label")).toHaveText(
    "无密码服务器 · 在线",
  );
});

test("credential lookup errors preserve the old session and allow an unremembered connection", async ({
  page,
}) => {
  await installCredentialBridge(page);
  await page.getByRole("button", { name: "已记住服务器 未连接" }).dblclick();
  await page.evaluate(() => {
    (
      window as unknown as { credentialControl: CredentialControl }
    ).credentialControl.statusError = true;
  });
  await page.getByRole("button", { name: "新服务器 未连接" }).dblclick();
  await expect(page.getByRole("dialog").getByRole("alert")).toHaveText(
    "读取钥匙串失败",
  );
  await expect(page.locator(".connection-label")).toHaveText(
    "已记住服务器 · 在线",
  );
  await page.getByLabel("记住密码", { exact: true }).uncheck();
  await page.getByRole("button", { name: "连接服务器", exact: true }).click();
  expect((await calls(page)).password).toEqual([["b", "", false]]);
  expect((await calls(page)).disconnect).toBe(0);
});

test("forgetting saved credentials makes the next activation request a password", async ({
  page,
}) => {
  await installCredentialBridge(page);
  await page.getByRole("button", { name: "已记住服务器 未连接" }).click();
  await page.getByRole("button", { name: "编辑 已记住服务器" }).click();
  await page.getByRole("button", { name: "忘记已保存密码" }).click();
  expect((await calls(page)).forget).toEqual(["a"]);
  await page.getByRole("button", { name: "取消", exact: true }).click();
  await page.getByRole("button", { name: "已记住服务器 未连接" }).dblclick();
  await expect(page.getByRole("dialog")).toBeVisible();
  expect((await calls(page)).saved).toEqual([]);
});

test("credential preparation failures retain the entered password for a single-use retry", async ({
  page,
}) => {
  await installCredentialBridge(page);
  await page.getByRole("button", { name: "已记住服务器 未连接" }).dblclick();
  await page.evaluate(() => {
    (
      window as unknown as { credentialControl: CredentialControl }
    ).credentialControl.preflightError = true;
  });
  await page.getByRole("button", { name: "新服务器 未连接" }).dblclick();
  await page.getByLabel("服务器密码（可选）").fill("retained-secret");
  await page.getByRole("button", { name: "连接服务器", exact: true }).click();
  await expect(page.getByRole("dialog").getByRole("alert")).toHaveText(
    "保存密码偏好失败",
  );
  await expect(page.getByLabel("服务器密码（可选）")).toHaveValue(
    "retained-secret",
  );
  await expect(page.locator(".connection-label")).toHaveText(
    "已记住服务器 · 在线",
  );
  await page.getByLabel("记住密码", { exact: true }).uncheck();
  await page.getByRole("button", { name: "连接服务器", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect((await calls(page)).password).toEqual([
    ["b", "retained-secret", true],
    ["b", "retained-secret", false],
  ]);
  expect(await page.evaluate(() => JSON.stringify(localStorage))).not.toContain(
    "retained-secret",
  );
});
