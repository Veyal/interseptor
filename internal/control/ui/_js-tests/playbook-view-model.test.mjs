import test from 'node:test';
import assert from 'node:assert/strict';
import {
  playbookRowModel, filterPlaybooks, coverageModel, mcpPrompt, getPlaybookCall, markdownSections,
} from '../js/playbook-view-model.js';

test('a bundled playbook reads as trusted, a project one as untrusted', () => {
  const bundled = playbookRowModel({ id: 'ssrf', title: 'SSRF', trusted: true, severityHint: 'High', appliesTo: ['param:url'] });
  assert.equal(bundled.trust, 'bundled');
  assert.equal(bundled.untrusted, false);
  assert.equal(bundled.severity, 'High');

  const project = playbookRowModel({ id: 'house', title: 'House', trusted: false });
  assert.equal(project.untrusted, true);
  assert.match(project.trust, /untrusted/);
});

test('filtering matches id, title and applies_to signals', () => {
  const list = [
    { id: 'object-authz', title: 'Object-level authorization', appliesTo: ['param:id'] },
    { id: 'ssrf-url-params', title: 'SSRF through URL parameters', appliesTo: ['param:url'] },
    { id: 'graphql-authz', title: 'GraphQL authorization', appliesTo: ['path:/graphql'] },
  ];
  assert.equal(filterPlaybooks(list, '').length, 3);
  assert.deepEqual(filterPlaybooks(list, 'ssrf').map((p) => p.id), ['ssrf-url-params']);
  assert.deepEqual(filterPlaybooks(list, '/graphql').map((p) => p.id), ['graphql-authz']);
  assert.deepEqual(filterPlaybooks(list, 'url').map((p) => p.id), ['ssrf-url-params']);
  assert.equal(filterPlaybooks(list, 'nothing-here').length, 0);
});

test('coverage reads as a rollup, and absence reads as not tested', () => {
  assert.equal(coverageModel(null).tested, false);
  const m = coverageModel({ run: 2, found: 1, dismissed: 1, considered: 0 });
  assert.equal(m.tested, true);
  assert.match(m.text, /2 run/);
  assert.match(m.text, /1 found/);
});

test('the copyable invocation is the prompt and the tool call', () => {
  assert.equal(mcpPrompt('ssrf-url-params'), '/playbook-ssrf-url-params');
  assert.equal(getPlaybookCall('ssrf-url-params'), 'get_playbook {"id":"ssrf-url-params"}');
});

test('markdown sections split headings, lists, quotes and code', () => {
  const blocks = markdownSections('# Title\n\nIntro line\n\n## Do it\n\n- first\n- second\n\n> UNTRUSTED note\n\n```\nx = 1\n```\n');
  assert.deepEqual(blocks.map((b) => b.kind), ['h1', 'p', 'h2', 'list', 'list', 'quote', 'code']);
  assert.equal(blocks[0].text, 'Title');
  assert.equal(blocks[3].text, 'first');
  assert.equal(blocks[6].text, 'x = 1');
});
