import { api, assetPath, badge, button, card, confirmChange, facts, field, fill, icon, list, loading, node, productPath, segment, statuses, timestamp } from "./workbench.js";

const actions = { ACTIVATE: "发布", WITHDRAW: "撤销", RESTORE: "恢复" };
const visibility = { EMPTY: "尚未发布", ACTIVE: "已生效", WITHDRAWN: "已撤销" };
const targetName = target => target.portal_key + " / " + target.audience + " / " + target.environment;

// publicationPanel shows where one API asset stands on each internal
// publication target of its product and lets a publisher activate, withdraw
// or restore its releases there. Slots on other targets are only listed.
// Without the asset's releases it reads them itself.
export function publicationPanel({ product, productName, asset, releases: known, publisher, signal, describe = item => item.version }) {
  const root = productPath(product);
  const releasePath = assetPath(product, asset.api_asset_id) + "/releases";
  const element = node("section", "", "publication");
  const heading = node("h3", "", "section-title");
  heading.append(icon("publish"), node("span", "发布"));
  heading.id = "publication-title";
  element.setAttribute("aria-labelledby", heading.id);
  const message = loading("");
  message.setAttribute("role", "status");
  const targetBoxes = node("div", "", "targets");
  const others = node("div");
  element.append(heading, message, targetBoxes, others);

  let releases = known || [];
  const panels = [];

  function versionOf(id) {
    return releases.find(item => item.release_id === id)?.version || id;
  }

  async function relist() {
    releases = await list(releasePath, signal);
    for (const panel of panels) panel.refill();
  }

  function targetPanel(target) {
    const suffix = "-" + target.publication_target_id;
    const refresh = button("刷新", "small ghost", "refresh");
    const box = card(target.portal_key + " / " + target.environment, { level: "h4", className: "publication-target", actions: [refresh] });
    const status = node("p", "", "status-line");
    status.setAttribute("role", "status");
    const action = node("select");
    for (const [value, label] of Object.entries(actions)) action.add(new Option(label, value));
    const release = node("select");
    const reason = node("input");
    reason.maxLength = 500;
    const apply = node("button", "执行发布操作", "primary");
    apply.type = "submit";
    const slotDetails = node("div");
    const history = node("div", "", "history");
    const releaseField = field("Release", release, "publication-release" + suffix);
    const reasonField = field("操作理由", reason, "publication-reason" + suffix);
    reasonField.classList.add("wide");
    const operationForm = node("form", "", "form operation-form");
    operationForm.append(field("操作", action, "publication-action" + suffix), releaseField, reasonField, apply);
    operationForm.hidden = !publisher;
    box.append(status, slotDetails, operationForm, history);

    let working = false;
    let slot = null;
    let pending;
    const slotPath = root + "/publication-targets/" + segment(target.publication_target_id) +
      "/assets/" + segment(asset.api_asset_id);

    function note(text, error = false) {
      status.textContent = text;
      status.classList.toggle("publication-error", error);
    }

    function sync() {
      const withdrawing = action.value === "WITHDRAW";
      refresh.disabled = working;
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

    function refill(chosen = release.value) {
      fill(release, [...releases].reverse(), "release_id", describe, "请选择 Release");
      if (releases.some(item => item.release_id === chosen)) release.value = chosen;
      sync();
    }

    function showSlot() {
      slotDetails.replaceChildren();
      if (!slot) return;
      slotDetails.append(facts([
        ["槽位状态", badge(slot.visibility, visibility[slot.visibility] || slot.visibility)],
        ["槽位代数", String(slot.generation)],
        ["当前 Release", slot.active_release_id ? versionOf(slot.active_release_id) : "无"],
        ["访问阻断", slot.blocked ? badge("BLOCKED", "已阻断") : "无"],
        ["边缘传播", badge(slot.delivery_status)],
      ], "facts grid"));
    }

    async function readSlot() {
      slot = null;
      showSlot();
      history.replaceChildren();
      const current = await api(slotPath, { signal });
      if (current.active_release_id && !releases.some(item => item.release_id === current.active_release_id)) await relist();
      slot = current;
      showSlot();
      if (!Number.isSafeInteger(slot.generation) || slot.generation === 0) return;
      const after = Math.max(0, slot.generation - 10);
      const operations = await api(slotPath + "/operations?after_generation=" + after + "&page_size=10", { signal });
      const rows = node("ol", "", "timeline");
      for (const op of [...operations.items].reverse()) {
        const entry = node("li");
        entry.dataset.action = op.action;
        const name = op.release_id || op.previous_release_id;
        entry.append(node("strong", "#" + op.generation + " · " + actions[op.action] + (name ? " " + versionOf(name) : "")),
          node("small", op.performed_by.subject + " · " + timestamp(op.occurred_at), "muted"));
        if (op.reason) entry.append(node("small", op.reason, "reason"));
        rows.append(entry);
      }
      if (rows.childElementCount) history.append(node("h5", "最近操作"), rows);
    }

    // A refresh also rereads the releases, which may have been prepared
    // since the asset was opened.
    function read(fresh = false) {
      return run(async () => {
        note("正在读取槽位…");
        if (fresh) await relist();
        await readSlot();
        note("");
      });
    }

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
        const chosenRelease = releases.find(item => item.release_id === (change.release_id || slot.active_release_id));
        const verb = actions[change.action];
        const values = [
          ["产品", productName],
          ["发布目标", targetName(target)],
          ["API 资产", asset.display_name],
          ["资产 ID", asset.api_asset_id],
          ["Release", chosenRelease?.version || change.release_id || slot.active_release_id],
          ["预期槽位代数", String(change.expected_generation)],
        ];
        if (chosenRelease?.artifact_digest) values.push(["制品摘要", chosenRelease.artifact_digest]);
        if (!(await confirmChange("确认" + verb, values, "确认" + verb, signal))) return;
        const path = slotPath + "/operations";
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

    refresh.onclick = () => read(true);
    for (const control of [action, release]) control.onchange = sync;
    refill();
    return { element: box, read, refill };
  }

  function listOthers(slots, targets) {
    const rows = node("ul", "", "slot-list");
    for (const slot of slots) {
      const target = targets.find(item => item.publication_target_id === slot.publication_target_id);
      if (!target || target.audience === "internal") continue;
      const entry = node("li");
      entry.append(node("strong", targetName(target)), node("small", [
        visibility[slot.visibility] || slot.visibility,
        "代数 " + slot.generation,
        "当前 Release " + (slot.active_release_id ? versionOf(slot.active_release_id) : "无"),
        slot.blocked ? "已阻断" : "",
        "边缘传播" + (statuses[slot.delivery_status] || slot.delivery_status),
      ].filter(Boolean).join(" · "), "muted"));
      rows.append(entry);
    }
    if (!rows.childElementCount) return;
    const box = card("其他发布目标", { level: "h4", className: "other-targets" });
    box.append(rows);
    others.replaceChildren(box);
  }

  async function start() {
    message.textContent = "正在读取发布目标…";
    try {
      const [targets, slots] = await Promise.all([
        list(root + "/publication-targets", signal),
        // Only the other targets are read from this list. Each internal
        // target reads its own slot and says so when it cannot.
        list(assetPath(product, asset.api_asset_id) + "/publications", signal).catch(() => []),
        known ? null : relist().catch(() => {}),
      ]);
      if (signal.aborted) return;
      const internal = targets.filter(item => item.audience === "internal");
      for (const target of internal) {
        const panel = targetPanel(target);
        panels.push(panel);
        targetBoxes.append(panel.element);
        panel.read();
      }
      listOthers(slots, targets);
      message.className = "empty";
      message.textContent = internal.length ? "" : "此产品尚未登记内部发布目标。";
    } catch (error) {
      if (!signal.aborted) {
        message.className = "notice";
        message.textContent = error.message;
      }
    }
  }

  start();
  return {
    element,
    setReleases(items) {
      releases = items;
      for (const panel of panels) panel.refill();
    },
  };
}
