// suppressed-model.js — pure text for the "the capture filter dropped traffic"
// banner. The proxy forwards these requests and History has none of them, so
// without this the operator and the agent both conclude the app sent nothing.
const COUNT = (n) => (Number.isFinite(Number(n)) && Number(n) > 0 ? Math.floor(Number(n)) : 0);

export function suppressedReasonText(reason) {
  switch (reason) {
    case 'out-of-scope':
      return 'out of scope';
    case 'android-telemetry':
      return 'Android telemetry';
    case 'browser-telemetry':
      return 'browser telemetry';
    default:
      return reason || 'filtered';
  }
}

/**
 * suppressedBannerModel(report) -> {show, total, host, reason, text, hint, key}
 * report is GET /api/flows/suppressed (windowSec, total, hosts, samples).
 */
export function suppressedBannerModel(report) {
  const total = COUNT(report && report.total);
  if (!total) return { show: false, total: 0, host: '', reason: '', text: '', hint: '', key: '' };
  const hosts = (report && report.hosts) || [];
  const top = hosts[0] || null;
  const host = top ? top.host : '';
  const reason = top ? top.reason : '';
  const noun = total === 1 ? 'request' : 'requests';
  const text = total + ' ' + noun + ' suppressed in the last 15 min' +
    (top ? ' — mostly ' + host + ' (' + CASE(top.count) + ', ' + suppressedReasonText(reason) + ').' : ' — forwarded but never captured.');
  const hint = reason && reason !== 'out-of-scope'
    ? 'Turn the telemetry suppression off in Settings → Proxy to capture these.'
    : 'Widen the scope, or turn off "capture in-scope traffic only" in Settings.';
  return { show: true, total, host, reason, text, hint, key: total + '|' + host + '|' + reason };
}

const CASE = (n) => String(Number(n) || 0);
