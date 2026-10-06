// shortcuts-table.js — the static table of every keyboard binding that predates
// the keys.js registry (app.js, proxy.js, tools.js document handlers). The
// shortcut cheatsheet merges it with registry bindings, so the sheet cannot
// drift from the keys that exist: ui_cmdk_test.go greps each `probe` in its
// `source` file and fails when a handler is added, renamed or removed without
// updating this table.
//
// Pure data, no imports; runs under node.
//
// keys: display form. Chords are space separated ("g p"), combinations use "+".
// scope: 'global' or the panel name that must be active.

const L = (group, scope, keys, label, source, probe) => ({ group, scope, keys, label, source, probe });

const GO = [
  ['p', 'Proxy'], ['i', 'Intercept'], ['r', 'Repeater'], ['u', 'Intruder'], ['s', 'Scanner'],
  ['m', 'Map'], ['f', 'Findings'], ['n', 'Notes'], ['a', 'Activity'], ['t', 'Settings'],
];

export const LEGACY_SHORTCUTS = [
  L('Navigation', 'global', 'Ctrl+K', 'Command palette: jump anywhere, run anything', 'app.js', "isModShortcut(e,'k')"),
  L('Navigation', 'global', '?', 'Keyboard shortcut sheet', 'app.js', 'isHelpShortcut(e)'),
  ...GO.map(([k, name]) => L('Navigation', 'global', 'g ' + k, 'Go to ' + name, 'app.js', `${k}:'${name.toLowerCase()}'`)),
  L('Navigation', 'global', 'Up / Down / Home / End', 'Move between rail tabs', 'app.js', "e.key==='Home'"),
  L('Navigation', 'proxy', '/', 'Focus the History search', 'app.js', "isPlainShortcut(e,'/')"),
  L('Navigation', 'proxy', 'Ctrl+F', 'Find inside the inspected request or response', 'proxy.js', "e.key.toLowerCase()==='f'"),
  L('History', 'proxy', 'j / k', 'Walk rows (or Up / Down)', 'app.js', "e.key!=='j'"),
  L('History', 'proxy', 'Ctrl+Shift+A', 'Select or clear all shown flows', 'app.js', "e.key.toLowerCase()==='a'"),
  L('History', 'proxy', 'x', 'Toggle selection on the current flow', 'app.js', "isPlainShortcut(e,'x')"),
  L('Send and replay', 'proxy', 'r', 'Send the selected flow to Repeater', 'app.js', "isPlainShortcut(e,'r')"),
  L('Send and replay', 'proxy', 'i', 'Send the selected flow to Intruder', 'app.js', "isPlainShortcut(e,'i')"),
  L('Send and replay', 'proxy', 'Ctrl+R', 'Send the selected flow to Repeater', 'app.js', "isModShortcut(e,'r')"),
  L('Send and replay', 'proxy', 'Ctrl+I', 'Send the selected flow to Intruder', 'app.js', "isModShortcut(e,'i')"),
  L('Send and replay', 'proxy', 'c', 'Copy the selected flow as cURL', 'app.js', "isPlainShortcut(e,'c')"),
  L('Send and replay', 'proxy', 'a', 'Add the selected flow(s) to a finding', 'app.js', "isPlainShortcut(e,'a')"),
  L('Send and replay', 'repeater', 'Ctrl+Enter', 'Send the current Repeater request', 'app.js', "activePanel()==='repeater'"),
  L('Send and replay', 'repeater', 'Ctrl+Space', 'Send the current Repeater request', 'app.js', 'isModSpace(e)'),
  L('Send and replay', 'intruder', 'Ctrl+Enter', 'Start the Intruder attack', 'app.js', "activePanel()==='intruder'"),
  L('Send and replay', 'intruder', 'Alt+M', 'Wrap the selected text in section-sign markers', 'tools.js', "e.code!=='KeyM'"),
  L('Intercept', 'intercept', 'f', 'Forward the selected held request (outside editors)', 'app.js', "isPlainShortcut(e,'f')"),
  L('Intercept', 'intercept', 'd', 'Drop the selected held request (outside editors)', 'app.js', "isPlainShortcut(e,'d')"),
  L('General', 'global', 'Esc', 'Close dialog, then context menu, then collapse the inspector', 'app.js', "e.key==='Escape'"),
];

// The scope names the registry dispatcher offers for the active panel are the
// panel ids (proxy, intercept, repeater, ...); 'drawer' while focus is inside
// the Flow Drawer. Registrations use the same names.
export const KNOWN_SCOPES = ['global', 'proxy', 'intercept', 'repeater', 'intruder', 'scanner', 'findings', 'map', 'notes', 'activity', 'settings', 'drawer'];

// parseDisplayKeys splits a display string into kbd groups for rendering:
// "g p" -> [['g'], ['p']] (chord); "Ctrl+Shift+A" -> [['Ctrl','Shift','A']].
export function parseDisplayKeys(keys) {
  return String(keys).split(' / ').map((alt) => alt.trim().split(/\s+/).map((step) => step.split('+').map((k) => (k === 'Ctrl' ? 'Ctrl/⌘' : k))));
}

// registryKeysToDisplay: keys.js spec ("Mod+Shift+K", "y c") -> display form.
export function registryKeysToDisplay(spec) {
  return String(spec).replace(/\bMod\b/g, 'Ctrl').replace(/\bCtrl\b/gi, 'Ctrl');
}
