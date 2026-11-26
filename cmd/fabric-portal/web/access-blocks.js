import { api, badge, button, card, confirmChange, facts, field, fill, icon, list, node, productPath, segment, timestamp } from "./workbench.js";

const scopeNames = { target: "整个发布目标", asset: "一个 API 资产", release: "一个 Release" };
const identifiers = new Set(["阻断编号", "API 资产 ID", "Release ID"]);

function blockScope(block) {
  const values = [["影响范围", scopeNames[block.release_id ? "release" : block.api_asset_id ? "asset" : "target"]]];
  if (block.api_asset_id) values.push(["API 资产 ID", block.api_asset_id]);
  if (block.release_id) values.push(["Release ID", block.release_id]);
  return values;
}

// accessBlockPanel is the access control of one product's publication
// targets: a security admin blocks a target, an asset on it or one release,
// and another security reviewer clears the block. It reads nothing until it
// is first shown.
export function accessBlockPanel({ product, productName, caller, canBlock, canClear, signal }) {
  const targetPath = productPath(product) + "/publication-targets";
  const panel = node("section", "", "access-blocks");
  const heading = node("h2", "访问阻断与解除", "visually-hidden");
  heading.id = "access-blocks-title";
  panel.setAttribute("aria-labelledby", heading.id);
  const target = node("select");
  const refresh = button("刷新阻断记录", "secondary", "refresh");
  const scope = node("div", "", "card control-row security-scope");
  scope.append(field("安全发布目标", target, "security-target"), refresh);
  const message = node("p", "", "status-line");
  message.setAttribute("role", "status");

  const createForm = node("form", "", "block-form");
  const createControls = node("fieldset", "", "block-inputs");
  const level = node("select");
  level.required = true;
  level.add(new Option("请选择范围", ""));
  for (const [value, label] of Object.entries(scopeNames)) level.add(new Option(label, value));
  const assetID = node("input");
  const releaseID = node("input");
  for (const input of [assetID, releaseID]) {
    input.autocomplete = "off";
    input.spellcheck = false;
  }
  const assetField = field("API 资产 ID", assetID, "block-asset");
  const releaseField = field("Release ID", releaseID, "block-release");
  const reason = node("input");
  reason.required = true;
  reason.maxLength = 500;
  const create = node("button", "", "primary danger");
  create.type = "submit";
  create.append(icon("lock"), node("span", "阻断访问"));
  createControls.append(field("阻断范围", level, "block-level"), assetField, releaseField,
    field("阻断理由", reason, "block-reason"), create);
  createForm.append(node("p", "源站会拒绝此范围内已发布制品的读取。限定到资产或 Release 时填写事故中确认的编号。", "hint"), createControls);
  const createCard = card("阻断访问", { iconName: "lock", className: "block-create" });
  createCard.append(createForm);
  createCard.hidden = !canBlock;

  const records = node("ul", "", "block-records");
  records.setAttribute("aria-label", "访问阻断记录");
  const more = button("加载更多阻断记录", "secondary more");
  const none = node("p", "请选择目标。", "empty");
  none.setAttribute("role", "status");
  const recordCard = card("访问阻断记录", { iconName: "shield", className: "block-list" });
  recordCard.append(records, none, more);
  const columns = node("div", "", "security-columns");
  columns.append(createCard, recordCard);
  panel.append(heading, scope, message, columns);

  let started = false;
  let working = false;
  let targets = [];
  let cursor;
  const known = new Map();
  const blocksPath = () => targetPath + "/" + segment(target.value) + "/access-blocks";

  function note(text, error = false) {
    message.textContent = text;
    message.classList.toggle("publication-error", error);
  }

  function sync() {
    target.disabled = working || !targets.length;
    refresh.disabled = working;
    createControls.disabled = working || !target.value;
    more.hidden = !cursor;
    more.disabled = working;
    for (const fields of records.querySelectorAll("fieldset")) fields.disabled = working;
    assetID.required = level.value === "asset" || level.value === "release";
    releaseID.required = level.value === "release";
    assetField.hidden = assetID.disabled = !assetID.required;
    releaseField.hidden = releaseID.disabled = !releaseID.required;
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

  function targetContext() {
    const selected = targets.find(item => item.publication_target_id === target.value);
    return [
      ["产品", productName],
      ["发布目标", selected.portal_key + " / " + selected.audience + " / " + selected.environment],
    ];
  }

  function remember(block) {
    known.set(block.access_block_id, block);
    renderRecords();
  }

  function renderRecords() {
    records.replaceChildren();
    for (const block of [...known.values()].sort((a, b) => b.generation - a.generation)) {
      const row = node("li");
      const active = block.status === "BLOCK_ACTIVE";
      row.dataset.state = block.status;
      const values = [
        ["阻断编号", block.access_block_id],
        ...blockScope(block),
        ["发起时间", block.created_by.subject + " · " + timestamp(block.created_at)],
        ["阻断理由", block.reason],
      ];
      if (block.cleared_by) {
        values.push(["解除时间", block.cleared_by.subject + " · " + timestamp(block.cleared_at)], ["解除理由", block.clear_reason]);
      }
      const states = node("div", "", "block-states");
      states.append(badge(block.status, active ? "源站阻断中" : "源站已解除"), node("span", "边缘传播"), badge(block.edge_status));
      row.append(states, facts(values.map(([label, value]) => [label, identifiers.has(label) ? node("code", value, "digest") : value]),
        "facts grid"));
      if (active && canClear) {
        if (block.created_by.issuer === caller.issuer && block.created_by.subject === caller.subject) {
          row.append(node("p", "请由另一位安全评审人解除。", "notice"));
        } else {
          row.append(clearanceForm(block));
        }
      }
      records.append(row);
    }
    none.hidden = known.size > 0;
    none.textContent = "此目标暂无访问阻断记录。";
    sync();
  }

  function clearanceForm(block) {
    const form = node("form", "", "block-form clearance");
    const controls = node("fieldset", "", "block-inputs control-row");
    const clearReason = node("input");
    clearReason.required = true;
    clearReason.maxLength = 500;
    const clear = node("button", "解除阻断", "primary");
    clear.type = "submit";
    controls.append(field("解除理由", clearReason, "block-clear-" + block.access_block_id), clear);
    form.append(controls);
    form.onsubmit = event => {
      event.preventDefault();
      const supplied = clearReason.value.trim();
      if (!supplied) {
        note("请填写解除理由。", true);
        return;
      }
      run(async () => {
        const values = [...targetContext(), ...blockScope(block), ["阻断编号", block.access_block_id], ["解除理由", supplied]];
        if (!(await confirmChange("确认解除阻断", values, "确认解除", signal,
          "请先撤销受影响的发布。解除后由发布人单独恢复。"))) return;
        note("正在解除源站阻断…");
        const result = await api(blocksPath() + "/" + segment(block.access_block_id) + "/clearance", { signal, data: { reason: supplied } });
        remember(result);
        note("源站已解除阻断。");
      });
    };
    return form;
  }

  async function readPage(append) {
    if (!append) {
      known.clear();
      records.replaceChildren();
      cursor = undefined;
      none.hidden = Boolean(target.value);
      none.textContent = "请选择目标。";
    }
    if (!target.value) return;
    note("正在读取阻断记录…");
    const nextFrom = append ? "&after_generation=" + cursor : "";
    const result = await api(blocksPath() + "?page_size=20" + nextFrom, { signal });
    const next = result.next_after_generation;
    if (next !== undefined && (!Number.isSafeInteger(next) || next <= (cursor || 0))) {
      cursor = undefined;
      throw new Error("阻断记录分页无法继续，请刷新后重试。");
    }
    cursor = next;
    for (const block of result.items) known.set(block.access_block_id, block);
    renderRecords();
    note("");
  }

  function reload() {
    return run(async () => {
      const selected = target.value;
      note("正在读取发布目标…");
      targets = await list(targetPath, signal);
      fill(target, targets, "publication_target_id",
        item => item.portal_key + " / " + item.audience + " / " + item.environment, "请选择目标");
      if (targets.some(item => item.publication_target_id === selected)) target.value = selected;
      await readPage(false);
      if (!target.value) note(targets.length ? "" : "此产品尚未登记发布目标。");
    });
  }

  createForm.onsubmit = event => {
    event.preventDefault();
    if (!canBlock || !target.value || !level.value) return;
    const data = { reason: reason.value.trim() };
    if (assetID.required) data.api_asset_id = assetID.value.trim();
    if (releaseID.required) data.release_id = releaseID.value.trim();
    if (!data.reason || (assetID.required && !data.api_asset_id) || (releaseID.required && !data.release_id)) {
      note("请填写所选范围的编号和阻断理由。", true);
      return;
    }
    run(async () => {
      const values = [...targetContext(), ...blockScope(data), ["阻断理由", data.reason]];
      if (!(await confirmChange("确认阻断访问", values, "确认阻断", signal,
        "源站会拒绝此范围内已发布制品的读取。"))) return;
      note("正在建立源站阻断…");
      const block = await api(blocksPath(), { signal, data });
      remember(block);
      createForm.reset();
      note("源站已阻断访问。");
    });
  };

  refresh.onclick = reload;
  target.onchange = () => run(async () => {
    createForm.reset();
    note("");
    await readPage(false);
  });
  more.onclick = () => { if (cursor) run(() => readPage(true)); };
  level.onchange = sync;
  sync();
  return {
    element: panel,
    start() {
      if (started) return;
      started = true;
      reload();
    },
  };
}
