import test from 'node:test';
import assert from 'node:assert/strict';
import { flowPreviewPngURL, activeFlowTheme, copyFailureMessage } from '../js/copy-image.js';

test('flowPreviewPngURL defaults suit pasting', () => {
  assert.equal(flowPreviewPngURL(123), '/api/flows/123/preview.png?side=both&pretty=1&layout=vertical&theme=light');
});
test('flowPreviewPngURL honours valid options and rejects junk', () => {
  assert.match(flowPreviewPngURL(7, { theme: 'dark', side: 'res', layout: 'horizontal', pretty: 0 }), /side=res&pretty=0&layout=horizontal&theme=dark$/);
  assert.match(flowPreviewPngURL(7, { theme: 'x&y', side: '../' }), /side=both&pretty=1&layout=vertical&theme=light$/);
  assert.equal(flowPreviewPngURL('1/../../x').startsWith('/api/flows/0/'), true);
});
test('activeFlowTheme maps light to light, dark and hc to dark', () => {
  const root = (t) => ({ getAttribute: () => t });
  assert.equal(activeFlowTheme(root('light')), 'light');
  assert.equal(activeFlowTheme(root('hc')), 'dark');
  assert.equal(activeFlowTheme(root(null)), 'dark');
  assert.equal(activeFlowTheme(null), 'dark');
});
test('insecure message names cause and remedy briefly', () => {
  const m = copyFailureMessage('insecure');
  assert.match(m, /HTTPS or localhost/);
  assert.match(m, /serve the UI over HTTPS/);
  assert.ok(m.length <= 120);
});
