// The History search box mirrors internal/store/flows_search_terms.go: "-term"
// excludes. Keep this table in step with the Go case table so a rule change in one
// language fails a test in the other.
import test from 'node:test';
import assert from 'node:assert/strict';
import { splitSearchTerms, withoutSearchTerm, onlyExcludedTerms } from '../js/proxy-filters.js';

const cases = [
  ['', '', []],
  ['jpg', 'jpg', []],
  ['-.jpg', '', ['.jpg']],
  ['api -.jpg -analytics', 'api', ['.jpg', 'analytics']],
  ['"content type"', 'content type', []],
  ['-"content type"', '', ['content type']],
  ['-', '-', []],
  ['-Analytics', '', ['analytics']],
  ['-a -a', '', ['a']],
  ['  api   users ', 'api users', []],
  ['content-type', 'content-type', []],
  ['--x-requested-with', '', ['-x-requested-with']],
  ['"foo bar', 'foo bar', []],
  ['-b -a', '', ['b', 'a']],
  ['api\t-.png\n-analytics', 'api', ['.png', 'analytics']],
];

test('splitSearchTerms separates excluded terms from the positive query', () => {
  for (const [input, positive, exclude] of cases) {
    assert.deepEqual(splitSearchTerms(input), { positive, exclude }, `input ${JSON.stringify(input)}`);
  }
});

test('withoutSearchTerm removes one exclusion and leaves the rest as typed', () => {
  assert.equal(withoutSearchTerm('api -.jpg -analytics', 1), 'api -.jpg');
  assert.equal(withoutSearchTerm('api -.jpg -analytics', 0), 'api -analytics');
  assert.equal(withoutSearchTerm('"exact phrase" -"content type" -.jpg', 0), '"exact phrase" -.jpg');
  // Every copy of the removed term goes, so the chip cannot survive its own click.
  assert.equal(withoutSearchTerm('api -.jpg -.jpg -pdf', 0), 'api -pdf');
  // An index with no term behind it is a no-op, not a lost search.
  assert.equal(withoutSearchTerm('api -.jpg', 4), 'api -.jpg');
});

test('onlyExcludedTerms keeps the exclusions when the query chip is cleared', () => {
  assert.equal(onlyExcludedTerms('api -.jpg -"content type"'), '-.jpg -"content type"');
  assert.equal(onlyExcludedTerms('api'), '');
  assert.equal(onlyExcludedTerms(''), '');
});
