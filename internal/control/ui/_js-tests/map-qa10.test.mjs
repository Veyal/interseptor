import test from 'node:test';
import assert from 'node:assert/strict';
import { wheelZoomFactor, zoomAround, gestureZoom, endpointUrl, paramsCountText, mapCountSuffix, MAP_ZOOM_MAX, MAP_ZOOM_MIN } from '../js/map-model.js';

test('a mouse wheel notch steps about 10%, not 2.7x', () => {
  const f = wheelZoomFactor(-100);
  assert.ok(f > 1.08 && f < 1.12, String(f));
  let k = 1.2;
  for (let i = 0; i < 5; i++) k *= wheelZoomFactor(-100);
  assert.ok(k < 2.2, `5 notches from 1.2 reached ${k}`);
  assert.ok(wheelZoomFactor(100) < 1);
  assert.equal(wheelZoomFactor(0), 1);
});

test('trackpad pinch deltas stay continuous and line-mode wheels are scaled', () => {
  const f = wheelZoomFactor(-4);
  assert.ok(f > 1.03 && f < 1.05, String(f));
  const lines = wheelZoomFactor(-3, 1);
  assert.ok(lines > 1.08 && lines < 1.4, String(lines));
});

test('zoomAround keeps the anchor point fixed and clamps the scale', () => {
  const z = { k: 1, x: 10, y: 20 };
  const n = zoomAround(z, 100, 100, 2);
  assert.equal(n.k, 2);
  // world point under the anchor before/after
  const before = (100 - z.x) / z.k, after = (100 - n.x) / n.k;
  assert.ok(Math.abs(before - after) < 1e-9);
  assert.equal(zoomAround(z, 0, 0, 99).k, MAP_ZOOM_MAX);
  assert.equal(zoomAround(z, 0, 0, 0.0001).k, MAP_ZOOM_MIN);
});

test('gestureZoom scales from the scale captured at gesturestart', () => {
  const start = { k: 1.5, x: 30, y: 40 };
  const half = gestureZoom(start, 1, 200, 150);
  assert.deepEqual(half, { k: 1.5, x: 30, y: 40 });
  const bigger = gestureZoom(start, 2, 200, 150);
  assert.equal(bigger.k, 3);
  assert.ok(Math.abs((200 - start.x) / start.k - (200 - bigger.x) / bigger.k) < 1e-9);
  assert.equal(gestureZoom(start, 10, 0, 0).k, MAP_ZOOM_MAX);
  assert.equal(gestureZoom(start, 0.01, 0, 0).k, MAP_ZOOM_MIN);
  assert.deepEqual(gestureZoom(start, NaN, 1, 1), start);
});

test('endpoint URL shows a non-default port', () => {
  assert.equal(endpointUrl({ scheme: 'http', host: 'h2.localhost', port: 18906, path: '/api/items/2' }), 'http://h2.localhost:18906/api/items/2');
  assert.equal(endpointUrl({ scheme: 'https', host: 'a.test', port: 443, path: '/x' }), 'https://a.test/x');
  assert.equal(endpointUrl({ scheme: 'http', host: 'a.test', path: '' }), 'http://a.test/');
});

test('endpoint URL does not repeat a port the host already carries', () => {
  assert.equal(endpointUrl({ scheme: 'http', host: '127.0.0.1:18753', port: 18753, path: '/a' }), 'http://127.0.0.1:18753/a');
});

test('params line flags a capped sample; count suffix explains collapsing', () => {
  assert.equal(paramsCountText({ flowsScanned: 2000, capped: true }), '2,000 flows · param miner · newest sample, hit counts are partial');
  assert.equal(paramsCountText({ flowsScanned: 12 }), '12 flows · param miner');
  assert.equal(mapCountSuffix(1207, 5610), ' (5,610 before collapsing)');
  assert.equal(mapCountSuffix(5, 5), '');
});
