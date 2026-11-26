import { api, fill, list, productPath, segment } from "./workbench.js";
import { accessBlockPanel } from "./access-blocks.js";
import { catalogPanel } from "./catalog.js";
import { dashboardPanel } from "./dashboard.js";

const el = id => document.getElementById(id);
const productSelect = el("product");
const workspaceSelect = el("workspace");

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

function showWorkspace(visible) {
  el("workspace-field").hidden = el("views").hidden = !visible;
  showView(visible ? shownView : "");
}

function showView(name) {
  el("catalog-view").hidden = name !== "catalog";
  el("dashboard-view").hidden = name !== "dashboard";
  if (!name) return;
  shownView = name;
  for (const mode of ["dashboard", "catalog"]) el("view-" + mode).setAttribute("aria-pressed", String(name === mode));
  if (name === "catalog") view?.catalog.start();
  if (name === "dashboard") view?.dashboard.start();
}

async function chooseProduct() {
  productRequest?.abort();
  view?.controller.abort();
  view = null;
  notice();
  clearWorkspace();
  showWorkspace(false);
  const security = el("security-controls");
  security.hidden = true;
  security.replaceChildren();
  el("workbench-title").textContent = "契约检查与发布";
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
  showWorkspace(true);
  fill(workspaceSelect, workspaces, "workspace_id", item => item.display_name);
  workspaceSelect.onchange = () => chooseWorkspace(product, callerInProduct);
  chooseWorkspace(product, callerInProduct);
}

function clearWorkspace() {
  el("dashboard-view").replaceChildren();
  el("catalog-view").replaceChildren();
}

// The catalog and the dashboard read on their own and report in place. The
// roles of the workspace decide what the catalog lets the caller do.
function chooseWorkspace(product, callerInProduct) {
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
  const productName = productSelect.selectedOptions[0].text;
  el("context").textContent = productName + " / " + workspaceSelect.selectedOptions[0].text;
  const selection = { controller: new AbortController() };
  view = selection;
  const { signal } = selection.controller;
  const assetsPath = productPath(product) + "/assets?workspace_id=" + segment(workspace);
  const assets = list(assetsPath, signal).then(items => new Map(items.map(item => [item.api_asset_id, item])));
  assets.catch(() => {});
  selection.catalog = catalogPanel({ product, productName, workspace, roles, signal });
  selection.dashboard = dashboardPanel({
    product, workspace, assets, signal,
    openAsset: asset => {
      showView("catalog");
      selection.catalog.open(asset);
    },
  });
  el("catalog-view").replaceChildren(selection.catalog.element);
  el("dashboard-view").replaceChildren(selection.dashboard.element);
  if (shownView === "catalog") selection.catalog.start();
  if (shownView === "dashboard") selection.dashboard.start();
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
el("view-dashboard").onclick = () => showView("dashboard");
el("view-catalog").onclick = () => showView("catalog");
start().catch(error => {
  el("context").textContent = "";
  report(error);
});
