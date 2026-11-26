import { api, assetPath, badge, field, fill, list, node, page, productPath, revisionPath, segment, statuses, timestamp } from "./workbench.js";
import { checkDetail } from "./checks.js";
import { publicationPanel } from "./publication.js";
import { releaseForm } from "./releases.js";

const kinds = { HTTP: "HTTP", EVENT: "事件" };
const lifecycles = { EXPERIMENTAL: "试验", STABLE: "稳定", DEPRECATED: "已弃用", RETIRED: "已退役" };
const sources = { UPLOAD: "上传", GIT: "Git" };
const releaseStates = { READY: "可发布" };

const settle = promise => promise.then(value => ({ value }), error => ({ error }));
const short = digest => digest.length > 19 ? digest.slice(0, 19) + "…" : digest;
const newestFirst = (a, b) => Date.parse(b.completed_at) - Date.parse(a.completed_at);
const revisionLabel = item => "#" + item.revision_sequence + " · " + (statuses[item.status] || item.status);

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

// A release prepared under an approval of before CR-0001 has its waivers in
// that review, not in the release.
function waiversOf(release) {
  if (release.review_id) return "旧审批";
  if (!release.waivers?.length) return "无";
  const lines = node("div");
  for (const waiver of release.waivers) {
    const line = node("small");
    line.append(node("span", waiver.rule_id, "rule"), " " + waiver.reason);
    lines.append(line);
  }
  return lines;
}

function releaseTable(items) {
  return table("Release", ["版本", "状态", "制品摘要", "准备时间", "豁免"], items.map(item => [
    item.version, releaseStates[item.status] || item.status, code(item.artifact_digest), timestamp(item.prepared_at),
    waiversOf(item)]));
}

