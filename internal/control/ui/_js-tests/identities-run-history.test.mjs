import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import * as M from '../js/identities-model.js';

const read = (name) => fs.readFileSync(new URL('../js/' + name, import.meta.url), 'utf8');

// The store keeps the newest 200 authz runs and the panel filters them per
// identity on the client, so it must fetch the whole retained history; asking
// for 50 hid an identity's older runs even though the server still had them.
test('identity run history requests the full retained history', () => {
  assert.equal(M.RUN_HISTORY_LIMIT, 200);
  assert.equal(M.runsURL(), '/api/authz/runs?limit=200');
  const src = read('identities.js');
  assert.match(src, /api\(M\.runsURL\(\)\)/);
  assert.doesNotMatch(src, /\/api\/authz\/runs\?limit=50/);
});
