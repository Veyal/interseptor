// collections-run-plan.js — what a bulk run is about to do, as plain data.
// Pure: no DOM, no fetch. Node tests pin it (_js-tests/collections-run-plan.test.mjs).
//
// Every surface that fires more than one stored request -- the collection
// runner, a folder run, the identity matrix -- builds a plan here and shows it
// before sending anything. The identity matrix used to fire the whole
// collection against every saved identity straight from the command palette,
// so picking a collection could send a hundred live requests, DELETEs
// included, at a real target with nothing asked and nothing shown first.
//
// The plan is text and numbers only: no element ever carries markup, so a
// caller can escape it into a confirmation dialog. Strings are stripped of
// angle brackets here rather than trusting each caller to escape.

export const CONFIRM_PHRASE = 'RUN';

// Methods that can change state on the target. A pentest collection run
// against a live system is the one case where "it was only a dry run" is never
// true, so the typed phrase is required whenever one of these is in scope.
export const STATE_CHANGING = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

const plain = (s) => String(s == null ? '' : s).replace(/[<>]/g, '').trim();
const methodOf = (it) => (String((it && it.method) || 'GET').trim().toUpperCase() || 'GET');

function hostOf(rawUrl) {
  const m = /^[a-z][a-z0-9+.-]*:\/\/([^/?#]+)/i.exec(String(rawUrl || '').trim());
  return m ? m[1].replace(/^[^@]*@/, '') : '';
}

// requestsInScope walks the subtree under folderUid, or the whole collection.
// A folder's nested folders count, which is what the runner actually sends.
function requestsInScope(items, scope, folderUid) {
  const all = Array.isArray(items) ? items : [];
  if (scope !== 'folder' || !folderUid) return all.filter((it) => it && it.kind !== 'folder');
  const children = new Map();
  for (const it of all) {
    const k = (it && it.parentUid) || '';
    if (!children.has(k)) children.set(k, []);
    children.get(k).push(it);
  }
  const out = [];
  const stack = [folderUid];
  const seen = new Set([folderUid]);
  while (stack.length) {
    for (const it of children.get(stack.pop()) || []) {
      if (!it || seen.has(it.uid)) continue;
      seen.add(it.uid);
      if (it.kind === 'folder') stack.push(it.uid);
      else out.push(it);
    }
  }
  return out;
}

function scopeLabelFor(scope, items, folderUid, collectionName) {
  if (scope === 'folder') {
    const f = (Array.isArray(items) ? items : []).find((it) => it && it.uid === folderUid);
    const name = plain(f && f.name) || 'this folder';
    return 'Folder "' + name + '"';
  }
  const name = plain(collectionName);
  return name ? 'Whole collection "' + name + '"' : 'The whole collection';
}

/**
 * buildRunPlan describes a bulk run without performing it.
 * @param {Array} items stored collection items ({uid, kind, method, url, parentUid, name})
 * @param {Object} opts {scope:'collection'|'folder', folderUid, collectionName, identities}
 */
export function buildRunPlan(items, opts = {}) {
  const scope = opts.scope === 'folder' ? 'folder' : 'collection';
  const reqs = requestsInScope(items, scope, opts.folderUid);
  const identityNames = (Array.isArray(opts.identities) ? opts.identities : []).map(plain).filter(Boolean);
  const identities = Math.max(1, identityNames.length);

  const perMethod = new Map();
  const hosts = new Set();
  let stateChangingReqs = 0;
  for (const it of reqs) {
    const m = methodOf(it);
    perMethod.set(m, (perMethod.get(m) || 0) + 1);
    if (STATE_CHANGING.has(m)) stateChangingReqs++;
    const h = hostOf(it && it.url);
    if (h) hosts.add(h);
  }

  const requests = reqs.length;
  const liveRequests = requests * identities;
  const destructive = stateChangingReqs * identities;
  return {
    scope,
    scopeLabel: scopeLabelFor(scope, items, opts.folderUid, opts.collectionName),
    requests,
    identities,
    identityNames,
    liveRequests,
    destructive,
    destructiveMethods: [...perMethod.keys()].filter((m) => STATE_CHANGING.has(m)).sort(),
    methods: [...perMethod.entries()].map(([method, n]) => ({ method, count: n * identities })),
    hosts: [...hosts].sort(),
    empty: requests === 0,
    needsConfirm: liveRequests > 0,
    needsPhrase: destructive > 0,
  };
}

// methodBreakdown orders the methods by how many live requests each accounts
// for, so the dialog leads with whatever dominates the run.
export function methodBreakdown(plan) {
  return [...((plan && plan.methods) || [])]
    .map((m) => ({ method: m.method, count: m.count, stateChanging: STATE_CHANGING.has(m.method) }))
    .sort((a, b) => b.count - a.count || a.method.localeCompare(b.method));
}

export function planConfirmed(plan, typed) {
  if (!plan || !plan.needsConfirm) return false;
  if (!plan.needsPhrase) return true;
  return String(typed == null ? '' : typed).trim() === CONFIRM_PHRASE;
}

// planSummary is the one sentence shown above the breakdown. It states the
// live request count first, because that is the number the user is consenting
// to, then who it is sent as, then what it can change, then where it goes.
export function planSummary(plan) {
  if (!plan || plan.empty) return 'Nothing to send: there are no requests in scope.';
  const parts = [];
  parts.push(plan.liveRequests + ' live request' + (plan.liveRequests === 1 ? '' : 's'));
  if (plan.identities > 1) {
    parts.push('as ' + plan.identities + ' identities (' + plan.requests + ' request' + (plan.requests === 1 ? '' : 's') + ' each)');
  }
  let s = plan.scopeLabel + ': ' + parts.join(', ') + '.';
  if (plan.destructive > 0) {
    s += ' ' + plan.destructive + ' of them change state (' + plan.destructiveMethods.join(', ') + ').';
  }
  if (plan.hosts.length) {
    s += ' Sent to ' + plan.hosts.slice(0, 4).join(', ') + (plan.hosts.length > 4 ? ' and ' + (plan.hosts.length - 4) + ' more' : '') + '.';
  }
  return s;
}
