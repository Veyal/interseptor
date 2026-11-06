// setup-model.js — pure first-run rules for the setup wizard (no DOM).

// setupBootAction decides what the first-run wizard does on boot.
//   done        the operator already finished or skipped it for this project
//   hasFlows    History already holds traffic
//   scopeRules  number of scope rules, or null when they could not be read
// Returns 'skip' (do nothing), 'mark-done' (a configured project: remember it)
// or 'open'. Traffic alone never skips the wizard: a system or browser proxy
// often fills History before the UI is ever opened.
export function setupBootAction({ done, hasFlows, scopeRules }) {
  if (done) return 'skip';
  if (!hasFlows) return 'open';
  if (scopeRules == null) return 'skip';
  return scopeRules > 0 ? 'mark-done' : 'open';
}

// SETUP_LAST is the index of the final (done) step; the wizard shows SETUP_LAST+1 steps.
export const SETUP_LAST = 4;

// setupEnterAction names the control Enter presses on a step that has a text
// input. It is the step's own primary action, never "Next": pressing Enter after
// typing a host or a target used to advance the wizard, so the typed value was
// dropped and the last step still reported the configuration as saved. The CA
// step returns '' because its checkbox gates Next and Enter must not bypass it.
export function setupEnterAction(step) {
  if (step === 1) return '';
  if (step === 2) return 'setupScopeAdd';
  if (step === 3) return 'setupTargetSave';
  return 'setupNext';
}
