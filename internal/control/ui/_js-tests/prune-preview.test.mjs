import test from 'node:test';
import assert from 'node:assert/strict';

// The retention panel must never call the destructive purge without a dry run
// first: a preview that reports exact counts, then the same selection.
const src = await import('node:fs/promises')
  .then((fs) => fs.readFile(new URL('../js/settings.js', import.meta.url), 'utf8'));

test('every purge call site goes through pruneWithPreview', () => {
  const calls = src.match(/\/api\/flows\/purge/g) || [];
  const preview = src.match(/pruneWithPreview\(/g) || [];
  // One dry run + one commit inside the helper, and one per call site outside.
  assert.ok(calls.length >= 2, 'expected the purge endpoint in the helper');
  assert.ok(preview.length >= 4, 'each delete flow should call pruneWithPreview, got ' + preview.length);
  assert.match(src, /dryRun:true/, 'the helper must dry-run before deleting');
});

test('the helper confirms with the exact counts before deleting', () => {
  const start = src.indexOf('export async function pruneWithPreview');
  assert.ok(start > 0);
  const body = src.slice(start, src.indexOf('export async function refreshPrunePending'));
  const previewAt = body.indexOf('dryRun:true');
  const confirmAt = body.indexOf('uiConfirm(');
  const commitAt = body.indexOf('runRetentionMutation(');
  assert.ok(previewAt > 0 && confirmAt > previewAt && commitAt > confirmAt,
    'order must be preview → confirm → commit');
  assert.match(body, /Nothing has been deleted yet/);
});

test('an undo affordance exists and refreshes from the pending list', () => {
  assert.match(src, /refreshPrunePending/);
  assert.match(src, /\/api\/flows\/prune\/pending/);
  assert.match(src, /\/api\/flows\/prune\/undo/);
});
