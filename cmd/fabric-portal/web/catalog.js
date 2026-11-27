import {
  api, assetPath, badge, button, card, empty, facts, field, fill, icon, list, loading, node, page, productPath, revisionPath,
  segment, statuses, timestamp,
} from "./workbench.js";
import { checkDetail } from "./checks.js";
import { compareSection } from "./compare.js";
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
const specificationLabel = item => (item.specification === "ASYNCAPI" ? "AsyncAPI " : "OpenAPI ") + item.spec_version;
const lifecycle = value => badge(value, lifecycles[value] || value);

function code(text) {
  return node("code", text, "digest");
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
  if (release.review_id) return badge("LEGACY", "旧审批");
  if (!release.waivers?.length) return "无";
  const lines = node("div", "", "waiver-lines");
  for (const waiver of release.waivers) {
    const line = node("small");
    line.append(node("span", waiver.rule_id, "rule"), " " + waiver.reason);
    lines.append(line);
  }
  return lines;
}

function releaseTable(items) {
  return table("Release", ["版本", "状态", "制品摘要", "准备时间", "豁免"], items.map(item => [
    node("strong", item.version, "version"), badge(item.status, releaseStates[item.status] || item.status), code(item.artifact_digest),
    timestamp(item.prepared_at), waiversOf(item)]));
}

