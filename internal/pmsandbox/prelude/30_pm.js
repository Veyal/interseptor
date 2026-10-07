// part 4: pm.* assembly, sendRequest, cookies, console, flow control, isp.*.
const pmExpect = expectFn;
const PASS_KEYS = { then: 1, toJSON: 1, inspect: 1, constructor: 1, valueOf: 1, toString: 1, asymmetricMatch: 1, nodeType: 1, tagName: 1 };
function strict(obj, label) {
  const px = new Proxy(obj, {
    get(t, k, r) {
      if (typeof k === 'symbol' || k in t || PASS_KEYS[k]) return Reflect.get(t, k, r);
      unsupported(label + '.' + String(k));
    },
  });
  if (SCOPE_DATA.has(obj)) SCOPE_DATA.set(px, SCOPE_DATA.get(obj));
  return px;
}

const flow = { nextSet: false, next: '', skip: false };
function setNext(name) {
  flow.nextSet = true;
  flow.next = name === null || name === undefined ? '' : String(name);
}
const scriptErrors = [];
function scriptErr(e) { if (scriptErrors.length < 20) scriptErrors.push(e); }

// ---- console ----------------------------------------------------------------
const consoleObj = {};
['log', 'info', 'warn', 'error', 'debug'].forEach((lv) => { consoleObj[lv] = function () { __h.console(lv, consoleText(arguments)); }; });
consoleObj.trace = consoleObj.debug;
consoleObj.dir = consoleObj.log;
consoleObj.table = consoleObj.log;
consoleObj.group = consoleObj.groupEnd = consoleObj.time = consoleObj.timeEnd = function () {};
consoleObj.assert = function (c) { if (!c) __h.console('error', 'Assertion failed: ' + consoleText(Array.prototype.slice.call(arguments, 1))); };

// ---- sendRequest ------------------------------------------------------------
function hostSend(req) {
  if (typeof req === 'string') req = { url: req };
  if (isObj(req) && req.url !== undefined && !isStr(req.url)) req = Object.assign({}, req, { url: String(req.url && req.url.raw !== undefined ? req.url.raw : req.url) });
  let headers = [];
  const h = req.header || req.headers;
  if (Array.isArray(h)) headers = h.map((x) => ({ key: String(x.key), value: String(x.value) }));
  else if (h instanceof PropertyList) headers = h.toJSON();
  else if (isObj(h)) headers = Object.keys(h).map((k) => ({ key: k, value: String(h[k]) }));
  const body = new Body(req.body && typeof req.body === 'string' ? { mode: 'raw', raw: req.body } : req.body).toJSON();
  const out = JSON.parse(__h.send(JSON.stringify({ method: String(req.method || 'GET').toUpperCase(), url: String(req.url), headers: headers, body: body })));
  if (out.error) throw new Error(out.error);
  return new ResponseObj({ code: out.code, status: out.status, headers: out.headers, body: out.body, responseTime: out.timeMs, size: out.size });
}
function pmSendRequest(req, cb) {
  need('netSend', 'pm.sendRequest');
  const p = new Promise((resolve, reject) => {
    Promise.resolve().then(() => { try { resolve(hostSend(req)); } catch (e) { reject(e); } });
  });
  if (typeof cb === 'function') {
    pending.push(p.then((r) => cb(null, r), (e) => cb(e, undefined)).catch(scriptErr));
    return undefined;
  }
  return p;
}

// ---- cookies ----------------------------------------------------------------
const cookieOps = [];
const jarList = new CookieList(__in.cookies || []);
function cbOrPromise(fn, cb) {
  const p = new Promise((res, rej) => { Promise.resolve().then(() => { try { res(fn()); } catch (e) { rej(e); } }); });
  if (typeof cb === 'function') { pending.push(p.then((v) => cb(null, v), (e) => cb(e)).catch(scriptErr)); return undefined; }
  return p;
}
const jar = {
  get(url, name, cb) { need('cookiesRead', 'cookie jar'); return cbOrPromise(() => jarList.get(name), cb); },
  getAll(url, cb) { need('cookiesRead', 'cookie jar'); return cbOrPromise(() => jarList.all(), cb); },
  set(url, name, value, cb) {
    need('cookiesWrite', 'cookie jar');
    if (typeof value === 'function') { cb = value; value = undefined; }
    const c = isObj(name) ? name : { name: name, value: value };
    return cbOrPromise(() => { jarList.upsert(new Cookie(c)); cookieOps.push({ op: 'set', url: String(url), cookie: new Cookie(c).toJSON() }); }, cb);
  },
  unset(url, name, cb) {
    need('cookiesWrite', 'cookie jar');
    return cbOrPromise(() => { jarList.remove(name); cookieOps.push({ op: 'clear', url: String(url), cookie: { name: String(name), value: '' } }); }, cb);
  },
  clear(url, cb) {
    need('cookiesWrite', 'cookie jar');
    return cbOrPromise(() => { jarList.clear(); cookieOps.push({ op: 'clear', url: String(url), cookie: { name: '', value: '' } }); }, cb);
  },
};
const cookiesObj = strict({
  get(n) { need('cookiesRead', 'pm.cookies'); return jarList.get(n); },
  has(n) { need('cookiesRead', 'pm.cookies'); return jarList.has(n); },
  toObject() { need('cookiesRead', 'pm.cookies'); return jarList.toObject(); },
  jar() { return jar; },
}, 'pm.cookies');

