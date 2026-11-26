import { api, badge, button, card, empty, fill, field, loading, node, segment } from "./workbench.js";

const classifications = { COMPATIBLE: "兼容", RISKY: "有风险", UNKNOWN: "无法判断", BREAKING: "破坏性" };

const verdict = classification => badge(classification, classifications[classification] || classification);

// compareSection compares the revision with another one of the asset, as the
// comparator says when asked now. It is not a check: a release rests on a
// check, and on a check that compared against the revision a slot replaces.
export function compareSection({ path, revision, others, signal }) {
  const box = card("兼容性比较", { iconName: "compare", className: "comparison-card" });
  if (!others.length) {
    box.append(empty("此资产没有其他修订可以比较。"));
    return box;
  }
  const baseline = node("select");
  const ordered = others.toSorted((a, b) => b.revision_sequence - a.revision_sequence);
  fill(baseline, ordered, "revision_id", item => "#" + item.revision_sequence);
  const earlier = ordered.find(item => item.revision_sequence < revision.revision_sequence);
  if (earlier) baseline.value = earlier.revision_id;
  const compare = button("比较", "secondary", "compare");
  const controls = node("div", "", "control-row");
  controls.append(field("基线修订", baseline, "compare-baseline"), compare);
  const result = node("div", "", "comparison");
  result.setAttribute("aria-live", "polite");
  box.append(controls, node("p", "比较两个修订的契约，不是检查结论。准备 Release 以检查为准；替换另一修订已发布的 Release 时，检查须以那个修订为对比基线。",
    "hint"), result);

  let request;
  compare.onclick = async () => {
    request?.abort();
    request = new AbortController();
    const current = AbortSignal.any([request.signal, signal]);
    const chosen = ordered.find(item => item.revision_id === baseline.value);
    compare.disabled = true;
    result.replaceChildren(loading("正在比较…"));
    try {
      const comparison = await api(path + "/compatibility?baseline_revision_id=" + segment(baseline.value), { signal: current });
      if (current.aborted) return;
      const summary = node("p", "", "verdict");
      summary.dataset.state = comparison.classification;
      summary.append(node("span", "修订 #" + chosen.revision_sequence + " → #" + revision.revision_sequence + "：", "verdict-pair"),
        verdict(comparison.classification), node("span", comparison.tool.name + " " + comparison.tool.version, "muted"));
      const parts = [summary];
      if (!comparison.changes.length) {
        parts.push(empty("没有发现变化。"));
      } else {
        const changes = node("ul", "", "changes");
        for (const change of comparison.changes) {
          const entry = node("li");
          entry.append(verdict(change.classification), node("span", change.code, "rule"));
          if (change.pointer) entry.append(node("code", change.pointer, "digest pointer"));
          changes.append(entry);
        }
        parts.push(changes);
      }
      if (comparison.truncated) parts.push(node("p", "变化太多，只列出了一部分。", "hint"));
      result.replaceChildren(...parts);
    } catch (error) {
      if (!current.aborted) result.replaceChildren(node("p", error.message, "notice error"));
    } finally {
      if (!current.aborted) compare.disabled = false;
    }
  };
  return box;
}
