// Readable names for workflow steps. awf only knows the YAML keys ("rediff"),
// so the page keeps the wording. A step missing here shows its key, capitalised.
// A null label hides a container step (a loop) that adds nothing to the trail.
const LABELS = {
  "review-pr": {
    guard: "Check PR",
    fetch: "Fetch PR",
    wait: "Wait for CI",
    rework: null,
    review: "Review",
    verify: "Verify findings",
    gate: "Gate",
    post: "Post review",
    decide: "Decide",
    address: "Fix findings",
    rediff: "Generate new diff",
    push: "Push fixes",
    automerge: "Auto-merge",
    notify: "Notify",
  },
  "reply-pr": {
    fetch: "Fetch PR",
    guard: "Check PR",
    reply: "Write replies",
    push: "Push changes",
    post: "Post replies",
    notify: "Notify",
  },
  "fix-issue": { fetch: "Fetch issue", fix: "Fix issue", push: "Open PR", notify: "Notify" },
  "fix-ci": { fetch: "Fetch CI logs", guard: "Check failure", fix: "Fix CI", push: "Push fix", notify: "Notify" },
  "tend-repo": { survey: "Survey repo", learn: "Learn", file: "File issues", patch: "Patch", notify: "Notify" },
  "ask-pr": { fetch: "Fetch PR", ask: "Answer question" },
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
