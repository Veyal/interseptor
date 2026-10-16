// part 5: CryptoJS, Buffer, atob/btoa, TextEncoder, URL, require().
const conv = (from, to, s) => __h.conv(from, to, s);
class WordArray {
  constructor(words, sigBytes) {
    this.words = words || [];
    this.sigBytes = sigBytes === undefined ? this.words.length * 4 : sigBytes;
  }
  toString(enc) { return (enc || EncHex).stringify(this); }
  concat(o) { return hexToWA(waToHex(this) + waToHex(o)); }
  clone() { return new WordArray(this.words.slice(), this.sigBytes); }
  clamp() { return this; }
  static create(words, sig) { return new WordArray(words, sig); }
  static random(n) { return hexToWA(__h.randhex(n)); }
}
function hexToWA(hex) {
  const words = [];
  for (let i = 0; i < hex.length; i += 8) {
    let chunk = hex.slice(i, i + 8);
    while (chunk.length < 8) chunk += '0';
    words.push(parseInt(chunk, 16) | 0);
  }
  return new WordArray(words, hex.length / 2);
}
function waToHex(wa) {
  let h = '';
  for (let i = 0; i < wa.sigBytes; i++) {
    const b = (wa.words[i >>> 2] >>> (24 - (i % 4) * 8)) & 0xff;
    h += (b < 16 ? '0' : '') + b.toString(16);
  }
  return h;
}
const mkEnc = (fmtName) => ({
  stringify: (wa) => conv('hex', fmtName, waToHex(wa)),
  parse: (s) => hexToWA(conv(fmtName, 'hex', String(s))),
});
const EncHex = { stringify: waToHex, parse: (s) => hexToWA(String(s)) };
const EncUtf8 = mkEnc('utf8');
const EncB64 = mkEnc('base64');
const EncB64u = mkEnc('base64url');
const EncLatin1 = mkEnc('latin1');
function toHex(m) {
  if (m instanceof WordArray) return waToHex(m);
  if (isObj(m) && m.words) return waToHex(m);
  return conv('utf8', 'hex', String(m));
}
const hashFn = (alg) => (m) => hexToWA(__h.hash(alg, toHex(m)));
const hmacFn = (alg) => (m, k) => hexToWA(__h.hmac(alg, toHex(k), toHex(m)));
const MODES = { CBC: { name: 'cbc' }, ECB: { name: 'ecb' }, CTR: { name: 'ctr' }, CFB: { name: 'cfb' }, OFB: { name: 'ofb' } };
const PADS = { Pkcs7: { name: 'pkcs7' }, NoPadding: { name: 'none' }, ZeroPadding: { name: 'zero' } };
class CipherParams {
  constructor(o) { Object.assign(this, o); }
  toString(f) { return (f || FmtOpenSSL).stringify(this); }
}
const FmtOpenSSL = {
  stringify(p) {
    const ct = waToHex(p.ciphertext);
    return conv('hex', 'base64', p.salt ? '53616c7465645f5f' + waToHex(p.salt) + ct : ct);
  },
  parse(s) {
    const h = conv('base64', 'hex', String(s));
    if (h.slice(0, 16) === '53616c7465645f5f') return new CipherParams({ ciphertext: hexToWA(h.slice(32)), salt: hexToWA(h.slice(16, 32)) });
    return new CipherParams({ ciphertext: hexToWA(h) });
  },
};
const FmtHex = { stringify: (p) => waToHex(p.ciphertext), parse: (s) => new CipherParams({ ciphertext: hexToWA(String(s)) }) };
function evpKdf(passHex, saltHex, keyLen, ivLen) {
  let out = '', prev = '';
  while (out.length < (keyLen + ivLen) * 2) { prev = __h.hash('md5', prev + passHex + saltHex); out += prev; }
  return { key: out.slice(0, keyLen * 2), iv: out.slice(keyLen * 2, (keyLen + ivLen) * 2) };
}
function aesCfg(cfg) {
  cfg = cfg || {};
  return { mode: (cfg.mode && cfg.mode.name) || 'cbc', pad: (cfg.padding && cfg.padding.name) || 'pkcs7', iv: cfg.iv };
}
function zeroPadHex(h) { const r = (h.length / 2) % 16; return r === 0 ? h : h + '00'.repeat(16 - r); }
function aesCrypt(op, dataHex, key, cfg, saltHex) {
  const c = aesCfg(cfg);
  let keyHex, ivHex;
  if (typeof key === 'string') {
    const d = evpKdf(conv('utf8', 'hex', key), saltHex, 32, 16);
    keyHex = d.key; ivHex = d.iv;
  } else { keyHex = waToHex(key); ivHex = c.iv ? toHex(c.iv) : '00'.repeat(16); }
  let pad = c.pad;
  if (pad === 'zero') { pad = 'none'; if (op === 'enc') dataHex = zeroPadHex(dataHex); }
  let out = __h.aes(op, c.mode, keyHex, ivHex, dataHex, pad);
  if (c.pad === 'zero' && op === 'dec') out = out.replace(/(00)+$/, '');
  return { out: out, key: keyHex, iv: ivHex };
}
const AES = {
  encrypt(msg, key, cfg) {
    let salt = null;
    if (typeof key === 'string') salt = __h.randhex(8);
    const r = aesCrypt('enc', toHex(msg), key, cfg, salt);
    return new CipherParams({ ciphertext: hexToWA(r.out), key: hexToWA(r.key), iv: hexToWA(r.iv), salt: salt ? hexToWA(salt) : undefined, algorithm: 'AES' });
  },
  decrypt(ct, key, cfg) {
    const p = typeof ct === 'string' ? FmtOpenSSL.parse(ct) : ct;
    const r = aesCrypt('dec', waToHex(p.ciphertext), key, cfg, p.salt ? waToHex(p.salt) : '');
    return hexToWA(r.out);
  },
};
const PBKDF2 = (pass, salt, cfg) => {
  cfg = cfg || {};
  const keyBytes = (cfg.keySize || 4) * 4;
  const alg = cfg.hasher && cfg.hasher.__alg ? cfg.hasher.__alg : 'sha1';
  return hexToWA(__h.pbkdf2(alg, toHex(pass), toHex(salt), cfg.iterations || 1, keyBytes));
};
const hasherTag = (fn, alg) => { fn.__alg = alg; return fn; };
const CryptoJSImpl = {
  MD5: hashFn('md5'), SHA1: hashFn('sha1'), SHA224: hashFn('sha224'), SHA256: hashFn('sha256'), SHA384: hashFn('sha384'), SHA512: hashFn('sha512'),
  HmacMD5: hmacFn('md5'), HmacSHA1: hmacFn('sha1'), HmacSHA224: hmacFn('sha224'), HmacSHA256: hmacFn('sha256'), HmacSHA384: hmacFn('sha384'), HmacSHA512: hmacFn('sha512'),
  AES: AES, PBKDF2: PBKDF2,
  enc: { Hex: EncHex, Utf8: EncUtf8, Base64: EncB64, Base64url: EncB64u, Latin1: EncLatin1 },
  mode: MODES, pad: PADS,
  format: { OpenSSL: FmtOpenSSL, Hex: FmtHex },
  lib: { WordArray: WordArray, CipherParams: CipherParams },
  algo: { SHA1: hasherTag({}, 'sha1'), SHA256: hasherTag({}, 'sha256'), SHA512: hasherTag({}, 'sha512'), MD5: hasherTag({}, 'md5') },
};
CryptoJSImpl.algo.SHA1.__alg = 'sha1'; CryptoJSImpl.algo.SHA256.__alg = 'sha256'; CryptoJSImpl.algo.SHA512.__alg = 'sha512'; CryptoJSImpl.algo.MD5.__alg = 'md5';
const CryptoJS = new Proxy(CryptoJSImpl, {
  get(t, k) { if (typeof k === 'symbol' || k in t) return t[k]; unsupported('CryptoJS.' + String(k)); },
});

