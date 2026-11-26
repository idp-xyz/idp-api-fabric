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
    REVIEWER_NOT_INDEPENDENT: "此评审需由未参与修订、检查或提交的评审人决定。",
    POLICY_STALE: "检查证据已过期，请让作者重新检查。",
    REVIEW_BLOCKED: "检查存在阻断项，此修订无法提交评审。",
    WAIVER_REQUIRED: "请填写所有必需的豁免理由。",
    APPROVAL_REQUIRED: "该 Release 缺少可用批准。",
    APPROVAL_EXPIRED: "批准已到期。",
    WAIVER_EXPIRED: "审批豁免已到期。",
    REVIEWER_AUTH_REVOKED: "评审人授权已撤销。",
    APPROVAL_SCOPE_MISMATCH: "该目标不在评审批准范围内。",
    PUBLISHER_NOT_INDEPENDENT: "请由未参与修订、检查或评审的发布人操作。",
    ACCESS_BLOCK_EXISTS: "此范围已有访问阻断。",
    ACCESS_BLOCK_CLEARED: "此阻断已解除，请刷新记录。",
    WITHDRAW_BEFORE_CLEARING: "请由发布人先撤销受此阻断影响的发布。",
    SECURITY_REVIEWER_NOT_INDEPENDENT: "请由另一位安全评审人解除。",
    PUBLICATION_BLOCKED: "访问阻断覆盖此发布操作。",
    RELEASE_NOT_IN_SLOT_HISTORY: "该 Release 从未在此槽位生效，无法恢复。",
    PRECONDITION_FAILED: "槽位代数已变化，请核对当前状态。",
    REVISION_STATUS_CONFLICT: "修订状态已有变化，请刷新队列。",
    REVIEW_IN_PROGRESS: "此修订已有待处理的评审。",
    PRODUCT_NOT_ACTIVE: "产品已暂停，暂不接受新的操作。",
    FABRIC_UNAVAILABLE: "Fabric API 暂不可用，请稍后重试。",
  };
  let text = messages[code];
  if (!text && status === 401) text = "登录已失效或尚未登录。请从内部登录入口进入。";
  if (!text && status === 404) text = "资源不存在，或不在当前可见范围内。";
  return (text || "请求失败。") + (code ? " " + code : " HTTP " + status) +
    (trace ? " · 请求 " + trace : "");
}

export async function api(path, { signal, data, idempotencyKey, post = Boolean(data) } = {}) {
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
  return body;
}

export const segment = value => encodeURIComponent(value);
export const productPath = id => "/v1/products/" + segment(id);
export const assetPath = (product, asset) => productPath(product) + "/assets/" + segment(asset);
export const revisionPath = (product, asset, revision) => assetPath(product, asset) + "/revisions/" + segment(revision);
export const reviewPath = review => revisionPath(review.product_id, review.api_asset_id, review.revision_id) +
  "/reviews/" + segment(review.review_id);
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
