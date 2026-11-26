import { api, assetPath, badge, list, node, page, productPath, revisionPath, field, segment, statuses, timestamp } from "./workbench.js";
import { checkDetail } from "./checks.js";

const kinds = { HTTP: "HTTP", EVENT: "事件" };
const lifecycles = { EXPERIMENTAL: "试验", STABLE: "稳定", DEPRECATED: "已弃用", RETIRED: "已退役" };
const visibility = { EMPTY: "尚未发布", ACTIVE: "已生效", WITHDRAWN: "已撤销" };
const sources = { UPLOAD: "上传", GIT: "Git" };
const releaseStates = { READY: "可发布" };

const settle = promise => promise.then(value => ({ value }), error => ({ error }));
const short = digest => digest.length > 19 ? digest.slice(0, 19) + "…" : digest;

function code(text) {
  return node("code", text, "digest");
}

function facts(fields) {
  const items = node("dl", "", "facts");
  for (const [label, value] of fields) {
    const description = node("dd");
    description.append(value);
    items.append(node("dt", label), description);
  }
  return items;
}

function table(caption, headings, rows) {
  const wrap = node("div", "", "table-wrap");
  const data = node("table", "", "data");
  data.append(node("caption", caption));
  const head = node("tr");
  for (const heading of headings) {
    const cell = node("th", heading);
    cell.scope = "col";
    head.append(cell);
  }
  const body = node("tbody");
  for (const cells of rows) {
    const row = node("tr");
    for (const value of cells) {
      const cell = node("td");
      cell.append(value ?? "—");
      row.append(cell);
    }
    body.append(row);
  }
  const header = node("thead");
  header.append(head);
  data.append(header, body);
  wrap.append(data);
  return wrap;
}