// ---- Buffer ----------------------------------------------------------------
function checkAlloc(n) {
  if (!(n >= 0) || n > LIM.maxAlloc) throw new RangeError('Invalid buffer size ' + n + ' (sandbox limit ' + LIM.maxAlloc + ')');
}
const ENCS = { utf8: 'utf8', 'utf-8': 'utf8', hex: 'hex', base64: 'base64', base64url: 'base64url', latin1: 'latin1', binary: 'latin1', ascii: 'latin1' };
const encName = (e) => { const n = ENCS[String(e || 'utf8').toLowerCase()]; if (!n) throw new TypeError('Unknown encoding: ' + e); return n; };
function hexToBytes(hex) { const n = hex.length / 2; checkAlloc(n); const u = new Uint8Array(n); for (let i = 0; i < n; i++) u[i] = parseInt(hex.substr(i * 2, 2), 16); return u; }
function bytesToHex(u) { let h = ''; for (let i = 0; i < u.length; i++) h += (u[i] < 16 ? '0' : '') + u[i].toString(16); return h; }
class Buffer extends Uint8Array {
  static from(v, enc) {
    let u;
    if (typeof v === 'string') u = hexToBytes(conv(encName(enc), 'hex', v));
    else if (v instanceof ArrayBuffer) u = new Uint8Array(v);
    else if (v && v.type === 'Buffer' && Array.isArray(v.data)) u = Uint8Array.from(v.data);
    else if (v && typeof v.length === 'number') { checkAlloc(v.length); u = Uint8Array.from(v); } else throw new TypeError('The first argument must be a string, Buffer, ArrayBuffer, Array, or array-like object.');
    const b = new Buffer(u.length); b.set(u); return b;
  }
  static alloc(n, fill) { checkAlloc(n); const b = new Buffer(n); if (fill !== undefined) b.fill(typeof fill === 'string' ? fill.charCodeAt(0) : fill); return b; }
  static allocUnsafe(n) { return Buffer.alloc(n); }
  static isBuffer(o) { return o instanceof Buffer; }
  static byteLength(s, enc) { return typeof s === 'string' ? Buffer.from(s, enc).length : s.length; }
  static concat(list) {
    let n = 0; list.forEach((b) => { n += b.length; }); checkAlloc(n);
    const out = new Buffer(n); let off = 0; list.forEach((b) => { out.set(b, off); off += b.length; }); return out;
  }
  static compare(a, b) { return bytesToHex(a) < bytesToHex(b) ? -1 : bytesToHex(a) > bytesToHex(b) ? 1 : 0; }
  toString(enc, s, e) {
    const u = (s !== undefined || e !== undefined) ? this.subarray(s || 0, e === undefined ? this.length : e) : this;
    return conv('hex', encName(enc), bytesToHex(u));
  }
  toJSON() { return { type: 'Buffer', data: Array.from(this) }; }
  equals(o) { return bytesToHex(this) === bytesToHex(o); }
  slice(s, e) { return Buffer.from(Uint8Array.prototype.slice.call(this, s, e)); }
  subarray(s, e) { return Buffer.from(Uint8Array.prototype.slice.call(this, s, e)); }
  readUInt8(o) { return this[o || 0]; }
  readUInt16BE(o) { o = o || 0; return (this[o] << 8) | this[o + 1]; }
  readUInt32BE(o) { o = o || 0; return ((this[o] << 24) | (this[o + 1] << 16) | (this[o + 2] << 8) | this[o + 3]) >>> 0; }
  writeUInt8(v, o) { this[o || 0] = v; return (o || 0) + 1; }
  writeUInt32BE(v, o) { o = o || 0; this[o] = v >>> 24; this[o + 1] = v >>> 16; this[o + 2] = v >>> 8; this[o + 3] = v; return o + 4; }
}
const btoaImpl = (s) => {
  s = String(s);
  for (let i = 0; i < s.length; i++) if (s.charCodeAt(i) > 255) throw new Error('Invalid character');
  return conv('latin1', 'base64', s);
};
const atobImpl = (s) => conv('base64', 'latin1', String(s).replace(/\s+/g, ''));
class TextEncoderImpl { get encoding() { return 'utf-8'; } encode(s) { return hexToBytes(conv('utf8', 'hex', String(s === undefined ? '' : s))); } }
class TextDecoderImpl { get encoding() { return 'utf-8'; } decode(u) { return u === undefined ? '' : conv('hex', 'utf8', bytesToHex(new Uint8Array(u.buffer || u))); } }

