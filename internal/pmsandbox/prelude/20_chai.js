// part 3: Chai-subset assertions and pm.test registry.
const CHAIN_WORDS = ['to', 'be', 'been', 'is', 'that', 'which', 'and', 'has', 'have', 'with', 'at', 'of', 'same', 'but', 'does', 'still', 'also', 'itself'];
const PASSTHROUGH = { then: 1, toJSON: 1, inspect: 1, constructor: 1, valueOf: 1, toString: 1, asymmetricMatch: 1 };

class Assertion {
  constructor(obj, msg, flags) {
    this.__obj = obj; this.__msg = msg ? String(msg) + ': ' : '';
    this.__f = flags || {};
  }
  __flag(name) { const f = Object.assign({}, this.__f); f[name] = true; return this.__make(f); }
  __make(f) { return wrapAssertion(new this.constructor(this.__obj, this.__msg.replace(/: $/, ''), f)); }
  __assert(ok, pos, neg, expected, actual) {
    const pass = this.__f.not ? !ok : ok;
    if (pass) return;
    const text = this.__msg + 'expected ' + fmt(this.__obj) + ' to ' + (this.__f.not ? neg : pos);
    throw new AssertionError(text, { expected: expected === undefined ? undefined : fmt(expected), actual: actual === undefined ? fmt(this.__obj) : fmt(actual) });
  }
  get not() { return this.__flag('not'); }
  get deep() { return this.__flag('deep'); }
  get own() { return this.__flag('own'); }
  get nested() { return this.__flag('nested'); }
  get any() { return this.__flag('any'); }
  get all() { return this.__flag('all'); }
  get ordered() { return this.__flag('ordered'); }
  get ok() { this.__assert(!!this.__obj, 'be truthy', 'be falsy'); return this.__self(); }
  get true() { this.__assert(this.__obj === true, 'be true', 'not be true'); return this.__self(); }
  get false() { this.__assert(this.__obj === false, 'be false', 'not be false'); return this.__self(); }
  get null() { this.__assert(this.__obj === null, 'be null', 'not be null'); return this.__self(); }
  get undefined() { this.__assert(this.__obj === undefined, 'be undefined', 'not be undefined'); return this.__self(); }
  get NaN() { this.__assert(this.__obj !== this.__obj, 'be NaN', 'not be NaN'); return this.__self(); }
  get exist() { this.__assert(this.__obj !== null && this.__obj !== undefined, 'exist', 'not exist'); return this.__self(); }
  get empty() {
    const o = this.__obj; let e;
    if (typeof o === 'string' || Array.isArray(o)) e = o.length === 0;
    else if (o instanceof Map || o instanceof Set) e = o.size === 0;
    else if (isObj(o)) e = Object.keys(o).length === 0;
    else throw new AssertionError(this.__msg + '.empty was passed non-string primitive ' + fmt(o));
    this.__assert(e, 'be empty', 'not be empty'); return this.__self();
  }
  get length() {
    const self = this.__make(Object.assign({}, this.__f, { len: true }));
    const fn = function (n) { return self.lengthOf(n); };
    return new Proxy(fn, { get(t, p) { return self[p]; } });
  }
  __self() { return this.__proxy || this; }
  equal(v) { this.__assert(this.__f.deep ? deepEqual(this.__obj, v) : this.__obj === v, 'equal ' + fmt(v), 'not equal ' + fmt(v), v); return this.__self(); }
  eql(v) { this.__assert(deepEqual(this.__obj, v), 'deeply equal ' + fmt(v), 'not deeply equal ' + fmt(v), v); return this.__self(); }
  property(name, val) {
    const o = this.__obj;
    if (o === null || o === undefined) throw new AssertionError(this.__msg + 'Target cannot be null or undefined.');
    let has, v;
    if (this.__f.nested) { const r = getPath(o, name); has = r.found; v = r.value; } else if (this.__f.own) { has = hasOwn(Object(o), name); v = o[name]; } else { has = name in Object(o); v = o[name]; }
    if (arguments.length > 1) {
      const eq = this.__f.deep ? deepEqual(v, val) : v === val;
      this.__assert(has && eq, 'have property ' + fmt(name) + ' of ' + fmt(val) + (has ? ', but got ' + fmt(v) : ''), 'not have property ' + fmt(name) + ' of ' + fmt(val), val, v);
    } else this.__assert(has, 'have property ' + fmt(name), 'not have property ' + fmt(name));
    return wrapAssertion(new this.constructor(v, '', {}));
  }
  ownProperty(name, val) { return this.__flag('own').property.apply(this.__flag('own'), arguments); }
  a(type) {
    const t = typeOf(this.__obj);
    this.__assert(t === String(type).toLowerCase(), 'be a ' + type, 'not be a ' + type, type, t);
    return this.__self();
  }
  an(type) { return this.a(type); }
  instanceof(c) { this.__assert(this.__obj instanceof c, 'be an instance of ' + (c && c.name), 'not be an instance of ' + (c && c.name)); return this.__self(); }
  instanceOf(c) { return this.instanceof(c); }
  above(n) { return this.__cmp((a, b) => a > b, 'above', n); }
  gt(n) { return this.above(n); }
  greaterThan(n) { return this.above(n); }
  below(n) { return this.__cmp((a, b) => a < b, 'below', n); }
  lt(n) { return this.below(n); }
  lessThan(n) { return this.below(n); }
  least(n) { return this.__cmp((a, b) => a >= b, 'at least', n); }
  gte(n) { return this.least(n); }
  greaterThanOrEqual(n) { return this.least(n); }
  most(n) { return this.__cmp((a, b) => a <= b, 'at most', n); }
  lte(n) { return this.most(n); }
  lessThanOrEqual(n) { return this.most(n); }
  __cmp(fn, word, n) {
    let v = this.__obj;
    if (this.__f.len) v = v.length;
    if (typeof v !== 'number' && typeof v !== 'bigint') throw new AssertionError(this.__msg + 'expected ' + fmt(v) + ' to be a number');
    this.__assert(fn(v, n), (this.__f.len ? 'have length ' : 'be ') + word + ' ' + n, (this.__f.len ? 'not have length ' : 'not be ') + word + ' ' + n, n, v);
    return this.__self();
  }
  within(a, b) {
    const v = this.__f.len ? this.__obj.length : this.__obj;
    this.__assert(v >= a && v <= b, 'be within ' + a + '..' + b, 'not be within ' + a + '..' + b);
    return this.__self();
  }
  closeTo(n, d) { this.__assert(Math.abs(this.__obj - n) <= d, 'be close to ' + n + ' +/- ' + d, 'not be close to ' + n + ' +/- ' + d, n); return this.__self(); }
  approximately(n, d) { return this.closeTo(n, d); }
  lengthOf(n) {
    const o = this.__obj;
    const l = (o instanceof Map || o instanceof Set) ? o.size : (o === null || o === undefined ? undefined : o.length);
    if (l === undefined) throw new AssertionError(this.__msg + 'expected ' + fmt(o) + ' to have property length');
    this.__assert(l === n, 'have length ' + n + ', but got ' + l, 'not have length ' + n, n, l);
    return this.__self();
  }
  oneOf(list) {
    if (!Array.isArray(list)) throw new AssertionError(this.__msg + 'expected ' + fmt(list) + ' to be an array');
    this.__assert(list.indexOf(this.__obj) >= 0, 'be one of ' + fmt(list), 'not be one of ' + fmt(list), list);
    return this.__self();
  }
  match(re) { this.__assert(re.test(String(this.__obj)), 'match ' + re, 'not match ' + re, re); return this.__self(); }
  matches(re) { return this.match(re); }
  string(s) { this.__assert(typeof this.__obj === 'string' && this.__obj.indexOf(s) >= 0, 'contain ' + fmt(s), 'not contain ' + fmt(s), s); return this.__self(); }
  keys() {
    const want = [].concat.apply([], Array.prototype.slice.call(arguments)).map(String);
    const o = this.__obj;
    const have = o instanceof Map ? Array.from(o.keys()).map(String) : Object.keys(o);
    let ok;
    if (this.__f.any) ok = want.some((k) => have.indexOf(k) >= 0);
    else if (this.__f.contains) ok = want.every((k) => have.indexOf(k) >= 0);
    else ok = want.every((k) => have.indexOf(k) >= 0) && want.length === have.length;
    this.__assert(ok, 'have keys ' + fmt(want), 'not have keys ' + fmt(want), want, have);
    return this.__self();
  }
  key() { return this.keys.apply(this, arguments); }
  members(set) {
    const o = this.__obj;
    const same = o.length === set.length && o.every((x) => set.some((y) => this.__f.deep ? deepEqual(x, y) : x === y));
    const sub = set.every((y) => o.some((x) => this.__f.deep ? deepEqual(x, y) : x === y));
    this.__assert(this.__f.contains ? sub : same, 'have members ' + fmt(set), 'not have members ' + fmt(set), set);
    return this.__self();
  }
  satisfy(fn) { this.__assert(!!fn(this.__obj), 'satisfy ' + fmt(fn), 'not satisfy ' + fmt(fn)); return this.__self(); }
  throw(errType, errMsg) {
    let thrown, did = false;
    try { this.__obj(); } catch (e) { did = true; thrown = e; }
    let ok = did;
    if (did && typeof errType === 'function') ok = thrown instanceof errType;
    if (did && (isStr(errType) || errType instanceof RegExp)) errMsg = errType;
    if (did && errMsg !== undefined) { const m = String(thrown && thrown.message); ok = ok && (errMsg instanceof RegExp ? errMsg.test(m) : m.indexOf(errMsg) >= 0); }
    this.__assert(ok, 'throw an error', 'not throw an error'); return this.__self();
  }
  throws(a, b) { return this.throw(a, b); }
  get exists() { return this.exist; }
}
// include/contain are both chain flag and method in chai; expose method form.
Object.defineProperty(Assertion.prototype, 'include', {
  get() { const self = this; const f = Object.assign({}, this.__f, { contains: true }); const t = this.__make(f); const fn = function (v) { return Assertion.prototype.__includeImpl.call(self, v); }; return new Proxy(fn, { get(_t, p) { return t[p]; } }); },
  configurable: true,
});
Assertion.prototype.__includeImpl = function (v) {
  const o = this.__obj; let ok;
  if (typeof o === 'string') ok = o.indexOf(String(v)) >= 0;
  else if (Array.isArray(o)) ok = this.__f.deep ? o.some((x) => deepEqual(x, v)) : o.indexOf(v) >= 0;
  else if (o instanceof Set) ok = o.has(v);
  else if (o instanceof Map) ok = Array.from(o.values()).indexOf(v) >= 0;
  else if (isObj(o) && isObj(v)) ok = Object.keys(v).every((k) => k in o && (this.__f.deep ? deepEqual(o[k], v[k]) : o[k] === v[k]));
  else if (isObj(o)) ok = v in o;
  else throw new AssertionError(this.__msg + 'object tested must be an array, a map, an object, a set, a string, or a weakset');
  this.__assert(ok, 'include ' + fmt(v), 'not include ' + fmt(v), v);
  return this.__self();
};
Object.defineProperty(Assertion.prototype, 'contain', { get() { return this.include; }, configurable: true });
Object.defineProperty(Assertion.prototype, 'contains', { get() { return this.include; }, configurable: true });
Object.defineProperty(Assertion.prototype, 'includes', { get() { return this.include; }, configurable: true });
// a/an are both chain words and type assertions: `.to.be.an.instanceof(X)`, `.to.be.an('array')`.
['a', 'an'].forEach((n) => {
  const method = Assertion.prototype[n];
  Object.defineProperty(Assertion.prototype, n, {
    get() { const self = this; const fn = function (type) { return method.call(self, type); }; return new Proxy(fn, { get(_t, p) { return self[p]; } }); },
    configurable: true,
  });
});
CHAIN_WORDS.forEach((w) => Object.defineProperty(Assertion.prototype, w, { get() { return this.__self(); }, configurable: true }));

