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
