import { badge, button, card, field, icon, node, page, productPath } from "./workbench.js";

// What Fabric records, in words. A read is recorded only when it is refused.
// An action not named here is shown by its code alone.
const actions = {
  "product.register": "登记产品",
  "product.read": "读取产品",
  "product.change_brand": "修改产品品牌",
  "workspace.manage": "管理 Workspace",
  "workspace.read": "读取 Workspace",
  "member.grant": "授予成员资格",
  "member.revoke": "结束成员资格",
  "member.read": "读取成员",
  "product_role.grant": "授予产品角色",
  "product_role.revoke": "撤销产品角色",
  "product_role.read": "读取产品角色",
  "audience_config.configure": "添加受众配置",
  "audience_config.change": "修改受众配置",
  "audience_config.remove": "移除受众配置",
  "audience_config.read": "读取受众配置",
  "asset.create": "登记 API 资产",
  "asset.read": "读取 API 资产",
  "asset.search": "搜索 API 资产",
  "revision.create": "导入修订",
  "revision.read": "读取修订",
  "revision.check": "运行检查",
  "review.read": "读取历史评审",
  "release.prepare": "准备 Release",
  "release.read": "读取 Release 来源",
  "release.download": "下载 Release 制品",
  "publication.target.create": "登记发布目标",
  "publication.target.read": "读取发布目标",
  "publication.activate": "发布",
  "publication.withdraw": "撤销发布",
  "publication.restore": "恢复发布",
  "publication.read": "读取发布",
  "publication.history": "读取发布历史",
  "publication.download": "下载已发布制品",
  "security.block": "阻断访问",
  "security.unblock": "解除阻断",
  "security.block.read": "读取访问阻断",
  "audit.search": "查询审计记录",
};
const outcomes = { SUCCEEDED: "成功", DENIED: "被拒绝" };
const columns = ["时间", "操作人", "操作", "结果与规则", "资源", "关联 ID"];
const dayFormat = new Intl.DateTimeFormat("zh-CN", { dateStyle: "full" });
const timeFormat = new Intl.DateTimeFormat("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" });
const dayKey = date => [date.getFullYear(), date.getMonth(), date.getDate()].join("-");

function textInput(maxLength, placeholder) {
  const input = node("input");
  input.maxLength = maxLength;
  input.autocomplete = "off";
  input.spellcheck = false;
  input.placeholder = placeholder;
  return input;
}

function instant(input) {
  if (!input.value) return null;
  const value = new Date(input.value);
  return Number.isNaN(value.getTime()) ? null : value;
}

