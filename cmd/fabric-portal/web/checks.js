import { badge, facts, node, timestamp } from "./workbench.js";

const categories = {
  SECURITY_MANDATORY: "安全", INTEGRITY_MANDATORY: "完整性", CONTRACT_COMPATIBILITY: "兼容性", QUALITY: "质量",
};
const severities = { BLOCK: "阻断", WARN: "警告" };

// blockers splits the rules that stand between a check and a release as the
// release gate does: every rule that failed or could not be judged, whatever
// its severity, since a rule that warns can still be UNKNOWN. The waivable
// ones are waived by name; any other one refuses the release.
export function blockers(check) {
  const open = check.outcomes.filter(rule => rule.status === "FAIL" || rule.status === "UNKNOWN");
  return { waivable: open.filter(rule => rule.waivable), unwaivable: open.filter(rule => !rule.waivable) };
}

// checkDetail shows one recorded check: what it ran on and with, then each
// rule's result and findings, grouped by the kind of rule.
export function checkDetail(check) {
  const run = check.check_run;
  const { summary } = run;
  const box = node("div", "", "check-detail");
  const counts = node("span", "", "tally");
  for (const [state, label, count] of [
    ["PASS", "通过", summary.pass], ["WARN", "警告", summary.warn], ["FAIL", "失败", summary.fail],
    ["UNKNOWN", "未知", summary.unknown], ["NOT_RUN", "未执行", summary.not_run], ["FINDINGS", "发现", summary.finding_count],
  ]) {
    const item = node("span", label + " " + count, "tally-item");
    item.dataset.state = count ? state : "NONE";
    counts.append(item);
  }
  box.append(facts([
    ["结论", badge(run.status)],
    ["完成时间", timestamp(run.completed_at)],
    ["策略", run.policy_set_id],
    ["工具", run.tool_manifest.map(tool => tool.name + " " + tool.version).join("，")],
    ["规则结论", counts],
    ["策略摘要", node("code", run.policy_digest, "digest")],
    ["输入摘要", node("code", run.input_digest, "digest")],
  ], "facts grid"));

  const groups = new Map();
  for (const rule of check.outcomes) groups.set(rule.category, [...(groups.get(rule.category) || []), rule]);
  for (const [category, rules] of groups) {
    box.append(node("h4", (categories[category] || category) + "（" + rules.length + "）", "check-group"));
    const list = node("ul", "", "rules");
    for (const rule of rules) {
      const entry = node("li");
      entry.dataset.state = rule.status;
      const traits = [severities[rule.severity] || rule.severity];
      if (rule.waivable) traits.push("可豁免");
      if (rule.finding_count) traits.push(rule.finding_count + " 项发现");
      if (rule.tool) traits.push(rule.tool.name + " " + rule.tool.version);
      const line = node("div", "", "rule-line");
      line.append(node("span", rule.rule_id, "rule"), badge(rule.status), node("span", traits.join(" · "), "muted"));
      entry.append(line, node("p", rule.description, "rule-description"));
      for (const finding of check.findings.filter(item => item.rule_id === rule.rule_id)) {
        const place = finding.path + (finding.line ? ":" + finding.line : "") + (finding.pointer ? " " + finding.pointer : "");
        const found = node("p", "", "finding");
        found.append(node("span", severities[finding.level] || finding.level, "finding-level"), node("code", place, "digest"),
          node("span", finding.message));
        entry.append(found);
      }
      list.append(entry);
    }
    box.append(list);
  }
  return box;
}