class URLSearchParamsImpl {
  constructor(init) {
    this.__l = [];
    if (typeof init === 'string') parseQuery(init.replace(/^\?/, '')).forEach((q) => this.__l.push([q.key, q.value]));
    else if (isObj(init)) Object.keys(init).forEach((k) => this.__l.push([k, String(init[k])]));
  }
  append(k, v) { this.__l.push([String(k), String(v)]); }
  get(k) { const e = this.__l.find((x) => x[0] === k); return e ? e[1] : null; }
  getAll(k) { return this.__l.filter((x) => x[0] === k).map((x) => x[1]); }
  has(k) { return this.__l.some((x) => x[0] === k); }
  set(k, v) { this.delete(k); this.append(k, v); }
  delete(k) { this.__l = this.__l.filter((x) => x[0] !== k); }
  forEach(fn) { this.__l.forEach((x) => fn(x[1], x[0])); }
  toString() { return this.__l.map((x) => encQ(x[0]) + '=' + encQ(x[1])).join('&'); }
}
class URLImpl {
  constructor(s, base) {
    s = String(s);
    if (!/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(s)) { if (!base) throw new TypeError('Invalid URL'); s = String(base).replace(/\/$/, '') + (s.charAt(0) === '/' ? '' : '/') + s; }
    this.__u = new Url(s);
    this.searchParams = new URLSearchParamsImpl(this.__u.getQueryString());
  }
  get protocol() { return (this.__u.protocol || '') + ':'; }
  get hostname() { return this.__u.getHost(); }
  get port() { return this.__u.port || ''; }
  get host() { return this.__u.getRemote(); }
  get origin() { return this.protocol + '//' + this.host; }
  get pathname() { return this.__u.getPath(); }
  get search() { const q = this.searchParams.toString(); return q ? '?' + q : ''; }
  get hash() { return this.__u.hash ? '#' + this.__u.hash : ''; }
  get href() { return this.origin + this.pathname + this.search + this.hash; }
  toString() { return this.href; }
  toJSON() { return this.href; }
}

