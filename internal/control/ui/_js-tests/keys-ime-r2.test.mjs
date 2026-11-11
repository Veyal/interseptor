// Enter that commits an IME composition (Japanese, Chinese, Korean input) is not
// "submit". Chrome reports it as a keydown with isComposing; Safari and the macOS
// WKWebView shell fire it after compositionend with keyCode 229. Fields that act
// on Enter must ignore both.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import * as keys from '../js/keys.js';
import * as finder from '../js/finder.js';

const js = (name) => fs.readFileSync(new URL('../js/' + name, import.meta.url), 'utf8');

test('isImeKeyEvent recognises a composition keydown in every engine', () => {
  assert.equal(typeof keys.isImeKeyEvent, 'function', 'keys.js exports isImeKeyEvent');
  assert.equal(keys.isImeKeyEvent({ key: 'Enter', isComposing: true, keyCode: 229 }), true, 'Chrome / Firefox');
  assert.equal(keys.isImeKeyEvent({ key: 'Enter', isComposing: false, keyCode: 229 }), true, 'Safari: after compositionend');
  assert.equal(keys.isImeKeyEvent({ key: 'Process', isComposing: true }), true);
  assert.equal(keys.isImeKeyEvent({ key: 'Enter', isComposing: false, keyCode: 13 }), false, 'a real Enter');
  assert.equal(keys.isImeKeyEvent({ key: 'a' }), false);
  assert.equal(keys.isImeKeyEvent(null), false);
});

test('the key registry ignores a composition keydown', () => {
  const ran = [];
  const r = keys.createKeyRegistry({ singleKeyEnabled: () => true });
  r.register({ id: 'x', keys: 'Mod+Enter', run: () => ran.push('x') });
  const ev = (extra) => ({ key: 'Enter', ctrlKey: true, target: {}, preventDefault() { this.prevented = true; }, ...extra });
  const ime = ev({ isComposing: true, keyCode: 229 });
  assert.equal(r.handle(ime), false);
  assert.equal(ime.prevented, undefined, 'the IME keeps its key');
  assert.equal(r.handle(ev({ keyCode: 13 })), true);
  assert.deepEqual(ran, ['x']);
});

// Every owned field that submits or runs something on Enter. The handler must
// consult the guard before it looks at the key.
const ENTER_FIELDS = [
  ['humaninput.js', 'inp.onkeydown = e => {', 'respond(inp.dataset.id, inp.value)'],
  ['cmdk.js', 'cmdk.input.onkeydown=e=>{', 'cmdkRun(cmdk.sel'],
  ['evidence-attach.js', "root.addEventListener('keydown', (e) => {", 'finish(rows[active].value)'],
  ['report-preflight.js', "type?.addEventListener('keydown', (e) => {", 'go();'],
];
for (const [file, open, action] of ENTER_FIELDS) {
  test(file + ': Enter during composition does not submit', () => {
    const src = js(file);
    const start = src.indexOf(open);
    assert.ok(start >= 0, file + ' handler found');
    const end = src.indexOf(action, start);
    assert.ok(end > start, file + ' action found');
    assert.match(src.slice(start, end), /isImeKeyEvent\(e\)\)\s*(?:\{\s*e\.stopPropagation\(\);\s*)?return/, 'the handler returns early for an IME keydown');
    assert.match(src, /import \{[^}]*\bisImeKeyEvent\b[^}]*\} from '\.\/keys\.js'/);
  });
}

test('finder keys: Enter steps, composition is left alone', () => {
  assert.equal(typeof finder.finderKeyAction, 'function', 'finder.js exports finderKeyAction');
  const a = finder.finderKeyAction;
  assert.equal(a({ key: 'Enter' }), 'next');
  assert.equal(a({ key: 'Enter', shiftKey: true }), 'prev');
  assert.equal(a({ key: 'Escape' }), 'close');
  assert.equal(a({ key: 'Enter', isComposing: true, keyCode: 229 }), null);
  assert.equal(a({ key: 'Escape', isComposing: true, keyCode: 229 }), null, 'Escape cancels the composition, not the bar');
  assert.equal(a({ key: 'x' }), null);
});

test('finder keys: Alt+R and Alt+C work on macOS, where Option changes the character', () => {
  const a = finder.finderKeyAction;
  assert.equal(a({ key: 'r', code: 'KeyR', altKey: true }), 'regex');
  assert.equal(a({ key: 'C', code: 'KeyC', altKey: true, shiftKey: true }), 'case');
  // macOS: Option+R types "®", Option+C types "ç".
  assert.equal(a({ key: '®', code: 'KeyR', altKey: true }), 'regex');
  assert.equal(a({ key: 'ç', code: 'KeyC', altKey: true }), 'case');
  // AltGr on Windows is Ctrl+Alt and types a character: not the shortcut.
  assert.equal(a({ key: '®', code: 'KeyR', altKey: true, ctrlKey: true }), null);
  assert.equal(a({ key: 'r', code: 'KeyR' }), null, 'a plain letter is typed into the field');
});
