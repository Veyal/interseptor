import test from 'node:test';
import assert from 'node:assert/strict';
import {
  PNG_MIME, MAX_PIXELS, CopyImageError, isCopyableImageURL, sniffImageMime, clipboardSupport,
  toPngBlob, copyImage, copyFailureMessage, copyImageFileName,
} from '../js/copy-image.js';

const ORIGIN = 'http://127.0.0.1:19995';
const PNG_HEAD = Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0]);
const JPG_HEAD = Uint8Array.from([0xff, 0xd8, 0xff, 0xe0, 0, 0, 0, 0, 0, 0]);
const blobOf = (bytes, type) => new Blob([bytes], { type });

test('only the finding own image URLs may be fetched', () => {
  const ok = [
    '/api/findings/images/' + 'a'.repeat(64),
    ORIGIN + '/api/findings/images/' + 'b'.repeat(64),
    '/api/flows/42/preview.png?side=both&pretty=1&layout=vertical&theme=light',
  ];
  for (const u of ok) assert.equal(isCopyableImageURL(u, ORIGIN), true, u);
  const bad = [
    '', null, 'https://evil.example.com/api/findings/images/' + 'a'.repeat(64),
    '//evil.example.com/api/findings/images/' + 'a'.repeat(64),
    '/api/findings/images/../../etc/passwd', '/api/findings/images/zz',
    '/api/flows/x/preview.png', '/api/settings', 'data:image/png;base64,AAAA', 'javascript:alert(1)',
  ];
  for (const u of bad) assert.equal(isCopyableImageURL(u, ORIGIN), false, String(u));
});

test('sniffImageMime reads magic bytes and ignores a lying header', () => {
  assert.equal(sniffImageMime(PNG_HEAD), 'image/png');
  assert.equal(sniffImageMime(JPG_HEAD), 'image/jpeg');
  assert.equal(sniffImageMime(Uint8Array.from([0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0, 0, 0, 0])), 'image/gif');
  assert.equal(sniffImageMime(Uint8Array.from([0x52, 0x49, 0x46, 0x46, 0, 0, 0, 0, 0x57, 0x45, 0x42, 0x50])), 'image/webp');
  assert.equal(sniffImageMime(Uint8Array.from([0x42, 0x4d, 0, 0, 0, 0, 0, 0, 0, 0])), 'image/bmp');
  assert.equal(sniffImageMime(Uint8Array.from([0, 0, 0, 0x20, 0x66, 0x74, 0x79, 0x70, 0x61, 0x76, 0x69, 0x66])), 'image/avif');
  assert.equal(sniffImageMime(Uint8Array.from([60, 115, 118, 103, 62, 0, 0, 0, 0, 0])), '');
});

test('clipboardSupport explains each failure class', () => {
  const full = { isSecureContext: true, navigator: { clipboard: { write() {} } }, ClipboardItem: class {} };
  assert.deepEqual(clipboardSupport(full), { ok: true });
  assert.equal(clipboardSupport({ ...full, isSecureContext: false, navigator: {} }).reason, 'insecure');
  assert.equal(clipboardSupport({ ...full, navigator: {} }).reason, 'insecure');
  assert.equal(clipboardSupport({ ...full, ClipboardItem: undefined }).reason, 'unsupported');
  assert.equal(clipboardSupport({ ...full, navigator: { clipboard: {} } }).reason, 'unsupported');
});

test('toPngBlob passes PNG through untouched', async () => {
  const b = blobOf(PNG_HEAD, 'image/png');
  assert.equal(await toPngBlob(b), b);
});

test('toPngBlob sniffs bytes when the header type is missing or wrong', async () => {
  const b = blobOf(PNG_HEAD, 'application/octet-stream');
  const out = await toPngBlob(b);
  assert.equal(out.type, PNG_MIME);
});

test('toPngBlob converts jpeg through a canvas into image/png', async () => {
  const calls = [];
  const deps = {
    createImageBitmap: async (blob) => { calls.push(['bitmap', blob.type]); return { width: 40, height: 30, close() { calls.push(['close']); } }; },
    makeCanvas: (w, h) => ({
      width: w, height: h,
      getContext: () => ({ drawImage: (...a) => calls.push(['draw', a[1], a[2]]), fillRect() {}, fillStyle: '' }),
      toBlob: (cb, type) => { calls.push(['toBlob', type]); cb(blobOf(PNG_HEAD, type)); },
    }),
  };
  const out = await toPngBlob(blobOf(JPG_HEAD, 'image/jpeg'), deps);
  assert.equal(out.type, 'image/png');
  assert.deepEqual(calls.map((c) => c[0]), ['bitmap', 'draw', 'toBlob', 'close']);
});