// ---- require() ----------------------------------------------------------------
function mkCrypto() {
  const algName = (a) => String(a).toLowerCase().replace('-', '');
  const outEnc = (hex, enc) => (enc ? conv('hex', encName(enc), hex) : Buffer.from(hexToBytes(hex)));
  const dataHex = (d, enc) => (typeof d === 'string' ? conv(encName(enc), 'hex', d) : bytesToHex(d));
  const hashObj = (alg, key) => {
    let acc = '';
    const o = {
      update(d, enc) { acc += dataHex(d, enc); return o; },
      digest(enc) { return outEnc(key === undefined ? __h.hash(alg, acc) : __h.hmac(alg, key, acc), enc); },
    };
    return o;
  };
  const cipher = (decrypt) => (alg, key, iv) => {
    const m = /^aes-(128|192|256)-(cbc|ecb|ctr|gcm|cfb|ofb)$/.exec(String(alg).toLowerCase());
    if (!m) unsupported('crypto cipher ' + alg);
    let acc = '', aad = '', tag = null;
    const o = {
      setAAD(a) { aad = dataHex(a); return o; },
      setAutoPadding() { return o; },
      setAuthTag(t) { tag = dataHex(t); return o; },
      getAuthTag() { return Buffer.from(hexToBytes(o.__tag || '')); },
      update(d, inEnc, outE) { acc += dataHex(d, inEnc); return outE ? '' : Buffer.alloc(0); },
      final(outE) {
        let out;
        const kh = dataHex(key), ih = iv === null || iv === undefined ? '' : dataHex(iv);
        if (m[2] === 'gcm') {
          if (decrypt) out = __h.gcm('dec', kh, ih, acc + (tag || ''), aad);
          else { const r = __h.gcm('enc', kh, ih, acc, aad); out = r.slice(0, r.length - 32); o.__tag = r.slice(r.length - 32); }
        } else out = __h.aes(decrypt ? 'dec' : 'enc', m[2], kh, ih || '00'.repeat(16), acc, m[2] === 'ctr' || m[2] === 'cfb' || m[2] === 'ofb' ? 'none' : 'pkcs7');
        return outEnc(out, outE);
      },
    };
    return o;
  };
  return {
    createHash: (a) => hashObj(algName(a)),
    createHmac: (a, k) => hashObj(algName(a), dataHex(k)),
    randomBytes: (n) => { checkAlloc(n); return Buffer.from(hexToBytes(__h.randhex(n))); },
    randomUUID: () => __h.uuid(),
    randomInt: (a, b) => { if (b === undefined) { b = a; a = 0; } return a + Math.floor(Math.random() * (b - a)); },
    createCipheriv: cipher(false), createDecipheriv: cipher(true),
    timingSafeEqual: (a, b) => bytesToHex(a) === bytesToHex(b),
    getHashes: () => ['md5', 'sha1', 'sha224', 'sha256', 'sha384', 'sha512'],
    get subtle() { return unsupported('crypto.subtle'); },
  };
}
const uuidMod = { v4: () => __h.uuid(), validate: (s) => /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(String(s)) };
const modCache = {};
const MODS = {
  'crypto': mkCrypto, 'crypto-js': () => CryptoJS, 'buffer': () => ({ Buffer: Buffer }),
  'uuid': () => uuidMod, 'url': () => ({ URL: URLImpl, URLSearchParams: URLSearchParamsImpl }),
  'chai': () => ({ expect: pmExpect }),
  'querystring': () => ({
    parse: (s) => { const o = {}; parseQuery(String(s)).forEach((q) => { if (hasOwn(o, q.key)) setOwn(o, q.key, [].concat(o[q.key], q.value)); else setOwn(o, q.key, q.value); }); return o; },
    stringify: (o) => Object.keys(o).map((k) => encQ(k) + '=' + encQ(String(o[k]))).join('&'),
  }),
  'atob': () => atobImpl, 'btoa': () => btoaImpl,
  'util': () => ({ format: (...a) => consoleText(a), inspect: (v) => fmt(v, 1), isDeepStrictEqual: deepEqual }),
};
function requireImpl(name) {
  name = String(name).replace(/^node:/, '');
  if (hasOwn(modCache, name)) return modCache[name];
  if (!hasOwn(MODS, name)) unsupported('require(' + JSON.stringify(name) + ')');
  modCache[name] = MODS[name]();
  return modCache[name];
}