// catalogPanel lists the API assets of one workspace with the filters the
// server applies before paging, and opens an asset in their place: its
// revisions, a revision's checks, releases and past reviews, and where the
// asset is published. There an author runs checks and a publisher prepares
// releases and publishes them. onDetail hears the name of the open asset,
// or nothing once the list is back.
export function catalogPanel({ product, productName, workspace, roles, signal, onDetail = () => {} }) {
  const author = roles.includes("internal_author");
  const publisher = roles.includes("internal_publisher");
  const element = node("div", "", "catalog");

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
  pendingLabel.append(pending, node("span", "只看已检查、未准备 Release"));
  const apply = node("button", "", "primary");
  apply.type = "submit";
  apply.append(icon("filter"), node("span", "筛选"));
  const clear = button("清空筛选", "ghost");
  const search = field("关键字", query, "catalog-query");
  search.classList.add("search-field");
  search.append(icon("search", "icon field-icon"));
  const actions = node("div", "", "filter-actions");
  actions.append(apply, clear);
  const form = node("form", "", "card filter-bar");
  form.setAttribute("role", "search");
  form.setAttribute("aria-label", "筛选 API 资产");
  form.append(search, field("类型", kind, "catalog-kind"), field("最近检查", check, "catalog-check"), pendingLabel, actions);

  const status = node("p");
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
  const assets = node("table", "", "data assets");
  assets.append(node("caption", "API 资产"), header, rows);
  const wrap = node("div", "", "table-wrap");
  wrap.hidden = true;
  wrap.append(assets);
  const more = button("加载更多", "secondary more");
  more.hidden = true;
  const listCard = card("API 资产", { level: "h2", iconName: "asset", className: "asset-list" });
  const listHeading = listCard.querySelector(".card-title");
  listHeading.id = "catalog-title";
  listHeading.tabIndex = -1;
  listCard.setAttribute("aria-labelledby", listHeading.id);
  listCard.append(wrap, status, more);
  const listView = node("div", "", "catalog-list");
  listView.append(form, listCard);
  const detail = node("div", "", "asset-detail");
  detail.hidden = true;
  element.append(listView, detail);

  let started = false;
  let request;
  let detailRequest;
  let next = "";
  let filters;
  let opened = "";

  function say(kind, text = "") {
    status.className = { loading: "loading", empty: "empty", error: "notice error" }[kind] || "";
    status.textContent = text;
  }

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
    wrap.hidden = !rows.children.length;
    say("loading", "正在加载资产…");
    try {
      const result = await page(productPath(product) + "/assets?" + filters, 20, next, current);
      if (current.aborted) return;
      for (const asset of result.items) rows.append(assetRow(asset));
      next = result.next_page_token || "";
      if (rows.children.length) say("");
      else say("empty", "当前筛选下没有可见的资产。");
    } catch (error) {
      if (!current.aborted) say("error", error.message);
    } finally {
      if (!current.aborted) {
        more.hidden = !next;
        wrap.hidden = !rows.children.length;
      }
    }
  }

  function assetRow(asset) {
    const overview = asset.overview;
    const open = node("button", asset.display_name, "row-link");
    open.type = "button";
    open.dataset.asset = asset.api_asset_id;
    open.setAttribute("aria-pressed", "false");
    open.onclick = () => showAsset(asset);
    const name = node("div", "", "asset-name");
    name.append(open, node("small", asset.asset_key, "muted"));
    const revision = overview?.latest_revision;
    const latestCheck = overview?.latest_check;
    let revisionCell = overview ? "暂无修订" : null;
    if (revision) {
      revisionCell = node("span", "", "inline");
      revisionCell.append(node("span", "#" + revision.revision_sequence, "sequence"), badge(revision.status));
    }
    const release = node("span", "", "inline");
    if (overview?.latest_release) release.append(node("span", overview.latest_release.version, "version"));
    if (overview?.release_pending) release.append(badge("RELEASE_PENDING", "待准备"));
    const cells = [
      name, asset.domain_key, kinds[asset.api_kind] || asset.api_kind, lifecycle(asset.lifecycle),
      revisionCell, latestCheck ? badge(latestCheck.status) : null, release.childNodes.length ? release : null,
    ];
    const row = node("tr");
    for (const value of cells) {
      const cell = node("td");
      cell.append(value ?? "—");
      row.append(cell);
    }
    return row;
  }

  // releasedFrom brings an asset's row up to date with a release just
  // prepared, as listAssets would answer now: the release is the latest, and
  // the latest revision waits for none once it is prepared from that revision.
  function releasedFrom(asset, release) {
    const overview = asset.overview;
    const row = rows.querySelector("button[data-asset=\"" + CSS.escape(asset.api_asset_id) + "\"]")?.closest("tr");
    if (!overview || !row) return;
    asset.overview = {
      ...overview,
      latest_release: { release_id: release.release_id, revision_id: release.revision_id, version: release.version, prepared_at: release.prepared_at },
      release_pending: overview.release_pending && overview.latest_revision?.revision_id !== release.revision_id,
    };
    const replacement = assetRow(asset);
    replacement.querySelector("button[data-asset]").setAttribute("aria-pressed", String(opened === asset.api_asset_id));
    row.replaceWith(replacement);
  }

  // closeAsset leaves the asset and brings the list back as it was.
  function closeAsset() {
    detailRequest?.abort();
    opened = "";
    detail.hidden = true;
    detail.replaceChildren();
    listView.hidden = false;
    for (const item of rows.querySelectorAll("button[data-asset]")) item.setAttribute("aria-pressed", "false");
    onDetail("");
  }

  function back() {
    const left = opened;
    closeAsset();
    const row = rows.querySelector("button[data-asset=\"" + CSS.escape(left) + "\"]");
    (row || listHeading).focus();
  }

  function section(text, iconName, outcome, render) {
    const block = card(text, { iconName });
    if (outcome.error) block.append(node("p", outcome.error.message, "notice"));
    else block.append(render(outcome.value));
    return block;
  }

  async function showAsset(asset) {
    detailRequest?.abort();
    const controller = new AbortController();
    detailRequest = controller;
    const current = AbortSignal.any([controller.signal, signal]);
    opened = asset.api_asset_id;
    for (const item of rows.querySelectorAll("button[data-asset]")) {
      item.setAttribute("aria-pressed", String(item.dataset.asset === asset.api_asset_id));
    }
    const leave = button("返回 API 目录", "ghost back", "back");
    leave.onclick = back;
    const title = node("h2", asset.display_name, "asset-title");
    title.tabIndex = -1;
    const mark = node("span", "", "asset-mark");
    mark.append(icon("asset"));
    const naming = node("div", "", "asset-naming");
    naming.append(title, node("p", asset.asset_key, "muted"));
    const tags = node("div", "", "inline");
    tags.append(badge(asset.api_kind, kinds[asset.api_kind] || asset.api_kind), lifecycle(asset.lifecycle));
    const top = node("div", "", "asset-hero-head");
    top.append(mark, naming, tags);
    const hero = node("section", "", "card asset-hero");
    hero.setAttribute("aria-label", asset.display_name);
    hero.append(top, facts([
      ["键", asset.asset_key], ["领域", asset.domain_key],
      ["类型", kinds[asset.api_kind] || asset.api_kind], ["生命周期", lifecycles[asset.lifecycle] || asset.lifecycle],
      ["资产 ID", code(asset.api_asset_id)], ["登记时间", timestamp(asset.created_at)],
    ], "facts grid"));
    const reading = loading("正在读取修订和发布…");
    reading.setAttribute("role", "status");
    detail.replaceChildren(leave, hero, reading);
    listView.hidden = true;
    detail.hidden = false;
    onDetail(asset.display_name);
    scrollTo({ top: 0 });
    title.focus({ preventScroll: true });

    const base = assetPath(product, asset.api_asset_id);
    const [revisions, releases] = await Promise.all([
      settle(list(base + "/revisions", current)),
      settle(list(base + "/releases", current)),
    ]);
    if (current.aborted) return;
    let releaseList = releases.value || [];
    let releaseError = releases.error;
    let revisionRequest;
    const revisionBox = node("div", "", "revision-panel");
    const ordered = (revisions.value || []).toSorted((a, b) => b.revision_sequence - a.revision_sequence);
    const sequenceOf = id => ordered.find(item => item.revision_id === id)?.revision_sequence;
    const revisionSection = section("修订", "revision", revisions, () => {
      if (!ordered.length) return empty("此资产还没有修订。");
      return table("修订", ["修订", "状态", "来源", "规范版本", "源文件摘要", "引用闭包摘要", "导入时间"], ordered.map(item => {
        const pick = node("button", "#" + item.revision_sequence, "row-link");
        pick.type = "button";
        pick.dataset.revision = item.revision_id;
        pick.setAttribute("aria-pressed", "false");
        pick.onclick = () => showRevision(item);
        return [pick, badge(item.status), sources[item.source_kind] || item.source_kind, specificationLabel(item),
          code(short(item.source_digest)), code(short(item.ref_closure_digest)), timestamp(item.created_at)];
      }));
    });
    if (ordered.length) revisionSection.firstChild.append(node("span", "从新到旧 · 选择修订查看检查和 Release", "hint"));
    const publication = publicationPanel({
      product, productName, asset, releases: releases.value, publisher, signal: current,
      describe: item => item.version + (sequenceOf(item.revision_id) ? " · 修订 #" + sequenceOf(item.revision_id) : ""),
    });
    reading.remove();
    detail.append(revisionSection, revisionBox);
    // A caller may read the releases of an asset without its revisions.
    if (revisions.error && releaseList.length) detail.append(section("Release", "release", releases, releaseTable));
    detail.append(publication.element);
    if (ordered.length) await showRevision(ordered[0]);

    async function showRevision(revision) {
      revisionRequest?.abort();
      const controller = new AbortController();
      revisionRequest = controller;
      const shown = AbortSignal.any([controller.signal, current]);
      for (const item of revisionSection.querySelectorAll("button[data-revision]")) {
        item.setAttribute("aria-pressed", String(item.dataset.revision === revision.revision_id));
      }
      const path = revisionPath(product, revision.api_asset_id, revision.revision_id);
      revisionBox.replaceChildren(loading("正在读取修订 #" + revision.revision_sequence + "…"));
      const [checks, reviews] = await Promise.all([
        settle(list(path + "/check-runs", shown)),
        settle(list(path + "/reviews", shown)),
      ]);
      if (shown.aborted) return;
      const origin = revision.git_origin;
      const summary = card("修订 #" + revision.revision_sequence, { iconName: "revision", className: "revision-summary" });
      summary.append(facts([
        ["状态", badge(revision.status)],
        ["来源", origin ? "Git " + origin.repository + " @ " + origin.commit + (origin.ref ? " (" + origin.ref + ")" : "") :
          sources[revision.source_kind] || revision.source_kind],
        ["规范版本", specificationLabel(revision)],
        ["导入时间", timestamp(revision.created_at)],
        ["修订 ID", code(revision.revision_id)],
        ["入口文件", code(revision.entrypoint)],
        ["源文件摘要", code(revision.source_digest)],
        ["引用闭包摘要", code(revision.ref_closure_digest)],
      ], "facts grid"));

      let checkItems = checks.value || [];
      const checkList = node("div");
      const checkBox = node("div");
      const checkSection = card("检查", { iconName: "check" });
      if (author) checkSection.append(checkRunForm());
      checkSection.append(checkList, checkBox);
      const pair = node("div", "", "split");
      pair.append(compareSection({
        path, revision, others: ordered.filter(item => item.revision_id !== revision.revision_id), signal: shown,
      }));
      // Preparing needs the list of the revision's checks to choose from.
      const preparation = publisher && !checks.error ? releaseForm({
        productName, asset, revision, path, releasePath: base + "/releases", sequenceOf, signal: shown, onPrepared: prepared,
      }) : null;
      if (preparation) {
        const preparing = card("准备 Release", { iconName: "release" });
        preparing.append(preparation.element);
        pair.append(preparing);
      }
      if (checks.error) checkList.replaceChildren(node("p", checks.error.message, "notice"));
      else showChecks(checkItems);

      const releaseBox = node("div");
      const releaseSection = card("Release", { iconName: "release" });
      releaseSection.append(releaseBox);
      const parts = [summary, checkSection, pair, releaseSection];
      showReleases();
      if (reviews.error || reviews.value.length) {
        parts.push(section("历史评审", "history", reviews, items => table("历史评审", ["状态", "提交人", "提交时间", "审批范围", "批准有效期"],
          items.map(item => [badge(item.status), item.requested_by.subject, timestamp(item.requested_at),
            item.scope.map(scope => scope.audience + " / " + scope.environment).join("，"),
            item.conclusion?.valid_until ? timestamp(item.conclusion.valid_until) : null]))));
      }
      revisionBox.replaceChildren(...parts);

      async function openCheck(item, view) {
        for (const other of checkList.querySelectorAll("button[data-check]")) {
          other.setAttribute("aria-pressed", String(other === view));
        }
        checkBox.replaceChildren(loading("正在读取检查…"));
        try {
          const result = await api(path + "/check-runs/" + segment(item.check_run_id), { signal: shown });
          if (!shown.aborted && view.getAttribute("aria-pressed") === "true") checkBox.replaceChildren(checkDetail(result));
        } catch (error) {
          if (!shown.aborted) checkBox.replaceChildren(node("p", error.message, "notice"));
        }
      }

      // showChecks lists the checks newest first. A check just run is opened
      // and offered for a release.
      function showChecks(items, opened) {
        checkItems = items.toSorted(newestFirst);
        checkList.replaceChildren(checkItems.length ? table("检查", ["结论", "完成时间", "策略摘要", "兼容性对比", "详情"],
          checkItems.map(item => {
            const view = node("button", "查看", "small secondary toggle");
            view.type = "button";
            view.dataset.check = item.check_run_id;
            view.setAttribute("aria-pressed", String(item.check_run_id === opened?.check_run.check_run_id));
            view.onclick = () => openCheck(item, view);
            return [badge(item.status), timestamp(item.completed_at), code(short(item.policy_digest)),
              item.compatibility ? "对比 #" + (sequenceOf(item.compatibility.baseline.revision_id) ?? "?") : "未对比", view];
          }))
          : empty("此修订还没有检查。"));
        if (opened) checkBox.replaceChildren(checkDetail(opened));
        preparation?.setChecks(checkItems, opened?.check_run.check_run_id);
      }

      // Only an author runs a check. Whether it was recorded now or before
      // is what the status of the answer says.
      function checkRunForm() {
        const baseline = node("select");
        fill(baseline, ordered.filter(item => item.revision_id !== revision.revision_id), "revision_id", revisionLabel, "不对比");
        const runButton = button("运行检查", "primary", "check");
        const controls = node("div", "", "control-row");
        controls.append(field("兼容性对比基线（可选）", baseline, "catalog-baseline"), runButton);
        const status = node("p", "", "status-line");
        status.setAttribute("role", "status");
        const box = node("div", "", "tool-box");
        box.append(controls, node("p", "按现行策略和工具检查此修订。相同修订、基线、策略和工具的检查沿用已有记录；策略更新后再运行会得到新的检查。",
          "hint"), status);
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
        if (releaseError) releaseBox.replaceChildren(node("p", releaseError.message, "notice"));
        else releaseBox.replaceChildren(prepared.length ? releaseTable(prepared) : empty("此修订还没有准备 Release。"));
      }

      // Should the releases not be read again, the one just prepared is
      // still shown and offered for publication.
      async function prepared(release) {
        releasedFrom(asset, release);
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
