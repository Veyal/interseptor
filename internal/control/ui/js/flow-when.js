// Pure, DOM-free timestamp formatting for flow rows. The History "Time" column
// used to show only the time of day, so a flow from last week looked like one
// from this morning. formatFlowWhen() keeps today's rows compact (time only)
// and adds a short date for everything else. Month names come from a fixed
// English table, never the viewer's locale, so the column reads the same
// everywhere and the output is testable. Local calendar days (not 24h deltas)
// decide "today"/"yesterday", so DST days and month/year boundaries are exact.

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const p2 = n => String(n).padStart(2, '0');
const p3 = n => String(n).padStart(3, '0');
const NONE = Object.freeze({ date: '', time: '—', title: '', kind: 'none' });

// Whole local calendar days between two instants (positive when `then` is earlier).
function dayIndex(d) { return Math.round(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()) / 86400000); }

function offsetLabel(d) {
  const off = -d.getTimezoneOffset();
  const abs = Math.abs(off);
  return 'UTC' + (off < 0 ? '-' : '+') + p2(Math.floor(abs / 60)) + ':' + p2(abs % 60);
}

// Full local datetime with milliseconds and the UTC offset: the tooltip and the
// screen-reader text for a cell that may visibly show only the time.
export function formatFlowWhenFull(ts) {
  const n = Number(ts);
  if (!Number.isFinite(n) || n <= 0) return '';
  const d = new Date(n);
  if (Number.isNaN(d.getTime())) return '';
  return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}.${p3(d.getMilliseconds())} ${offsetLabel(d)}`;
}

export function formatFlowWhen(ts, now = Date.now()) {
  const n = Number(ts);
  if (ts == null || ts === '' || !Number.isFinite(n) || n <= 0) return NONE;
  const d = new Date(n);
  if (Number.isNaN(d.getTime())) return NONE;
  const ref = new Date(Number.isFinite(Number(now)) ? Number(now) : Date.now());
  const hms = `${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}`;
  const title = formatFlowWhenFull(n);
  const age = dayIndex(ref) - dayIndex(d);
  if (age === 0) return { date: '', time: hms, title, kind: 'today' };
  if (age === 1) return { date: 'Yest', time: hms, title, kind: 'yesterday' };
  if (d.getFullYear() === ref.getFullYear()) {
    return { date: `${MONTHS[d.getMonth()]} ${d.getDate()}`, time: hms, title, kind: 'year' };
  }
  return { date: `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())}`, time: hms.slice(0, 5), title, kind: 'other' };
}

// Milliseconds until the next local midnight (DST-safe: built from calendar
// fields, not +24h). Never returns less than 1s so a timer cannot spin.
export function msUntilNextMidnight(now = Date.now()) {
  const d = new Date(now);
  const next = new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1, 0, 0, 0, 0);
  return Math.max(1000, next.getTime() - d.getTime());
}
