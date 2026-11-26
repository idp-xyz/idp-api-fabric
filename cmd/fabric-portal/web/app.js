import { api, assetPath, badge, field, fill, list, node, page, productPath, revisionPath, reviewPath,
  segment, statuses, timestamp } from "./workbench.js";
import { publicationPanel } from "./publication.js";
import { accessBlockPanel } from "./access-blocks.js";
import { catalogPanel } from "./catalog.js";
import { dashboardPanel } from "./dashboard.js";
import { checkDetail } from "./checks.js";

const el = id => document.getElementById(id);
const productSelect = el("product");
const workspaceSelect = el("workspace");
const assetSelect = el("asset");
const revisionSelect = el("revision");
const checkSelect = el("check");
const baselineSelect = el("baseline");
const targetSelect = el("target");
const readers = ["workspace_maintainer", "internal_author", "internal_reviewer", "internal_publisher"];

let productRequest;
let view;
let caller;
let shownView = "dashboard";

function notice(text = "", error = false) {
  const item = el("notice");
  item.hidden = !text;
  item.classList.toggle("error", error);
  item.textContent = text;
}

function report(error) {
  if (error.name === "AbortError") return;
  notice(error.message, true);
}

function showReviewWorkspace(visible) {
  el("workspace-field").hidden = el("views").hidden = !visible;
  showView(visible ? shownView : "");
}

function showView(name) {
  for (const id of ["filter-field", "refresh", "reviews-view"]) el(id).hidden = name !== "reviews";
  el("catalog-view").hidden = name !== "catalog";
  el("dashboard-view").hidden = name !== "dashboard";
  if (!name) return;
  shownView = name;
  for (const mode of ["dashboard", "reviews", "catalog"]) el("view-" + mode).setAttribute("aria-pressed", String(name === mode));
  if (name === "catalog") view?.catalog.start();
  if (name === "dashboard") view?.dashboard.start();
}

async function chooseProduct(preferredWorkspace = "") {
  productRequest?.abort();
  view?.controller.abort();
  view = null;
  notice();
  clearWorkspace();
  showReviewWorkspace(false);
  const security = el("security-controls");
  security.hidden = true;
  security.replaceChildren();
  el("workbench-title").textContent = "契约评审与发布";
  workspaceSelect.replaceChildren();
  workspaceSelect.onchange = null;
  workspaceSelect.disabled = true;
  const product = productSelect.value;
  if (!product) return;
  const controller = new AbortController();
  productRequest = controller;
  const { signal } = controller;
  el("context").textContent = "正在读取产品和工作区…";
  const callerInProduct = await api(productPath(product) + "/caller", { signal });
  if (signal.aborted) return;
  const { roles } = callerInProduct;
  const canSeeWorkspace = roles.includes("product_owner") || callerInProduct.workspaces.length > 0;
  const canBlock = roles.includes("security_admin");
  const canClear = roles.includes("security_reviewer");
  const productName = productSelect.selectedOptions[0].text;
  let blocks;
  if (canBlock || canClear) {
    blocks = accessBlockPanel({ product, productName, caller, canBlock, canClear, signal });
    security.replaceChildren(blocks);
    security.hidden = false;
  }
  const workspaces = canSeeWorkspace ? await list(productPath(product) + "/workspaces", signal) : [];
  if (signal.aborted) return;
  if (!workspaces.length && blocks) {
    el("workbench-title").textContent = "发布访问控制";
    el("context").textContent = productName;
    blocks.open = true;
    return;
  }
  if (!canSeeWorkspace) {
    el("context").textContent = "当前身份在此产品下没有可用的工作区。";
    return;
  }
  showReviewWorkspace(true);
  fill(workspaceSelect, workspaces, "workspace_id", item => item.display_name);
  if (workspaces.some(item => item.workspace_id === preferredWorkspace)) workspaceSelect.value = preferredWorkspace;
  workspaceSelect.onchange = () => chooseWorkspace(product, callerInProduct).catch(report);
  await chooseWorkspace(product, callerInProduct);
}

