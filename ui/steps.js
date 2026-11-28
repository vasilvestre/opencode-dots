import { t } from "./i18n.js";
// Readable names for workflow steps. awf only knows the YAML keys ("rediff"),
// so the page keeps the wording. A step missing here shows its key, capitalised.
// A null label hides a container step (a loop) that adds nothing to the trail.
const LABELS = {
  "review-pr": {
    guard: t("steps.check_pr"),
    pick_review_model: null,
    pick_address_model: null,
    fetch: t("steps.fetch_pr"),
    wait: t("steps.wait_for_ci"),
    rework: null,
    review: t("common.review"),
    verify: t("steps.verify_findings"),
    gate: t("steps.gate"),
    post: t("steps.post_review"),
    decide: t("steps.decide"),
    address: t("steps.fix_findings"),
    rediff: t("steps.generate_new_diff"),
    push: t("steps.push_fixes"),
    automerge: t("common.auto_merge"),
    notify: t("steps.notify"),
  },
  "reply-pr": {
    fetch: t("steps.fetch_pr"),
    guard: t("steps.check_pr"),
    reply: t("steps.write_replies"),
    push: t("steps.push_changes"),
    post: t("steps.post_replies"),
    notify: t("steps.notify"),
  },
  "fix-issue": {
    fetch: t("steps.fetch_issue"),
    route: null,
    fix_owned: t("steps.fix_issue"),
    fix_external: t("steps.fix_issue_upstream"),
    push: t("steps.open_pr"),
    notify: t("steps.notify"),
  },
  "_fix-issue-owned": { fix: t("steps.fix_issue") },
  "_fix-issue-external": { fix: t("steps.fix_issue_upstream") },
  "fix-ci": { fetch: t("steps.fetch_ci_logs"), guard: t("steps.check_failure"), fix: t("steps.fix_ci"), push: t("steps.push_fix"), notify: t("steps.notify") },
  "tend-repo": { survey: t("steps.survey_repo"), learn: t("steps.learn"), file: t("steps.file_issues"), patch: t("steps.patch"), notify: t("steps.notify") },
  "ask-pr": { fetch: t("steps.fetch_pr"), ask: t("steps.answer_question") },
};

export function stepLabel(workflow, step) {
  const map = LABELS[workflow];
  if (map && step in map) return map[step];
  const s = String(step).replace(/[-_]+/g, " ");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

// trailText joins the steps a run went through: "Review → Gate → Review".
// Long trails keep the last steps, since the current one is what matters.
export function trailText(workflow, steps, max = 8) {
  const labels = steps.map((s) => stepLabel(workflow, s)).filter((l) => l);
  const tail = labels.length > max ? ["…", ...labels.slice(-max)] : labels;
  return tail.join(" → ");
}
