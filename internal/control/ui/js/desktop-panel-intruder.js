// desktop-panel-intruder.js — desktop skin of the Intruder panel (docs/desktop-design.md,
// Panels). The attack setup reads top to bottom: Target (with the mode and the one
// Start/Stop), Positions, Payloads, Options; results sit beside it. What moves:
//   - threads, delay, repeat and barrier go into Options, with Flag/Extract/
//     Process and the saved presets (the old "Advanced options" disclosure);
//   - the permanent mode sentence becomes an (i) button with the sentence as its
//     tooltip (the sentence element stays, for screen readers);
//   - Preview image, Attach, Diff vs baseline and History go into the results overflow menu;
//   - the three progress/summary strings become one line in the results header;
//   - "Start ▸" reads "Start", the saved-setup picker reads "Presets" like the
//     payload one, and a closed Options section opens before the app focuses one
//     of its fields (Start's validation focuses threads, delay or repeat).
// Every control is the original element, re-parented: ids, listeners, tooltips
// and enabled state are the web ones.
import { runLine, stripGlyph } from './desktop-panel-tools-model.js';
import { whenReady, observe, caseLabels, createOverflow, iconNode, move, revealBeforeFocus } from './desktop-panel-kit.js';

const $ = (sel) => document.querySelector(sel);
const OPTS_KEY = 'dsk.intruder.options';

// Options start open on a tall window and closed on a short one, until the user chooses.
const readOpen = () => {
  try { const v = localStorage.getItem(OPTS_KEY); if (v) return v !== 'closed'; } catch { /* no storage */ }
  return window.innerHeight >= 800;
};
const writeOpen = (open) => { try { localStorage.setItem(OPTS_KEY, open ? 'open' : 'closed'); } catch { /* not persisted */ } };

// wireSetup: labels, the (i) mode hint and the Options section.
function wireSetup() {
  const panel = $('#panel-intruder');
  const bar = panel.querySelector('.intr-bar');
  caseLabels(panel, '.pane-head .lbl');
  const positions = panel.querySelector('.intr-config .pane-head .lbl');
  if (positions) { positions.textContent = 'Positions'; positions.title = 'Request template'; }

  // (i): the mode sentence as a tooltip. #intrHint stays in the DOM and is the
  // button's description, so assistive tech still reads it.
  const hint = $('#intrHint');
  const type = $('#intrType');
  if (hint && type && !$('#intrInfo')) {
    const info = document.createElement('button');
    info.type = 'button';
    info.id = 'intrInfo';
    info.className = 'btn dsk-info';
    info.setAttribute('aria-label', 'About this attack type');
    info.setAttribute('aria-describedby', 'intrHint');
    info.appendChild(iconNode('info'));
    observe(hint, () => info.setAttribute('data-tooltip', hint.textContent.trim()), { childList: true, subtree: true, characterData: true });
    const listMode = bar.querySelector('.ui-select-btn');
    (listMode || type).after(info);
  }

  // Options: runtime numbers first, then the old advanced row.
  const details = panel.querySelector('.intr-opts-disc');
  const summary = details && details.querySelector('summary');
  if (details && summary && !details.querySelector('.dsk-opts-runtime')) {
    const runtime = document.createElement('div');
    runtime.className = 'intr-opts-row dsk-opts-runtime';
    ['#intrThreads', '#intrDelay', '#intrRepeat', '#intrBarrier'].forEach((id) => {
      const input = $(id);
      const wrap = input && input.closest('label');
      if (wrap) move(wrap, runtime);
    });
    // Saved presets belong to the whole setup: they sit at the end of this row.
    const presetWrap = $('#intrPreset') && $('#intrPreset').closest('.ui-select');
    const save = $('#intrPresetSave');
    if (presetWrap) move(presetWrap, runtime);
    if (save) move(save, runtime);
    // One label for both preset pickers: "Presets", sentence case. The app
    // refills the list, so the placeholder (and the picker's shown value) is
    // renamed whenever it changes.
    // The picker may be enhanced after this runs, so watch the whole row.
    observe(runtime, () => {
      const presetSel = $('#intrPreset');
      const first = presetSel && presetSel.options[0];
      if (first && first.value === '' && first.textContent !== 'Presets' && /^(saved )?presets/i.test(first.textContent.trim())) first.textContent = 'Presets';
      const shown = runtime.querySelector('.ui-select-value');
      if (shown && shown.textContent !== 'Presets' && /^(saved )?presets/i.test(shown.textContent.trim())) shown.textContent = 'Presets';
    });
    summary.after(runtime);
    summary.textContent = 'Options';
    const sum = document.createElement('span');
    sum.className = 'dsk-opts-sum';
    sum.setAttribute('aria-hidden', 'true');
    summary.appendChild(sum);
    const refresh = () => {
      const th = Number($('#intrThreads') && $('#intrThreads').value) || 1;
      const dl = Number($('#intrDelay') && $('#intrDelay').value) || 0;
      sum.textContent = `${th} ${th === 1 ? 'thread' : 'threads'} · ${dl} ms delay`;
    };
    refresh();
    details.addEventListener('input', refresh);
    details.addEventListener('change', refresh);
    details.open = readOpen();
    revealBeforeFocus(details);
    // Remember only what the user chooses, never the default picked from the window height.
    summary.addEventListener('click', () => setTimeout(() => writeOpen(details.open), 0));
  }

  // "Start ▸" is plain "Start"; the app re-writes the label on every state change.
  const start = $('#intrStart');
  if (start) observe(start, () => { const t = stripGlyph(start.textContent); if (t !== start.textContent && !start.querySelector('*')) start.textContent = t; });

  // The payload header text is written by the app on every re-render.
  const wrap = $('#intrPayloadsWrap');
  if (wrap) {
    observe(wrap, () => {
      wrap.querySelectorAll('.intr-pl-h').forEach((h) => {
        [...h.childNodes].forEach((n) => {
          if (n.nodeType !== 3) return;
          const t = n.data.trim();
          if (!t) return;
          if (t === t.toUpperCase() && t !== t.toLowerCase()) n.data = t.charAt(0) + t.slice(1).toLowerCase();
          // The set's name is a bare text node; in its own span it can truncate
          // so the header stays one row in a narrow pane (a 900px window).
          const name = document.createElement('span');
          name.className = 'dsk-pl-name';
          name.title = n.data.trim();
          h.insertBefore(name, n);
          name.appendChild(n);
        });
      });
    }, { childList: true, subtree: true });
  }
}