// catalogPanel lists the API assets of one workspace with the filters the
// server applies before paging, and opens an asset in place: its revisions,
// a revision's checks, releases and past reviews, and where the asset is
// published. There an author runs checks and a publisher prepares releases
// and publishes them.
export function catalogPanel({ product, productName, workspace, roles, signal }) {
  const author = roles.includes("internal_author");
  const publisher = roles.includes("internal_publisher");
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
  pendingLabel.append(pending, "只看已检查、未准备 Release");
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
    const [revisions, releases] = await Promise.all([
      settle(list(base + "/revisions", current)),
      settle(list(base + "/releases", current)),
    ]);
    if (current.aborted) return;
    let releaseList = releases.value || [];
    let releaseError = releases.error;
    let revisionRequest;
    const revisionBox = node("div");
    const ordered = (revisions.value || []).toSorted((a, b) => b.revision_sequence - a.revision_sequence);
    const sequenceOf = id => ordered.find(item => item.revision_id === id)?.revision_sequence;
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
    const publication = publicationPanel({
      product, productName, asset, releases: releases.value, publisher, signal: current,
      describe: item => item.version + (sequenceOf(item.revision_id) ? " · 修订 #" + sequenceOf(item.revision_id) : ""),
    });
    loading.remove();
    detail.append(revisionSection, revisionBox);
    // A caller may read the releases of an asset without its revisions.
    if (revisions.error && releaseList.length) detail.append(section("Release", releases, releaseTable));
    detail.append(publication.element);
    if (ordered.length) await showRevision(ordered[0]);

    async function showRevision(revision) {
      revisionRequest?.abort();
      const controller = new AbortController();
      revisionRequest = controller;
      const shown = AbortSignal.any([controller.signal, current]);
      for (const button of revisionSection.querySelectorAll("button[data-revision]")) {
        button.setAttribute("aria-pressed", String(button.dataset.revision === revision.revision_id));
      }
      const path = revisionPath(product, revision.api_asset_id, revision.revision_id);
      revisionBox.replaceChildren(node("p", "正在读取修订 #" + revision.revision_sequence + "…", "muted"));
      const [checks, reviews] = await Promise.all([
        settle(list(path + "/check-runs", shown)),
        settle(list(path + "/reviews", shown)),
      ]);
      if (shown.aborted) return;
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

      let checkItems = checks.value || [];
      const checkList = node("div");
      const checkBox = node("div");
      const checkSection = node("section", "", "detail-section");
      checkSection.append(node("h4", "检查"));
      if (author) checkSection.append(checkRunForm());
      checkSection.append(checkList, checkBox);
      block.append(checkSection);
      // Preparing needs the list of the revision's checks to choose from.
      const preparation = publisher && !checks.error ? releaseForm({
        productName, asset, revision, path, releasePath: base + "/releases", sequenceOf, signal: shown, onPrepared: prepared,
      }) : null;
      if (preparation) {
        const preparing = node("section", "", "detail-section");
        preparing.append(node("h4", "准备 Release"), preparation.element);
        block.append(preparing);
      }
      if (checks.error) checkList.replaceChildren(node("p", checks.error.message, "muted"));
      else showChecks(checkItems);

      const releaseBox = node("div");
      const releaseSection = node("section", "", "detail-section");
      releaseSection.append(node("h4", "Release"), releaseBox);
      block.append(releaseSection);
      showReleases();
      if (reviews.error || reviews.value.length) {
        block.append(section("历史评审", reviews, items => table("历史评审", ["状态", "提交人", "提交时间", "审批范围", "批准有效期"],
          items.map(item => [badge(item.status), item.requested_by.subject, timestamp(item.requested_at),
            item.scope.map(scope => scope.audience + " / " + scope.environment).join("，"),
            item.conclusion?.valid_until ? timestamp(item.conclusion.valid_until) : null]))));
      }
      revisionBox.replaceChildren(block);

      async function openCheck(item, button) {
        for (const other of checkList.querySelectorAll("button[data-check]")) {
          other.setAttribute("aria-pressed", String(other === button));
        }
        checkBox.replaceChildren(node("p", "正在读取检查…", "muted"));
        try {
          const result = await api(path + "/check-runs/" + segment(item.check_run_id), { signal: shown });
          if (!shown.aborted && button.getAttribute("aria-pressed") === "true") checkBox.replaceChildren(checkDetail(result));
        } catch (error) {
          if (!shown.aborted) checkBox.replaceChildren(node("p", error.message, "muted"));
        }
      }

      // showChecks lists the checks newest first. A check just run is opened
      // and offered for a release.
      function showChecks(items, opened) {
        checkItems = items.toSorted(newestFirst);
        checkList.replaceChildren(checkItems.length ? table("检查", ["结论", "完成时间", "策略摘要", "兼容性对比", "详情"],
          checkItems.map(item => {
            const view = node("button", "查看", "row-action");
            view.type = "button";
            view.dataset.check = item.check_run_id;
            view.setAttribute("aria-pressed", String(item.check_run_id === opened?.check_run.check_run_id));
            view.onclick = () => openCheck(item, view);
            return [badge(item.status), timestamp(item.completed_at), code(short(item.policy_digest)),
              item.compatibility ? "对比 #" + (sequenceOf(item.compatibility.baseline.revision_id) ?? "?") : "未对比", view];
          }))
          : node("p", "此修订还没有检查。", "muted"));
        if (opened) checkBox.replaceChildren(checkDetail(opened));
        preparation?.setChecks(checkItems, opened?.check_run.check_run_id);
      }

      // Only an author runs a check. Whether it was recorded now or before
      // is what the status of the answer says.
      function checkRunForm() {
        const baseline = node("select");
        fill(baseline, ordered.filter(item => item.revision_id !== revision.revision_id), "revision_id", revisionLabel, "不对比");
        const runButton = node("button", "运行检查");
        runButton.type = "button";
        const controls = node("div", "", "check-run");
        controls.append(field("兼容性对比基线（可选）", baseline, "catalog-baseline"), runButton);
        const status = node("p", "", "muted");
        status.setAttribute("role", "status");
        const box = node("div", "", "check-run-form");
        box.append(controls, node("p", "按现行策略和工具检查此修订。相同修订、基线、策略和工具的检查沿用已有记录；策略更新后再运行会得到新的检查。",
          "hint muted"), status);
        runButton.onclick = async () => {
          runButton.disabled = true;
          status.classList.remove("publication-error");
          status.textContent = "正在运行检查…";
          let url = path + "/check-runs";
          if (baseline.value) url += "?baseline_revision_id=" + segment(baseline.value);
          try {
            const { status: answered, body } = await api(url, { post: true, signal: shown, withStatus: true });
            const listed = await settle(list(path + "/check-runs", shown));
            if (shown.aborted) return;
            const run = body.check_run;
            showChecks(listed.value || [...checkItems.filter(item => item.check_run_id !== run.check_run_id), run], body);
            status.textContent = (answered === 201 ? "检查已记录。" : "相同输入、基线、策略和工具的检查已有记录。") +
              "结论：" + statuses[run.status] + "。";
          } catch (error) {
            if (shown.aborted) return;
            status.textContent = error.message;
            status.classList.add("publication-error");
          } finally {
            if (!shown.aborted) runButton.disabled = false;
          }
        };
        return box;
      }

      function showReleases() {
        const prepared = releaseList.filter(item => item.revision_id === revision.revision_id);
        if (releaseError) releaseBox.replaceChildren(node("p", releaseError.message, "muted"));
        else releaseBox.replaceChildren(prepared.length ? releaseTable(prepared) : node("p", "此修订还没有准备 Release。", "muted"));
      }

      // Should the releases not be read again, the one just prepared is
      // still shown and offered for publication.
      async function prepared(release) {
        const listed = await settle(list(base + "/releases", current));
        if (current.aborted) return;
        releaseList = listed.value || [...releaseList.filter(item => item.release_id !== release.release_id), release];
        if (listed.value) releaseError = undefined;
        showReleases();
        publication.setReleases(releaseList);
      }
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