test('toPngBlob refuses oversized bitmaps and tainted or failed conversion', async () => {
  const big = { createImageBitmap: async () => ({ width: 20000, height: 20000, close() {} }), makeCanvas: () => assert.fail('no canvas for oversized') };
  await assert.rejects(toPngBlob(blobOf(JPG_HEAD, 'image/jpeg'), big), (e) => e instanceof CopyImageError && e.reason === 'too-large');
  assert.ok(MAX_PIXELS > 0);
  const tainted = {
    createImageBitmap: async () => ({ width: 2, height: 2, close() {} }),
    makeCanvas: () => ({ getContext: () => ({ drawImage() {} }), toBlob() { throw new DOMException('tainted', 'SecurityError'); } }),
  };
  await assert.rejects(toPngBlob(blobOf(JPG_HEAD, 'image/jpeg'), tainted), (e) => e.reason === 'convert');
  const noBitmap = { createImageBitmap: async () => { throw new Error('decode'); }, makeCanvas: () => ({}) };
  await assert.rejects(toPngBlob(blobOf(JPG_HEAD, 'image/jpeg'), noBitmap), (e) => e.reason === 'convert');
  await assert.rejects(toPngBlob(blobOf(JPG_HEAD, 'image/jpeg'), {}), (e) => e.reason === 'convert');
});

function env({ fetchImpl, write, secure = true } = {}) {
  const written = [];
  return {
    written,
    win: {
      isSecureContext: secure,
      location: { origin: ORIGIN },
      navigator: { clipboard: { write: write || (async (items) => { written.push(...items); await Promise.all(items.map((i) => i.__p)); }) } },
      ClipboardItem: class { constructor(o) { this.o = o; this.__p = Promise.resolve(o['image/png']); } },
      fetch: fetchImpl || (async () => ({ ok: true, status: 200, blob: async () => blobOf(PNG_HEAD, 'image/png') })),
    },
  };
}

test('copyImage hands ClipboardItem a promise synchronously (Safari gesture rule)', async () => {
  const e = env();
  const run = copyImage('/api/findings/images/' + 'a'.repeat(64), { win: e.win });
  // write() must already have been called before any await resolved.
  assert.equal(e.written.length, 1);
  assert.ok(e.written[0].o['image/png'] instanceof Promise);
  const res = await run.result;
  assert.equal(res.ok, true);
  assert.equal((await run.blob).type, 'image/png');
});

test('copyImage reports insecure contexts without writing, but still loads the PNG for Download', async () => {
  let fetched = 0;
  const e = env({ secure: false, fetchImpl: async () => { fetched++; return { ok: true, blob: async () => blobOf(PNG_HEAD, 'image/png') }; } });
  delete e.win.navigator.clipboard;
  const run = copyImage('/api/findings/images/' + 'a'.repeat(64), { win: e.win });
  const res = await run.result;
  assert.deepEqual([res.ok, res.reason], [false, 'insecure']);
  assert.equal(e.written.length, 0);
  assert.equal((await run.blob).type, 'image/png');
  assert.equal(fetched, 1);
});

test('copyImage maps NotAllowedError and fetch failures to reasons and keeps the blob for Download', async () => {
  const denied = env({ write: async (items) => { await items[0].__p; throw new DOMException('no', 'NotAllowedError'); } });
  const r1 = copyImage('/api/findings/images/' + 'a'.repeat(64), { win: denied.win });
  const res1 = await r1.result;
  assert.deepEqual([res1.ok, res1.reason], [false, 'denied']);
  assert.equal((await r1.blob).type, 'image/png'); // Download can still use it

  const gone = env({ fetchImpl: async () => ({ ok: false, status: 404 }) });
  gone.win.navigator.clipboard.write = async (items) => { await items[0].__p; };
  const r2 = copyImage('/api/findings/images/' + 'a'.repeat(64), { win: gone.win });
  const res2 = await r2.result;
  assert.deepEqual([res2.ok, res2.reason], [false, 'fetch']);
  await assert.rejects(r2.blob);
});

test('copyImage rejects URLs outside the finding image routes', async () => {
  const e = env();
  const run = copyImage('https://evil.example.com/x.png', { win: e.win });
  assert.equal((await run.result).reason, 'blocked');
  assert.equal(e.written.length, 0);
});

test('failure messages are actionable and name the fallback', () => {
  assert.match(copyFailureMessage('insecure'), /https or localhost/);
  assert.match(copyFailureMessage('insecure'), /download it instead/);
  // The toast's Download button is read right after the message: the text must not repeat its label.
  for (const r of ['insecure', 'unsupported', 'denied', 'convert', 'too-large', 'failed']) assert.doesNotMatch(copyFailureMessage(r), /Download/, r);
  for (const r of ['unsupported', 'denied', 'fetch', 'convert', 'too-large', 'blocked', 'failed']) assert.ok(copyFailureMessage(r).length > 10, r);
});

test('copyImageFileName is a safe png name', () => {
  assert.equal(copyImageFileName('Generated HTTP report preview for flow #7'), 'generated-http-report-preview-for-flow-7.png');
  assert.equal(copyImageFileName(''), 'finding-image.png');
  assert.equal(copyImageFileName('../../x/..\\y'), 'x-y.png');
});
