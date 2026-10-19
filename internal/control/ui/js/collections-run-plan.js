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

const rawUrlOf = (it) => {
  const u = it && it.url;
  return String(typeof u === 'string' ? u : (u && u.raw) || '').trim();
};

function hostOf(rawUrl) {
  const m = /^[a-z][a-z0-9+.-]*:\/\/([^/?#]+)/i.exec(String(rawUrl || '').trim());
  return m ? m[1].replace(/^[^@]*@/, '') : '';
}

// templateHostOf names the host part of a URL that still holds a {{variable}}:
// the leading {{baseUrl}} base, or the host text around a variable. It is what
// the review shows for a host it could not resolve.
function templateHostOf(raw) {
  const m = /^(?:[a-z][a-z0-9+.-]*:\/\/)?([^/?#]*)/i.exec(raw);
  const h = m ? m[1].replace(/^[^@]*@/, '') : raw;
  return plain(h || raw);
}

/**
 * planResolveTargets lists the requests in scope whose URL holds a {{variable}}.
 * The one resolver is the server's (/api/variables/resolve), so a caller
 * resolves exactly these before building the plan; this module stays pure.
 */
export function planResolveTargets(items, opts = {}) {
  const scope = opts.scope === 'folder' ? 'folder' : 'collection';
  return requestsInScope(items, scope, opts.folderUid).filter((it) => rawUrlOf(it).includes('{{'));
}

/**
 * resolvePlanUrls resolves every variable URL of a run with the injected
 * resolveOne(item) -> {value, unresolved}. A failed call leaves that request
 * out of the result, which the plan then reports as unresolved.
 */
export async function resolvePlanUrls(items, opts, resolveOne) {
  const targets = planResolveTargets(items, opts);
  const out = {};
  const queue = targets.slice();
  const worker = async () => {
    for (let it = queue.shift(); it; it = queue.shift()) {
      try { out[it.uid] = await resolveOne(it); } catch (e) { /* reported as unresolved */ }
    }
  };
  await Promise.all(Array.from({ length: Math.min(6, targets.length) }, worker));
  return out;
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

function scopeLabelFor(scope, items, folderUid, collectionName, override) {
  if (override) return plain(override);
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
 * @param {Object} opts {scope:'collection'|'folder', folderUid, collectionName, identities,
 *   scopeLabel} -- scopeLabel names the scope outright, for a run over a subset
 *   of items (a rerun of just the failures) that is neither the whole
 *   collection nor one folder.
 */
export function buildRunPlan(items, opts = {}) {
  const scope = opts.scope === 'folder' ? 'folder' : 'collection';
  const reqs = requestsInScope(items, scope, opts.folderUid);
  const identityNames = (Array.isArray(opts.identities) ? opts.identities : []).map(plain).filter(Boolean);
  const identities = Math.max(1, identityNames.length);

  const perMethod = new Map();
  const hosts = new Set();
  const unresolvedHosts = new Set();
  const unresolvedVars = new Set();
  const blockedHosts = new Set();
  const resolved = opts.resolved || {};
  const blockCheck = typeof opts.blockCheck === 'function' ? opts.blockCheck : null;
  let unresolvedReqs = 0;
  let unresolvedPathReqs = 0;
  let blockedScope = 0;
  let blockedPin = 0;
  let blockedGuard = 0;
  let stateChangingReqs = 0;
  for (const it of reqs) {
    const m = methodOf(it);
    perMethod.set(m, (perMethod.get(m) || 0) + 1);
    if (STATE_CHANGING.has(m)) stateChangingReqs++;
    const raw = rawUrlOf(it);
    let h = hostOf(raw);
    let full = raw;
    const r = raw.includes('{{') ? resolved[it && it.uid] : null;
    if (raw.includes('{{')) {
      const names = (r && r.unresolved) || [];
      if (h && !h.includes('{{')) {
        // The host is literal: a variable in the path or query cannot change
        // where the request goes, so the host is known. The request itself is
        // still blocked by the server until the variable is defined.
        if (names.length) {
          unresolvedPathReqs++;
          names.forEach((n) => unresolvedVars.add(plain(n)));
        } else if (r && r.value) full = r.value;
      } else {
        h = r && !names.length ? hostOf(r.value) : '';
        if (!h) { unresolvedReqs++; unresolvedHosts.add(templateHostOf(raw)); continue; }
        full = r.value;
      }
    }
    if (h) hosts.add(h);
    // A request with unresolved variables is blocked for that reason already;
    // only the rest can be blocked by scope or the environment target pin.
    if (blockCheck && !(r && (r.unresolved || []).length)) {
      const why = blockCheck(full);
      if (why === 'base_target_pin') blockedPin++;
      else if (why === 'ip_guard') blockedGuard++;
      else if (why) blockedScope++;
      if (why && h) blockedHosts.add(h);
    }
  }

  const requests = reqs.length;
  const liveRequests = requests * identities;
  const destructive = stateChangingReqs * identities;
  return {
    scope,
    scopeLabel: scopeLabelFor(scope, items, opts.folderUid, opts.collectionName, opts.scopeLabel),
    requests,
    identities,
    identityNames,
    liveRequests,
    destructive,
    destructiveMethods: [...perMethod.keys()].filter((m) => STATE_CHANGING.has(m)).sort(),
    methods: [...perMethod.entries()].map(([method, n]) => ({ method, count: n * identities })),
    hosts: [...hosts].sort(),
    unresolvedRequests: unresolvedReqs,
    unresolvedHosts: [...unresolvedHosts].sort(),
    unresolvedPathRequests: unresolvedPathReqs,
    unresolvedVariables: [...unresolvedVars].sort(),
    blocked: {
      total: (blockedScope + blockedPin + blockedGuard) * identities, scope: blockedScope * identities, pin: blockedPin * identities,
      guard: blockedGuard * identities,
      requests: blockedScope + blockedPin + blockedGuard, hosts: [...blockedHosts].sort(),
      all: requests > 0 && blockedScope + blockedPin + blockedGuard === requests,
    },
    empty: requests === 0,
    needsConfirm: liveRequests > 0,
    needsPhrase: destructive > 0,
  };
}

// blockedText says how many requests the scope guard or the environment pin
// will refuse, and why. '' when none. Shown in the review before RUN is typed.
export function blockedText(plan) {
  const b = plan && plan.blocked;
  if (!b || !b.total) return '';
  const parts = [];
  if (b.scope) parts.push(b.scope + ' out of scope');
  if (b.guard) parts.push(b.guard + ' to a private or loopback address');
  if (b.pin) parts.push(b.pin + ' not matching the environment target pin');
  const addScope = b.scope || b.guard;
  const lead = b.all ? 'Every request will be blocked' : b.total + ' of ' + plan.liveRequests + ' request' + (plan.liveRequests === 1 ? '' : 's') + ' will be blocked';
  return lead + ' (' + parts.join(', ') + '). '
    + (addScope ? 'Add the host' + (b.hosts.length === 1 ? '' : 's') + ' to scope' : '') + (addScope && b.pin ? ' and ' : '') + (b.pin ? (addScope ? 'pick' : 'Pick') + ' an environment whose pin matches' : '') + ' to send them.';
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
//
// Pass {withScope: false} where the scope is already on screen -- a sheet whose
// title is the scope label would otherwise print it twice in a row. Pass
// {withWarnings: false} where the blocked / unresolved-path warnings get their
// own lines (the run review), so they are not said twice.
export function planSummary(plan, { withScope = true, withWarnings = true } = {}) {
  if (!plan || plan.empty) return 'Nothing to send: there are no requests in scope.';
  const parts = [];
  parts.push(plan.liveRequests + ' live request' + (plan.liveRequests === 1 ? '' : 's'));
  if (plan.identities > 1) {
    parts.push('as ' + plan.identities + ' identities (' + plan.requests + ' request' + (plan.requests === 1 ? '' : 's') + ' each)');
  }
  const lead = withScope ? plan.scopeLabel + ': ' : '';
  let s = lead + (withScope ? parts.join(', ') : parts.join(', ').replace(/^./, (c) => c.toUpperCase())) + '.';
  if (plan.destructive > 0) {
    s += ' ' + plan.destructive + ' of them change state (' + plan.destructiveMethods.join(', ') + ').';
  }
  if (plan.hosts.length) {
    s += ' Sent to ' + plan.hosts.slice(0, 4).join(', ') + (plan.hosts.length > 4 ? ' and ' + (plan.hosts.length - 4) + ' more' : '') + '.';
  }
  if (plan.unresolvedRequests > 0) {
    s += ' ' + plan.unresolvedRequests + ' request' + (plan.unresolvedRequests === 1 ? ' goes' : 's go') + ' to a host that could not be resolved (' + plan.unresolvedHosts.join(', ') + ').';
  }
  if (withWarnings && plan.unresolvedPathRequests > 0) {
    s += ' ' + plan.unresolvedPathRequests + ' request' + (plan.unresolvedPathRequests === 1 ? ' has' : 's have') + ' an unresolved variable in the path or query' + (plan.unresolvedVariables.length ? ' (' + plan.unresolvedVariables.join(', ') + ')' : '') + ' and will be blocked.';
  }
  const bt = withWarnings ? blockedText(plan) : '';
  if (bt) s += ' ' + bt;
  return s;
}
