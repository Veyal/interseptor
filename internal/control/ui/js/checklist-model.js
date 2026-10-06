// checklist-model.js — the first-run checklist, derived purely from real project
// state (never a manual tick). No imports and no DOM access: runs under node.

export const CHECKLIST_STEPS = [
  { id: 'ca', label: 'Trust the CA', action: 'setup' },
  { id: 'proxy', label: 'Point a device or browser at the proxy', action: 'devices' },
  { id: 'scope', label: 'Define scope', action: 'scope' },
  { id: 'flow', label: 'Capture a first flow', action: 'proxy' },
  { id: 'finding', label: 'Create a first finding', action: 'findings' },
];

const check = (facts, id) => ((facts && facts.readiness && facts.readiness.checks) || []).find((c) => c.id === id);
const num = (v) => (Number.isFinite(v) && v > 0 ? v : 0);

// facts: {readiness:{checks}|null, scope:{enabled,inCount}, evidence:{flows}, findings:{total}}.
// Missing or failed facts leave a step undone; they never count as progress.
export function deriveChecklist(facts = {}) {
  const flows = num(facts.evidence && facts.evidence.flows);
  const done = {
    ca: !!(check(facts, 'tls_intercept') && check(facts, 'tls_intercept').ok),
    proxy: !!(check(facts, 'traffic') && check(facts, 'traffic').ok) || flows > 0,
    scope: !!(facts.scope && facts.scope.enabled && num(facts.scope.inCount) > 0),
    flow: flows > 0,
    finding: num(facts.findings && facts.findings.total) > 0,
  };
  const steps = CHECKLIST_STEPS.map((s) => ({ ...s, done: done[s.id] }));
  const count = steps.filter((s) => s.done).length;
  return { steps, done: count, total: steps.length, complete: count === steps.length, text: count + ' of ' + steps.length };
}

// The card hides itself when complete or dismissed.
export function checklistVisible(model, dismissed) {
  return !dismissed && !model.complete;
}
