// desktop-panel-map-model.js — pure rules for the Map panel's desktop toolbar.
// No imports and no DOM access, so it runs under node.

// activeFilterCount counts the filters that differ from the panel's defaults
// (hide 403/404 on, collapse identical on, search in path/host, everything else
// empty): the number shown on the Filters button.
export function activeFilterCount(v) {
  let n = 0;
  if (v.domain) n++;
  if (v.method) n++;
  if (v.status) n++;
  if (v.tag) n++;
  if (v.searchScope && v.searchScope !== 'path') n++;
  if (v.hideNoise === false) n++;
  if (v.collapseIdentical === false) n++;
  if (v.onlyUnlinked) n++;
  return n;
}