// auditPanel reads the audit records of one product for an auditor of it,
// newest first and grouped by day. The filters are the server's and apply
// before paging. A record is shown as the trail holds it: the actor is not
// masked (CR-0002), and nothing here changes one. It reads nothing until it
// is first shown.
export function auditPanel({ product, signal }) {
  const element = node("div", "", "audit");

  const action = textInput(64, "如 release.prepare");
  action.pattern = "[a-z][a-z0-9_]*(\\.[a-z][a-z0-9_]*)+";
  action.title = "操作代码，如 release.prepare";
  const suggestions = node("datalist");
  suggestions.id = "audit-action-codes";
  for (const [code, label] of Object.entries(actions)) suggestions.append(new Option(label, code));
  action.setAttribute("list", suggestions.id);
  const outcome = node("select");
  outcome.add(new Option("全部", ""));
  for (const [value, label] of Object.entries(outcomes)) outcome.add(new Option(label, value));
  const subject = textInput(255, "主体，如 alice");
  const correlation = textInput(255, "错误提示里的请求编号");
  const since = node("input");
  const before = node("input");
  for (const input of [since, before]) {
    input.type = "datetime-local";
    input.step = "1";
  }
  const issuer = textInput(255, "签发方");
  const resourceType = textInput(64, "如 PublicationSlot");
  resourceType.pattern = "[A-Za-z][A-Za-z0-9]*";
  const resourceID = textInput(36, "UUID");
  resourceID.pattern = "[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}";
  resourceID.title = "资源的 UUID";

  const more = node("details", "", "filter-more");
  const moreFields = node("div", "", "filter-more-fields");
  moreFields.append(field("签发方", issuer, "audit-issuer"), field("资源类型", resourceType, "audit-resource-type"),
    field("资源 ID", resourceID, "audit-resource-id"));
  more.append(node("summary", "更多条件"), moreFields);
  const apply = node("button", "", "primary");
  apply.type = "submit";
  apply.append(icon("filter"), node("span", "筛选"));
  const clear = button("清空筛选", "ghost");
  const controls = node("div", "", "filter-actions");
  controls.append(apply, clear);
  const form = node("form", "", "card filter-bar");
  form.setAttribute("role", "search");
  form.setAttribute("aria-label", "筛选审计记录");
  form.append(field("操作", action, "audit-action"), suggestions, field("结果", outcome, "audit-outcome"),
    field("操作人", subject, "audit-subject"), field("关联 ID", correlation, "audit-correlation"),
    field("开始时间（含）", since, "audit-since"), field("结束时间（不含）", before, "audit-before"), controls, more);

  const status = node("p");
  status.setAttribute("role", "status");
  const head = node("tr");
  for (const label of columns) {
    const cell = node("th", label);
    cell.scope = "col";
    head.append(cell);
  }
  const header = node("thead");
  header.append(head);
  const records = node("table", "", "data audit-records");
  records.append(node("caption", "审计记录"), header);
  const wrap = node("div", "", "table-wrap");
  wrap.hidden = true;
  wrap.append(records);
  const next = button("加载更多", "secondary more");
  next.hidden = true;
  const listCard = card("审计记录", {
    level: "h2", iconName: "history", className: "audit-list",
    actions: [node("p", "最新的在前，时间按本机时区显示", "hint")],
  });
  const listHeading = listCard.querySelector(".card-title");
  listHeading.id = "audit-title";
  listHeading.tabIndex = -1;
  listCard.setAttribute("aria-labelledby", listHeading.id);
  listCard.append(wrap, status, next);
  element.append(form, listCard);

  let started = false;
  let request;
  let token = "";
  let filters;
  let shown = 0;
  let day = null;

  function say(kind, text = "") {
    status.className = { loading: "loading", empty: "empty", error: "notice error" }[kind] || "status-line";
    status.textContent = text;
  }

  function currentFilters() {
    const params = new URLSearchParams();
    for (const [name, input] of [["action", action], ["actor_issuer", issuer], ["actor_subject", subject],
      ["correlation_id", correlation], ["resource_type", resourceType]]) {
      const value = input.value.trim();
      if (value) params.set(name, value);
    }
    const id = resourceID.value.trim().toLowerCase();
    if (id) params.set("resource_id", id);
    if (outcome.value) params.set("outcome", outcome.value);
    const from = instant(since);
    const to = instant(before);
    if (from && to && from >= to) throw new Error("开始时间须早于结束时间。");
    if (from) params.set("recorded_since", from.toISOString());
    if (to) params.set("recorded_before", to.toISOString());
    return params;
  }

  // A record goes under the day it was recorded on, here.
  function append(record) {
    const at = new Date(record.recorded_at);
    if (!day || dayKey(at) !== dayKey(day.date)) {
      const group = node("tbody");
      const title = node("tr", "", "audit-day");
      const cell = node("th", dayFormat.format(at));
      cell.colSpan = columns.length;
      cell.scope = "rowgroup";
      title.append(cell);
      group.append(title);
      records.append(group);
      day = { date: at, group };
    }
    day.group.append(recordRow(record, at));
    shown += 1;
  }

  function recordRow(record, at) {
    const row = node("tr");
    row.dataset.outcome = record.outcome;
    const when = node("time", timeFormat.format(at));
    when.dateTime = record.recorded_at;
    when.title = record.recorded_at;
    const actor = node("div", "", "audit-actor");
    const who = button(record.actor.subject, "row-link");
    who.title = "只看此操作人的记录";
    who.onclick = () => {
      issuer.value = record.actor.issuer;
      subject.value = record.actor.subject;
      more.open = true;
      load(true, true);
    };
    actor.append(who, node("small", record.actor.issuer, "muted"));
    const what = node("div");
    what.append(node("span", actions[record.action] || record.action), node("small", record.action, "muted code"));
    const resource = node("div");
    resource.append(node("span", record.resource_type));
    if (record.resource_id) resource.append(node("code", record.resource_id, "digest"));
    const result = node("div", "", "audit-result");
    result.append(badge(record.outcome, outcomes[record.outcome] || record.outcome), node("small", "依据 " + record.rule_id, "muted code"));
    const cells = [when, actor, what, result, resource, node("code", record.correlation_id, "digest")];
    for (const value of cells) {
      const cell = node("td");
      cell.append(value);
      row.append(cell);
    }
    return row;
  }

  // A page token belongs to the filters it was issued for, so "more" keeps
  // the filters of the first page even if the form was edited since.
  async function load(reset, focus = false) {
    if (reset) {
      try {
        filters = currentFilters();
      } catch (error) {
        say("error", error.message);
        return;
      }
    }
    request?.abort();
    const controller = new AbortController();
    request = controller;
    const current = AbortSignal.any([controller.signal, signal]);
    if (reset) {
      for (const group of records.querySelectorAll("tbody")) group.remove();
      token = "";
      shown = 0;
      day = null;
    }
    next.hidden = true;
    wrap.hidden = !shown;
    if (focus) listHeading.focus();
    say("loading", "正在读取审计记录…");
    try {
      const result = await page(productPath(product) + "/audit-records?" + filters, 50, token, current);
      if (current.aborted) return;
      for (const record of result.items) append(record);
      token = result.next_page_token || "";
      if (shown) say("", "已显示 " + shown + " 条" + (token ? "，还有更早的记录" : "，没有更早的记录了") + "。");
      else say("empty", filters.size ? "没有符合条件的审计记录。" : "此产品还没有审计记录。");
    } catch (error) {
      if (current.aborted) return;
      const hint = error.status === 400 || error.status === 422 ? "请检查筛选条件。" : "";
      say("error", hint + error.message);
    } finally {
      if (!current.aborted) {
        next.hidden = !token;
        wrap.hidden = !shown;
      }
    }
  }

  form.onsubmit = event => {
    event.preventDefault();
    load(true);
  };
  clear.onclick = () => {
    form.reset();
    more.open = false;
    load(true);
  };
  next.onclick = () => load(false);
  return {
    element,
    start() {
      if (started) return;
      started = true;
      load(true);
    },
  };
}
