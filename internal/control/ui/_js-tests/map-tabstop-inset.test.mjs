// map.js — the virtual table's roving Tab stop and the sticky header.
//
// Reported defect: paintMapVt() gave the single tabindex="0" row to
// Math.ceil(scrollTop / rowH), which is the row at the top of the SCROLLER. The rows start
// `inset` px lower — the sticky <thead> covers the top of the scroller — and mapVtKey() and
// revealScrollTop() both add that inset. Without it, the Tab stop could land on a row
// completely hidden behind the thead: Tab would move focus somewhere the operator cannot
// see, and the list would not scroll (the browser treats a row inside the scroller's
// content box as already visible).
//
// The index math is pure; map.js itself imports core.js, which touches the DOM at load, so
// the helper is extracted from the module source and exercised here — the same extraction
// the repo's UI tests already use (see windowed-list.test.mjs).
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const source = fs.readFileSync(new URL('../js/map.js', import.meta.url), 'utf8');

// extractFunction pulls one top-level `export function name(...) {...}` out of a module's
// source and evaluates it. The function must be self-contained (no imports, no module
// constants) — which is what makes it pure enough to test without a DOM.
function extractFunction(src, name) {
  const sig = 'export function ' + name + '(';
  const start = src.indexOf(sig);
  assert.ok(start >= 0, 'map.js exports ' + name + '()');
  // Walk past the parameter list first: its destructuring braces are not the body.
  let i = start + sig.length - 1;
  let parens = 0;
  for (; i < src.length; i++) {
    if (src[i] === '(') parens++;
    else if (src[i] === ')' && --parens === 0) { i++; break; }
  }
  const bodyStart = src.indexOf('{', i);
  assert.ok(bodyStart >= i, name + '() has a body');
  let depth = 0;
  for (let j = bodyStart; j < src.length; j++) {
    if (src[j] === '{') depth++;
    else if (src[j] === '}' && --depth === 0) {
      return new Function('return (' + src.slice(start + 'export '.length, j + 1) + ')')();
    }
  }
  throw new Error('unbalanced ' + name + '()');
}

const mapTabStopIndex = extractFunction(source, 'mapTabStopIndex');

test('the Tab stop is the first row below the sticky header, not the row at the scroll offset', () => {
  // The regression: a 60px thead, 30px rows, scrolled 30px. The old formula
  // (ceil(30/30) = 1) picked row 1, which spans y=30..60 — entirely behind the header.
  const rowH = 30;
  const inset = 60;
  const scrollTop = 30;
  const oldFormula = Math.ceil(scrollTop / rowH);
  assert.equal(oldFormula, 1);
  assert.ok((oldFormula + 1) * rowH <= scrollTop + inset, 'row 1 ends where the header ends, so it is fully covered');

  const idx = mapTabStopIndex({ scrollTop, rowH, inset, start: 0, end: 40 });
  assert.equal(idx, 3, 'ceil((scrollTop + inset) / rowH)');
  assert.ok((idx + 1) * rowH > scrollTop + inset, 'the chosen row starts below the header');
  // Same value ensureTabStop() in windowed-list.js computes for a pinned header.
  assert.equal(idx, Math.ceil((scrollTop + inset) / rowH));
});

test('whatever the offset, the chosen row is at least partly visible below the header', () => {
  const viewportH = 400;
  for (const rowH of [28, 30, 41, 44]) {
    for (const inset of [0, 28, 56, 73.5]) {
      for (let scrollTop = 0; scrollTop <= 2000; scrollTop += 7) {
        const idx = mapTabStopIndex({ scrollTop, rowH, inset, start: 0, end: 100000 });
        assert.ok(Number.isInteger(idx) && idx >= 0, `index ${idx} for ${scrollTop}/${rowH}/${inset}`);
        // Content-space band the operator can actually see: [scrollTop + inset, scrollTop + viewportH).
        assert.ok((idx + 1) * rowH > scrollTop + inset,
          `row ${idx} is behind the ${inset}px header at scrollTop ${scrollTop} (rowH ${rowH})`);
        assert.ok(idx * rowH < scrollTop + viewportH,
          `row ${idx} starts below the fold at scrollTop ${scrollTop} (rowH ${rowH})`);
      }
    }
  }
});

test('the index is clamped into the mounted window, which is what the caller can focus', () => {
  // Scrolled to the end: the last mounted row, never an index that is not in the DOM.
  assert.equal(mapTabStopIndex({ scrollTop: 99999, rowH: 28, inset: 56, start: 900, end: 910 }), 909);
  // A window that already starts below the header keeps the first mounted row.
  assert.equal(mapTabStopIndex({ scrollTop: 0, rowH: 28, inset: 0, start: 5, end: 20 }), 5);
  // Nothing mounted (an empty repaint): the caller must not look for a row at all.
  assert.equal(mapTabStopIndex({ scrollTop: 0, rowH: 28, inset: 0, start: 0, end: 0 }), -1);
  assert.equal(mapTabStopIndex({ scrollTop: 0, rowH: 28, inset: 0, end: 0 }), -1);
});

test('a degenerate row height cannot produce NaN or a negative index', () => {
  for (const rowH of [0, -1, undefined, null, NaN, '']) {
    const idx = mapTabStopIndex({ scrollTop: 100, rowH, inset: 20, start: 0, end: 50 });
    assert.ok(Number.isInteger(idx) && idx >= 0 && idx <= 49, `rowH ${rowH} -> ${idx}`);
  }
  // No window at all is "nothing mounted": the caller sets no tab stop rather than
  // guessing at a row that may not be in the DOM.
  assert.equal(mapTabStopIndex({ scrollTop: 0, rowH: 28, inset: 0 }), -1);
});
