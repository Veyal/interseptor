import test from 'node:test';
import assert from 'node:assert/strict';
import {
  playbookRowModel, filterPlaybooks, coverageModel, mcpPrompt, getPlaybookCall,
  markdownBlocks, inlineTokens, playbookFacts, severityClass,
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

test('markdown blocks split headings, paragraphs, quotes, rules and code', () => {
  const blocks = markdownBlocks('# Title\n\nIntro line\nwrapped onto a second line\n\n## Do it\n\n> UNTRUSTED note\n> continues\n\n---\n\n```\nx = 1\n\ny = 2\n```\n\n#### Deep heading ##\n');
  assert.deepEqual(blocks.map((b) => b.kind), ['h1', 'p', 'h2', 'quote', 'hr', 'code', 'h3']);
  assert.equal(blocks[0].text, 'Title');
  // Hard-wrapped source lines are one paragraph, not one paragraph per line.
  assert.equal(blocks[1].text, 'Intro line wrapped onto a second line');
  assert.equal(blocks[3].text, 'UNTRUSTED note continues');
  assert.equal(blocks[5].text, 'x = 1\n\ny = 2');
  assert.equal(blocks[6].text, 'Deep heading');
});

test('numbered steps keep their wrapped lines and nested bullets', () => {
  const md = [
    '1. Find every endpoint that produces a document: CSV export, invoice,',
    '   statement, audit log.',
    '2. Group the fields:',
    '   - privilege: `role`, `isAdmin`',
    '   - ownership: `tenantId`',
    '3. Send again.',
    '',
    'After the list.',
  ].join('\n');
  const blocks = markdownBlocks(md);
  assert.deepEqual(blocks.map((b) => b.kind), ['ol', 'p']);
  const [list] = blocks;
  assert.equal(list.items.length, 3);
  assert.equal(list.items[0].text, 'Find every endpoint that produces a document: CSV export, invoice, statement, audit log.');
  assert.equal(list.items[1].children.length, 1);
  assert.equal(list.items[1].children[0].kind, 'ul');
  assert.deepEqual(list.items[1].children[0].items.map((x) => x.text), ['privilege: `role`, `isAdmin`', 'ownership: `tenantId`']);
  assert.equal(list.items[2].text, 'Send again.');
  assert.equal(blocks[1].text, 'After the list.');
});

test('a bullet list and a numbered list next to each other stay separate', () => {
  const blocks = markdownBlocks('- one\n- two\n1. first\n2. second\n\n- loose\n\n- items\n');
  assert.deepEqual(blocks.map((b) => [b.kind, b.items.length]), [['ul', 2], ['ol', 2], ['ul', 2]]);
});

test('an unterminated code fence and a lone marker do not lose text', () => {
  assert.deepEqual(markdownBlocks('```\nstill here'), [{ kind: 'code', text: 'still here' }]);
  assert.deepEqual(markdownBlocks('* * *'), [{ kind: 'hr' }]);
  assert.deepEqual(markdownBlocks(''), []);
  assert.deepEqual(markdownBlocks(null), []);
});

test('inline tokens mark code, strong and emphasis and leave stray stars alone', () => {
  assert.deepEqual(inlineTokens('Edit **one** field with `role: "admin"` *now*'), [
    { t: 'text', v: 'Edit ' },
    { t: 'strong', children: [{ t: 'text', v: 'one' }] },
    { t: 'text', v: ' field with ' },
    { t: 'code', v: 'role: "admin"' },
    { t: 'text', v: ' ' },
    { t: 'em', children: [{ t: 'text', v: 'now' }] },
  ]);
  assert.deepEqual(inlineTokens('**`kid`** header'), [
    { t: 'strong', children: [{ t: 'code', v: 'kid' }] },
    { t: 'text', v: ' header' },
  ]);
  // Arithmetic and unbalanced markers are text, not emphasis.
  assert.deepEqual(inlineTokens('2 * 3 * 4'), [{ t: 'text', v: '2 * 3 * 4' }]);
  assert.deepEqual(inlineTokens('a ` b'), [{ t: 'text', v: 'a ` b' }]);
  assert.deepEqual(inlineTokens('**open'), [{ t: 'text', v: '**open' }]);
  // Markup characters are data: they come back as text for textContent.
  assert.deepEqual(inlineTokens('<img src=x onerror=alert(1)>'), [{ t: 'text', v: '<img src=x onerror=alert(1)>' }]);
});

test('unbalanced inline markers stay linear', () => {
  const hostile = '*a '.repeat(40000) + '`'.repeat(3) + ' **b '.repeat(40000);
  const t0 = performance.now();
  const tokens = inlineTokens(hostile);
  const ms = performance.now() - t0;
  assert.ok(tokens.length > 0);
  assert.ok(ms < 500, `inlineTokens took ${ms.toFixed(0)} ms on 320 KB of unbalanced markers`);
  const t1 = performance.now();
  markdownBlocks(('- item\n' + '  '.repeat(40) + '- deep\n').repeat(4000));
  assert.ok(performance.now() - t1 < 1500, 'markdownBlocks must stay fast on deeply indented lists');
});

test('facts list only what the frontmatter carries', () => {
  const facts = playbookFacts({ appliesTo: ['path:/export', 'param:tenant'], tools: ['send_request'], evidence: ' One export. ', references: [] });
  assert.deepEqual(facts.map((f) => f.label), ['Applies to', 'Tools', 'Evidence']);
  assert.deepEqual(facts[0].value, ['path:/export', 'param:tenant']);
  assert.equal(facts[2].value, 'One export.');
  assert.deepEqual(playbookFacts({}), []);
  assert.deepEqual(playbookFacts(null), []);
});

test('severity maps to the shared chip class only for known levels', () => {
  assert.equal(severityClass('high'), 'High');
  assert.equal(severityClass(' Critical '), 'Critical');
  assert.equal(severityClass('Sev-1" onclick="x'), '');
  assert.equal(severityClass(undefined), '');
});
