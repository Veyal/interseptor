import test from 'node:test';
import assert from 'node:assert/strict';
import { importCommitMessage } from '../js/collections-model.js';

test('import toast mentions quarantine only when the file carried scripts', () => {
  const noScripts = { scriptsQuarantined: false, stats: { collectionsAdded: 1, itemsAdded: 4 } };
  assert.equal(importCommitMessage(noScripts), 'Imported 4 items.');
  const withScripts = { scriptsQuarantined: true, stats: { collectionsAdded: 1, itemsAdded: 1 } };
  assert.equal(importCommitMessage(withScripts), 'Imported 1 item. Scripts are quarantined until you approve them.');
});

test('a re-import that adds nothing says so instead of claiming an import', () => {
  const dup = { scriptsQuarantined: false, stats: { collectionsSkipped: 1, itemsAdded: 0, itemsSkipped: 3 } };
  assert.equal(importCommitMessage(dup), 'Nothing new: 3 items were already in the collection.');
  const mixed = { scriptsQuarantined: false, stats: { itemsAdded: 2, itemsUpdated: 1, itemsSkipped: 1 } };
  assert.equal(importCommitMessage(mixed), 'Imported 2 items, updated 1, 1 already present.');
});

test('an environment-only import names what it added', () => {
  const env = { scriptsQuarantined: false, stats: { environmentsAdded: 1, variablesAdded: 2 } };
  assert.equal(importCommitMessage(env), 'Imported 1 environment.');
  assert.equal(importCommitMessage(null), 'Imported.');
});
