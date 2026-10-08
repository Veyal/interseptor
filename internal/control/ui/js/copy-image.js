// copy-image.js — pure, DOM-light logic for the Findings "Copy image" button.
//
// The clipboard only reliably accepts image/png, so the flow is: fetch the
// finding's own image bytes (same origin, allow-listed routes only), keep PNG as
// is, convert anything else (jpeg/webp/gif/avif/bmp) through a canvas, then hand
// navigator.clipboard.write() a ClipboardItem whose 'image/png' value is a
// *promise*. The promise has to be created synchronously inside the click handler
// so Safari keeps the user gesture while the fetch and conversion run.
//
// Nothing here imports core.js; browser globals are injected (`win`, `deps`) so
// the module loads and is tested under `node --test`.

export const PNG_MIME = 'image/png';
export const MAX_PIXELS = 64 * 1024 * 1024; // decoded pixels allowed for canvas conversion
export const MAX_BYTES = 32 * 1024 * 1024; // source bytes allowed

export class CopyImageError extends Error {
  constructor(reason, message) {
    super(message || reason);
    this.name = 'CopyImageError';
    this.reason = reason;
  }
}

const FINDING_IMAGE = /^\/api\/findings\/images\/[0-9a-f]{64}$/;
const FLOW_PREVIEW = /^\/api\/flows\/\d+\/preview\.png$/;

// isCopyableImageURL: only the finding's own image routes on this origin.
export function isCopyableImageURL(url, origin) {
  if (typeof url !== 'string' || !url || url.startsWith('//')) return false;
  let u;
  try { u = new URL(url, origin || 'http://localhost'); } catch { return false; }
  if (origin && u.origin !== new URL(origin).origin) return false;
  if (/^[a-z][a-z0-9+.-]*:/i.test(url) && !/^https?:/i.test(url)) return false;
  return FINDING_IMAGE.test(u.pathname) || FLOW_PREVIEW.test(u.pathname);
}

const startsWith = (b, sig, at = 0) => sig.every((v, i) => b[at + i] === v);

// sniffImageMime reads magic bytes; '' when the bytes are not a known raster format.
export function sniffImageMime(bytes) {
  const b = bytes || [];
  if (startsWith(b, [0x89, 0x50, 0x4e, 0x47])) return 'image/png';
  if (startsWith(b, [0xff, 0xd8, 0xff])) return 'image/jpeg';
  if (startsWith(b, [0x47, 0x49, 0x46, 0x38])) return 'image/gif';
  if (startsWith(b, [0x52, 0x49, 0x46, 0x46]) && startsWith(b, [0x57, 0x45, 0x42, 0x50], 8)) return 'image/webp';
  if (startsWith(b, [0x42, 0x4d])) return 'image/bmp';
  if (startsWith(b, [0x66, 0x74, 0x79, 0x70], 4) && startsWith(b, [0x61, 0x76, 0x69], 8)) return 'image/avif';
  return '';
}

// clipboardSupport: can this page write an image to the clipboard at all?
export function clipboardSupport(win) {
  const w = win || {};
  const clip = w.navigator && w.navigator.clipboard;
  if (!w.isSecureContext || !clip) return { ok: false, reason: 'insecure' };
  if (typeof w.ClipboardItem !== 'function' || typeof clip.write !== 'function') return { ok: false, reason: 'unsupported' };
  return { ok: true };
}

function defaultCanvas(w, h) {
  const c = document.createElement('canvas');
  c.width = w; c.height = h;
  return c;
}

