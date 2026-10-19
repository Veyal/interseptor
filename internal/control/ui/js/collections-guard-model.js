// collections-guard-model.js — pure helpers for predicting the runner's destination guards.
// No imports, no DOM; node-tested in _js-tests/collections-guard-model.test.mjs.

// addableBlock: a block that adding the host to scope resolves. An include rule
// naming the host lifts both the scope refusal and the private-address IP guard.
export const addableBlock = (reason) => reason === 'out_of_scope' || reason === 'ip_guard';

// restrictedHost mirrors sender.restrictedIP for a literal destination host:
// loopback, RFC1918, link-local, CGNAT, unspecified and multicast addresses plus
// localhost names. A name that merely resolves to one cannot be told here; the
// server still enforces the guard at dial time.
export function restrictedHost(host) {
  const h = String(host || '').toLowerCase().replace(/^\[|\]$/g, '').replace(/\.$/, '');
  if (!h) return false;
  if (h === 'localhost' || h.endsWith('.localhost')) return true;
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(h);
  if (m) {
    const [a, b] = [Number(m[1]), Number(m[2])];
    return a === 127 || a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254)
      || (a === 100 && b >= 64 && b <= 127) || (a >= 224 && a <= 239) || h === '0.0.0.0';
  }
  if (!h.includes(':')) return false;
  if (h === '::' || h === '::1') return true;
  const first = parseInt(h.split(':')[0] || '0', 16);
  if (!Number.isFinite(first)) return false;
  return (first & 0xfe00) === 0xfc00 || (first & 0xffc0) === 0xfe80 || (first & 0xff00) === 0xff00;
}
