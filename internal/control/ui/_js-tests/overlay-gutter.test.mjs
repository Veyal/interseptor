import { test } from 'node:test';
import assert from 'node:assert/strict';
import { overlayPaddingRight, syncOverlayGutter } from '../js/overlay-gutter.js';

// Regression: WebKit with classic scrollbars reserves no gutter on the
// overflow:hidden highlight <pre>, so on a long body the textarea wrapped 10px
// narrower than its overlay and the colored text drifted from the caret.
test('the overlay gets the textarea scrollbar width as extra right padding', () => {
  assert.equal(overlayPaddingRight(14, 10, 0), 24);
  assert.equal(overlayPaddingRight(14, 10, 10), 14);
  assert.equal(overlayPaddingRight(14, 0, 0), 14);
  assert.equal(overlayPaddingRight(14, 0, 10), 4);
});

test('syncOverlayGutter measures both boxes and pads the overlay', () => {
  const ta = { offsetWidth: 561, clientWidth: 537, getBoundingClientRect: () => ({}) };
  const hl = { offsetWidth: 561, clientWidth: 547, style: {} };
  // borders are 0: gutter = offsetWidth - clientWidth
  syncOverlayGutter(ta, hl, { paddingRight: '14px', borderLeftWidth: '0px', borderRightWidth: '0px' }, { borderLeftWidth: '0px', borderRightWidth: '0px' });
  assert.equal(hl.style.paddingRight, '24px');
});