// ---- pm ---------------------------------------------------------------------
const reqObj = new RequestObj(__in.request || { method: 'GET', url: '' });
const respObj = __in.response ? new ResponseObj(Object.assign({}, __in.response, { body: __in.response.body })) : undefined;
const info = strict({
  eventName: __in.info.eventName || __in.phase, iteration: __in.info.iteration || 0, iterationCount: __in.info.iterationCount || 1,
  requestName: __in.info.requestName || reqObj.name || '', requestId: __in.info.requestId || reqObj.id || '',
}, 'pm.info');
const execution = strict({
  setNextRequest: setNext,
  skipRequest() { if (__in.phase !== 'prerequest') throw new Error('pm.execution.skipRequest is only valid in pre-request scripts'); flow.skip = true; },
}, 'pm.execution');
const pmObj = {
  info: info,
  environment: strict(scopes.environment, 'pm.environment'),
  globals: strict(scopes.globals, 'pm.globals'),
  collectionVariables: strict(scopes.collection, 'pm.collectionVariables'),
  iterationData: strict(scopes.iteration, 'pm.iterationData'),
  variables: strict(variables, 'pm.variables'),
  request: reqObj,
  cookies: cookiesObj,
  test: pmTest,
  expect: pmExpect,
  sendRequest: pmSendRequest,
  execution: execution,
  visualizer: { set() { unsupportedSeen.indexOf('pm.visualizer') < 0 && unsupportedSeen.push('pm.visualizer'); }, clear() {} },
};
pmObj.response = respObj; // undefined in pre-request scripts, as in Postman
const pm = strict(pmObj, 'pm');

// ---- isp.* (additive Interseptor namespace) --------------------------------------
function jwtPart(s) { return JSON.parse(conv('base64url', 'utf8', s)); }
const ispObj = {
  codec: {
    base64Encode: (s) => conv('utf8', 'base64', String(s)),
    base64Decode: (s) => conv('base64', 'utf8', String(s)),
    base64UrlEncode: (s) => conv('utf8', 'base64url', String(s)),
    base64UrlDecode: (s) => conv('base64url', 'utf8', String(s)),
    hexEncode: (s) => conv('utf8', 'hex', String(s)),
    hexDecode: (s) => conv('hex', 'utf8', String(s)),
    urlEncode: (s) => encodeURIComponent(String(s)),
    urlDecode: (s) => decodeURIComponent(String(s)),
  },
  flow: {
    next: (n) => setNext(n),
    stop: () => setNext(null),
    skip: () => { if (__in.phase !== 'prerequest') throw new Error('isp.flow.skip is only valid in pre-request scripts'); flow.skip = true; },
  },
  assertScope(url) {
    const why = __h.scope(String(url));
    if (why) throw new Error('out of scope: ' + why);
  },
  scope: { check: (url) => !__h.scope(String(url)) },
  fail(msg) { throw new AssertionError(String(msg === undefined ? 'isp.fail()' : msg)); },
  skip(name, msg) { record(name, 'skip', msg); },
  hash: (alg, data, enc) => conv('hex', enc || 'hex', __h.hash(String(alg).toLowerCase().replace('-', ''), conv('utf8', 'hex', String(data)))),
  hmac: (alg, key, data, enc) => conv('hex', enc || 'hex', __h.hmac(String(alg).toLowerCase().replace('-', ''), conv('utf8', 'hex', String(key)), conv('utf8', 'hex', String(data)))),
  jwt: { decode(t) { const p = String(t).split('.'); if (p.length < 2) throw new Error('not a JWT'); return { header: jwtPart(p[0]), payload: jwtPart(p[1]), signature: p[2] || '' }; } },
};
const isp = strict(ispObj, 'isp');

// ---- legacy globals -----------------------------------------------------------------
const legacyTests = new Proxy({}, {
  set(t, k, v) { record(String(k), v ? 'pass' : 'fail', v ? '' : 'legacy tests["' + String(k) + '"] was falsy'); t[k] = v; return true; },
});
const postmanLegacy = strict({
  setEnvironmentVariable: (k, v) => scopes.environment.set(k, v),
  getEnvironmentVariable: (k) => scopes.environment.get(k),
  clearEnvironmentVariable: (k) => scopes.environment.unset(k),
  clearEnvironmentVariables: () => scopes.environment.clear(),
  setGlobalVariable: (k, v) => scopes.globals.set(k, v),
  getGlobalVariable: (k) => scopes.globals.get(k),
  clearGlobalVariable: (k) => scopes.globals.unset(k),
  clearGlobalVariables: () => scopes.globals.clear(),
  setNextRequest: (n) => setNext(n),
  getResponseHeader: (n) => (respObj ? respObj.headers.get(n) : undefined),
  getResponseCookie: (n) => (respObj ? respObj.cookies.one(n) : undefined),
}, 'postman');
