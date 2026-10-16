// intercept-delta.js — pure reducer for the per-item intercept delta stream (no
// imports, no DOM), so it runs under `node --test`.
//
// The control plane sends the hold queue as deltas now, not a full re-broadcast
// on every event: `intercept.held` adds one item, `intercept.released` removes
// one. SSE is lossy — the server drops a frame for a slow client — so every
// delta and every snapshot carries a monotonic generation `seq` (the engine's
// counter after the change). The client applies a delta only when its seq is
// exactly one past what it holds; a gap (a missed or out-of-order frame) or an
// unknown baseline makes it ask for a fresh snapshot instead of guessing, and a
// duplicate (a seq already seen) is ignored. This is what keeps the queue
// correct across any dropped or doubled delta: a missed hold or release can
// never leave a request wrongly held or wrongly forwarded, because the client
// rebases on the authoritative snapshot rather than trusting a broken sequence.

// SEQ_UNKNOWN marks "no trusted baseline yet": before the first snapshot, and
// after a gap until the resnapshot lands. A delta is never applied in this state.
export const SEQ_UNKNOWN = -1;

// queueKeyForSide maps a delta's side to the interceptJSON field it mutates.
export function queueKeyForSide(side) {
  return side === 'resp' ? 'responseQueue' : 'queue';
}

// snapshotSeq reads a snapshot's seq, tolerating a missing/old field by
// reporting SEQ_UNKNOWN (an older server that never sends seq keeps working via
// full renders — it just never advances into the delta fast-path).
export function snapshotSeq(snapshot) {
  const s = snapshot && snapshot.seq;
  return typeof s === 'number' && Number.isFinite(s) && s >= 0 ? s : SEQ_UNKNOWN;
}

// deltaType normalizes an SSE message type / explicit kind to 'held'|'released'.
export function deltaType(delta) {
  if (!delta) return '';
  if (delta.type === 'intercept.held' || delta.type === 'held') return 'held';
  if (delta.type === 'intercept.released' || delta.type === 'released') return 'released';
  return '';
}

// applyDelta folds one delta into the intercept state. It never mutates the input
// — on 'applied' it returns a shallow-cloned state with the one changed queue.
// Returns { state, seq, status }:
//   'applied'   — contiguous (seq === currentSeq+1); queue + seq advanced
//   'duplicate' — seq <= currentSeq; already reflected, ignored (no change)
//   'gap'       — no baseline, a non-numeric seq, or a future seq; the caller
//                 must resnapshot (state + seq returned unchanged)
export function applyDelta(intercept, currentSeq, delta) {
  const state = intercept || {};
  const next = Number(delta && delta.seq);
  const kind = deltaType(delta);
  if (!kind || !Number.isFinite(next)) return { state, seq: currentSeq, status: 'gap' };
  if (currentSeq === SEQ_UNKNOWN) return { state, seq: currentSeq, status: 'gap' };
  if (next <= currentSeq) return { state, seq: currentSeq, status: 'duplicate' };
  if (next !== currentSeq + 1) return { state, seq: currentSeq, status: 'gap' };

  const key = queueKeyForSide(delta.side);
  const base = Array.isArray(state[key]) ? state[key] : [];
  let list;
  if (kind === 'held') {
    const item = delta.item || {};
    // Dedupe by id so a snapshot/delta overlap can never double-insert a row.
    list = base.filter((h) => h.id !== item.id).concat([item]);
  } else {
    const id = delta.id;
    list = base.filter((h) => h.id !== id);
  }
  return { state: { ...state, [key]: list }, seq: next, status: 'applied' };
}