// wireResults: one overflow menu, one run line, chip tooltips.
function wireResults() {
  const results = $('#panel-intruder .intr-results');
  const head = results && results.querySelector('.pane-head');
  if (!results || !head || $('#intrMore')) return;
  const hist = $('#intrHistToggle');
  if (hist) {
    // "⟲ History" → icon + "History", kept when the app rewrites the label.
    observe(hist, () => {
      if (hist.textContent.startsWith('⟲')) hist.textContent = hist.textContent.replace(/^⟲\s*/, '');
      if (!hist.querySelector('.icon')) hist.prepend(iconNode('clock'));
    });
  }
  const items = [$('#intrRender'), $('#intrAttach'), $('#intrDiffBtn'), hist].filter(Boolean);
  const more = createOverflow({
    id: 'intrMore',
    tooltip: 'Preview image, Attach, Diff vs baseline, History',
    items,
  });
  head.appendChild(more.button);

  // One summary line: "15 of 15 requests · 6 flagged · 0 errors · 1 anomaly".
  const summary = $('#intrSummary');
  const text = $('#intrSummaryText');
  const stats = $('#intrStats');
  if (summary && text && !$('#intrRunLine')) {
    const line = document.createElement('span');
    line.id = 'intrRunLine';
    line.className = 'dsk-runline';
    text.after(line);
    const sync = () => { const next = runLine(text.textContent, stats ? stats.textContent : ''); if (line.textContent !== next) line.textContent = next; };
    observe(text, sync);
    if (stats) observe(stats, sync);
  }

  // Result chips are icons in the desktop skin; their words become tooltips.
  const list = $('#intrResults');
  if (list) {
    list.addEventListener('mouseover', (e) => {
      const chip = e.target.closest && e.target.closest('.intr-chip');
      if (chip && !chip.hasAttribute('title')) chip.setAttribute('title', chip.textContent.trim());
    });
  }
}

export function initIntruderDesktop() {
  whenReady(() => $('#panel-intruder .intr-bar') && $('#intrHint') && $('#intrType') && $('#intrThreads') && $('#intrPayloadsWrap'), wireSetup);
  whenReady(() => $('#intrAttach') && $('#intrDiffBtn') && $('#intrSummary') && $('#intrRender') && $('#intrHistToggle'), wireResults);
}
