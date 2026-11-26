import { button, empty, icon, list, node, page, productPath, segment, statuses, timestamp } from "./workbench.js";

const settle = promise => promise.then(value => ({ value }), error => ({ error }));
const limit = 10;
const latest = (asset, detail) => {
  const revision = asset.overview?.latest_revision;
  return revision ? "#" + revision.revision_sequence + " · " + detail : detail;
};

// dashboardPanel gathers what needs someone's attention in one workspace:
// assets whose latest check failed, checked revisions without a release, and
// published slots that are blocked. Each item opens the asset in the catalog.
// Above the lists, a row of counts sums them up with the workspace's assets.
export function dashboardPanel({ product, workspace, assets, signal, openAsset }) {
  const element = node("section", "", "dashboard");
  element.setAttribute("aria-labelledby", "dashboard-title");
  const title = node("div", "", "section-head");
  const heading = node("h2", "待处理事项");
  heading.id = "dashboard-title";
  const refresh = button("刷新", "secondary", "refresh");
  const status = node("p", "", "loading");
  status.setAttribute("role", "status");
  const intro = node("div");
  intro.append(heading, status);
  title.append(intro, refresh);
  const kpis = node("ul", "", "kpis");
  kpis.setAttribute("aria-label", "事项计数");
  const cards = node("div", "", "dashboard-cards");
  element.append(title, kpis, cards);

  let started = false;
  let request;

  const tones = { assets: "info", failing: "danger", pending: "warn", problems: "danger" };
  const symbols = { assets: "asset", failing: "alert", pending: "pending", problems: "lock" };

  function kpi(kind, label, value) {
    const item = node("li", "", "kpi");
    item.dataset.tone = value === "0" || value === "—" ? "calm" : tones[kind];
    const mark = node("span", "", "kpi-icon");
    mark.append(icon(symbols[kind]));
    item.append(mark, node("strong", value, "kpi-value"), node("span", label, "kpi-label"));
    return item;
  }

  const counted = outcome => outcome.error ? "—" : outcome.value.items.length + (outcome.value.more ? "+" : "");

  // Until the first answer the counts and lists keep their place.
  function placeholders() {
    if (kpis.childElementCount) return;
    for (let index = 0; index < 4; index++) {
      const item = node("li", "", "kpi placeholder");
      item.setAttribute("aria-hidden", "true");
      item.append(node("span", "", "bar short"), node("span", "", "bar"));
      kpis.append(item);
    }
    for (let index = 0; index < 3; index++) {
      const box = node("div", "", "card placeholder");
      box.setAttribute("aria-hidden", "true");
      box.append(node("span", "", "bar short"), node("span", "", "bar"), node("span", "", "bar"));
      cards.append(box);
    }
  }

  function card(kind, text, outcome, emptyText, render) {
    const box = node("section", "", "card dashboard-card");
    box.dataset.tone = tones[kind];
    const head = node("div", "", "card-head");
    const name = node("h3", "", "card-title");
    name.append(icon(symbols[kind]), node("span", text));
    head.append(name);
    box.append(head);
    if (outcome.error) {
      box.append(node("p", outcome.error.message, "notice error"));
      return box;
    }
    const { items, more } = outcome.value;
    head.append(node("span", items.length + (more ? "+" : ""), "count"));
    if (!items.length) {
      const none = empty(emptyText);
      none.prepend(icon("done"));
      box.append(none);
      return box;
    }
    const entries = node("ol", "", "dashboard-items");
    items.forEach((item, index) => {
      const entry = node("li");
      entry.append(node("span", String(index + 1).padStart(2, "0"), "ordinal"), render(item));
      entries.append(entry);
    });
    box.append(entries);
    if (more) box.append(node("p", "仅列出前 " + items.length + " 项。", "hint"));
    return box;
  }

  function action(label, detail, onclick) {
    const item = node("div", "", "dashboard-item");
    const text = node("div", "", "item-text");
    text.append(node("strong", label), node("span", detail, "muted"));
    const open = button("查看", "small secondary");
    open.setAttribute("aria-label", "查看 " + label);
    open.onclick = onclick;
    item.append(text, open);
    return item;
  }

  async function assetPage(filter, current) {
    const query = new URLSearchParams({ workspace_id: workspace, ...filter });
    const result = await page(productPath(product) + "/assets?" + query, limit, "", current);
    return { items: result.items, more: Boolean(result.next_page_token) };
  }

  // Slots carry no workspace, so the slots of every target are read and only
  // those of this workspace's assets are kept.
  async function slotProblems(names, current) {
    const targets = await list(productPath(product) + "/publication-targets", current);
    const found = [];
    for (const target of targets) {
      const slots = await list(productPath(product) + "/publication-targets/" + segment(target.publication_target_id) +
        "/assets", current);
      for (const slot of slots) {
        if (names.has(slot.api_asset_id) && slot.blocked) found.push({ slot, target });
      }
    }
    return { items: found.slice(0, limit), more: found.length > limit };
  }

  async function load() {
    request?.abort();
    const controller = new AbortController();
    request = controller;
    const current = AbortSignal.any([controller.signal, signal]);
    refresh.disabled = true;
    status.className = "loading";
    status.textContent = "正在汇总…";
    placeholders();
    try {
      const names = await assets;
      if (current.aborted) return;
      const [failing, pending, problems] = await Promise.all([
        settle(assetPage({ check_status: "FAIL" }, current)),
        settle(assetPage({ release_pending: "true" }, current)),
        settle(slotProblems(names, current)),
      ]);
      if (current.aborted) return;
      const nameOf = id => names.get(id)?.display_name || id;
      kpis.replaceChildren(
        kpi("assets", "Workspace API 资产", String(names.size)),
        kpi("failing", "最近检查失败", counted(failing)),
        kpi("pending", "已检查、待准备 Release", counted(pending)),
        kpi("problems", "发布需要处理", counted(problems)),
      );
      cards.replaceChildren(
        card("failing", "最近检查失败", failing, "没有最新检查失败的资产。", asset => action(asset.display_name,
          latest(asset, timestamp(asset.overview?.latest_check?.completed_at ?? asset.created_at)), () => openAsset(asset))),
        card("pending", "已检查、待准备 Release", pending, "没有等待准备 Release 的修订。", asset => action(asset.display_name,
          latest(asset, "检查" + (statuses[asset.overview?.latest_check?.status] || "")), () => openAsset(asset))),
        card("problems", "发布需要处理", problems, "没有被阻断的发布。", ({ slot, target }) => action(nameOf(slot.api_asset_id),
          target.audience + " / " + target.environment + " · 已阻断", () => openAsset(names.get(slot.api_asset_id)))),
      );
      status.textContent = "";
    } catch (error) {
      if (!current.aborted) {
        status.className = "notice error";
        status.textContent = error.message;
        for (const item of element.querySelectorAll(".placeholder")) item.remove();
      }
    } finally {
      if (!current.aborted) refresh.disabled = false;
    }
  }

  refresh.onclick = () => load();

  return {
    element,
    start() {
      if (started) return;
      started = true;
      load();
    },
  };
}
