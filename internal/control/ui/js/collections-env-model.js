// collections-env-model.js — pure helpers for creating, renaming, duplicating and deleting environments.
// No DOM, no fetch. Node tests pin it (_js-tests/collections-env-model.test.mjs).

export const ENV_NAME_MAX = 80;

const same = (a, b) => String(a).trim().toLowerCase() === String(b).trim().toLowerCase();

// envNameError returns a human message, or '' when the name is usable.
// `others` are the sibling environments (the one being renamed excluded by the caller).
export function envNameError(name, others = []) {
  const n = String(name == null ? '' : name).trim();
  if (!n) return 'Give the environment a name.';
  if (n.length > ENV_NAME_MAX) return 'Use ' + ENV_NAME_MAX + ' characters or fewer.';
  if ((others || []).some((e) => e && same(e.name, n))) return 'An environment named "' + n + '" already exists in this collection.';
  return '';
}

// copyName picks "Name copy", "Name copy 2", ... not used by a sibling.
export function copyName(name, others = []) {
  const base = String(name || 'Environment').trim() + ' copy';
  let candidate = base;
  for (let i = 2; (others || []).some((e) => e && same(e.name, candidate)); i++) candidate = base + ' ' + i;
  return candidate.slice(0, ENV_NAME_MAX);
}

export function createBody(name, collectionUid) {
  return { name: String(name).trim(), kind: 'env', collectionUid: collectionUid || '' };
}

// duplicateBody copies the name, pin, identity binding and declared variables.
// Current values are deliberately left behind: they are local and may be secrets.
export function duplicateBody(env, name, collectionUid) {
  const vars = ((env && env.variables) || []).map((v) => ({
    key: v.key, type: v.type, initialValue: v.type === 'secret' ? '' : (v.initialValue || ''), enabled: v.enabled !== false,
  }));
  return {
    name: String(name).trim(), kind: 'env', collectionUid: collectionUid || '',
    boundIdentity: (env && env.boundIdentity) || '', baseTargetPin: (env && env.baseTargetPin) || '', variables: vars,
  };
}

export function renameBody(env, name) {
  return { name: String(name).trim(), kind: env.kind, collectionUid: env.collectionUid, boundIdentity: env.boundIdentity, baseTargetPin: env.baseTargetPin, rev: env.rev };
}

// deleteWarning is plain text (callers escape it); it names the environment and says its variables go too.
export function deleteWarning(env) {
  const n = ((env && env.variables) || []).length;
  const vars = n === 1 ? 'its 1 variable' : n ? 'its ' + n + ' variables' : 'any variables it holds';
  return { name: String((env && env.name) || 'this environment'), tail: ' will be deleted together with ' + vars + ', including current values kept on this machine. This cannot be undone.' };
}
