import { api, fill, list, productPath, segment } from "./workbench.js";
import { accessBlockPanel } from "./access-blocks.js";
import { catalogPanel } from "./catalog.js";
import { dashboardPanel } from "./dashboard.js";

const el = id => document.getElementById(id);
const productSelect = el("product");
const workspaceSelect = el("workspace");

// Each view is one entry of the sidebar. The workspace views need a
// workspace; access control is for the security roles of the product.
const pages = {
  dashboard: {
    title: "工作台",
    subtitle: "当前 Workspace 等待处理的事项：最新检查失败的资产、已检查但还没准备 Release 的修订，以及被访问阻断的发布。",
  },
  catalog: {
    title: "API 目录",
    subtitle: "当前 Workspace 的 API 资产。筛选和分页由 Fabric API 完成；打开资产查看修订、检查、Release 和发布。",
  },
  security: {
    title: "发布访问控制",
    subtitle: "阻断整个发布目标、其中的 API 资产或单个 Release，由另一位安全评审人解除。源站阻断、边缘传播和发布槽位分开显示。",
  },
};
const defaultTitle = "契约检查与发布";

let productRequest;
let view;
let caller;
let blocks;
let shownView = "dashboard";
let detailName = "";
const available = new Set();

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

function heading(title, subtitle = "") {
  el("workbench-title").textContent = title;
  el("subtitle").textContent = subtitle;
  el("subtitle").hidden = !subtitle;
}

function scope(text) {
  el("eyebrow").textContent = text;
  el("scope-foot").textContent = text;
}

// The breadcrumb ends with the open asset while the catalog shows one.
function trail() {
  const detail = shownView === "catalog" && !el("catalog-view").hidden ? detailName : "";
  const current = el("crumb-detail");
  current.textContent = detail;
  current.hidden = !detail;
  el("crumb-view").textContent = available.has(shownView) ? pages[shownView].title : defaultTitle;
  for (const item of [el("crumb-view"), current]) item.removeAttribute("aria-current");
  (detail ? current : el("crumb-view")).setAttribute("aria-current", "page");
}

function offer(names) {
  available.clear();
  for (const name of names) available.add(name);
  el("nav-workspace").hidden = !available.has("dashboard");
  el("nav-product").hidden = !available.has("security");
}

function showView(name) {
  if (name && !available.has(name)) name = [...available][0] || "";
  for (const mode of Object.keys(pages)) {
    el(mode + "-view").hidden = name !== mode;
    const item = el("nav-" + mode);
    if (name === mode) item.setAttribute("aria-current", "page");
    else item.removeAttribute("aria-current");
  }
  if (name) {
    shownView = name;
    heading(pages[name].title, pages[name].subtitle);
  }
  trail();
  if (name === "catalog") view?.catalog.start();
  if (name === "dashboard") view?.dashboard.start();
  if (name === "security") blocks?.start();
}

// On a narrow screen the sidebar is a drawer over the page, closed by a
// choice in it, by Escape or by a tap beside it.
const narrow = matchMedia("(max-width: 48rem)");
const menu = el("menu");
const isOpen = () => document.body.classList.contains("drawer-open");

function drawer(open, restore = true) {
  const wasOpen = isOpen();
  document.body.classList.toggle("drawer-open", open);
  menu.setAttribute("aria-expanded", String(open));
  menu.setAttribute("aria-label", open ? "关闭导航" : "打开导航");
  el("shade").hidden = !open;
  el("main-area").inert = open;
  if (open) (el("sidebar").querySelector(".nav-item[aria-current]") || el("sidebar").querySelector(".nav-item:not([hidden])"))?.focus();
  else if (wasOpen && restore) menu.focus();
}

function navigate(name) {
  showView(name);
  if (isOpen()) {
    drawer(false, false);
    el("main").focus();
  }
}

async function chooseProduct() {
  productRequest?.abort();
  view?.controller.abort();
  view = null;
  blocks = null;
  notice();
  clearWorkspace();
  el("security-view").replaceChildren();
  el("workspace-field").hidden = true;
  offer([]);
  showView("");
  heading(defaultTitle);
  scope("");
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
  if (canBlock || canClear) {
    blocks = accessBlockPanel({ product, productName, caller, canBlock, canClear, signal });
    el("security-view").replaceChildren(blocks.element);
  }
  const workspaces = canSeeWorkspace ? await list(productPath(product) + "/workspaces", signal) : [];
  if (signal.aborted) return;
  if (!workspaces.length && blocks) {
    el("context").textContent = "";
    scope(productName);
    offer(["security"]);
    showView("security");
    return;
  }
  if (!canSeeWorkspace) {
    el("context").textContent = "当前身份在此产品下没有可用的工作区。";
    return;
  }
  el("context").textContent = "";
  el("workspace-field").hidden = false;
  offer(blocks ? ["dashboard", "catalog", "security"] : ["dashboard", "catalog"]);
  showView(shownView);
  fill(workspaceSelect, workspaces, "workspace_id", item => item.display_name);
  workspaceSelect.onchange = () => chooseWorkspace(product, callerInProduct);
  chooseWorkspace(product, callerInProduct);
}

function clearWorkspace() {
  detailName = "";
  el("dashboard-view").replaceChildren();
  el("catalog-view").replaceChildren();
  trail();
}

// The catalog and the dashboard read on their own and report in place. The
// roles of the workspace decide what the catalog lets the caller do.
function chooseWorkspace(product, callerInProduct) {
  view?.controller.abort();
  view = null;
  clearWorkspace();
  notice();
  const workspace = workspaceSelect.value;
  const productName = productSelect.selectedOptions[0].text;
  if (!workspace) {
    scope(productName);
    el("context").textContent = "当前产品没有可见的工作区。";
    return;
  }
  const roles = callerInProduct.workspaces.find(item => item.workspace_id === workspace)?.roles || [];
  el("context").textContent = "";
  scope(productName + " · " + workspaceSelect.selectedOptions[0].text);
  const selection = { controller: new AbortController() };
  view = selection;
  const { signal } = selection.controller;
  const assetsPath = productPath(product) + "/assets?workspace_id=" + segment(workspace);
  const assets = list(assetsPath, signal).then(items => new Map(items.map(item => [item.api_asset_id, item])));
  assets.catch(() => {});
  selection.catalog = catalogPanel({
    product, productName, workspace, roles, signal,
    onDetail: name => {
      detailName = name;
      trail();
    },
  });
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
  el("account").textContent = el("account-foot").textContent = caller.subject;
  el("avatar").textContent = caller.subject.slice(0, 1).toUpperCase();
  el("account-chip").hidden = el("sidebar-foot").hidden = false;
  const products = await list("/v1/products");
  if (!products.length) {
    el("context").textContent = "当前身份没有可见的产品。";
    return;
  }
  fill(productSelect, products, "product_id", item => item.display_name);
  el("scope").hidden = false;
  el("workbench").hidden = false;
  await chooseProduct();
}

productSelect.onchange = () => chooseProduct().catch(report);
for (const name of Object.keys(pages)) el("nav-" + name).onclick = () => navigate(name);
menu.onclick = () => drawer(!isOpen());
el("shade").onclick = () => drawer(false);
document.addEventListener("keydown", event => {
  if (event.key === "Escape" && isOpen()) drawer(false);
});
narrow.addEventListener("change", () => {
  if (!narrow.matches && isOpen()) drawer(false, false);
});
start().catch(error => {
  el("context").textContent = "";
  report(error);
});
