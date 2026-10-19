import test from 'node:test';
import assert from 'node:assert/strict';
import { formatHexDump, hexLineFor, HEX_CHARS_16 } from '../js/hexdump.js';

test('16-byte lines keep the canonical layout', () => {
  const out = formatHexDump('ABCDEFGHIJKLMNOPQ');
  const [a, b] = out.split('\n');
  assert.equal(a, '00000000  41 42 43 44 45 46 47 48  49 4a 4b 4c 4d 4e 4f 50  |ABCDEFGHIJKLMNOP|');
  assert.equal(b, '00000010  51'.padEnd(60) + '|Q|'); // the ASCII column stays aligned on a short last line
  assert.equal(a.length, HEX_CHARS_16);
});

test('8-byte lines are 45 characters wide and keep the ASCII column', () => {
  const out = formatHexDump('ABCDEFGHIJ', 32768, 8);
  const [a, b] = out.split('\n');
  assert.equal(a, '00000000  41 42 43 44 45 46 47 48  |ABCDEFGH|');
  assert.equal(b, '00000008  49 4a'.padEnd(35) + '|IJ|');
  assert.ok(a.length < 50);
});

test('non-printable bytes are dots and a truncation note is added', () => {
  assert.match(formatHexDump(new Uint8Array([0, 10, 65]), 2), /\|\.\.\|\n\.\.\. \[truncated, 3 B total\]/);
  assert.equal(formatHexDump(''), '');
});

test('hexLineFor picks 8 bytes per line when 16 would overflow', () => {
  assert.equal(hexLineFor(700, 8), 16);
  assert.equal(hexLineFor(500, 8), 8);
  assert.equal(hexLineFor(0, 8), 16);
});

test('4-byte lines keep the ASCII column in a very narrow pane', () => {
  const out = formatHexDump('ABCDEF', 32768, 4);
  const [a, b] = out.split('\n');
  assert.equal(a, '00000000  41 42 43 44  |ABCD|');
  assert.equal(b, '00000004  45 46        |EF|');
  assert.equal(a.length, 29);
});

test('hexLineFor falls back to 4 bytes when even 8 would overflow', () => {
  assert.equal(hexLineFor(319, 8.3), 4);
  assert.equal(hexLineFor(400, 8.3), 8);
  assert.equal(hexLineFor(700, 8), 16);
});