function wrapAssertion(a) {
  const p = new Proxy(a, {
    get(t, k, recv) {
      if (typeof k === 'symbol' || k in t || PASSTHROUGH[k]) {
        const v = Reflect.get(t, k, p);
        return v;
      }
      unsupported('chai assertion .' + String(k));
    },
  });
  a.__proxy = p;
  return p;
}

// Response-aware assertions (pm.response.to.* and pm.expect(pm.response)).
class RespAssertion extends Assertion {
  __code() { return this.__obj.code; }
  __range(lo, hi, name) { const c = this.__code(); this.__assert(c >= lo && c < hi, 'be ' + name + ' (code ' + lo + '-' + (hi - 1) + ')', 'not be ' + name, undefined, c); return this.__self(); }
  get ok() { this.__assert(this.__code() === 200, 'have status code 200', 'not have status code 200', 200, this.__code()); return this.__self(); }
  get success() { return this.__range(200, 300, 'success'); }
  get info() { return this.__range(100, 200, 'info'); }
  get redirection() { return this.__range(300, 400, 'a redirection'); }
  get clientError() { return this.__range(400, 500, 'a client error'); }
  get serverError() { return this.__range(500, 600, 'a server error'); }
  get error() { const c = this.__code(); this.__assert(c >= 400 && c < 600, 'be an error', 'not be an error', undefined, c); return this.__self(); }
  get accepted() { return this.__codeIs(202, 'accepted'); }
  get badRequest() { return this.__codeIs(400, 'bad request'); }
  get unauthorized() { return this.__codeIs(401, 'unauthorized'); }
  get forbidden() { return this.__codeIs(403, 'forbidden'); }
  get notFound() { return this.__codeIs(404, 'not found'); }
  get rateLimited() { return this.__codeIs(429, 'rate limited'); }
  get withBody() { this.__assert(this.__obj.text().length > 0, 'have a non-empty body', 'not have a body'); return this.__self(); }
  get json() { let ok = true; try { this.__obj.json(); } catch (e) { ok = false; } this.__assert(ok, 'have a JSON body', 'not have a JSON body'); return this.__self(); }
  __codeIs(n, name) { this.__assert(this.__code() === n, 'be ' + name + ' (code ' + n + ')', 'not be ' + name, n, this.__code()); return this.__self(); }
  status(v) {
    const r = this.__obj;
    const ok = typeof v === 'number' ? r.code === v : String(r.status).toLowerCase() === String(v).toLowerCase();
    this.__assert(ok, 'have status ' + fmt(v) + ' but got ' + fmt(typeof v === 'number' ? r.code : r.status), 'not have status ' + fmt(v), v, typeof v === 'number' ? r.code : r.status);
    return this.__self();
  }
  header(name, val) {
    const h = this.__obj.headers.get(name);
    let ok = h !== undefined;
    if (arguments.length > 1) ok = ok && h === String(val);
    this.__assert(ok, 'have header ' + name + (arguments.length > 1 ? ' with value ' + fmt(val) : ''), 'not have header ' + name);
    return this.__self();
  }
  jsonBody(path, val) {
    let j;
    try { j = this.__obj.json(); } catch (e) { throw new AssertionError(this.__msg + 'expected response body to be valid JSON'); }
    if (path === undefined) return this.__self();
    if (typeof path === 'object') { this.__assert(deepEqual(j, path), 'have json body ' + fmt(path), 'not have json body ' + fmt(path), path, j); return this.__self(); }
    const r = getPath(j, path);
    if (arguments.length > 1) this.__assert(r.found && deepEqual(r.value, val), 'have json path ' + path + ' equal to ' + fmt(val), 'not have json path ' + path + ' equal to ' + fmt(val), val, r.value);
    else this.__assert(r.found, 'have json path ' + path, 'not have json path ' + path);
    return this.__self();
  }
  body(v) {
    const t = this.__obj.text();
    let ok;
    if (v === undefined) ok = t.length > 0; else if (v instanceof RegExp) ok = v.test(t); else ok = t.indexOf(String(v)) >= 0;
    this.__assert(ok, 'have body ' + fmt(v), 'not have body ' + fmt(v));
    return this.__self();
  }
  jsonSchema() { unsupported('pm.response.to.have.jsonSchema (ajv/tv4 not shipped)'); }
}
function newExpect(obj, msg) {
  if (isObj(obj) && obj.__isResponse) return wrapAssertion(new RespAssertion(obj, msg));
  return wrapAssertion(new Assertion(obj, msg));
}
function expectFn(obj, msg) { return newExpect(obj, msg); }
expectFn.fail = function (msg) { throw new AssertionError(msg === undefined ? 'expect.fail()' : String(msg)); };

