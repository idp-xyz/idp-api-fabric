export const statuses = {
  IN_REVIEW: "待评审", APPROVED: "已批准", REJECTED: "已驳回", WITHDRAWN: "已撤回",
  DRAFT: "草稿", PASS: "通过", FAIL: "失败", WARN: "警告", UNKNOWN: "未知", NOT_RUN: "未执行",
};
const dateFormat = new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" });

export function node(tag, text, className) {
  const item = document.createElement(tag);
  if (text) item.textContent = text;
  if (className) item.className = className;
  return item;
}

// badge names a state in words; its colour, set by the state, only repeats
// what the words say.
export function badge(state, label = statuses[state] || state) {
  const item = node("span", label, "badge");
  item.dataset.state = state;
  return item;
}

const icons = {
  refresh: ["M20 11.5A8 8 0 0 0 5.6 7.2L4 9", "M4 4.5V9h4.5", "M4 12.5a8 8 0 0 0 14.4 4.3L20 15", "M20 19.5V15h-4.5"],
  back: ["M19 12H5", "m11 18-6-6 6-6"],
  open: ["m9 6 6 6-6 6"],
  search: ["M10.8 18.1a7.3 7.3 0 1 0 0-14.6 7.3 7.3 0 0 0 0 14.6Z", "m20.5 20.5-4.4-4.4"],
  done: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Z", "m8.2 12.4 2.6 2.6 5-5.4"],
  alert: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Z", "M12 7.5v5.5", "M12 16.5h.01"],
  pending: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Z", "M12 7v5l3.2 2"],
  asset: ["M12 3 3.5 7.5 12 12l8.5-4.5L12 3Z", "M3.5 7.5v9L12 21l8.5-4.5v-9", "M12 12v9"],
  lock: ["M7 11V7.5a5 5 0 0 1 10 0V11", "M5.5 11h13a1 1 0 0 1 1 1v8a1 1 0 0 1-1 1h-13a1 1 0 0 1-1-1v-8a1 1 0 0 1 1-1Z"],
  publish: ["M12 15.5V4", "m7 9 5-5 5 5", "M5 20h14"],
  check: ["M9 5H6.5a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h11a1 1 0 0 0 1-1V6a1 1 0 0 0-1-1H15", "M9 3.5h6v3H9z", "m9 14 2 2 4-4.5"],
  compare: ["M7 4v16", "M17 4v16", "m4 8 3-4 3 4", "m14 16 3 4 3-4"],
  release: ["M20.6 13.4 13.4 20.6a2 2 0 0 1-2.8 0L3 13V3h10l7.6 7.6a2 2 0 0 1 0 2.8Z", "M7.5 7.5h.01"],
  history: ["M3.5 12a8.5 8.5 0 1 0 2.5-6", "M3.5 4v4.5H8", "M12 8v4.5l3 1.8"],
  revision: ["M6 3.5v11", "M18 9.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5Z", "M6 20.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5Z", "M18 9.5a9 9 0 0 1-9 9"],
  shield: ["M12 3 19 6v5.5c0 4.3-2.9 8-7 9.5-4.1-1.5-7-5.2-7-9.5V6l7-3Z"],
  filter: ["M3.5 5h17l-6.5 8v6l-4 1.5V13L3.5 5Z"],
  file: ["M14 3.5H6.5a1 1 0 0 0-1 1v15a1 1 0 0 0 1 1h11a1 1 0 0 0 1-1V8L14 3.5Z", "M14 3.5V8h4.5"],
  folder: ["M3.5 6.5a1 1 0 0 1 1-1h5l2 2.5h8a1 1 0 0 1 1 1v9.5a1 1 0 0 1-1 1h-15a1 1 0 0 1-1-1V6.5Z"],
};

