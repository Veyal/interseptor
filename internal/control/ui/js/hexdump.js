// hexdump.js — the canonical hexdump behind the History and Repeater Hex views.
// No imports, so node --test covers it. A dump line is `offset  <bytes>  |ascii|`
// with the bytes in groups of eight; 16 per line is the default, 8 per line is
// the narrow layout for panes where a 16-byte line would hide the ASCII column,
// and 4 per line is the narrowest, for panes too small even for 8.

export const HEX_LINE_16 = 16;
export const HEX_LINE_8 = 8;
export const HEX_LINE_4 = 4;

const sizeLabel = (n) => (!n ? '0 B' : n < 1024 ? n + ' B' : n < 1048576 ? (n / 1024).toFixed(n < 10240 ? 1 : 0) + ' KB' : (n / 1048576).toFixed(1) + ' MB');

export function formatHexDump(strOrBytes, maxBytes = 32768, perLine = HEX_LINE_16) {
  if (!strOrBytes) return '';
  const bytes = typeof strOrBytes === 'string' ? new TextEncoder().encode(strOrBytes) : new Uint8Array(strOrBytes);
  const width = perLine === HEX_LINE_8 ? HEX_LINE_8 : perLine === HEX_LINE_4 ? HEX_LINE_4 : HEX_LINE_16;
  const group = Math.min(8, width);
  const total = bytes.length;
  const len = Math.min(total, maxBytes);
  const lines = [];
  for (let i = 0; i < len; i += width) {
    const offset = i.toString(16).padStart(8, '0');
    const chunk = bytes.subarray(i, Math.min(i + width, len));
    const groups = [];
    let ascii = '';
    for (let g = 0; g < width; g += group) {
      const cells = [];
      for (let j = g; j < g + group; j++) {
        if (j < chunk.length) cells.push(chunk[j].toString(16).padStart(2, '0'));
        else cells.push('  ');
      }
      groups.push(cells.join(' '));
    }
    for (const b of chunk) ascii += (b >= 32 && b <= 126) ? String.fromCharCode(b) : '.';
    lines.push(`${offset}  ${groups.join('  ')}  |${ascii}|`);
  }
  if (total > maxBytes) lines.push(`... [truncated, ${sizeLabel(total)} total]`);
  return lines.join('\n');
}

// hexLineFor picks the line width: 16 bytes when a 16-byte line fits `avail`
// pixels at `charW` pixels per character, else 8 when that fits, else 4. A
// 16-byte line is 8 (offset) + 2 + 47 (hex) + 2 + 18 (ascii) = 78 characters;
// an 8-byte line is 45 and a 4-byte line is 29.
export const HEX_CHARS_16 = 78;
export const HEX_CHARS_8 = 45;
export function hexLineFor(avail, charW) {
  if (!(avail > 0) || !(charW > 0)) return HEX_LINE_16;
  if (avail >= HEX_CHARS_16 * charW) return HEX_LINE_16;
  return avail >= HEX_CHARS_8 * charW ? HEX_LINE_8 : HEX_LINE_4;
}
