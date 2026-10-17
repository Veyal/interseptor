// api-inflight.js — in-flight sharing for idempotent reads. Boot fans out to
// many independent loaders, and several of them GET the same endpoint at once
// (/api/readiness, /api/version, /api/project, …). core.js api() routes plain
// GETs through share() so concurrent identical reads cost one request; the entry
// is dropped as soon as it settles, so nothing is cached past the response.
//
// A shared read must never hand back data older than a change the caller could
// know about. core.js therefore calls clear() when a mutating request starts and
// when it settles, and app.js calls it for every live event: a read issued after
// either point starts a fresh request instead of joining an older one.
//
// No DOM and no core.js import, so this stays loadable under node --test.

export function createInflight() {
  const pending = new Map();
  return {
    // share(key, start) returns the pending promise for key, or calls start()
    // (on the next microtask) and remembers its promise until it settles.
    share(key, start) {
      const hit = pending.get(key);
      if (hit) return hit;
      const p = Promise.resolve().then(start);
      pending.set(key, p);
      const done = () => { if (pending.get(key) === p) pending.delete(key); };
      p.then(done, done);
      return p;
    },
    // clear() forgets every pending read; callers already waiting keep their
    // promise, later callers start a new request.
    clear() { pending.clear(); },
    size() { return pending.size; },
  };
}

// shareableRead reports whether an api() call may join another caller's
// request: a GET whose options carry nothing but the method. A caller-owned
// signal, custom headers, a cache mode or a body each make the call distinct.
export function shareableRead(opts) {
  const o = opts || {};
  if (String(o.method || 'GET').toUpperCase() !== 'GET') return false;
  return Object.keys(o).every((k) => k === 'method' || o[k] === undefined);
}
