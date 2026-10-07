// part 6: guards, globals and the host-facing finalizer.
(function guards() {
  const maxStr = LIM.maxString;
  const rep = String.prototype.repeat, padS = String.prototype.padStart, padE = String.prototype.padEnd, join = Array.prototype.join;
  const tooBig = (what) => new RangeError('Invalid ' + what + ' length (sandbox limit)');
  String.prototype.repeat = function repeat(n) { const c = Math.floor(Number(n)) || 0; if (String(this).length * c > maxStr) throw tooBig('string'); return rep.call(this, c); };
  String.prototype.padStart = function padStart(n, f) { if (Number(n) > maxStr) throw tooBig('string'); return padS.call(this, n, f); };
  String.prototype.padEnd = function padEnd(n, f) { if (Number(n) > maxStr) throw tooBig('string'); return padE.call(this, n, f); };
  Array.prototype.join = function (sep) { if (this.length > LIM.maxArray && (sep === undefined || String(sep).length > 0)) throw tooBig('array'); return join.call(this, sep); };
  // A plain wrapper (not a Proxy) keeps `x instanceof Array` working: the
  // wrapper shares the original prototype object.
  const lenGuard = (Orig, max, what) => {
    function Guard() {
      if (typeof arguments[0] === 'number' && arguments[0] > max) throw tooBig(what);
      return new.target === undefined ? Reflect.apply(Orig, undefined, arguments) : Reflect.construct(Orig, arguments, new.target === Guard ? Orig : new.target);
    }
    Object.setPrototypeOf(Guard, Orig);
    Guard.prototype = Orig.prototype;
    Object.defineProperty(Guard, 'name', { value: Orig.name });
    return Guard;
  };
  globalThis.Array = lenGuard(Array, LIM.maxArray, 'array');
  globalThis.ArrayBuffer = lenGuard(ArrayBuffer, LIM.maxAlloc, 'buffer');
  ['Uint8Array', 'Int8Array', 'Uint16Array', 'Int16Array', 'Uint32Array', 'Int32Array', 'Float32Array', 'Float64Array'].forEach((n) => { globalThis[n] = lenGuard(globalThis[n], LIM.maxAlloc, 'typed array'); });
})();

(function globals() {
  const def = (k, v) => Object.defineProperty(globalThis, k, { value: v, writable: true, configurable: true, enumerable: false });
  const lazyUnsupported = (k, label) => Object.defineProperty(globalThis, k, { get() { return unsupported(label); }, set() {}, configurable: true, enumerable: false });
  def('pm', pm); def('postman', postmanLegacy); def('isp', isp); def('console', consoleObj);
  def('CryptoJS', CryptoJS); def('Buffer', Buffer); def('atob', atobImpl); def('btoa', btoaImpl);
  def('TextEncoder', TextEncoderImpl); def('TextDecoder', TextDecoderImpl); def('URL', URLImpl); def('URLSearchParams', URLSearchParamsImpl);
  def('require', requireImpl);
  def('tests', legacyTests);
  def('environment', scopes.environment.toObject());
  def('globals', scopes.globals.toObject());
  def('data', Object.assign({}, scopes.iteration.__d));
  def('iteration', __in.info.iteration || 0);
  def('request', { url: reqObj.url.toString(), method: reqObj.method, headers: reqObj.headers.toObject(), data: reqObj.body.toString(), name: reqObj.name, id: reqObj.id });
  if (respObj) {
    def('responseBody', respObj.text()); def('responseTime', respObj.responseTime);
    def('responseCode', { code: respObj.code, name: respObj.status, detail: respObj.status });
    def('responseHeaders', respObj.headers.toObject());
    def('responseCookies', respObj.cookies.toObject());
  }
  lazyUnsupported('_', 'lodash (global _)'); lazyUnsupported('moment', 'moment'); lazyUnsupported('tv4', 'tv4');
  lazyUnsupported('xml2Json', 'xml2Json'); lazyUnsupported('cheerio', 'cheerio'); lazyUnsupported('ajv', 'ajv');
  lazyUnsupported('fetch', 'fetch'); lazyUnsupported('XMLHttpRequest', 'XMLHttpRequest'); lazyUnsupported('process', 'process');
  lazyUnsupported('WebSocket', 'WebSocket');
})();

async function settle() {
  let guard = 0;
  while (pending.length && guard++ < 10000) {
    const batch = pending.splice(0, pending.length);
    await Promise.all(batch.map((p) => Promise.resolve(p).catch(scriptErr)));
  }
}
function fail(e) {
  const kind = e instanceof UnsupportedError ? 'unsupported' : 'error';
  scriptErr({ kind: kind, name: e && e.name, message: e && e.message !== undefined ? String(e.message) : String(e), stack: e && e.stack ? String(e.stack) : '' });
}
function finish() {
  const dump = (s) => Object.assign({}, s.__d);
  const errs = scriptErrors.map((e) => (e && e.kind ? e : { kind: e instanceof UnsupportedError ? 'unsupported' : 'error', name: e && e.name, message: e && e.message !== undefined ? String(e.message) : String(e), stack: '' }));
  const rq = reqObj.toJSON();
  rq.name = reqObj.name; rq.id = reqObj.id;
  return JSON.stringify({
    tests: results, changes: changes, errors: errs, unsupported: unsupportedSeen,
    vars: { environment: dump(scopes.environment), globals: dump(scopes.globals), collection: dump(scopes.collection), local: dump(scopes.local) },
    request: rq, flow: flow, cookieOps: cookieOps, tested: testIndex,
  });
}
Object.defineProperty(globalThis, __fin, { value: { settle: settle, fail: fail, finish: finish }, enumerable: false, writable: false, configurable: false });
