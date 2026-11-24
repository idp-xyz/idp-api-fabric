import { api, assetPath, field, fill, list, node, productPath, segment, statuses, timestamp } from "./workbench.js";

const actions = { ACTIVATE: "发布", WITHDRAW: "撤销", RESTORE: "恢复" };
const visibility = { EMPTY: "尚未发布", ACTIVE: "已生效", WITHDRAWN: "已撤销" };

export function publicationPanel(review, publisher, signal) {
  const product = productPath(review.product_id);
  const releasePath = assetPath(review.product_id, review.api_asset_id) + "/releases";
  const panel = node("details", "", "publication");
  const refresh = node("button", "刷新发布状态");
  refresh.type = "button";
  const message = node("p", "", "muted");
  message.setAttribute("role", "status");
  const target = node("select");
  const release = node("select");
  const version = node("input");
  version.required = true;
  version.maxLength = 64;
  version.autocomplete = "off";
  const prepare = node("button", "准备 Release", "primary");
  prepare.type = "submit";
  const action = node("select");
  for (const [value, label] of Object.entries(actions)) action.add(new Option(label, value));
  const reason = node("input");
  reason.maxLength = 500;
  const apply = node("button", "执行发布操作", "primary");
  apply.type = "submit";
  const slotDetails = node("div");
  const history = node("div");
  const releaseField = field("Release", release, "publication-release");
  const prepareForm = node("form", "", "form");
  prepareForm.append(field("新版本", version, "publication-version"), prepare);
  prepareForm.hidden = !publisher || review.status !== "APPROVED";
  const operationForm = node("form", "", "form");
  operationForm.append(field("操作", action, "publication-action"), releaseField,
    field("操作理由", reason, "publication-reason"), apply);
  operationForm.hidden = !publisher;
  const scope = node("div", "", "publication-scope");
  scope.append(field("内部发布目标", target, "publication-target"), refresh);
  panel.append(node("summary", "Release 与内部发布"), message, prepareForm, scope, slotDetails, operationForm, history);

  let started = false;
  let working = false;
  let slot = null;
  let releases = [];
  let targets = [];
  let pending;
  const slotPath = () => product + "/publication-targets/" + segment(target.value) +
    "/assets/" + segment(review.api_asset_id);

  function note(text, error = false) {
    message.textContent = text;
    message.classList.toggle("publication-error", error);
  }

  function sync() {
    const withdrawing = action.value === "WITHDRAW";
    target.disabled = working || targets.length === 0;
    refresh.disabled = working;
    version.disabled = working;
    prepare.disabled = working || !version.value.trim();
    action.disabled = working || !slot;
    reason.disabled = working || !slot;
    reason.required = action.value !== "ACTIVATE";
    releaseField.hidden = withdrawing;
    release.disabled = working || withdrawing || !slot || releases.length === 0;
    release.required = !withdrawing;
    const selectable = withdrawing ? slot?.active_release_id : release.value && release.value !== slot?.active_release_id;
    apply.disabled = working || !slot || !Number.isSafeInteger(slot.generation) || !selectable;
  }

  async function run(task) {
    if (working || signal.aborted) return;
    working = true;
    sync();
    try {
      await task();
    } catch (error) {
      if (!signal.aborted) note(error.message, true);
    } finally {
      working = false;
      if (!signal.aborted) sync();
    }
  }

  function replaceReleases(items, chosen = release.value) {
    releases = items;
    fill(release, [...releases].reverse(), "release_id", item => item.version, "请选择 Release");
    if (releases.some(item => item.release_id === chosen)) release.value = chosen;
  }

  function versionOf(id) {
    return releases.find(item => item.release_id === id)?.version || id;
  }

  function showSlot() {
    slotDetails.replaceChildren();
    if (!slot) return;
    const facts = node("dl", "", "facts");
    const fields = [
      ["槽位状态", visibility[slot.visibility] || slot.visibility],
      ["槽位代数", String(slot.generation)],
      ["当前 Release", slot.active_release_id ? versionOf(slot.active_release_id) : "无"],
      ["交付状态", statuses[slot.delivery_status] || slot.delivery_status],
    ];
    if (slot.blocked) fields.push(["访问控制", "已阻断"]);
    if (slot.approval_problem) fields.push(["批准状态", slot.approval_problem]);
    for (const [label, value] of fields) facts.append(node("dt", label), node("dd", value));
    slotDetails.append(facts);
  }

  async function readSlot() {
    slot = null;
    showSlot();
    history.replaceChildren();
    if (!target.value) return;
    slot = await api(slotPath(), { signal });
    showSlot();
    if (!Number.isSafeInteger(slot.generation) || slot.generation === 0) return;
    const after = Math.max(0, slot.generation - 10);
    const operations = await api(slotPath() + "/operations?after_generation=" + after + "&page_size=10", { signal });
    const rows = node("ul", "", "checks");
    for (const op of [...operations.items].reverse()) {
      const entry = node("li");
      const name = op.release_id || op.previous_release_id;
      entry.append(node("strong", "#" + op.generation + " · " + actions[op.action] + (name ? " " + versionOf(name) : "")),
        node("small", op.performed_by.subject + " · " + timestamp(op.occurred_at), "muted"));
      if (op.reason) entry.append(node("small", op.reason));
      rows.append(entry);
    }
    if (rows.childElementCount) history.append(node("h3", "最近操作"), rows);
  }

  function reload() {
    return run(async () => {
      note("正在读取发布状态…");
      const selected = target.value;
      const [items, configured] = await Promise.all([list(releasePath, signal), list(product + "/publication-targets", signal)]);
      replaceReleases(items);
      targets = configured.filter(item => item.audience === "internal");
      fill(target, targets, "publication_target_id", item => item.portal_key + " / " + item.environment, "请选择目标");
      if (targets.some(item => item.publication_target_id === selected)) target.value = selected;
      await readSlot();
      note(targets.length ? "" : "此产品尚未登记内部发布目标。");
    });
  }

  prepareForm.onsubmit = event => {
    event.preventDefault();
    if (prepareForm.hidden || prepare.disabled) return;
    run(async () => {
      note("正在准备 Release…");
      const result = await api(releasePath, {
        signal,
        data: { revision_id: review.revision_id, review_id: review.review_id, version: version.value.trim() },
      });
      version.value = "";
      const listed = releases.some(item => item.release_id === result.release_id);
      replaceReleases(listed ? releases : [...releases, result], result.release_id);
      note("Release " + result.version + " 已就绪。");
    });
  };

  operationForm.onsubmit = event => {
    event.preventDefault();
    if (operationForm.hidden || apply.disabled) return;
    run(async () => {
      const change = { action: action.value, expected_generation: slot.generation };
      if (change.action !== "WITHDRAW") change.release_id = release.value;
      if (reason.value.trim()) change.reason = reason.value.trim();
      if (change.action !== "ACTIVATE" && !change.reason) {
        note("请填写撤销或恢复的理由。", true);
        return;
      }
      const path = slotPath() + "/operations";
      const request = path + JSON.stringify(change);
      if (pending?.request !== request) pending = { request, key: crypto.randomUUID() };
      note("正在记录发布操作…");
      let committed;
      try {
        committed = await api(path, { signal, data: change, idempotencyKey: pending.key });
      } catch (error) {
        if (error.status !== 412) throw error;
        pending = undefined;
        await readSlot();
        note("槽位已被更新，已刷新当前代数。请核对后重新提交。", true);
        return;
      }
      pending = undefined;
      reason.value = "";
      const result = actions[committed.action] + "已记录，槽位代数 " + committed.generation + "。";
      try {
        await readSlot();
      } catch (error) {
        if (!signal.aborted) note(result + "刷新发布状态失败。 " + error.message, true);
        return;
      }
      note(result);
    });
  };

  panel.ontoggle = () => {
    if (panel.open && !started) {
      started = true;
      reload();
    }
  };
  refresh.onclick = reload;
  target.onchange = () => run(async () => {
    note("");
    await readSlot();
  });
  for (const control of [action, release]) control.onchange = sync;
  version.oninput = sync;
  sync();
  return panel;
}
