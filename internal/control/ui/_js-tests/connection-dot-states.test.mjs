// connection.js decides the live-updates dot's class from the SSE status:
// 'sse-dot ok' | 'sse-dot reconnecting' | 'sse-dot offline'. The stylesheet only
// styled the base dot (the healthy --accent) and .reconnecting, so an offline
// stream drew the *healthy* colour while its label read "Offline" — a colour
// signal contradicting the text. This test ties the classes the model can emit to
// the rules the sheet actually declares, so adding a status without a state
// colour fails here instead of shipping a green "offline" dot.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { connectionModel } from '../js/connection.js';

const css = readFileSync(new URL('../app.css', import.meta.url), 'utf8');
const ruleFor = (sel) => {
  const escaped = sel.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const m = new RegExp('(?:^|[,{}])\\s*' + escaped + '\\s*\\{([^}]*)\\}', 'm').exec(css);
  return m ? m[1] : null;
};

test('every status the model emits maps to a dot class name', () => {
  assert.equal(connectionModel('ok').dotClass, 'sse-dot ok');
  assert.equal(connectionModel('reconnecting').dotClass, 'sse-dot reconnecting');
  assert.equal(connectionModel('offline').dotClass, 'sse-dot offline');
});

test('the base dot is the healthy colour, so a state class must override it', () => {
  // .sse-dot is declared both alone (transition) and in the shared dot group.
  const accentRule = /(?:^|[{}])\s*[^{}]*\.sse-dot[^{}]*\{([^}]*)\}/g;
  let accent = false;
  for (const m of css.matchAll(accentRule)) if (m[1].includes('background:var(--accent)')) accent = true;
  assert.ok(accent, '.sse-dot must inherit the healthy --accent background');
  const offline = ruleFor('.sse-dot.offline');
  assert.ok(offline, '.sse-dot.offline must be declared');
  assert.ok(/background:var\(--(red|danger|redSolid)\)/.test(offline), 'offline must override the healthy background');
  const reconnecting = ruleFor('.sse-dot.reconnecting');
  assert.ok(reconnecting && /background:var\(--pendingFill\)/.test(reconnecting), 'reconnecting keeps its pending colour');
});

test('an offline dot does not keep the reconnecting pulse', () => {
  const offline = ruleFor('.sse-dot.offline') || '';
  assert.ok(!offline.includes('animation'), 'offline is terminal, not a retry in progress');
});
