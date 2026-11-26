import { list, node, page, productPath, segment, statuses, timestamp } from "./workbench.js";

const settle = promise => promise.then(value => ({ value }), error => ({ error }));
const limit = 10;
const latest = (asset, detail) => {
  const revision = asset.overview?.latest_revision;
  return revision ? "#" + revision.revision_sequence + " · " + detail : detail;
};

// dashboardPanel gathers what needs someone's attention in one workspace:
// reviews waiting for a decision, assets whose latest check failed, approved
// revisions without a release, and published slots that are blocked or whose
// approval no longer holds. Each item opens where it is handled.
export function dashboardPanel({ product, workspace, assets, reviews, signal, openReview, openAsset }) {
  const element = node("section", "", "dashboard");
  element.setAttribute("aria-labelledby", "dashboard-title");
  const title = node("div", "", "dashboard-title");
  const heading = node("h2", "待处理事项");
  heading.id = "dashboard-title";
  const refresh = node("button", "刷新");
  refresh.type = "button";
  title.append(heading, refresh);
  const status = node("p", "", "muted");
  status.setAttribute("role", "status");
  const cards = node("div", "", "dashboard-cards");
  element.append(title, status, cards);

  let started = false;
  let request;

  function card(text, outcome, empty, render) {
    const box = node("section", "", "panel dashboard-card");
    const head = node("div", "", "panel-title");
    const name = node("h3", text);
    head.append(name);
    box.append(head);
    const body = node("div", "", "panel-body");
    box.append(body);
    if (outcome.error) {
      body.append(node("p", outcome.error.message, "muted"));
      return box;
    }
    const { items, more } = outcome.value;
    name.append(" ", node("span", items.length + (more ? "+" : ""), "count"));
    if (!items.length) {
      body.append(node("p", empty, "muted"));
      return box;
    }
    const entries = node("ul", "", "dashboard-items");
    for (const item of items) {
      const entry = node("li");
      entry.append(render(item));
      entries.append(entry);
    }
    body.append(entries);
    if (more) body.append(node("p", "仅列出前 " + items.length + " 项。", "muted"));
    return box;
  }

  function action(label, detail, onclick) {
    const button = node("button", "", "dashboard-item");
    button.type = "button";
    button.append(node("strong", label), node("span", detail, "muted"));
    button.onclick = onclick;
    return button;
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
        if (names.has(slot.api_asset_id) && (slot.blocked || slot.approval_problem)) found.push({ slot, target });
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
    status.textContent = "正在汇总…";
    try {
      const names = await assets;
      if (current.aborted) return;
      const reviewPath = productPath(product) + "/workspaces/" + segment(workspace) + "/reviews?status=IN_REVIEW";
      const [waiting, failing, pending, problems] = await Promise.all([
        reviews ? settle(page(reviewPath, limit, "", current).then(result => ({
          items: result.items, more: Boolean(result.next_page_token),
        }))) : { error: new Error("当前身份无法读取此工作区的评审。") },
        settle(assetPage({ check_status: "FAIL" }, current)),
        settle(assetPage({ release_pending: "true" }, current)),
        settle(slotProblems(names, current)),
      ]);
      if (current.aborted) return;
      const nameOf = id => names.get(id)?.display_name || id;
      cards.replaceChildren(
        card("待评审", waiting, "没有等待决定的评审。", review => action(nameOf(review.api_asset_id),
          review.requested_by.subject + " · " + timestamp(review.requested_at), () => openReview(review))),
        card("最近检查失败", failing, "没有最新检查失败的资产。", asset => action(asset.display_name,
          latest(asset, timestamp(asset.overview?.latest_check?.completed_at ?? asset.created_at)), () => openAsset(asset))),
        card("已批准、待准备 Release", pending, "没有等待准备 Release 的修订。", asset => action(asset.display_name,
          latest(asset, statuses.APPROVED), () => openAsset(asset))),
        card("发布需要处理", problems, "没有被阻断或批准有问题的发布。", ({ slot, target }) => action(nameOf(slot.api_asset_id),
          target.audience + " / " + target.environment + " · " +
            [slot.blocked ? "已阻断" : "", slot.approval_problem || ""].filter(Boolean).join("，"),
          () => openAsset(names.get(slot.api_asset_id)))),
      );
      status.textContent = "";
    } catch (error) {
      if (!current.aborted) status.textContent = error.message;
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
