// settings-appearance.js — the Appearance section: theme (System / Dark / Light /
// High contrast), density, the single-key shortcuts switch and hint visibility.
// Everything is a per-browser preference; storage is wrapped because it can throw.
import { $, $$, setSeg, setDensity, getDensity } from './core.js';
import { readSingleKeyPref, writeSingleKeyPref } from './keys.js';
import {
  normalizeThemeChoice, themeAttribute, themeStoragePlan, normalizeDensityChoice, readHintsPref, writeHintsPref,
} from './settings-model.js';

const root = document.documentElement;
const prefersLight = () => { try { return window.matchMedia('(prefers-color-scheme: light)').matches; } catch (e) { return false; } };
const storedTheme = () => { try { return localStorage.getItem('theme'); } catch (e) { return null; } };

export function currentThemeChoice() { return normalizeThemeChoice(storedTheme()); }

function applyThemeChoice(choice) {
  const attr = themeAttribute(choice, prefersLight());
  if (attr) root.setAttribute('data-theme', attr); else root.removeAttribute('data-theme');
  setToggleIcon(attr === 'light' ? 'sun' : 'moon');
}

// The topbar toggle shows the sun in light theme and the moon otherwise.
function setToggleIcon(name) {
  const toggle = $('#themeToggle');
  if (!toggle) return;
  const NS = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(NS, 'svg');
  svg.setAttribute('class', 'icon');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  const use = document.createElementNS(NS, 'use');
  use.setAttribute('href', '#i-' + name);
  svg.appendChild(use);
  toggle.replaceChildren(svg);
}

export function setThemeChoice(choice) {
  const plan = themeStoragePlan(choice);
  try { if (plan.action === 'remove') localStorage.removeItem('theme'); else localStorage.setItem('theme', plan.value); } catch (e) { /* not persisted */ }
  applyThemeChoice(choice);
  syncAppearance();
}

function syncSwitch(id, on, onText, offText) {
  const b = $('#' + id);
  if (!b) return;
  b.setAttribute('aria-pressed', on ? 'true' : 'false');
  b.classList.toggle('on', on);
  b.textContent = on ? onText : offText;
}

export function syncAppearance() {
  const theme = currentThemeChoice();
  $$('#themeSeg button').forEach((b) => setSeg(b, b.dataset.themeChoice === theme));
  const density = normalizeDensityChoice(getDensity());
  $$('#densitySeg button').forEach((b) => setSeg(b, b.dataset.densityChoice === density));
  syncSwitch('singleKeySwitch', readSingleKeyPref(), 'Single-key shortcuts are on', 'Single-key shortcuts are off');
  syncSwitch('hintsSwitch', readHintsPref(), 'Hints are on', 'Hints are off');
}

function applyHints() { root.setAttribute('data-hints', readHintsPref() ? 'on' : 'off'); }

function wire() {
  $$('#themeSeg button').forEach((b) => { b.onclick = () => setThemeChoice(b.dataset.themeChoice); });
  $$('#densitySeg button').forEach((b) => { b.onclick = () => { setDensity(b.dataset.densityChoice); syncAppearance(); }; });
  const keys = $('#singleKeySwitch');
  if (keys) keys.onclick = () => { writeSingleKeyPref(!readSingleKeyPref()); syncAppearance(); };
  const hints = $('#hintsSwitch');
  if (hints) hints.onclick = () => { writeHintsPref(!readHintsPref()); applyHints(); syncAppearance(); };
  // System follows the OS live; the topbar toggle and other writers change data-theme.
  try {
    window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => { if (currentThemeChoice() === 'system') applyThemeChoice('system'); syncAppearance(); });
  } catch (e) { /* matchMedia unavailable */ }
  new MutationObserver(syncAppearance).observe(root, { attributes: true, attributeFilter: ['data-theme'] });
  window.addEventListener('densitychange', syncAppearance);
}

applyHints();
wire();
syncAppearance();
