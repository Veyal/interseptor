// capture-loss.js — the History banner shown when the capture pipeline had to
// drop flow writes or WebSocket frames under load. It reads the counters the
// project state store already refreshes on live traffic (no extra request) and
// repaints only when they change. Icon plus text, never colour alone; it is a
// polite status region, and it cannot be dismissed because the loss is real.
import { projectState } from './project-state.js';
import { captureLossModel } from './capture-loss-model.js';

const NS = 'http://www.w3.org/2000/svg';
let lastKey = '';

function iconNode(name) {
  const svg = document.createElementNS(NS, 'svg');
  svg.setAttribute('class', 'icon');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  const use = document.createElementNS(NS, 'use');
  use.setAttribute('href', '#i-' + name);
  svg.appendChild(use);
  return svg;
}

export function renderCaptureLoss(s = projectState.get()) {
  const host = document.getElementById('captureLossBanner');
  if (!host) return;
  const m = captureLossModel(s && s.capture);
  if (m.key === lastKey) return;
  lastKey = m.key;
  host.textContent = '';
  host.hidden = !m.show;
  if (!m.show) return;
  const text = document.createElement('span');
  text.className = 'capture-loss-text';
  const strong = document.createElement('strong');
  strong.textContent = m.text;
  const hint = document.createElement('span');
  hint.className = 'capture-loss-hint';
  hint.textContent = m.hint;
  text.append(strong, ' ', hint);
  host.append(iconNode('alert'), text);
}

projectState.subscribe(renderCaptureLoss);
