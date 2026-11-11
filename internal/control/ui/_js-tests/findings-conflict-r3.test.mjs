// The editor must name the version it rendered from, and a 409 must keep the
// operator's draft while refreshing the base so Retry can succeed.
import test from 'node:test';
import assert from 'node:assert/strict';

// Resolved against this file so the suite works from any working directory
// (the Go test runs node with `ui` as the cwd).
const src = (await import('node:fs')).readFileSync(new URL('../js/findings.js', import.meta.url), 'utf8');

test('the body autosave names its base version', () => {
  assert.match(src, /const base = findingEditBases\.get\(fid\) \|\| 0;/);
  assert.match(src, /baseUpdatedTs: base/);
  // A save without a known base stays unconditional, so a first save never fails
  // for a version the client never saw.
  assert.match(src, /base \? \{ baseUpdatedTs: base \} : null/);
});

test('transport metadata is not staged as a draft field', () => {
  assert.match(src, /if \(extra\) queue\.pendingExtra/);
  // ...but it goes out in the body
  assert.match(src, /\{ \.\.\.fields, \.\.\.extra \}/);
});

test('a conflict keeps the draft and explains what happened', () => {
  assert.match(src, /e\.status === 409/);
  assert.match(src, /Your edit was not saved: Retry to overwrite, or Discard to load the newer version/);
  assert.match(src, /await refreshFindingEditBase\(fid\)/);
  // The server's version becomes the new base, so the next Retry is accepted.
  assert.match(src, /findingEditBases\.set\(id, Number\(fresh\.updatedTs\) \|\| 0\)/);
});