// ---- pm.test registry -----------------------------------------------------
const results = [];
const pending = [];
let testIndex = 0;
function record(name, status, message, extra) {
  if (results.length >= LIM.maxTests) {
    if (results.length === LIM.maxTests) results.push({ name: 'test limit reached', status: 'error', message: 'more than ' + LIM.maxTests + ' tests; the rest are dropped' });
    return;
  }
  const r = { name: String(name), status: status };
  if (message) r.message = String(message);
  if (extra) { if (extra.expected !== undefined) r.expected = extra.expected; if (extra.actual !== undefined) r.actual = extra.actual; if (extra.dur) r.durationMs = extra.dur; }
  results.push(r);
}
function classify(e) {
  if (e instanceof UnsupportedError) return 'unsupported';
  if (e instanceof AssertionError || (e && e.name === 'AssertionError')) return 'fail';
  return 'error';
}
function recordErr(name, e, dur) {
  const st = classify(e);
  record(name, st, e && e.message !== undefined ? e.message : String(e), { expected: e && e.expected, actual: e && e.actual, dur: dur });
}
function pmTest(name, fn) {
  testIndex++;
  const t0 = Date.now();
  if (typeof fn !== 'function') { record(name, 'skip', 'no test function'); return; }
  let result;
  try {
    if (fn.length > 0) {
      let doneCalled = false;
      let resolveDone;
      const p = new Promise((res) => { resolveDone = res; });
      const done = (err) => { if (doneCalled) return; doneCalled = true; if (err) recordErr(name, err, Date.now() - t0); else record(name, 'pass', '', { dur: Date.now() - t0 }); resolveDone(); };
      fn(done);
      pending.push(p.then(() => { if (!doneCalled) record(name, 'error', 'done() was never called'); }));
      return;
    }
    result = fn();
  } catch (e) { recordErr(name, e, Date.now() - t0); return; }
  if (result && typeof result.then === 'function') {
    pending.push(Promise.resolve(result).then(() => record(name, 'pass', '', { dur: Date.now() - t0 }), (e) => recordErr(name, e, Date.now() - t0)));
  } else record(name, 'pass', '', { dur: Date.now() - t0 });
}
pmTest.skip = function (name) { testIndex++; record(name, 'skip', 'skipped'); };
pmTest.index = function () { return testIndex; };
