import test from 'node:test';
import assert from 'node:assert/strict';
import { timelineItems } from '../js/identities-model.js';

test('a source flow re-pointed at a newer flow is ordered by time, not prepended', () => {
  // Refresh from flow re-points the identity's source at the flow that was just
  // used, so the "Login flow" row can be newer than the Created and Checked
  // events. The panel promises one oldest-first list.
  const items = timelineItems({ timeline: {
    source: { flowId: 900, ts: '2026-10-23T12:00:00Z', method: 'POST', host: 'example.com', path: '/api/login', status: 200 },
    events: [
      { ts: '2026-10-23T09:00:00Z', kind: 'created', actor: 'you' },
      { ts: '2026-10-23T11:00:00Z', kind: 'checked', actor: 'you', detail: '200 ok' },
      { ts: '2026-10-23T11:30:00Z', kind: 'refreshed', actor: 'you' },
    ],
  } });
  assert.deepEqual(items.map((i) => (i.kind === 'source' ? 'source' : i.event)), ['created', 'checked', 'refreshed', 'source']);
});

test('a source sharing a timestamp with its created event still comes first', () => {
  const items = timelineItems({ timeline: {
    source: { flowId: 812, ts: '2026-10-23T09:00:00Z', method: 'POST', host: 'example.com', path: '/api/login', status: 200 },
    events: [{ ts: '2026-10-23T09:00:00Z', kind: 'created' }],
  } });
  assert.deepEqual(items.map((i) => (i.kind === 'source' ? 'source' : i.event)), ['source', 'created']);
});

test('events without a timestamp keep their order after the dated rows', () => {
  const items = timelineItems({ timeline: {
    source: { flowId: 5, ts: '2026-10-23T09:00:00Z' },
    events: [{ kind: 'updated' }, { kind: 'marked-broken' }, { ts: '2026-10-23T10:00:00Z', kind: 'checked' }],
  } });
  assert.deepEqual(items.map((i) => (i.kind === 'source' ? 'source' : i.event)), ['source', 'checked', 'updated', 'marked-broken']);
});