// icon draws one of a few line icons. It is decoration only: the text next
// to it says what it stands for.
export function icon(name, className = "icon") {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  for (const [key, value] of [["viewBox", "0 0 24 24"], ["fill", "none"], ["stroke", "currentColor"], ["stroke-width", "1.8"],
    ["stroke-linecap", "round"], ["stroke-linejoin", "round"], ["aria-hidden", "true"], ["focusable", "false"], ["class", className]]) {
    svg.setAttribute(key, value);
  }
  for (const d of icons[name] || []) {
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", d);
    svg.append(path);
  }
  return svg;
}

// button makes a button that does not submit, with an optional icon before
// its text.
export function button(text, className = "", iconName = "") {
  const item = node("button", "", className);
  item.type = "button";
  if (iconName) item.append(icon(iconName));
  if (text) item.append(node("span", text));
  return item;
}

// facts lists label and value pairs. A value may be text or a node.
export function facts(fields, className = "facts") {
  const items = node("dl", "", className);
  for (const [label, value] of fields) {
    const pair = node("div");
    const description = node("dd");
    description.append(value ?? "—");
    pair.append(node("dt", label), description);
    items.append(pair);
  }
  return items;
}

// card is a titled box of the page. Its heading names the section for
// assistive technology too.
export function card(title, { level = "h3", iconName = "", className = "", actions = [] } = {}) {
  const box = node("section", "", "card" + (className ? " " + className : ""));
  const head = node("div", "", "card-head");
  const heading = node(level, "", "card-title");
  if (iconName) heading.append(icon(iconName));
  heading.append(node("span", title));
  head.append(heading, ...actions);
  box.append(head);
  return box;
}

export const empty = text => node("p", text, "empty");

export const loading = text => node("p", text, "loading");

function describeError(status, code, trace) {
  const messages = {
    FORBIDDEN: "当前身份没有所需权限。",
    API_ASSET_KEY_TAKEN: "此产品已有使用该资产键的 API 资产。",
    API_ASSET_KIND_UNSUPPORTED: "此类资产暂不支持导入契约。",
    CONTRACT_SOURCE_INVALID: "文件不符合导入要求：入口须为 OpenAPI 3.1.2，引用只能指向本次导入的文件，不能有远程引用、包外路径或循环引用。",
    REQUEST_TOO_LARGE: "导入内容超过大小上限。",
    POLICY_STALE: "该检查不是按现行策略和工具完成的，请重新运行检查。",
    RELEASE_BLOCKED: "检查中有不可豁免的规则失败或未知，不能准备 Release。",
    WAIVER_REQUIRED: "请为每条失败或未知的可豁免规则填写豁免理由。",
    WAIVER_FORBIDDEN: "不可豁免的规则不能豁免。",
    RELEASE_CHECK_INCOMPLETE: "检查中有必需规则未执行，不能准备 Release。",
    CHECK_RUN_NOT_FOUND: "此修订没有该检查记录。",
    RELEASE_VERSION_CONFLICT: "此版本已用于另一个 Release。",
    COMPATIBILITY_BASELINE_MISMATCH: "槽位上次生效的是另一修订。请以该修订为兼容性对比基线检查，在该检查上准备 Release 后再发布。",
    ACCESS_BLOCK_EXISTS: "此范围已有访问阻断。",
    ACCESS_BLOCK_CLEARED: "此阻断已解除，请刷新记录。",
    WITHDRAW_BEFORE_CLEARING: "请由发布人先撤销受此阻断影响的发布。",
    SECURITY_REVIEWER_NOT_INDEPENDENT: "请由另一位安全评审人解除。",
    PUBLICATION_BLOCKED: "访问阻断覆盖此发布操作。",
    RELEASE_NOT_IN_SLOT_HISTORY: "该 Release 从未在此槽位生效，无法恢复。",
    PRECONDITION_FAILED: "槽位代数已变化，请核对当前状态。",
    PRODUCT_NOT_ACTIVE: "产品已暂停，暂不接受新的操作。",
    FABRIC_UNAVAILABLE: "Fabric API 暂不可用，请稍后重试。",
  };
  let text = messages[code];
  if (!text && status === 401) text = "登录已失效或尚未登录。请从内部登录入口进入。";
  if (!text && status === 404) text = "资源不存在，或不在当前可见范围内。";
  return (text || "请求失败。") + (code ? " " + code : " HTTP " + status) +
    (trace ? " · 请求 " + trace : "");
}