function clearWorkspace() {
  el("dashboard-view").replaceChildren();
  el("catalog-view").replaceChildren();
  el("queue").replaceChildren();
  el("queue-status").textContent = "";
  el("more").hidden = true;
  el("submission").hidden = true;
  el("submission").open = false;
  el("detail").hidden = true;
  el("evidence").replaceChildren();
}

async function chooseWorkspace(product, callerInProduct) {
  view?.controller.abort();
  view = null;
  clearWorkspace();
  notice();
  const workspace = workspaceSelect.value;
  if (!workspace) {
    el("context").textContent = "当前产品没有可见的工作区。";
    return;
  }
  const roles = callerInProduct.workspaces.find(item => item.workspace_id === workspace)?.roles || [];
  const owner = callerInProduct.roles.includes("product_owner");
  const canReadReviews = owner || roles.some(role => readers.includes(role));
  el("context").textContent = productSelect.selectedOptions[0].text + " / " + workspaceSelect.selectedOptions[0].text;
  const selection = {
    product, workspace, roles, reviews: canReadReviews, controller: new AbortController(), assets: new Map(), targets: [],
  };
  view = selection;
  const { signal } = selection.controller;
  const assetsPath = productPath(product) + "/assets?workspace_id=" + segment(workspace);
  const assets = list(assetsPath, signal).then(items => new Map(items.map(item => [item.api_asset_id, item])));
  assets.catch(() => {});
  selection.catalog = catalogPanel({ product, workspace, signal });
  selection.dashboard = dashboardPanel({
    product, workspace, assets, reviews: canReadReviews, signal,
    openReview: review => {
      showView("reviews");
      showReview(selection, review, true).catch(report);
    },
    openAsset: asset => {
      showView("catalog");
      selection.catalog.open(asset);
    },
  });
  el("catalog-view").replaceChildren(selection.catalog.element);
  el("dashboard-view").replaceChildren(selection.dashboard.element);
  if (shownView === "catalog") selection.catalog.start();
  if (shownView === "dashboard") selection.dashboard.start();
  if (!canReadReviews) {
    el("queue-status").textContent = "当前身份无法读取此工作区的评审。";
    return;
  }
  const author = roles.includes("internal_author");
  const [visible, targets] = await Promise.all([
    assets,
    author ? list(productPath(product) + "/audience-configs", signal) : Promise.resolve([]),
  ]);
  if (signal.aborted) return;
  selection.assets = visible;
  selection.targets = targets;
  if (author) configureSubmission(selection, [...visible.values()]);
  await loadQueue(selection, true);
}

async function loadQueue(selection, reset) {
  selection.queueRequest?.abort();
  const controller = new AbortController();
  selection.queueRequest = controller;
  const signal = AbortSignal.any([controller.signal, selection.controller.signal]);
  if (reset) {
    el("queue").replaceChildren();
    selection.next = "";
    selection.queued = new Set();
  }
  el("more").hidden = true;
  el("queue-status").textContent = "正在加载评审…";
  const filter = el("filter").value;
  const path = productPath(selection.product) + "/workspaces/" + segment(selection.workspace) + "/reviews" +
    (filter ? "?status=" + segment(filter) : "");
  try {
    const result = await page(path, 20, selection.next, signal);
    if (signal.aborted) return;
    for (const review of result.items) {
      if (selection.queued.has(review.review_id)) continue;
      selection.queued.add(review.review_id);
      const title = selection.assets.get(review.api_asset_id)?.display_name || review.api_asset_id;
      const button = node("button");
      button.type = "button";
      button.dataset.review = review.review_id;
      button.setAttribute("aria-pressed", String(selection.selected === review.review_id));
      button.append(node("strong", title), badge(review.status),
        node("span", review.requested_by.subject + " · " + timestamp(review.requested_at), "muted"));
      button.onclick = () => showReview(selection, review, true).catch(report);
      const row = node("li");
      row.append(button);
      el("queue").append(row);
    }
    selection.next = result.next_page_token;
    el("queue-status").textContent = selection.queued.size ? "" : "当前筛选下没有评审。";
  } catch (error) {
    if (!signal.aborted) el("queue-status").textContent = "评审队列读取失败。";
    throw error;
  } finally {
    if (!signal.aborted) el("more").hidden = !selection.next;
  }
}

