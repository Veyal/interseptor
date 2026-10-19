import test from 'node:test';
import assert from 'node:assert/strict';
import { previewGate, previewStatusText, parseVector } from '../js/cvss-model.js';

const FULL = 'CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N';

test('a partial v4 vector is answered locally instead of posting a 400 per keystroke', () => {
  for (const partial of ['CVSS:4.0', 'CVSS:4.0/AV:N', 'CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N']) {
    const g = previewGate(partial);
    assert.equal(g.send, false, partial);
    assert.match(g.message, /of 11 base metrics set/);
  }
  assert.equal(previewGate('').send, false);
});

test('a complete vector, and a legacy 3.1 vector the server explains, are sent', () => {
  assert.equal(parseVector(FULL).valid, true);
  assert.equal(previewGate(FULL).send, true);
  assert.equal(previewGate('CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H').send, true);
});

test('preview text keeps the explanation and the severity note as separate sentences', () => {
  const r = { score: 9.3, rating: 'CRITICAL', nomenclature: 'CVSS-B', severity: 'Critical', explanation: 'Network reachable, no availability impact' };
  const t = previewStatusText(r, 'Medium');
  assert.equal(t, 'Score 9.3 · Critical (CVSS-B) — Network reachable, no availability impact. Current severity Medium differs; Apply sets Critical.');
  assert.equal(previewStatusText({ ...r, explanation: '' }, 'critical'), 'Score 9.3 · Critical (CVSS-B).');
});