// With withStatus the answer is { status, body }, for an operation whose 200
// and 201 say different things.
export async function api(path, { signal, data, idempotencyKey, post = Boolean(data), withStatus = false } = {}) {
  const options = { signal, headers: { Accept: "application/json" }, cache: "no-store" };
  if (post) options.method = "POST";
  if (data) {
    options.headers["Content-Type"] = "application/json";
    options.body = JSON.stringify(data);
  }
  if (idempotencyKey) options.headers["Idempotency-Key"] = idempotencyKey;
  const response = await fetch("/api" + path, options).catch(error => {
    if (signal?.aborted) throw error;
    throw new Error("请求中断，请重试。", { cause: error });
  });
  const body = await response.json().catch(error => {
    if (signal?.aborted) throw error;
    return null;
  });
  if (!response.ok) {
    const error = new Error(describeError(response.status, body?.error_code, body?.trace_id));
    error.status = response.status;
    throw error;
  }
  if (!body) throw new Error("Fabric API 返回了无法读取的结果。");
  return withStatus ? { status: response.status, body } : body;
}

export const segment = value => encodeURIComponent(value);
export const productPath = id => "/v1/products/" + segment(id);
export const assetPath = (product, asset) => productPath(product) + "/assets/" + segment(asset);
export const revisionPath = (product, asset, revision) => assetPath(product, asset) + "/revisions/" + segment(revision);
export const timestamp = value => dateFormat.format(new Date(value));

export function fill(select, items, key, label, placeholder) {
  select.replaceChildren();
  if (placeholder) select.add(new Option(placeholder, "", true, true));
  for (const item of items) select.add(new Option(label(item), item[key]));
  select.disabled = items.length === 0;
}

export async function page(path, limit, token, signal) {
  const url = new URL(path, location.origin);
  url.searchParams.set("page_size", String(limit));
  if (token) url.searchParams.set("page_token", token);
  return api(url.pathname + url.search, { signal });
}

export async function list(path, signal) {
  const items = [];
  const seen = new Set();
  let token;
  do {
    const result = await page(path, 100, token, signal);
    items.push(...result.items);
    token = result.next_page_token;
    if (token && seen.has(token)) throw new Error("列表分页无法继续，请刷新后重试。");
    seen.add(token);
  } while (token);
  return items;
}

export function field(text, control, id) {
  const field = node("div", "", "field");
  const label = node("label", text);
  control.id = id;
  label.htmlFor = id;
  field.append(label, control);
  return field;
}

export function confirmChange(title, values, action, signal, guidance = "") {
  if (signal.aborted) return Promise.resolve(false);
  return new Promise(resolve => {
    const dialog = node("dialog", "", "operation-dialog");
    const heading = node("h2", title);
    heading.id = "operation-confirm-title";
    dialog.setAttribute("aria-labelledby", heading.id);
    dialog.append(heading);
    if (guidance) dialog.append(node("p", guidance, "notice"));
    dialog.append(facts(values, "facts compact"));
    const controls = node("form", "", "dialog-actions");
    controls.method = "dialog";
    const cancel = node("button", "取消", "secondary");
    cancel.value = "cancel";
    cancel.autofocus = true;
    const confirm = node("button", action, "primary");
    confirm.value = "confirm";
    controls.append(cancel, confirm);
    dialog.append(controls);
    const abort = () => dialog.close();
    signal.addEventListener("abort", abort, { once: true });
    dialog.onclose = () => {
      signal.removeEventListener("abort", abort);
      dialog.remove();
      resolve(dialog.returnValue === "confirm" && !signal.aborted);
    };
    document.body.append(dialog);
    dialog.showModal();
  });
}
