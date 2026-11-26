import { api, badge, confirmChange, field, fill, node, segment, statuses, timestamp } from "./workbench.js";
import { blockers } from "./checks.js";

const versionPattern = /^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$/;

// releaseForm prepares a release of one revision on one of its checks. The
// rules of the check that failed or could not be judged are waived here, each
// for a reason, as the release gate asks; one that nothing waives keeps the
// form shut. Whether the check is of today's policy and tools only the server
// knows, so that refusal is shown when it comes.
export function releaseForm({ productName, asset, revision, path, releasePath, sequenceOf, signal, onPrepared }) {
  const form = node("form", "", "form release-form");
  const check = node("select");
  check.required = true;
  const compared = node("p", "", "hint muted");
  const rules = node("div");
  const version = node("input");
  version.required = true;
  version.maxLength = 64;
  version.autocomplete = "off";
  version.spellcheck = false;
  const submit = node("button", "准备 Release", "primary");
  submit.type = "submit";
  const message = node("p", "", "muted");
  message.setAttribute("role", "status");
  form.append(field("依据的检查", check, "release-check"), compared, rules, field("版本", version, "release-version"), submit, message);

  let checks = [];
  let detail = null;
  let reasons = [];
  let blocked = false;
  let working = false;
  let request;

  function note(text, error = false) {
    message.textContent = text;
    message.classList.toggle("publication-error", error);
  }

  function sync() {
    check.disabled = working || !checks.length;
    version.disabled = working;
    for (const { input } of reasons) input.disabled = working;
    submit.disabled = working || !detail || blocked;
  }

  const label = item => statuses[item.status] + " · " + timestamp(item.completed_at) +
    (item.compatibility ? " · 对比 #" + (sequenceOf(item.compatibility.baseline.revision_id) ?? "?") : "");

  function showRules() {
    const { waivable, unwaivable } = blockers(detail);
    const baseline = detail.check_run.compatibility?.baseline.revision_id;
    compared.textContent = baseline ? "兼容性对比基线：修订 #" + (sequenceOf(baseline) ?? "?") + "。" :
      "此检查未做兼容性对比。替换另一修订已发布的 Release 时，须以那个修订为基线检查。";
    blocked = unwaivable.length > 0;
    reasons = [];
    if (blocked) {
      const list = node("ul", "", "checks");
      for (const rule of unwaivable) {
        const entry = node("li");
        entry.append(node("span", rule.rule_id + " ", "rule"), badge(rule.status), node("small", rule.description, "muted"));
        list.append(entry);
      }
      rules.replaceChildren(node("p", "以下规则不可豁免，结论为失败或未知。此检查不能准备 Release，请修正契约后重新检查。",
        "publication-error"), list);
      return;
    }
    if (!waivable.length) {
      rules.replaceChildren(node("p", "此检查没有失败或未知的规则，无需豁免。", "muted"));
      return;
    }
    const waivers = node("fieldset", "", "waivers");
    waivers.append(node("legend", "豁免（每条规则填写一行理由）"));
    for (const rule of waivable) {
      const input = node("input");
      input.required = true;
      input.maxLength = 1000;
      input.autocomplete = "off";
      const box = field(rule.rule_id + " 豁免理由", input, "waiver-" + rule.rule_id);
      box.append(node("small", statuses[rule.status] + " · " + rule.description, "muted"));
      reasons.push({ rule, input });
      waivers.append(box);
    }
    rules.replaceChildren(waivers);
  }

  async function choose() {
    request?.abort();
    request = new AbortController();
    const current = AbortSignal.any([request.signal, signal]);
    detail = null;
    blocked = false;
    reasons = [];
    compared.textContent = "";
    rules.replaceChildren(check.value ? node("p", "正在读取检查…", "muted") : node("p", "请先由作者运行检查。", "muted"));
    sync();
    if (!check.value) return;
    try {
      const result = await api(path + "/check-runs/" + segment(check.value), { signal: current });
      if (current.aborted) return;
      detail = result;
      showRules();
    } catch (error) {
      if (!current.aborted) rules.replaceChildren(node("p", error.message, "publication-error"));
    } finally {
      if (!current.aborted) sync();
    }
  }

  form.onsubmit = async event => {
    event.preventDefault();
    if (submit.disabled) return;
    const chosen = version.value.trim();
    if (!versionPattern.test(chosen)) {
      note("版本须以字母或数字开头，只含字母、数字和 . _ + -，最长 64 个字符。", true);
      return;
    }
    const waivers = reasons.map(({ rule, input }) => ({ rule_id: rule.rule_id, reason: input.value.trim() }));
    if (waivers.some(waiver => !waiver.reason)) {
      note("请填写所有豁免理由。", true);
      return;
    }
    note("");
    const run = detail.check_run;
    working = true;
    sync();
    try {
      const values = [
        ["产品", productName],
        ["API 资产", asset.display_name],
        ["修订", "#" + revision.revision_sequence],
        ["检查", statuses[run.status] + " · " + timestamp(run.completed_at)],
        ["版本", chosen],
        ["豁免规则", waivers.length ? waivers.map(waiver => waiver.rule_id).join("，") : "无"],
      ];
      if (!(await confirmChange("确认准备 Release", values, "确认准备", signal,
        "Release 记录所依据的检查和豁免理由，准备后不能更改。"))) return;
      note("正在准备 Release…");
      const data = { revision_id: revision.revision_id, check_run_id: run.check_run_id, version: chosen };
      if (waivers.length) data.waivers = waivers;
      const { status, body } = await api(releasePath, { signal, data, withStatus: true });
      version.value = "";
      for (const { input } of reasons) input.value = "";
      note(status === 200 ? "相同修订、检查和豁免的 Release " + body.version + " 已有记录。" : "Release " + body.version + " 已就绪。");
      await onPrepared(body);
    } catch (error) {
      if (!signal.aborted) note(error.message, true);
    } finally {
      working = false;
      if (!signal.aborted) sync();
    }
  };
  check.onchange = () => {
    note("");
    choose();
  };

  return {
    element: form,
    // setChecks lists the revision's checks newest first and chooses the
    // given one, or else the latest.
    setChecks(items, chosen) {
      checks = items;
      fill(check, checks, "check_run_id", label, checks.length ? "" : "此修订还没有检查");
      if (checks.some(item => item.check_run_id === chosen)) check.value = chosen;
      choose();
    },
  };
}
