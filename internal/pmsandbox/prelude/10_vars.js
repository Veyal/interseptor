// part 2: variable scopes, Url, Request, Response, cookies.
const secretNames = {};
(__in.vars.secret || []).forEach((n) => { secretNames[n] = true; });
const changes = [];
function chgPush(c) { if (changes.length < 5000) changes.push(c); }

function dyn(name) {
  switch (name) {
    case '$guid': case '$randomUUID': return __h.uuid();
    case '$timestamp': return String(Math.floor(Date.now() / 1000));
    case '$isoTimestamp': return new Date(Date.now()).toISOString();
    case '$randomInt': return String(Math.floor(Math.random() * 1001));
    default: return undefined;
  }
}

const SCOPE_DATA = new WeakMap();
const sd = (s) => SCOPE_DATA.get(s).d;
class VarScope {
  constructor(name, data, writable) {
    this.__name = name; SCOPE_DATA.set(this, { d: Object.assign(Object.create(null), data || {}) }); this.__w = writable !== false;
  }
  get(k) {
    need('varsRead', 'reading variables');
    if (!hasOwn(sd(this), k)) return undefined;
    if (secretNames[k] && !CAPS.secretsRead) return undefined;
    return sd(this)[k];
  }
  has(k) { need('varsRead', 'reading variables'); return hasOwn(sd(this), k); }
  set(k, v) {
    need('varsWrite', 'writing variables');
    if (!this.__w) throw new Error('pm.' + this.__name + ' is read-only');
    if (typeof v === 'function' || typeof v === 'symbol') throw new TypeError('cannot store a ' + typeof v + ' in pm.' + this.__name);
    const key = String(k);
    sd(this)[key] = v;
    chgPush({ scope: this.__name, name: key, op: 'set', value: v });
  }
  unset(k) {
    need('varsWrite', 'writing variables');
    if (!this.__w) throw new Error('pm.' + this.__name + ' is read-only');
    delete sd(this)[k];
    chgPush({ scope: this.__name, name: String(k), op: 'unset' });
  }
  clear() {
    need('varsWrite', 'writing variables');
    if (!this.__w) throw new Error('pm.' + this.__name + ' is read-only');
    SCOPE_DATA.get(this).d = Object.create(null);
    chgPush({ scope: this.__name, name: '', op: 'clear' });
  }
  toObject() {
    need('varsRead', 'reading variables');
    const o = {};
    Object.keys(sd(this)).forEach((k) => { if (!secretNames[k] || CAPS.secretsRead) setOwn(o, k, sd(this)[k]); });
    return o;
  }
  replaceIn(s) { return replaceIn(String(s)); }
  toJSON() { return this.toObject(); }
}
const scopes = {
  environment: new VarScope('environment', __in.vars.environment),
  globals: new VarScope('globals', __in.vars.globals),
  collection: new VarScope('collection', __in.vars.collection),
  local: new VarScope('local', __in.vars.local),
  iteration: new VarScope('iterationData', __in.vars.iterationData, false),
};
Object.defineProperty(scopes.environment, 'name', { value: __in.vars.envName || '', enumerable: false });
const ORDER = [scopes.local, scopes.iteration, scopes.collection, scopes.environment, scopes.globals];
function lookup(k) {
  for (let i = 0; i < ORDER.length; i++) if (hasOwn(sd(ORDER[i]), k)) return { found: true, value: sd(ORDER[i])[k] };
  return { found: false };
}
function replaceIn(s, depth) {
  depth = depth || 0;
  if (depth > 4) return s;
  return s.replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (m, name) => {
    let r;
    if (name.charAt(0) === '$') r = dyn(name);
    else { const l = lookup(name); if (l.found) r = l.value; }
    if (r === undefined) return m;
    if (secretNames[name] && !CAPS.secretsRead) return m;
    return replaceIn(typeof r === 'string' ? r : fmt(r, 1), depth + 1);
  });
}
const variables = {
  get(k) { need('varsRead', 'reading variables'); const l = lookup(k); if (l.found && secretNames[k] && !CAPS.secretsRead) return undefined; return l.value; },
  has(k) { need('varsRead', 'reading variables'); return lookup(k).found; },
  set(k, v) { scopes.local.set(k, v); },
  unset(k) { scopes.local.unset(k); },
  clear() { scopes.local.clear(); },
  replaceIn(s) { need('varsRead', 'reading variables'); return replaceIn(String(s)); },
  toObject() {
    need('varsRead', 'reading variables');
    const o = {};
    for (let i = ORDER.length - 1; i >= 0; i--) Object.keys(sd(ORDER[i])).forEach((k) => setOwn(o, k, sd(ORDER[i])[k]));
    Object.keys(o).forEach((k) => { if (secretNames[k] && !CAPS.secretsRead) delete o[k]; });
    return o;
  },
};

