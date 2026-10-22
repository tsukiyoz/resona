const $ = (selector) => document.querySelector(selector);
const base = "/api/admin/v1";
const roleNames = { owner: "所有者", admin: "管理员", member: "成员" };
const esc = (value) =>
  String(value ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const icon = (name) =>
  `<svg aria-hidden="true"><use href="icons.svg#${name}"></use></svg>`;
const tool = (name, title, action, id, disabled = false) =>
  `<button class="icon" title="${esc(title)}" aria-label="${esc(title)}" data-action="${action}" data-id="${esc(id)}" ${disabled ? "disabled" : ""}>${icon(name)}</button>`;
const command = (text, action, id, danger = false) =>
  `<button data-action="${action}" data-id="${esc(id)}" class="${danger ? "danger" : ""}">${esc(text)}</button>`;
const date = (value) =>
  new Date(value).toLocaleString("zh-CN", { hour12: false });
let snapshot = null,
  session = null,
  view = "channels",
  live = false,
  pending = false;
let stream = null,
  reconnect = null,
  retry = 0,
  generation = 0;
let editor = null,
  settingsDraft = null,
  settingsRevision = 0;
let operationEpoch = 0;
let loggingIn = false,
  loggingOut = false;
const requests = new Set();

async function api(path, method = "GET", body) {
  const controller = new AbortController(),
    current = generation;
  const timeout = setTimeout(() => controller.abort(), 8000);
  requests.add(controller);
  try {
    const response = await fetch(base + path, {
      method,
      credentials: "same-origin",
      signal: controller.signal,
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": session?.csrf ?? "",
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const result = await response.json();
    if (current !== generation)
      throw new DOMException("Cancelled", "AbortError");
    if (!response.ok) {
      if (response.status === 401 && path !== "/login" && session)
        reset(result.error);
      throw Object.assign(new Error(result.error || "请求失败"), {
        status: response.status,
      });
    }
    return result;
  } catch (error) {
    if (error.name === "AbortError")
      throw new Error(
        method === "GET"
          ? "读取状态失败，请检查连接后重试"
          : "操作结果尚未确认，请刷新状态后核对",
      );
    if (error instanceof TypeError)
      throw new Error(
        method === "GET"
          ? "无法连接管理服务，请检查网络"
          : "连接中断，操作结果尚未确认，请刷新后核对",
      );
    throw error;
  } finally {
    clearTimeout(timeout);
    requests.delete(controller);
  }
}

function notice(message, error = false) {
  $("#notice").textContent = message;
  $("#notice").className = error ? "error" : "";
  $("#notice").hidden = !message;
}

function connected(value) {
  live = value;
  $("#connection-status").textContent = value ? "已同步" : "连接中断，正在恢复";
  $("#connection-status").className = value ? "ready" : "offline";
  setDisabled();
}

function setDisabled() {
  document
    .querySelectorAll("[data-action], #save-settings, #save-editor")
    .forEach((button) => {
      button.disabled = pending || !live || button.dataset.locked === "true";
    });
  const form = $("#settings-form");
  if (form)
    form
      .querySelectorAll("input")
      .forEach(
        (input) =>
          (input.disabled =
            pending || !live || !snapshot.capabilities.settings),
      );
  $("#cancel-editor").disabled = false;
  $("#close-editor").disabled = false;
  if (pending) $("#cancel-editor").textContent = "取消等待";
}

function reset(message = "") {
  generation++;
  operationEpoch++;
  stream?.close();
  stream = null;
  clearTimeout(reconnect);
  reconnect = null;
  requests.forEach((request) => request.abort());
  snapshot = session = settingsDraft = editor = null;
  live = pending = false;
  if ($("#editor").open) $("#editor").close();
  $("#view").replaceChildren();
  $("#workspace").hidden = true;
  $("#login-screen").hidden = false;
  $("#token").value = "";
  $("#login-error").textContent = message;
  $("#login-form button").disabled = loggingOut;
  $("#login-form button").textContent = "登录";
}

async function refresh() {
  const state = await api("/state");
  accept(state);
  return state;
}

function accept(state) {
  snapshot = state;
  $("#server-name").textContent = state.name;
  $("#member-count").textContent = state.members.length;
  $("#role-label").textContent = roleNames[state.role];
  if (editor && !state.capabilities[editor.capability]) {
    $("#editor").close();
    editor = null;
    notice("权限已改变，当前操作已关闭", true);
  }
  render();
}

async function subscribe() {
  const current = generation;
  clearTimeout(reconnect);
  stream?.close();
  stream = new EventSource(base + "/events");
  stream.addEventListener("state", (event) => {
    if (current !== generation) return;
    try {
      accept(JSON.parse(event.data));
      retry = 0;
      connected(true);
    } catch {
      stream.onerror();
    }
  });
  stream.addEventListener("expired", (event) => {
    if (current === generation) reset(JSON.parse(event.data).error);
  });
  stream.onerror = () => {
    if (current !== generation) return;
    stream.close();
    connected(false);
    reconnect = setTimeout(
      async () => {
        if (current !== generation) return;
        try {
          session = await api("/session");
          await refresh();
        } catch (error) {
          if (!session) return;
          notice(error.message, true);
        }
        if (current === generation && session) subscribe();
      },
      Math.min(1000 * 2 ** Math.min(retry++, 5), 30000),
    );
  };
}

async function enter() {
  $("#login-screen").hidden = true;
  $("#workspace").hidden = false;
  await refresh();
  subscribe();
}

function render() {
  if (!snapshot) return;
  const titles = {
    channels: ["频道", "频道顺序、音质与描述"],
    members: [
      "在线成员",
      `${snapshot.members.length} 人在线 · 上限 ${snapshot.max_clients} 人`,
    ],
    access: ["身份与权限", "管理员、封禁与登录凭据"],
    settings: ["服务器设置", "在线配置与消息保留"],
  };
  $("#view-title").textContent = titles[view][0];
  $("#view-subtitle").textContent = titles[view][1];
  document.querySelectorAll("nav button").forEach((button) => {
    button.classList.toggle("active", button.dataset.view === view);
    button.setAttribute(
      "aria-current",
      button.dataset.view === view ? "page" : "false",
    );
  });
  if (view === "channels") renderChannels();
  if (view === "members") renderMembers();
  if (view === "access") renderAccess();
  if (view === "settings" && (!$("#settings-form") || !settingsDraft))
    renderSettings();
  setDisabled();
}

function table(headers, rows, empty) {
  return `<div class="table-wrap"><table><thead><tr>${headers.map((h) => `<th>${h}</th>`).join("")}</tr></thead><tbody>${rows || `<tr><td class="empty" colspan="${headers.length}">${esc(empty)}</td></tr>`}</tbody></table></div>`;
}

function renderChannels() {
  const channels = snapshot.channels;
  $("#view").innerHTML =
    `<div class="toolbar"><span class="summary">${channels.length} 个频道</span>${snapshot.capabilities.channels ? `<button class="primary" data-action="create-channel">${icon("plus")}新建频道</button>` : ""}</div>` +
    table(
      ["频道", "音质", "在线", ""],
      channels
        .map(
          (ch, index) =>
            `<tr><td class="name"><strong>${esc(ch.name)}</strong>${ch.id === snapshot.default_channel ? '<span class="tag">默认</span>' : ""}<span class="description">${esc(ch.description || "—")}</span></td><td>${(ch.bitrate || 48000) / 1000} kbps</td><td>${snapshot.members.filter((m) => m.channel === ch.id).length}</td><td><div class="actions">${tool("up", "上移", "up", ch.id, index === 0)}${tool("down", "下移", "down", ch.id, index === channels.length - 1)}${tool("edit", "编辑频道", "edit-channel", ch.id)}${tool("trash", "删除频道", "delete-channel", ch.id, ch.id === snapshot.default_channel)}</div></td></tr>`,
        )
        .join(""),
      "暂无频道",
    );
  $("#view")
    .querySelectorAll("button:disabled")
    .forEach((button) => (button.dataset.locked = "true"));
  if (!snapshot.capabilities.channels)
    $("#view")
      .querySelectorAll("[data-action]")
      .forEach((button) => (button.dataset.locked = "true"));
}

function canModerate(member) {
  return (
    snapshot.capabilities.members &&
    member.role !== "owner" &&
    (snapshot.role === "owner" || member.role === "member")
  );
}

function renderMembers() {
  $("#view").innerHTML = table(
    ["成员", "频道", "状态", ""],
    snapshot.members
      .map(
        (m) =>
          `<tr><td class="name"><strong>${esc(m.nickname)}</strong><span class="tag">${roleNames[m.role]}</span><div class="identity" title="${esc(m.identity)}">${esc(m.identity.slice(0, 10))}…${esc(m.identity.slice(-6))} ${tool("copy", "复制身份", "copy-identity", m.identity)}</div></td><td>${esc(snapshot.channels.find((ch) => ch.id === m.channel)?.name || m.channel)}</td><td>${m.muted ? "麦克风静音" : "麦克风开启"}</td><td><div class="actions">${canModerate(m) ? command("移动", "move", m.id) + command("踢出", "kick", m.id) + command("封禁", "ban-member", m.id, true) : ""}</div></td></tr>`,
      )
      .join(""),
    "当前没有在线成员",
  );
}

function renderAccess() {
  const identities = [
    { identity: snapshot.owner, role: "owner" },
    ...(snapshot.admins || []).map((identity) => ({ identity, role: "admin" })),
  ];
  let html = `<div class="section-heading"><h3>管理身份</h3>${snapshot.capabilities.roles ? `<button data-action="add-admin">${icon("plus")}添加管理员</button>` : ""}</div>`;
  html += table(
    ["身份", "角色", ""],
    identities
      .filter((v) => v.identity)
      .map(
        (v) =>
          `<tr><td class="identity">${esc(v.identity)}</td><td>${roleNames[v.role]}</td><td class="actions">${snapshot.capabilities.roles && v.role === "admin" ? command("撤销管理员", "revoke-admin", v.identity, true) : ""}</td></tr>`,
      )
      .join(""),
    "暂无管理身份",
  );
  html += `<section class="section"><div class="section-heading"><h3>封禁名单</h3>${snapshot.capabilities.members ? '<button data-action="add-ban">添加封禁</button>' : ""}</div>`;
  html +=
    table(
      ["身份 / 原因", "到期时间", ""],
      (snapshot.bans || [])
        .map(
          (b) =>
            `<tr><td><div class="identity">${esc(b.identity)}</div><small>${esc(b.reason)}</small></td><td>${date(b.expires)}${new Date(b.expires) < new Date() ? '<span class="tag">已过期</span>' : ""}</td><td class="actions">${snapshot.role === "owner" || !(snapshot.admins || []).includes(b.identity) ? command("解除", "unban", b.identity) : ""}</td></tr>`,
        )
        .join(""),
      "暂无封禁",
    ) + "</section>";
  if (snapshot.capabilities.tokens) {
    html +=
      '<section class="section"><div class="section-heading"><h3>登录凭据</h3><button data-action="create-credential">' +
      icon("plus") +
      "创建凭据</button></div>";
    html +=
      table(
        ["名称 / 身份", "到期时间", ""],
        (snapshot.credentials || [])
          .map(
            (c) =>
              `<tr><td><strong>${esc(c.label)}</strong><div class="identity">${esc(c.identity.slice(0, 12))}…</div></td><td>${date(c.expires)}</td><td class="actions">${command("撤销", "revoke-credential", c.id, true)}</td></tr>`,
          )
          .join(""),
        "暂无登录凭据",
      ) + "</section>";
  }
  $("#view").innerHTML = html;
}

function renderSettings() {
  const v = settingsDraft ?? snapshot.settings;
  if (!settingsDraft) settingsRevision = snapshot.revision;
  $("#view").innerHTML = `<form id="settings-form" class="settings-form">
      <fieldset><legend>基本设置</legend>
        <label for="setting-name">服务器名称</label>
        <input id="setting-name" name="name" required maxlength="100" value="${esc(v.name)}">
        <label for="setting-capacity">人数上限</label>
        <input id="setting-capacity" name="max_clients" type="number" min="1" max="64" required value="${v.max_clients}">
      </fieldset>
      <fieldset><legend>近期消息保留</legend>
        <div class="field-grid">
          <div><label for="setting-messages">每频道最多消息数</label>
            <input id="setting-messages" name="chat_max_messages" type="number" min="1" max="10000" required value="${v.chat_max_messages}"></div>
          <div><label for="setting-bytes">全服消息容量（MiB）</label>
            <input id="setting-bytes" name="megabytes" type="number" min="0.015625" max="4096" step="any" required value="${v.chat_max_bytes / 1048576}"></div>
          <div><label for="setting-days">保留时长（天）</label>
            <input id="setting-days" name="days" type="number" min="0.000011574074074074073" max="365" step="any" required value="${v.chat_max_age_seconds / 86400}"></div>
        </div>
        <small>达到任一限制时淘汰最旧消息。缩小限制可能立即删除已有记录。</small>
      </fieldset>
      ${snapshot.capabilities.settings ? '<div class="form-footer"><button type="submit" class="primary" id="save-settings">保存更改</button><button type="button" id="reset-settings">还原</button></div>' : '<p class="muted section">仅所有者可修改服务器设置</p>'}
      <div id="settings-conflict" role="alert"></div>
      <section class="read-only"><h3>部署配置</h3><p>监听地址、服务器连接密码、Noise 私钥和存储目录由启动配置管理，需要在服务器上修改。</p></section>
    </form>`;
}

function readSettings() {
  const f = $("#settings-form");
  return {
    name: f.elements.name.value,
    max_clients: Number(f.elements.max_clients.value),
    chat_max_messages: Number(f.elements.chat_max_messages.value),
    chat_max_bytes: Math.round(Number(f.elements.megabytes.value) * 1048576),
    chat_max_age_seconds: Math.round(Number(f.elements.days.value) * 86400),
  };
}

function describeSettings(value) {
  return `${value.name} · 上限 ${value.max_clients} 人 · 每频道 ${value.chat_max_messages} 条 · ${(value.chat_max_bytes / 1048576).toFixed(3)} MiB · ${(value.chat_max_age_seconds / 86400).toFixed(3)} 天`;
}

const field = (label, name, value = "", extra = "") =>
  `<label for="edit-${name}">${esc(label)}</label><input id="edit-${name}" name="${name}" value="${esc(value)}" ${extra}>`;

function openEditor(
  title,
  fields,
  capability,
  submit,
  success = "已保存",
  latest = null,
) {
  editor = { capability, submit, success, latest };
  $("#editor-form").dataset.revision = snapshot.revision;
  $("#editor-title").textContent = title;
  $("#editor-fields").innerHTML = fields;
  $("#editor-error").textContent = "";
  $("#conflict").hidden = true;
  $("#save-editor").textContent = "保存";
  $("#save-editor").hidden = false;
  $("#cancel-editor").textContent = "取消";
  $("#editor").showModal();
  setDisabled();
}

function closeEditor() {
  if (pending) {
    operationEpoch++;
    requests.forEach((request) => request.abort());
    pending = false;
    notice("已取消等待，操作可能已在服务端生效，请刷新状态核对", true);
  }
  $("#editor").close();
  editor = null;
  setDisabled();
}

function confirmAction(title, text, capability, submit, success) {
  openEditor(title, `<p>${esc(text)}</p>`, capability, submit, success);
  $("#save-editor").textContent = "确认";
}

async function showConflict(error) {
  $("#editor-error").textContent = error.message;
  if (error.status !== 409) return;
  try {
    const current = await refresh();
    $("#conflict").replaceChildren();
    const text = document.createElement("p");
    text.textContent =
      "服务器最新配置：" +
      (editor?.latest?.(current) || "列表已刷新，请核对当前对象。");
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = "保留草稿，继续编辑";
    button.onclick = () => {
      $("#editor-form").dataset.revision = current.revision;
      $("#conflict").hidden = true;
      $("#editor-error").textContent = "请核对后再次保存";
    };
    $("#conflict").append(text, button);
    $("#conflict").hidden = false;
  } catch (failure) {
    notice(failure.message, true);
  }
}

async function action(name, id) {
  if (!snapshot || pending || !live) return;
  const ch = snapshot.channels.find((v) => v.id === Number(id));
  const member = snapshot.members.find((v) => v.id === Number(id));
  if (name === "copy-identity") {
    try {
      await navigator.clipboard.writeText(id);
      notice("身份已复制");
    } catch {
      notice("复制失败，请选中身份文字复制", true);
    }
    return;
  }
  if (name === "create-channel" || name === "edit-channel") {
    openEditor(
      ch ? "编辑频道" : "新建频道",
      field("频道名称", "name", ch?.name || "", 'required maxlength="100"') +
        `<label for="edit-description">描述</label><textarea id="edit-description" name="description" maxlength="1024">${esc(ch?.description || "")}</textarea>` +
        field(
          "音频码率（kbps）",
          "bitrate",
          (ch?.bitrate || 32000) / 1000,
          'type="number" min="16" max="64" required',
        ),
      "channels",
      (f, revision) =>
        api(ch ? `/channels/${ch.id}` : "/channels", ch ? "PATCH" : "POST", {
          revision,
          name: f.elements.name.value,
          description: f.elements.description.value,
          bitrate: Number(f.elements.bitrate.value) * 1000,
        }),
      "频道已保存",
      (current) => {
        const c = current.channels.find((v) => v.id === ch?.id);
        return c
          ? `${c.name} · ${c.bitrate / 1000} kbps · ${c.description}`
          : "频道列表已更新";
      },
    );
  } else if (name === "delete-channel" && ch) {
    confirmAction(
      "删除频道",
      `删除「${ch.name}」？频道内有成员时无法删除。`,
      "channels",
      (_, revision) => api(`/channels/${ch.id}`, "DELETE", { revision }),
      "频道已删除",
    );
  } else if ((name === "up" || name === "down") && ch) {
    const index = snapshot.channels.indexOf(ch);
    const before =
      name === "up"
        ? snapshot.channels[index - 1]?.id
        : snapshot.channels[index + 2]?.id || 0;
    if (before === undefined) return;
    const current = generation;
    pending = true;
    setDisabled();
    try {
      await api(`/channels/${ch.id}/order`, "POST", {
        revision: snapshot.revision,
        before_id: before,
      });
      await refresh();
      notice("频道顺序已更新");
    } catch (error) {
      if (current === generation) notice(error.message, true);
    } finally {
      if (current === generation) {
        pending = false;
        render();
      }
    }
  } else if (name === "move" && member) {
    openEditor(
      "移动成员",
      `<p>${esc(member.nickname)}</p><label for="edit-channel">目标频道</label><select id="edit-channel" name="channel">${snapshot.channels.map((c) => `<option value="${c.id}" ${c.id === member.channel ? "disabled" : ""}>${esc(c.name)}</option>`).join("")}</select>`,
      "members",
      (f, revision) =>
        api(`/members/${member.id}/move`, "POST", {
          revision,
          instance: member.instance,
          channel: Number(f.elements.channel.value),
        }),
      "成员已移动",
    );
  } else if (name === "kick" && member) {
    confirmAction(
      "踢出成员",
      `将「${member.nickname}」移出服务器？对方可以重新连接。身份 …${member.identity.slice(-8)}`,
      "members",
      (_, revision) =>
        api(`/members/${member.id}/kick`, "POST", {
          revision,
          instance: member.instance,
        }),
      "成员已移出",
    );
  } else if (name === "ban-member" || name === "add-ban") {
    openEditor(
      "封禁身份",
      field(
        "身份 UID",
        "identity",
        member?.identity || "",
        'required pattern="[0-9a-f]{64}" maxlength="64"',
      ) +
        field(
          "封禁时长（分钟）",
          "minutes",
          1440,
          'type="number" min="1" max="525600" required',
        ) +
        field("原因", "reason", "", 'maxlength="200"'),
      "members",
      (f, revision) =>
        api(`/bans/${f.elements.identity.value}`, "POST", {
          revision,
          minutes: Number(f.elements.minutes.value),
          reason: f.elements.reason.value,
        }),
      "身份已封禁",
    );
  } else if (name === "unban") {
    confirmAction(
      "解除封禁",
      `解除身份 …${id.slice(-8)} 的封禁？`,
      "members",
      (_, revision) => api(`/bans/${id}`, "DELETE", { revision }),
      "封禁已解除",
    );
  } else if (name === "add-admin") {
    openEditor(
      "添加管理员",
      field(
        "身份 UID",
        "identity",
        "",
        'required pattern="[0-9a-f]{64}" maxlength="64"',
      ),
      "roles",
      (f, revision) =>
        api(`/roles/${f.elements.identity.value}`, "PUT", {
          revision,
          role: "admin",
        }),
      "管理员已添加",
    );
  } else if (name === "revoke-admin") {
    confirmAction(
      "撤销管理员",
      `撤销身份 …${id.slice(-8)} 的管理员权限及其登录凭据？`,
      "roles",
      (_, revision) => api(`/roles/${id}`, "PUT", { revision, role: "member" }),
      "管理员已撤销",
    );
  } else if (name === "create-credential") {
    openEditor(
      "创建登录凭据",
      field("名称", "label", "", 'required maxlength="64"') +
        `<label for="edit-identity">管理身份</label><select id="edit-identity" name="identity">${[snapshot.owner, ...(snapshot.admins || [])].map((uid) => `<option value="${uid}">${uid === snapshot.owner ? "所有者" : "管理员"} · …${uid.slice(-12)}</option>`).join("")}</select>` +
        field(
          "有效期（小时）",
          "hours",
          720,
          'type="number" min="1" max="2160" required',
        ),
      "tokens",
      (f, revision) =>
        api("/credentials", "POST", {
          revision,
          label: f.elements.label.value,
          identity: f.elements.identity.value,
          hours: Number(f.elements.hours.value),
        }),
    );
  } else if (name === "revoke-credential") {
    confirmAction(
      "撤销登录凭据",
      "使用此凭据的管理会话将失效。",
      "tokens",
      (_, revision) => api(`/credentials/${id}`, "DELETE", { revision }),
      "登录凭据已撤销",
    );
  }
}

document.addEventListener("click", (event) => {
  const button = event.target.closest("button");
  if (!button) return;
  if (button.dataset.view) {
    view = button.dataset.view;
    $("#view").replaceChildren();
    render();
  }
  if (button.dataset.action) action(button.dataset.action, button.dataset.id);
  if (button.id === "reset-settings" && !pending) {
    settingsDraft = null;
    renderSettings();
    setDisabled();
  }
});

$("#login-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (loggingIn || loggingOut) return;
  const current = generation,
    button = event.submitter;
  loggingIn = true;
  button.disabled = true;
  button.textContent = "登录中…";
  $("#login-error").textContent = "";
  try {
    session = await api("/login", "POST", { token: $("#token").value });
    $("#token").value = "";
    await enter();
  } catch (error) {
    if (current === generation) {
      $("#login-error").textContent = error.message;
      if (session) {
        notice(error.message, true);
        subscribe();
      }
    }
  } finally {
    loggingIn = false;
    if (current === generation) {
      button.disabled = false;
      button.textContent = "登录";
    }
  }
});

$("#editor-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (pending || !live || !editor) return;
  const operation = editor,
    epoch = ++operationEpoch;
  pending = true;
  setDisabled();
  $("#editor-error").textContent = "";
  $("#save-editor").textContent = "正在保存…";
  try {
    const result = await operation.submit(
      event.currentTarget,
      Number(event.currentTarget.dataset.revision),
    );
    if (editor !== operation) return;
    if (result.token) {
      $("#editor-title").textContent = "登录凭据已创建";
      $("#editor-fields").innerHTML =
        `<p>此凭据仅显示一次</p><div class="secret" id="new-secret">${esc(result.token)}</div><button type="button" id="copy-secret">${icon("copy")}复制凭据</button>`;
      $("#copy-secret").onclick = async () => {
        try {
          await navigator.clipboard.writeText(result.token);
          $("#copy-secret").textContent = "已复制";
        } catch {
          $("#editor-error").textContent = "复制失败，请选中凭据文字复制";
        }
      };
      $("#save-editor").hidden = true;
      $("#cancel-editor").textContent = "完成";
      editor = null;
    } else {
      $("#editor").close();
      editor = null;
      notice(operation.success);
    }
    await refresh();
  } catch (error) {
    if (editor === operation) await showConflict(error);
  } finally {
    if (epoch === operationEpoch) {
      pending = false;
      $("#save-editor").textContent = "保存";
      setDisabled();
    }
  }
});

document.addEventListener("input", (event) => {
  if (event.target.closest("#settings-form")) settingsDraft = readSettings();
});
document.addEventListener("submit", async (event) => {
  if (event.target.id !== "settings-form") return;
  event.preventDefault();
  if (pending || !live) return;
  const value = readSettings();
  const previous = snapshot.settings;
  if (
    value.chat_max_messages < previous.chat_max_messages ||
    value.chat_max_bytes < previous.chat_max_bytes ||
    value.chat_max_age_seconds < previous.chat_max_age_seconds
  ) {
    openEditor(
      "缩小消息保留范围",
      "<p>超出新限制的旧消息可能立即删除，且无法恢复。确认保存这些设置？</p>",
      "settings",
      (_, revision) => api("/settings", "PUT", { revision, settings: value }),
      "服务器设置已保存",
    );
    $("#editor-form").dataset.revision = settingsRevision;
    editor.latest = (current) => describeSettings(current.settings);
    const original = editor.submit;
    editor.submit = async (...args) => {
      const result = await original(...args);
      settingsDraft = null;
      return result;
    };
    return;
  }
  const current = generation;
  pending = true;
  setDisabled();
  try {
    await api("/settings", "PUT", {
      revision: settingsRevision,
      settings: value,
    });
    settingsDraft = null;
    await refresh();
    notice("服务器设置已保存");
  } catch (error) {
    if (current !== generation) return;
    notice(error.message, true);
    if (error.status === 409) {
      try {
        await refresh();
        const area = $("#settings-conflict");
        if (!area) return;
        area.replaceChildren();
        const text = document.createElement("p");
        text.textContent =
          "服务器最新配置：" + describeSettings(snapshot.settings);
        const button = document.createElement("button");
        button.type = "button";
        button.textContent = "保留草稿，继续编辑";
        const revision = snapshot.revision;
        button.onclick = () => {
          settingsRevision = revision;
          area.replaceChildren();
          notice("请核对后再次保存");
        };
        area.append(text, button);
      } catch (failure) {
        if (current === generation) notice(failure.message, true);
      }
    }
  } finally {
    if (current === generation) {
      pending = false;
      setDisabled();
    }
  }
});

$("#close-editor").onclick = closeEditor;
$("#cancel-editor").onclick = closeEditor;
$("#editor").addEventListener("cancel", (event) => {
  event.preventDefault();
  closeEditor();
});
$("#refresh").onclick = async () => {
  try {
    await refresh();
    if (!live) subscribe();
    notice("状态已刷新");
  } catch (error) {
    notice(error.message, true);
  }
};
async function logout() {
  if (loggingOut) return;
  loggingOut = true;
  const csrf = session?.csrf;
  reset("正在退出登录…");
  const current = generation,
    controller = new AbortController(),
    timeout = setTimeout(() => controller.abort(), 8000);
  const button = $("#login-form button");
  button.disabled = true;
  button.textContent = "登录";
  try {
    const response = await fetch(base + "/logout", {
      method: "POST",
      credentials: "same-origin",
      signal: controller.signal,
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": csrf ?? "",
      },
      body: "{}",
    });
    if (!response.ok && response.status !== 401) throw new Error();
    if (current === generation) $("#login-error").textContent = "";
  } catch {
    if (current === generation)
      $("#login-error").textContent =
        "已退出本页，服务端注销未确认；请重试登录或关闭浏览器";
  } finally {
    clearTimeout(timeout);
    loggingOut = false;
    if (current === generation) button.disabled = false;
  }
}
$("#logout").onclick = logout;
$("#editor-logout").onclick = logout;
window.addEventListener("pagehide", () => {
  connected(false);
  stream?.close();
  clearTimeout(reconnect);
  requests.forEach((r) => r.abort());
});
window.addEventListener("pageshow", async (event) => {
  if (!event.persisted || !session) return;
  connected(false);
  try {
    session = await api("/session");
    await refresh();
    subscribe();
  } catch (error) {
    if (session) {
      notice(error.message, true);
      subscribe();
    }
  }
});
const startupGeneration = generation;
$("#login-form button").disabled = true;
try {
  session = await api("/session");
  await enter();
} catch (error) {
  if (startupGeneration === generation && session) {
    notice(error.message, true);
    subscribe();
  }
} finally {
  if (startupGeneration === generation && !loggingOut)
    $("#login-form button").disabled = false;
}