function configureSubmission(selection, assets) {
  fill(assetSelect, assets.filter(asset => asset.api_kind === "HTTP"), "api_asset_id", item => item.display_name, "请选择资产");
  fill(revisionSelect, [], "", () => "", "请选择修订");
  fill(checkSelect, [], "", () => "", "请选择检查");
  fill(baselineSelect, [], "", () => "", "不对比");
  selection.revisions = [];
  fill(targetSelect, selection.targets, "audience_config_id", item => item.audience + " / " + item.environment);
  el("note").value = "";
  el("submission").hidden = false;
  el("run-check").disabled = true;
  updateRequestButton();
  assetSelect.onchange = () => loadRevisions(selection).catch(report);
  revisionSelect.onchange = () => loadChecks(selection).catch(report);
  checkSelect.onchange = targetSelect.onchange = updateRequestButton;
  el("run-check").onclick = () => runCheck(selection).catch(report);
}

function choiceSignal(selection) {
  selection.choice?.abort();
  selection.choice = new AbortController();
  return AbortSignal.any([selection.choice.signal, selection.controller.signal]);
}

const revisionLabel = item => "#" + item.revision_sequence + " · " + statuses[item.status];

async function loadRevisions(selection) {
  const signal = choiceSignal(selection);
  const asset = assetSelect.value;
  selection.revisions = [];
  fill(revisionSelect, [], "", () => "", "请选择修订");
  fill(checkSelect, [], "", () => "", "请选择检查");
  fill(baselineSelect, [], "", () => "", "不对比");
  el("run-check").disabled = true;
  updateRequestButton();
  if (!asset) return;
  const revisions = await list(assetPath(selection.product, asset) + "/revisions", signal);
  if (signal.aborted) return;
  selection.revisions = revisions;
  const eligible = revisions.filter(revision => ["DRAFT", "APPROVED", "WITHDRAWN"].includes(revision.status));
  fill(revisionSelect, eligible, "revision_id", revisionLabel, eligible.length ? "请选择修订" : "没有可提交的修订");
}

async function loadChecks(selection) {
  const signal = choiceSignal(selection);
  const revision = revisionSelect.value;
  fill(checkSelect, [], "", () => "", "请选择检查");
  const baselines = revision ? selection.revisions.filter(item => item.revision_id !== revision) : [];
  fill(baselineSelect, baselines, "revision_id", revisionLabel, "不对比");
  el("run-check").disabled = !revision;
  updateRequestButton();
  if (!revision) return;
  await listChecks(selection, revision, signal);
}

async function listChecks(selection, revision, signal) {
  const checks = await list(revisionPath(selection.product, assetSelect.value, revision) + "/check-runs", signal);
  if (signal.aborted) return;
  const compared = item => {
    const baseline = selection.revisions.find(other => other.revision_id === item.compatibility?.baseline.revision_id);
    return item.compatibility ? " · 对比 " + (baseline ? "#" + baseline.revision_sequence : "另一修订") : "";
  };
  fill(checkSelect, checks, "check_run_id", item => statuses[item.status] + " · " + timestamp(item.completed_at) + compared(item),
    checks.length ? "请选择检查" : "修订还没有检查证据");
}

// Only an author runs a check; a reviewer reads the evidence and runs none.
async function runCheck(selection) {
  const revision = revisionSelect.value;
  if (!selection.roles.includes("internal_author") || !revision) return;
  const signal = choiceSignal(selection);
  let path = revisionPath(selection.product, assetSelect.value, revision) + "/check-runs";
  if (baselineSelect.value) path += "?baseline_revision_id=" + segment(baselineSelect.value);
  const recorded = new Set(Array.from(checkSelect.options, option => option.value));
  el("run-check").disabled = true;
  notice("正在运行检查…");
  try {
    const { check_run: run } = await api(path, { post: true, signal });
    if (signal.aborted) return;
    await listChecks(selection, revision, signal);
    if (signal.aborted) return;
    checkSelect.value = run.check_run_id;
    updateRequestButton();
    notice((recorded.has(run.check_run_id) ? "相同输入、基线、策略和工具的检查已有记录，已选中。" : "检查已记录并选中。") +
      "结论：" + statuses[run.status] + "。");
  } finally {
    if (!signal.aborted) el("run-check").disabled = false;
    else if (view === selection) notice();
  }
}