// ---- Url -------------------------------------------------------------------
class Query {
  constructor(o) { this.key = o.key; this.value = o.value === undefined || o.value === null ? '' : String(o.value); if (o.disabled) this.disabled = true; }
  toString() { return this.value === '' && this.noEq ? this.key : this.key + '=' + this.value; }
  toJSON() { return { key: this.key, value: this.value }; }
}
function parseQuery(q) {
  if (!q) return [];
  return q.split('&').filter((x) => x !== '').map((p) => {
    const i = p.indexOf('=');
    const dec = (x) => { try { return decodeURIComponent(x.replace(/\+/g, ' ')); } catch (e) { return x; } };
    return i < 0 ? { key: dec(p), value: '' } : { key: dec(p.slice(0, i)), value: dec(p.slice(i + 1)) };
  });
}
function encQ(s) { return encodeURIComponent(s).replace(/%7B/g, '{').replace(/%7D/g, '}').replace(/%24/g, '$'); }
class Url {
  constructor(src) {
    this.query = new PropertyList([], 'key', Query);
    this.protocol = undefined; this.__host = ''; this.__port = undefined; this.path = []; this.hash = undefined; this.auth = undefined;
    if (isObj(src) && !(src instanceof Url)) {
      if (src.raw !== undefined) this.update(src.raw); else {
        this.protocol = src.protocol; this.__host = [].concat(src.host || []).join('.'); this.__port = src.port;
        this.path = [].concat(src.path || []); (src.query || []).forEach((q) => this.query.add(q));
      }
    } else if (src instanceof Url) this.update(src.toString()); else if (src !== undefined) this.update(String(src));
  }
  get host() { return this.__host === '' ? [] : this.__host.split('.'); }
  set host(v) { this.__host = [].concat(v).join('.'); }
  get port() { return this.__port; }
  set port(v) { this.__port = v === undefined || v === null || v === '' ? undefined : String(v); }
  update(s) {
    s = String(s);
    const m = /^(?:([a-zA-Z][a-zA-Z0-9+.-]*):\/\/)?([^\/?#]*)([^?#]*)(?:\?([^#]*))?(?:#(.*))?$/.exec(s);
    this.protocol = m[1]; let hp = m[2];
    const at = hp.lastIndexOf('@');
    if (at >= 0) { this.auth = hp.slice(0, at); hp = hp.slice(at + 1); }
    const pm = /^(.*?):(\d+)$/.exec(hp);
    if (pm && hp.indexOf('{{') < 0) { this.__host = pm[1]; this.__port = pm[2]; } else if (pm) { this.__host = pm[1]; this.__port = pm[2]; } else { this.__host = hp; this.__port = undefined; }
    this.path = m[3] ? m[3].replace(/^\//, '').split('/') : [];
    if (this.path.length === 1 && this.path[0] === '') this.path = [];
    this.query = new PropertyList(parseQuery(m[4]), 'key', Query);
    this.hash = m[5];
    return this;
  }
  getHost() { return this.__host; }
  getPath() { return '/' + this.path.join('/'); }
  getQueryString() { return this.query.members.filter((q) => !q.disabled).map((q) => encQ(q.key) + '=' + encQ(q.value)).join('&'); }
  getPathWithQuery() { const q = this.getQueryString(); return this.getPath() + (q ? '?' + q : ''); }
  getRemote() { return this.__host + (this.__port ? ':' + this.__port : ''); }
  addQueryParams(p) {
    if (typeof p === 'string') p = parseQuery(p);
    [].concat(p).forEach((q) => this.query.add(q));
  }
  removeQueryParams(names) { [].concat(names).forEach((n) => this.query.remove(isObj(n) ? n.key : n)); }
  toString(forceProtocol) {
    let s = '';
    if (this.protocol) s += this.protocol + '://'; else if (forceProtocol) s += 'http://';
    if (this.auth) s += this.auth + '@';
    s += this.__host;
    if (this.__port) s += ':' + this.__port;
    if (this.path.length || this.query.count()) s += this.getPath();
    const q = this.getQueryString();
    if (q) s += '?' + q;
    if (this.hash !== undefined) s += '#' + this.hash;
    return s;
  }
  toJSON() { return this.toString(); }
}

// ---- Request ---------------------------------------------------------------
class Body {
  constructor(b) {
    b = b || {};
    this.mode = b.mode || (b.raw ? 'raw' : undefined);
    this.raw = b.raw;
    this.options = b.language ? { raw: { language: b.language } } : undefined;
    this.urlencoded = new PropertyList(b.urlencoded || [], 'key', Header);
    this.formdata = new PropertyList(b.formdata || [], 'key', Header);
  }
  isEmpty() {
    if (this.mode === 'raw') return !this.raw;
    if (this.mode === 'urlencoded') return this.urlencoded.count() === 0;
    if (this.mode === 'formdata') return this.formdata.count() === 0;
    return !this.mode;
  }
  toString() {
    if (this.mode === 'raw') return this.raw === undefined ? '' : this.raw;
    if (this.mode === 'urlencoded') return this.urlencoded.members.filter((m) => !m.disabled).map((m) => encQ(m.key) + '=' + encQ(m.value)).join('&');
    return '';
  }
  update(spec) {
    if (typeof spec === 'string') { this.mode = 'raw'; this.raw = spec; return; }
    if (spec.mode) this.mode = spec.mode;
    if (spec.raw !== undefined) this.raw = spec.raw;
    if (spec.urlencoded) this.urlencoded = new PropertyList(Array.isArray(spec.urlencoded) ? spec.urlencoded : (spec.urlencoded.members || []), 'key', Header);
    if (spec.formdata) this.formdata = new PropertyList(Array.isArray(spec.formdata) ? spec.formdata : (spec.formdata.members || []), 'key', Header);
    if (spec.options) this.options = spec.options;
  }
  toJSON() {
    const o = { mode: this.mode };
    if (this.mode === 'raw') { o.raw = this.raw; if (this.options && this.options.raw) o.language = this.options.raw.language; }
    if (this.mode === 'urlencoded') o.urlencoded = this.urlencoded.toJSON();
    if (this.mode === 'formdata') o.formdata = this.formdata.toJSON();
    return o;
  }
}
class RequestObj {
  constructor(r) {
    this.name = r.name; this.id = r.id;
    this.method = r.method || 'GET';
    this.__url = new Url(r.url || '');
    this.headers = new PropertyList(r.headers || [], 'key', Header);
    this.body = new Body(r.body);
    this.auth = r.auth ? { type: r.auth.type, params: Object.assign({}, r.auth.params) } : undefined;
  }
  get url() { return this.__url; }
  set url(v) { this.__url = v instanceof Url ? v : new Url(String(v)); }
  getHeaders(opts) { return this.headers.toObject(opts && opts.ignoreCase === false ? false : false, false, opts && opts.enableMultiValue); }
  addHeader(h) { this.headers.add(h); }
  removeHeader(k) { this.headers.remove(k); }
  upsertHeader(h) { this.headers.upsert(h); }
  update(spec) {
    if (typeof spec === 'string') { this.url = spec; return; }
    if (spec.url !== undefined) this.url = isObj(spec.url) && spec.url.raw ? spec.url.raw : spec.url;
    if (spec.method) this.method = spec.method;
    if (spec.header || spec.headers) this.headers = new PropertyList(spec.header || spec.headers, 'key', Header);
    if (spec.body) this.body.update(spec.body);
  }
  toJSON() {
    return { method: this.method, url: this.url.toString(), headers: this.headers.toJSON(), body: this.body.toJSON(), auth: this.auth };
  }
}

// ---- Response --------------------------------------------------------------
class CookieList extends PropertyList {
  constructor(items) { super(items, 'name', Cookie); }
  toObject() { const o = {}; this.members.forEach((m) => { o[m.name] = m.value; }); return o; }
}
class Cookie {
  constructor(c) { Object.assign(this, c); this.name = String(c.name); this.value = c.value === undefined ? '' : String(c.value); }
  toString() { return this.name + '=' + this.value; }
  toJSON() { return Object.assign({}, this); }
}
class ResponseObj {
  constructor(r) {
    this.code = r.code; this.status = r.status || '';
    this.headers = new PropertyList(r.headers || [], 'key', Header);
    this.responseTime = r.responseTime || 0;
    this.__body = r.body || '';
    this.responseSize = r.size === undefined ? this.__body.length : r.size;
    this.cookies = new CookieList(r.cookies || []);
    Object.defineProperty(this, '__isResponse', { value: true, enumerable: false });
  }
  text() { return this.__body; }
  json(reviver) {
    const b = this.__body.charCodeAt(0) === 0xFEFF ? this.__body.slice(1) : this.__body;
    return JSON.parse(b, reviver);
  }
  reason() { return this.status; }
  size() { return { body: this.responseSize, header: 0, total: this.responseSize }; }
  get stream() { unsupported('pm.response.stream'); }
  get to() { return wrapAssertion(new RespAssertion(this)); }
  toJSON() { return { code: this.code, status: this.status, headers: this.headers.toJSON() }; }
}
