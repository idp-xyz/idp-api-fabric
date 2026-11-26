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

export function badge(state) {
  const item = node("span", statuses[state] || state, "badge");
  item.dataset.state = state;
  return item;
}

function describeError(status, code, trace) {
  const messages = {
    FORBIDDEN: "当前身份没有所需权限。",
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
    if (guidance) dialog.append(node("p", guidance));
    const facts = node("dl", "", "facts");
    for (const [label, value] of values) facts.append(node("dt", label), node("dd", value));
    dialog.append(facts);
    const controls = node("form", "", "dialog-actions");
    controls.method = "dialog";
    const cancel = node("button", "取消");
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