function updateRequestButton() {
  el("request").disabled = !assetSelect.value || !revisionSelect.value || !checkSelect.value || !targetSelect.selectedOptions.length;
}

async function submitReview(event) {
  event.preventDefault();
  const selection = view;
  if (!selection?.roles.includes("internal_author") || el("request").disabled) return;
  const scope = Array.from(targetSelect.selectedOptions, option => selection.targets.find(item => item.audience_config_id === option.value))
    .filter(Boolean).map(({ audience, environment }) => ({ audience, environment }));
  if (!scope.length || scope.length > 6) {
    notice("请选择 1 至 6 个审批目标。", true);
    return;
  }
  const data = { check_run_id: checkSelect.value, scope };
  const note = el("note").value.trim();
  if (note) data.note = note;
  el("request").disabled = true;
  try {
    const path = revisionPath(selection.product, assetSelect.value, revisionSelect.value) + "/reviews";
    const result = await api(path, { data, signal: selection.controller.signal });
    if (view !== selection) return;
    el("submission").open = false;
    el("note").value = "";
    el("filter").value = "IN_REVIEW";
    notice("评审已提交。");
    await loadQueue(selection, true);
    await showReview(selection, result, true);
  } finally {
    if (view === selection) updateRequestButton();
  }
}

async function showReview(selection, listing, focus = false) {
  selection.detailRequest?.abort();
  selection.detailRequest = new AbortController();
  const signal = AbortSignal.any([selection.detailRequest.signal, selection.controller.signal]);
  selection.selected = listing.review_id;
  for (const button of el("queue").querySelectorAll("button")) {
    button.setAttribute("aria-pressed", String(button.dataset.review === listing.review_id));
  }
  const evidence = el("evidence");
  el("detail").hidden = false;
  evidence.replaceChildren(node("p", "正在读取评审与检查证据…", "muted"));
  if (focus) el("detail-title").focus();
  const revision = revisionPath(listing.product_id, listing.api_asset_id, listing.revision_id);
  try {
    const [review, recorded, check] = await Promise.all([
      api(reviewPath(listing), { signal }),
      api(revision, { signal }),
      api(revision + "/check-runs/" + segment(listing.check_run_id), { signal }),
    ]);
    if (!signal.aborted) renderEvidence(selection, review, recorded, check, signal);
  } catch (error) {
    if (!signal.aborted) evidence.replaceChildren(node("p", error.message, "muted"));
    throw error;
  }
}

function renderEvidence(selection, review, revision, check, signal) {
  const evidence = el("evidence");
  const asset = selection.assets.get(review.api_asset_id);
  const scope = review.scope.map(target => target.audience + " / " + target.environment).join("，");
  const facts = node("dl", "", "facts");
  const fields = [
    ["修订", "#" + revision.revision_sequence + " · " + revision.entrypoint],
    ["提交人", review.requested_by.subject],
    ["审批范围", scope],
    ["输入摘要", review.input_digest],
    ["检查", statuses[check.check_run.status] + " · " + timestamp(check.check_run.completed_at)],
  ];
  if (review.note) fields.push(["说明", review.note]);
  if (review.conclusion) {
    fields.push(["评审人", review.conclusion.concluded_by.subject]);
    if (review.conclusion.reason) fields.push(["理由", review.conclusion.reason]);
    if (review.conclusion.valid_until) fields.push(["有效期至", timestamp(review.conclusion.valid_until)]);
  }
  for (const [key, value] of fields) facts.append(node("dt", key), node("dd", value));
  evidence.replaceChildren(node("h3", asset?.display_name || review.api_asset_id), badge(review.status), facts);

  const rules = node("details");
  rules.append(node("summary", "检查规则与发现"), checkDetail(check));
  evidence.append(rules);
  if (review.status === "IN_REVIEW" && selection.roles.includes("internal_reviewer")) {
    const ownRequest = review.requested_by.issuer === caller.issuer && review.requested_by.subject === caller.subject;
    if (ownRequest) {
      evidence.append(node("p", "请由独立评审人处理此请求。", "muted"));
    } else {
      evidence.append(decisionForm(selection, review, check));
    }
  }
  evidence.append(publicationPanel(review, {
    publisher: selection.roles.includes("internal_publisher"), signal,
    productName: productSelect.selectedOptions[0].text,
  }));
}

