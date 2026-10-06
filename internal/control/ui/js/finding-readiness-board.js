import { esc, escAttr } from './core.js';

// Project-wide "what blocks the report" table from GET /api/findings/readiness.
// Rows arrive sorted by severity; this module only renders them.
export function renderReadinessBoard(quality) {
  const rows = quality?.board || [];
  if (!rows.length) return `<p class="hint">${esc(quality?.message || 'No findings match the selected statuses.')}</p>`;
  const s = quality.summary || { ready: 0, blocked: 0 };
  const body = rows.map(r => `<tr><td>#${Number(r.id)}</td><td>${esc(r.title || 'Untitled')}</td><td>${esc(r.severity || '')}</td><td>${esc(r.status || '')}</td><td>${r.ready ? '<span class="find-ready">Ready</span>' : '<span class="find-draft">Blocked</span>'}</td><td>${(r.gaps || []).map(g => `<code>${esc(g)}</code>`).join(' ') || '—'}</td><td><button type="button" class="btn xs" data-review-finding="${escAttr(String(r.id))}">Open</button></td></tr>`).join('');
  return `<p class="hint">${Number(s.ready)} ready · ${Number(s.blocked)} blocked</p><div class="find-board-scroll"><table class="find-board"><thead><tr><th>ID</th><th>Finding</th><th>Severity</th><th>Status</th><th>Ready</th><th>Blocking gaps</th><th></th></tr></thead><tbody>${body}</tbody></table></div>`;
}
