// Deciding how to show a codec's decoded plaintext.
//
// A codec decodes an encrypted app-layer body, so the History "Pretty" view
// (which formats the raw wire bytes — hex, in this case) cannot help. The
// decoded text is what has to be readable, and it is usually compact JSON.

/**
 * decodedShape picks the text to show for a decoded body.
 * @param {string} plaintext  what decode() returned
 * @param {number} prettyMax  byte/char ceiling for beautifying
 * @param {(s: string) => string} beautify  the shared body beautifier
 * @returns {{shown: string, raw: string, beautified: boolean}}
 */
export function decodedShape(plaintext, prettyMax, beautify) {
  const raw = typeof plaintext === 'string' ? plaintext : '';
  if (!raw || raw.length > prettyMax) return { shown: raw, raw, beautified: false };
  let shown = raw;
  try {
    shown = beautify(raw);
  } catch (e) {
    shown = raw;
  }
  // White-space-only differences still change the shape the operator asked for,
  // so compare meaningfully: identical text means nothing was reformatted.
  return { shown, raw, beautified: shown !== raw };
}