// toPngBlob returns an image/png Blob. PNG passes through; other rasters are
// decoded and re-encoded via canvas (bounded, same-origin so never tainted).
export async function toPngBlob(blob, deps) {
  if (blob.size > MAX_BYTES) throw new CopyImageError('too-large');
  const head = new Uint8Array(await blob.slice(0, 16).arrayBuffer());
  const kind = sniffImageMime(head) || '';
  if (kind === PNG_MIME) return blob.type === PNG_MIME ? blob : new Blob([blob], { type: PNG_MIME });
  const d = deps || {};
  const decode = d.createImageBitmap || (typeof createImageBitmap === 'function' ? createImageBitmap : null);
  const make = d.makeCanvas || (typeof document !== 'undefined' ? defaultCanvas : null);
  if (!decode || !make) throw new CopyImageError('convert');
  let bitmap;
  try { bitmap = await decode(blob); } catch { throw new CopyImageError('convert'); }
  try {
    if (!(bitmap.width > 0 && bitmap.height > 0)) throw new CopyImageError('convert');
    if (bitmap.width * bitmap.height > MAX_PIXELS) throw new CopyImageError('too-large');
    const canvas = make(bitmap.width, bitmap.height);
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new CopyImageError('convert');
    ctx.drawImage(bitmap, 0, 0);
    return await new Promise((resolve, reject) => {
      try {
        canvas.toBlob((out) => (out ? resolve(out) : reject(new CopyImageError('convert'))), PNG_MIME);
      } catch { reject(new CopyImageError('convert')); }
    });
  } catch (e) {
    throw e instanceof CopyImageError ? e : new CopyImageError('convert');
  } finally {
    if (bitmap && bitmap.close) bitmap.close();
  }
}

function classify(err) {
  if (err instanceof CopyImageError) return err.reason;
  const n = err && err.name;
  if (n === 'NotAllowedError' || n === 'SecurityError') return 'denied';
  if (n === 'TypeError' || n === 'NotSupportedError') return 'unsupported';
  return 'failed';
}

// copyImage starts the copy. Returns { blob, result }:
//   blob   Promise<Blob>  the PNG (still resolves when only the clipboard write failed, so Download works)
//   result Promise<{ok:true} | {ok:false, reason, error}>
// It calls clipboard.write() synchronously, before any await.
export function copyImage(url, opts) {
  const win = (opts && opts.win) || (typeof window !== 'undefined' ? window : {});
  const origin = win.location && win.location.origin;
  const fail = (reason) => {
    const blob = Promise.reject(new CopyImageError(reason));
    blob.catch(() => {});
    return { blob, result: Promise.resolve({ ok: false, reason }) };
  };
  if (!isCopyableImageURL(url, origin)) return fail('blocked');
  const support = clipboardSupport(win);

  const blob = (async () => {
    let res;
    try { res = await win.fetch(url, { credentials: 'same-origin', cache: 'force-cache' }); } catch { throw new CopyImageError('fetch'); }
    if (!res || !res.ok) throw new CopyImageError('fetch');
    return toPngBlob(await res.blob(), opts && opts.deps);
  })();
  blob.catch(() => {}); // surfaced through result; avoid an unhandled rejection

  const result = (async () => {
    // Unsupported contexts still load the PNG (above) so the caller can offer Download.
    if (!support.ok) return { ok: false, reason: support.reason };
    try {
      const item = new win.ClipboardItem({ [PNG_MIME]: blob });
      await win.navigator.clipboard.write([item]);
      return { ok: true };
    } catch (error) {
      // Prefer the real cause (fetch/convert) over the clipboard's wrapper error.
      let reason = classify(error);
      try { await blob; } catch (inner) { reason = classify(inner); }
      return { ok: false, reason, error };
    }
  })();
  return { blob, result };
}

const MESSAGES = {
  insecure: 'Clipboard image copy needs https or localhost — download it instead',
  unsupported: 'This browser cannot copy images to the clipboard — download it instead',
  denied: 'Clipboard permission was denied — allow it for this site or download it instead',
  fetch: 'Could not load the image to copy — reload Findings and try again',
  convert: 'Could not convert the image to PNG for the clipboard — download it instead',
  'too-large': 'Image is too large to copy — download it instead',
  blocked: 'Only finding images can be copied',
  failed: 'Copy image failed — download it instead',
};
export function copyFailureMessage(reason) { return MESSAGES[reason] || MESSAGES.failed; }

// copyImageFileName: a safe .png download name from a caption.
export function copyImageFileName(caption) {
  const slug = String(caption || '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 60);
  return (slug || 'finding-image') + '.png';
}
