// pm.* sandbox prelude, part 1: core helpers. All parts are concatenated into
// one closure by pmsandbox; nothing here is reachable from user code except
// through the globals defined at the end of the prelude.
'use strict';
const __h = globalThis.__h;
const __in = JSON.parse(globalThis.__in);
const __fin = globalThis.__fin;
delete globalThis.__h;
delete globalThis.__in;
delete globalThis.__fin;

const LIM = __in.limits;
const CAPS = __in.caps;

class UnsupportedError extends Error {
  constructor(api) { super('unsupported: ' + api); this.name = 'UnsupportedError'; this.api = api; }
}
class AssertionError extends Error {
  constructor(msg, extra) {
    super(msg); this.name = 'AssertionError';
    if (extra) { this.expected = extra.expected; this.actual = extra.actual; }
  }
}
const unsupportedSeen = [];
function unsupported(api) {
  if (unsupportedSeen.indexOf(api) < 0) unsupportedSeen.push(api);
  throw new UnsupportedError(api);
}
function need(cap, what) {
  if (!CAPS[cap]) throw new Error('capability denied: ' + what + ' requires ' + cap);
}

const hasOwn = (o, k) => Object.prototype.hasOwnProperty.call(o, k);
function setOwn(o, k, v) { Object.defineProperty(o, k, { value: v, writable: true, enumerable: true, configurable: true }); }
const isObj = (v) => v !== null && typeof v === 'object';
const isStr = (v) => typeof v === 'string';
function typeOf(v) {
  if (v === null) return 'null';
  if (v === undefined) return 'undefined';
  if (Array.isArray(v)) return 'array';
  if (v instanceof Date) return 'date';
  if (v instanceof RegExp) return 'regexp';
  if (v instanceof Error) return 'error';
  if (v instanceof Map) return 'map';
  if (v instanceof Set) return 'set';
  return typeof v;
}

function fmt(v, depth) {
  depth = depth || 0;
  if (typeof v === 'string') return depth ? JSON.stringify(v) : "'" + v + "'";
  if (v === undefined) return 'undefined';
  if (typeof v === 'function') return '[Function' + (v.name ? ': ' + v.name : '') + ']';
  if (typeof v === 'bigint') return v + 'n';
  if (v instanceof RegExp || v instanceof Date) return String(v);
  if (v instanceof Error) return v.name + ': ' + v.message;
  if (isObj(v)) {
    try {
      if (v.toJSON && typeof v.toJSON === 'function' && !Array.isArray(v)) return fmt(v.toJSON(), depth + 1);
      const s = JSON.stringify(v);
      return s === undefined ? String(v) : (s.length > 200 ? s.slice(0, 200) + '...' : s);
    } catch (e) { return '[object]'; }
  }
  return String(v);
}
function consoleText(args) {
  const out = [];
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    out.push(typeof a === 'string' ? a : fmt(a, 1));
  }
  return out.join(' ');
}

function deepEqual(a, b, seen) {
  if (a === b) return a !== 0 || 1 / a === 1 / b;
  if (a !== a && b !== b) return true;
  if (!isObj(a) || !isObj(b)) return false;
  if (Object.getPrototypeOf(a) !== Object.getPrototypeOf(b)) return false;
  if (a instanceof Date) return a.getTime() === b.getTime();
  if (a instanceof RegExp) return String(a) === String(b);
  seen = seen || [];
  for (let i = 0; i < seen.length; i++) if (seen[i][0] === a && seen[i][1] === b) return true;
  seen.push([a, b]);
  if (a instanceof Map || a instanceof Set) {
    if (a.size !== b.size) return false;
    const x = Array.from(a), y = Array.from(b);
    return deepEqual(x, y, seen);
  }
  const ka = Object.keys(a), kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  for (let i = 0; i < ka.length; i++) {
    if (!hasOwn(b, ka[i])) return false;
    if (!deepEqual(a[ka[i]], b[ka[i]], seen)) return false;
  }
  return true;
}

function getPath(obj, path) {
  const parts = String(path).replace(/\[(\d+)\]/g, '.$1').split('.').filter((x) => x !== '');
  let cur = obj;
  for (let i = 0; i < parts.length; i++) {
    if (cur === null || cur === undefined) return { found: false };
    if (!(parts[i] in Object(cur))) return { found: false };
    cur = cur[parts[i]];
  }
  return { found: true, value: cur };
}

// ---- PropertyList / HeaderList -------------------------------------------
class Header {
  constructor(o, v) {
    if (typeof o === 'string' && v === undefined) {
      const i = o.indexOf(':');
      this.key = i < 0 ? o : o.slice(0, i).trim();
      this.value = i < 0 ? '' : o.slice(i + 1).trim();
    } else if (typeof o === 'string') { this.key = o; this.value = String(v); } else {
      this.key = String(o.key); this.value = o.value === undefined ? '' : String(o.value);
      if (o.disabled) this.disabled = true;
    }
  }
  toString() { return this.key + ': ' + this.value; }
  toJSON() { return this.disabled ? { key: this.key, value: this.value, disabled: true } : { key: this.key, value: this.value }; }
}

class PropertyList {
  constructor(items, keyName, Ctor) {
    this.__keyName = keyName || 'key';
    this.__Ctor = Ctor || Header;
    this.members = [];
    (items || []).forEach((i) => this.members.push(new this.__Ctor(i)));
  }
  __match(k) { const l = String(k).toLowerCase(); return (m) => String(m[this.__keyName]).toLowerCase() === l; }
  get(k) { const m = this.members.find(this.__match(k)); return m ? m.value : undefined; }
  one(k) { return this.members.find(this.__match(k)); }
  has(k) { return this.members.some(this.__match(k)); }
  add(i) { this.members.push(i instanceof this.__Ctor ? i : new this.__Ctor(i)); }
  append(i) { this.add(i); }
  prepend(i) { this.members.unshift(i instanceof this.__Ctor ? i : new this.__Ctor(i)); }
  upsert(i) {
    const n = i instanceof this.__Ctor ? i : new this.__Ctor(i);
    const idx = this.members.findIndex(this.__match(n[this.__keyName]));
    if (idx < 0) this.members.push(n); else this.members[idx] = n;
  }
  remove(k) {
    if (typeof k === 'function') { this.members = this.members.filter((m) => !k(m)); return; }
    const mm = this.__match(isObj(k) ? k[this.__keyName] : k);
    this.members = this.members.filter((m) => !mm(m));
  }
  clear() { this.members = []; }
  count() { return this.members.length; }
  all() { return this.members.slice(); }
  idx(i) { return this.members[i]; }
  each(fn, ctx) { this.members.slice().forEach((m) => fn.call(ctx, m)); }
  map(fn, ctx) { return this.members.map((m) => fn.call(ctx, m)); }
  filter(fn, ctx) { return this.members.filter((m) => fn.call(ctx, m)); }
  find(fn, ctx) { return this.members.find((m) => fn.call(ctx, m)); }
  reduce(fn, acc) { return this.members.reduce(fn, acc); }
  populate(items) { (items || []).forEach((i) => this.add(i)); }
  toObject(excludeDisabled, caseSensitive, multi) {
    const o = {};
    this.members.forEach((m) => {
      if (excludeDisabled && m.disabled) return;
      const k = caseSensitive ? m[this.__keyName] : m[this.__keyName];
      if (multi && hasOwn(o, k)) setOwn(o, k, [].concat(o[k], m.value)); else setOwn(o, k, m.value);
    });
    return o;
  }
  toString() { return this.members.map((m) => m.toString()).join('\n'); }
  toJSON() { return this.members.map((m) => m.toJSON()); }
}
