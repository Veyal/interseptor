// capture-loss-model.js — pure text for the History "capture fell behind"
// banner. The server counts flow writes and WebSocket frames the capture
// pipeline had to drop since start (GET /api/project/readiness .capture); any
// drop means History is missing traffic that went through the proxy, so the
// banner is not dismissible and stays until the process restarts.

const count = (n) => (Number.isFinite(Number(n)) && Number(n) > 0 ? Math.floor(Number(n)) : 0);
const fmt = (n) => n.toLocaleString('en-US');
const noun = (n, one, many) => fmt(n) + ' ' + (n === 1 ? one : many);

// captureLossModel(capture) -> {show:false} or {show, text, hint, key}.
export function captureLossModel(capture) {
  const flows = count(capture && capture.flowWritesDropped);
  const ws = count(capture && capture.wsFramesDropped);
  if (!flows && !ws) return { show: false, text: '', hint: '', key: '' };
  const parts = [];
  if (flows) parts.push(noun(flows, 'captured flow write', 'captured flow writes'));
  if (ws) parts.push(noun(ws, 'WebSocket frame', 'WebSocket frames'));
  const verb = flows + ws === 1 ? 'was' : 'were';
  return {
    show: true,
    text: 'History is incomplete: ' + parts.join(' and ') + ' ' + verb + ' dropped because capture fell behind.',
    hint: 'Slow the client or narrow scope, then re-capture any request you need as evidence.',
    key: flows + '/' + ws,
  };
}