function decisionForm(selection, review, check) {
  const form = node("form", "", "form");
  const outcome = node("select");
  outcome.add(new Option("批准", "APPROVED"));
  outcome.add(new Option("驳回", "REJECTED"));
  const reason = node("input");
  reason.maxLength = 1000;
  const expiry = node("input");
  expiry.type = "datetime-local";
  const expiryLabel = field("批准有效期至（本地时间）", expiry, "decision-expiry");
  const waivers = node("div", "", "form-grid");
  const reasons = [];
  for (const rule of check.outcomes.filter(item => item.waivable && ["FAIL", "UNKNOWN"].includes(item.status))) {
    const input = node("input");
    input.maxLength = 1000;
    reasons.push({ ruleID: rule.rule_id, input });
    waivers.append(field(rule.rule_id + " 豁免理由", input, "waiver-" + rule.rule_id));
  }
  const send = node("button", "记录评审决定", "primary");
  send.type = "submit";
  const status = node("p", "", "muted");
  status.setAttribute("role", "status");
  const sync = () => {
    const approving = outcome.value === "APPROVED";
    expiryLabel.hidden = !approving;
    waivers.hidden = !approving || !reasons.length;
    expiry.required = approving;
    reason.required = !approving;
    for (const { input } of reasons) input.required = approving;
  };
  outcome.onchange = sync;
  sync();
  form.append(field("决定", outcome, "decision-outcome"), field("理由", reason, "decision-reason"), expiryLabel, waivers, send, status);
  form.onsubmit = async event => {
    event.preventDefault();
    if (send.disabled) return;
    const data = { outcome: outcome.value };
    if (reason.value.trim()) data.reason = reason.value.trim();
    if (data.outcome === "REJECTED" && !data.reason) {
      status.textContent = "请填写驳回理由。";
      return;
    }
    if (data.outcome === "APPROVED") {
      const date = new Date(expiry.value);
      if (Number.isNaN(date.valueOf()) || date <= new Date()) {
        status.textContent = "请输入将来的批准到期时间。";
        return;
      }
      data.valid_until = date.toISOString();
      if (reasons.some(({ input }) => !input.value.trim())) {
        status.textContent = "请填写所有必需的豁免理由。";
        return;
      }
      if (reasons.length) {
        data.waivers = reasons.map(({ ruleID, input }) => ({
          rule_id: ruleID, reason: input.value.trim(), expires_at: data.valid_until,
        }));
      }
    }
    send.disabled = true;
    status.textContent = "正在记录评审决定…";
    try {
      const result = await api(reviewPath(review) + "/decision", { data, signal: selection.controller.signal });
      if (view !== selection) return;
      notice("评审决定已记录。");
      if (selection.selected === review.review_id) await showReview(selection, result);
      await loadQueue(selection, true);
    } catch (error) {
      if (error.name !== "AbortError") status.textContent = error.message;
    } finally {
      send.disabled = false;
    }
  };
  return form;
}

async function start() {
  el("context").textContent = "正在验证登录…";
  const identity = await api("/v1/caller");
  caller = identity.principal;
  el("account").textContent = caller.subject;
  const products = await list("/v1/products");
  if (!products.length) {
    el("context").textContent = "当前身份没有可见的产品。";
    return;
  }
  fill(productSelect, products, "product_id", item => item.display_name);
  el("workbench").hidden = false;
  await chooseProduct();
}

productSelect.onchange = () => chooseProduct().catch(report);
el("filter").onchange = () => { if (view?.reviews) loadQueue(view, true).catch(report); };
el("more").onclick = () => { if (view?.reviews) loadQueue(view, false).catch(report); };
el("view-dashboard").onclick = () => showView("dashboard");
el("view-reviews").onclick = () => showView("reviews");
el("view-catalog").onclick = () => showView("catalog");
el("refresh").onclick = () => chooseProduct(workspaceSelect.value).catch(report);
el("request-form").onsubmit = event => submitReview(event).catch(report);
start().catch(error => {
  el("context").textContent = "";
  report(error);
});
