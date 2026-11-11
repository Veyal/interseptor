import test from 'node:test';
import assert from 'node:assert/strict';
import { outlierChips, chipsHTML } from '../js/intruder.js';

// Grep inspects only the first 8 MiB of a response. A row whose body was cut
// must say so: "no match" on such a row is not proof of absence.
test('a result whose grep was cut short carries a chip that says so', () => {
  const base = { status: 200, length: 100, timeMs: 50 };
  const chips = outlierChips({ status: 200, length: 100, timeMs: 50, grepTruncated: true }, base);
  assert.equal(chips.length, 1);
  assert.equal(chips[0].kind, 'partial');
  assert.match(chips[0].label, /first 8 MiB/);
  assert.match(chips[0].label, /grep/i);
});

test('the chip appears beside a match and without a baseline', () => {
  const kinds = outlierChips({ status: 200, matched: true, grepTruncated: true }, null).map((c) => c.kind);
  assert.deepEqual(kinds, ['match', 'partial']);
});

test('rows that were grepped in full are unchanged', () => {
  const base = { status: 200, length: 100, timeMs: 50 };
  assert.deepEqual(outlierChips({ status: 200, length: 100, timeMs: 50 }, base), []);
  assert.deepEqual(outlierChips({ status: 200, length: 100, timeMs: 50, grepTruncated: false }, base), []);
});

test('the chip renders with an icon and its label as the accessible name', () => {
  const html = chipsHTML({ status: 200, grepTruncated: true }, null, (s) => s);
  assert.match(html, /intr-chip-partial/);
  assert.match(html, /aria-label="[^"]*first 8 MiB[^"]*"/);
  assert.match(html, /<use href="#i-[a-z-]+"/);
  assert.doesNotMatch(html, /#i-undefined/);
});
