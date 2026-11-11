// map.js — review findings (agent ui-js-b).
//
// 1. buildGraphTree() keyed an endpoint leaf by its method only ('ep|GET'). The
//    server groups endpoints by host + method + path, and the stored path is the
//    request URI (query string included), while the graph folders are the path
//    WITHOUT the query. So `GET /search?q=a` and `GET /search?q=b` (or `/a` and
//    `/a/`) are two endpoints that land in the same folder with the same leaf
//    key: the second silently replaced the first. The graph then drew fewer
//    endpoints than the Tree/Table and the header count, and the surviving node
//    opened whichever flow happened to come last.
// 2. The Table view's last header cell was an empty <th> (no accessible name).
//
// map.js imports core.js (DOM at load), so the pure pieces are sliced out of the
// module source and evaluated with a stub mapState — the extraction approach the
// other map tests use.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const source = fs.readFileSync(new URL('../js/map.js', import.meta.url), 'utf8');

function sliceFunction(src, name) {
  const m = new RegExp('(?:export )?function ' + name + '\\(').exec(src);
  assert.ok(m, 'map.js defines ' + name + '()');
  let i = m.index + m[0].length - 1, parens = 0;
  for (; i < src.length; i++) {
    if (src[i] === '(') parens++;
    else if (src[i] === ')' && --parens === 0) { i++; break; }
  }
  const bodyStart = src.indexOf('{', i);
  let depth = 0;
  for (let j = bodyStart; j < src.length; j++) {
    if (src[j] === '{') depth++;
    else if (src[j] === '}' && --depth === 0) return src.slice(m.index, j + 1).replace(/^export /, '');
  }
  throw new Error('unbalanced ' + name + '()');
}

function loadGraph(mapState) {
  const ext = /const MAP_MEDIA_EXT = new Set\(\[[\s\S]*?\]\);/.exec(source);
  assert.ok(ext, 'map.js defines MAP_MEDIA_EXT');
  const body = [
    ext[0],
    "let _gtKey = '', _gtCache = null;",
    ...['isMapMediaEndpoint', 'pruneGraphNode', 'graphEps', 'mapTreeExpansionSignature', 'gCount', 'buildGraphTree'].map((n) => sliceFunction(source, n)),
    'return { buildGraphTree, gCount };',
  ].join('\n');
  return new Function('mapState', body)(mapState);
}

const freshState = () => ({
  _dataVersion: 1, domain: '', method: '', statusClass: 0, collapseIdentical: true, searchScope: 'path', search: '',
  expandedClusters: new Set(), searchExpandedClusters: new Set(),
});

function leaves(n, out = []) {
  if (n.type === 'ep') out.push(n);
  (n.children || []).forEach((c) => leaves(c, out));
  return out;
}

test('graph keeps endpoints that differ only by query string', () => {
  const { buildGraphTree, gCount } = loadGraph(freshState());
  const eps = [
    { host: 'app.example.com', method: 'GET', path: '/search?q=a', lastFlowId: 1 },
    { host: 'app.example.com', method: 'GET', path: '/search?q=b', lastFlowId: 2 },
    { host: 'app.example.com', method: 'POST', path: '/search?q=a', lastFlowId: 3 },
  ];
  const root = buildGraphTree(eps);
  const eplist = leaves(root);
  assert.deepEqual(eplist.map((n) => n.ep.lastFlowId).sort(), [1, 2, 3]);
  assert.equal(gCount(root.children[0]), 3);
  assert.equal(new Set(eplist.map((n) => n.key)).size, 3, 'leaf keys are unique');
});

test('graph keeps /a and /a/ as two endpoints', () => {
  const { buildGraphTree } = loadGraph(freshState());
  const root = buildGraphTree([
    { host: 'app.example.com', method: 'GET', path: '/a', lastFlowId: 10 },
    { host: 'app.example.com', method: 'GET', path: '/a/', lastFlowId: 11 },
  ]);
  assert.deepEqual(leaves(root).map((n) => n.ep.lastFlowId).sort(), [10, 11]);
});

test('graph leaf keeps the method as its label and one node per plain endpoint', () => {
  const { buildGraphTree } = loadGraph(freshState());
  const root = buildGraphTree([
    { host: 'app.example.com', method: 'GET', path: '/api/users', lastFlowId: 1 },
    { host: 'app.example.com', method: 'DELETE', path: '/api/users', lastFlowId: 2 },
    { host: 'app.example.com', method: 'GET', path: '/logo.png', lastFlowId: 3 },
  ]);
  const eplist = leaves(root);
  assert.deepEqual(eplist.map((n) => n.label).sort(), ['DELETE', 'GET']);
  assert.ok(eplist.every((n) => n.type === 'ep' && n.children.length === 0));
});

test('graph tree build stays linear on thousands of endpoints', () => {
  const { buildGraphTree } = loadGraph(freshState());
  const eps = [];
  for (let i = 0; i < 20000; i++) eps.push({ host: 'h' + (i % 20) + '.example.com', method: 'GET', path: '/api/v' + (i % 7) + '/item/' + i + '?page=' + (i % 3), lastFlowId: i + 1 });
  const t0 = performance.now();
  const root = buildGraphTree(eps);
  const ms = performance.now() - t0;
  assert.equal(leaves(root).length, 20000);
  assert.ok(ms < 2000, 'buildGraphTree(20000) took ' + ms.toFixed(1) + 'ms');
});

test('Table view action column header has an accessible name', () => {
  const head = /const head = `<thead><tr>[\s\S]*?<\/tr><\/thead>`;/.exec(source);
  assert.ok(head, 'renderMapTable builds the header');
  assert.ok(!/<th[^>]*>\s*<\/th>/.test(head[0]), 'no empty <th>');
  assert.match(head[0], /<th[^>]*><span class="u-sr">Actions<\/span><\/th>/);
});