// catalogPanel lists the API assets of one workspace with the filters the
// server applies before paging, and opens an asset's revisions, checks,
// reviews, releases and publications in place.
export function catalogPanel({ product, workspace, signal }) {
  const element = node("section", "", "panel catalog");
  element.setAttribute("aria-labelledby", "catalog-title");
  const title = node("div", "", "panel-title");
  const heading = node("h2", "API 目录");
  heading.id = "catalog-title";
  title.append(heading);

  const query = node("input");
  query.type = "search";
  query.maxLength = 120;
  query.autocomplete = "off";
  query.placeholder = "名称、键或领域";
  const kind = node("select");
  kind.add(new Option("全部", ""));
  for (const [value, label] of Object.entries(kinds)) kind.add(new Option(label, value));
  const check = node("select");
  check.add(new Option("不限", ""));
  for (const value of ["PASS", "WARN", "FAIL", "UNKNOWN"]) check.add(new Option(statuses[value], value));
  const pending = node("input");
  pending.type = "checkbox";
  pending.id = "catalog-pending";
  const pendingLabel = node("label", "", "inline-check");
  pendingLabel.htmlFor = pending.id;
  pendingLabel.append(pending, "只看已批准、未准备 Release");
  const apply = node("button", "筛选", "primary");
  apply.type = "submit";
  const clear = node("button", "清空筛选");
  clear.type = "button";
  const form = node("form", "", "catalog-filters");
  form.setAttribute("role", "search");
  form.append(field("关键字", query, "catalog-query"), field("类型", kind, "catalog-kind"),
    field("最近检查", check, "catalog-check"), pendingLabel, apply, clear);

  const status = node("p", "", "panel-body muted");
  status.setAttribute("role", "status");
  const rows = node("tbody");
  const head = node("tr");
  for (const label of ["API 资产", "领域", "类型", "生命周期", "最新修订", "最近检查", "最新 Release"]) {
    const cell = node("th", label);
    cell.scope = "col";
    head.append(cell);
  }
  const header = node("thead");
  header.append(head);
  const assets = node("table", "", "data");
  assets.append(node("caption", "API 资产"), header, rows);
  const wrap = node("div", "", "table-wrap panel-body");
  wrap.append(assets);
  const more = node("button", "加载更多", "more");
  more.type = "button";
  more.hidden = true;
  const detail = node("div", "", "asset-detail panel-body");
  detail.hidden = true;
  element.append(title, form, status, wrap, more, detail);

  let started = false;
  let request;
  let detailRequest;
  let next = "";
  let filters;

  function currentFilters() {
    const params = new URLSearchParams({ workspace_id: workspace });
    const text = query.value.trim();
    if (text) params.set("q", text);
    if (kind.value) params.set("api_kind", kind.value);
    if (check.value) params.set("check_status", check.value);
    if (pending.checked) params.set("release_pending", "true");
    return params;
  }

  // A page token belongs to the filters it was issued for, so "more" keeps
  // the filters of the first page even if the form was edited since.
  async function load(reset) {
    request?.abort();
    const controller = new AbortController();
    request = controller;
    const current = AbortSignal.any([controller.signal, signal]);
    if (reset) {
      rows.replaceChildren();
      next = "";
      filters = currentFilters();
      closeAsset();
    }
    more.hidden = true;
    status.textContent = "正在加载资产…";
    try {
      const result = await page(productPath(product) + "/assets?" + filters, 20, next, current);
      if (current.aborted) return;
      for (const asset of result.items) rows.append(assetRow(asset));
      next = result.next_page_token || "";
      status.textContent = rows.children.length ? "" : "当前筛选下没有可见的资产。";
    } catch (error) {
      if (!current.aborted) status.textContent = error.message;
    } finally {
      if (!current.aborted) more.hidden = !next;
    }
  }

  function assetRow(asset) {
    const overview = asset.overview;
    const open = node("button", asset.display_name, "row-action");
    open.type = "button";
    open.dataset.asset = asset.api_asset_id;
    open.setAttribute("aria-pressed", "false");
    open.onclick = () => showAsset(asset);
    const name = node("div");
    name.append(open, node("small", asset.asset_key, "muted"));
    const revision = overview?.latest_revision;
    const latestCheck = overview?.latest_check;
    const release = node("span");
    if (overview?.latest_release) release.append(overview.latest_release.version);
    if (overview?.release_pending) release.append(" ", node("span", "待准备", "badge"));
    const cells = [
      name, asset.domain_key, kinds[asset.api_kind] || asset.api_kind, lifecycles[asset.lifecycle] || asset.lifecycle,
      revision ? "#" + revision.revision_sequence + " · " + statuses[revision.status] : overview ? "暂无修订" : null,
      latestCheck ? badge(latestCheck.status) : null,
      release.childNodes.length ? release : null,
    ];
    const row = node("tr");
    for (const value of cells) {
      const cell = node("td");
      cell.append(value ?? "—");
      row.append(cell);
    }
    return row;
  }

  function closeAsset() {
    detailRequest?.abort();
    detail.hidden = true;
    detail.replaceChildren();
  }

  function section(text, outcome, render) {
    const block = node("section", "", "detail-section");
    block.append(node("h4", text));
    if (outcome.error) block.append(node("p", outcome.error.message, "muted"));
    else block.append(render(outcome.value));
    return block;
  }

  async function showAsset(asset) {
    detailRequest?.abort();
    const controller = new AbortController();
    detailRequest = controller;
    const current = AbortSignal.any([controller.signal, signal]);
    for (const button of rows.querySelectorAll("button[data-asset]")) {
      button.setAttribute("aria-pressed", String(button.dataset.asset === asset.api_asset_id));
    }
    const title = node("h3", asset.display_name);
    title.tabIndex = -1;
    const loading = node("p", "正在读取修订和发布…", "muted");
    loading.setAttribute("role", "status");
    detail.replaceChildren(title, facts([
      ["资产 ID", code(asset.api_asset_id)], ["键", asset.asset_key], ["领域", asset.domain_key],
      ["类型", kinds[asset.api_kind] || asset.api_kind], ["生命周期", lifecycles[asset.lifecycle] || asset.lifecycle],
      ["登记时间", timestamp(asset.created_at)],
    ]), loading);
    detail.hidden = false;
    title.focus();

    const base = assetPath(product, asset.api_asset_id);
    const [revisions, releases, slots, targets] = await Promise.all([
      settle(list(base + "/revisions", current)),
      settle(list(base + "/releases", current)),
      settle(list(base + "/publications", current)),
      settle(list(productPath(product) + "/publication-targets", current)),
    ]);
    if (current.aborted) return;
    const releaseList = releases.value || [];
    const versionOf = id => releaseList.find(item => item.release_id === id)?.version || short(id);
    const revisionBox = node("div");
    const ordered = (revisions.value || []).toSorted((a, b) => b.revision_sequence - a.revision_sequence);
    const revisionSection = section("修订", revisions, () => {
      if (!ordered.length) return node("p", "此资产还没有修订。", "muted");
      return table("修订", ["修订", "状态", "来源", "规范版本", "源文件摘要", "引用闭包摘要", "导入时间"], ordered.map(item => {
        const pick = node("button", "#" + item.revision_sequence, "row-action");
        pick.type = "button";
        pick.dataset.revision = item.revision_id;
        pick.setAttribute("aria-pressed", "false");
        pick.onclick = () => showRevision(item);
        return [pick, badge(item.status), sources[item.source_kind] || item.source_kind, item.spec_version,
          code(short(item.source_digest)), code(short(item.ref_closure_digest)), timestamp(item.created_at)];
      }));
    });
    const publicationSection = section("发布状态", slots, items => {
      if (!items.length) return node("p", "此资产尚未发布到任何目标。", "muted");
      const targetOf = id => {
        const found = targets.value?.find(item => item.publication_target_id === id);
        return found ? found.audience + " / " + found.environment + " · " + found.portal_key : short(id);
      };
      return table("发布状态", ["发布目标", "槽位状态", "生效 Release", "槽位代数", "访问阻断", "批准状态", "边缘传播"],
        items.map(slot => [targetOf(slot.publication_target_id), visibility[slot.visibility] || slot.visibility,
          slot.active_release_id ? versionOf(slot.active_release_id) : "无", String(slot.generation),
          slot.blocked ? "已阻断" : "无", slot.approval_problem || "无问题",
          statuses[slot.delivery_status] || slot.delivery_status]));
    });
    loading.remove();
    detail.append(revisionSection, revisionBox);
    // A caller may read the releases of an asset without its revisions.
    if (revisions.error && releaseList.length) {
      detail.append(section("Release", releases, items => table("Release", ["版本", "状态", "制品摘要", "准备时间"],
        items.map(item => [item.version, releaseStates[item.status] || item.status, code(item.artifact_digest),
          timestamp(item.prepared_at)]))));
    }
    detail.append(publicationSection);
    if (ordered.length) await showRevision(ordered[0]);

    async function showRevision(revision) {
      const shown = Symbol();
      revisionBox.shown = shown;
      for (const button of revisionSection.querySelectorAll("button[data-revision]")) {
        button.setAttribute("aria-pressed", String(button.dataset.revision === revision.revision_id));
      }
      const path = revisionPath(product, revision.api_asset_id, revision.revision_id);
      revisionBox.replaceChildren(node("p", "正在读取修订 #" + revision.revision_sequence + "…", "muted"));
      const [checks, reviews] = await Promise.all([
        settle(list(path + "/check-runs", current)),
        settle(list(path + "/reviews", current)),
      ]);
      if (current.aborted || revisionBox.shown !== shown) return;
      const sequenceOf = id => ordered.find(item => item.revision_id === id)?.revision_sequence;
      const origin = revision.git_origin;
      const block = node("section", "", "detail-section revision");
      block.append(node("h4", "修订 #" + revision.revision_sequence), facts([
        ["修订 ID", code(revision.revision_id)],
        ["状态", badge(revision.status)],
        ["来源", origin ? "Git " + origin.repository + " @ " + origin.commit + (origin.ref ? " (" + origin.ref + ")" : "") :
          sources[revision.source_kind] || revision.source_kind],
        ["入口文件", code(revision.entrypoint)],
        ["规范版本", revision.spec_version],
        ["源文件摘要", code(revision.source_digest)],
        ["引用闭包摘要", code(revision.ref_closure_digest)],
        ["导入时间", timestamp(revision.created_at)],
      ]));
      const checkBox = node("div");
      const openCheck = async (item, button) => {
        for (const other of block.querySelectorAll("button[data-check]")) {
          other.setAttribute("aria-pressed", String(other === button));
        }
        checkBox.replaceChildren(node("p", "正在读取检查…", "muted"));
        try {
          const detail = await api(path + "/check-runs/" + segment(item.check_run_id), { signal: current });
          if (!current.aborted && button.getAttribute("aria-pressed") === "true") checkBox.replaceChildren(checkDetail(detail));
        } catch (error) {
          if (!current.aborted) checkBox.replaceChildren(node("p", error.message, "muted"));
        }
      };
      block.append(section("检查", checks, items => items.length ? table("检查", ["结论", "完成时间", "策略摘要", "兼容性对比", "详情"],
        items.map(item => {
          const view = node("button", "查看", "row-action");
          view.type = "button";
          view.dataset.check = item.check_run_id;
          view.setAttribute("aria-pressed", "false");
          view.onclick = () => openCheck(item, view);
          return [badge(item.status), timestamp(item.completed_at), code(short(item.policy_digest)),
            item.compatibility ? "对比 #" + (sequenceOf(item.compatibility.baseline.revision_id) ?? "?") : "未对比", view];
        }))
        : node("p", "此修订还没有检查。", "muted")), checkBox);
      block.append(section("评审", reviews, items => items.length ? table("评审", ["状态", "提交人", "提交时间", "审批范围", "批准有效期"],
        items.map(item => [badge(item.status), item.requested_by.subject, timestamp(item.requested_at),
          item.scope.map(scope => scope.audience + " / " + scope.environment).join("，"),
          item.conclusion?.valid_until ? timestamp(item.conclusion.valid_until) : null]))
        : node("p", "此修订还没有评审。", "muted")));
      const prepared = releaseList.filter(item => item.revision_id === revision.revision_id);
      block.append(section("Release", releases, () => prepared.length ? table("Release", ["版本", "状态", "制品摘要", "准备时间"],
        prepared.map(item => [item.version, releaseStates[item.status] || item.status, code(item.artifact_digest),
          timestamp(item.prepared_at)]))
        : node("p", "此修订还没有准备 Release。", "muted")));
      revisionBox.replaceChildren(block);
    }
  }

  form.onsubmit = event => {
    event.preventDefault();
    load(true);
  };
  clear.onclick = () => {
    form.reset();
    load(true);
  };
  more.onclick = () => load(false);

  function start() {
    if (started) return;
    started = true;
    load(true);
  }

  return {
    element,
    start,
    open(asset) {
      start();
      showAsset(asset);
    },
  };
}
