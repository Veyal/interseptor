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

const L = (group, scope, keys, label, source, probe, extra) => ({ group, scope, keys, label, source, probe, ...extra });

const GO = [
  ['p', 'Proxy'], ['i', 'Intercept'], ['r', 'Repeater'], ['u', 'Intruder'], ['c', 'Collections'], ['s', 'Scanner'],
  ['m', 'Map'], ['f', 'Findings'], ['n', 'Notes'], ['a', 'Activity'], ['t', 'Settings'],
];

export const LEGACY_SHORTCUTS = [
  L('Navigation', 'global', 'Ctrl+K', 'Command palette: jump anywhere, run anything', 'app.js', "isModShortcut(e,'k')"),
  L('Navigation', 'global', 'Ctrl+J', 'Open the Decoder "Convert" drawer', 'app.js', "isModShortcut(e,'j')"),
  L('Navigation', 'global', '?', 'Keyboard shortcut sheet', 'app.js', 'isHelpShortcut(e)'),
  ...GO.map(([k, name]) => L('Navigation', 'global', 'g ' + k, 'Go to ' + name, 'app.js', `${k}:'${name.toLowerCase()}'`)),
  L('Navigation', 'global', 'Up / Down / Home / End', 'Move between rail tabs', 'app.js', "e.key==='Home'"),
  L('Navigation', 'proxy', '/', 'Focus the History search', 'app.js', "isPlainShortcut(e,'/')"),
  L('Navigation', 'proxy', 'Ctrl+F', 'Find inside the inspected request or response', 'proxy.js', "e.key.toLowerCase()==='f'"),
  L('History', 'proxy', 'j / k', 'Walk rows (or Up / Down)', 'app.js', "e.key!=='j'"),
  L('History', 'proxy', 'Ctrl+Shift+A', 'Select or clear all shown flows', 'app.js', "e.key.toLowerCase()==='a'"),
  L('History', 'proxy', 'x', 'Toggle selection on the current flow', 'app.js', "isPlainShortcut(e,'x')"),
  L('History', 'proxy', 'Ctrl+Alt+P / Space', 'Pause or resume the live history stream (capture continues)', 'app.js', 'toggleLiveFreeze()'),
  L('Send and replay', 'proxy', 'r', 'Send the selected flow to Repeater', 'app.js', "isPlainShortcut(e,'r')"),
  L('Send and replay', 'proxy', 'i', 'Send the selected flow to Intruder', 'app.js', "isPlainShortcut(e,'i')"),
  L('Send and replay', 'proxy', 'Ctrl+R', 'Send the selected flow to Repeater', 'app.js', "isModShortcut(e,'r')"),
  L('Send and replay', 'proxy', 'c', 'Copy the selected flow as cURL', 'app.js', "isPlainShortcut(e,'c')"),
  L('Send and replay', 'proxy', 'a', 'Add the selected flow(s) to a finding', 'app.js', "isPlainShortcut(e,'a')"),
  L('Send and replay', 'repeater', 'Ctrl+Enter', 'Send the current Repeater request', 'app.js', "activePanel()==='repeater'"),
  L('Send and replay', 'repeater', 'Ctrl+Space', 'Send the current Repeater request', 'app.js', 'isModSpace(e)', { notMac: true }),
  L('Send and replay', 'intruder', 'Ctrl+Enter', 'Start the Intruder attack', 'app.js', "activePanel()==='intruder'"),
  L('Send and replay', 'intruder', 'Alt+M', 'Wrap the selected text in section-sign markers', 'tools.js', "e.code!=='KeyM'"),
  L('Intercept', 'intercept', 'f', 'Forward the selected held request (outside editors)', 'app.js', "isPlainShortcut(e,'f')"),
  L('Intercept', 'intercept', 'd', 'Drop the selected held request (outside editors)', 'app.js', "isPlainShortcut(e,'d')"),
  L('Intercept', 'intercept', 'Ctrl+Enter', 'Forward the held request (works in the editor)', 'app.js', "isModShortcut(e,'Enter')"),
  L('Intercept', 'intercept', 'Ctrl+Shift+D', 'Drop the held request (works in the editor)', 'app.js', "isModShiftShortcut(e,'d')"),
  L('General', 'global', 'Esc', 'Close dialog, then context menu, then collapse the inspector', 'app.js', "e.key==='Escape'"),
];

// legacyShortcutsFor(mac) is the table for one platform. Cmd+Space is Spotlight
// on macOS and never reaches the page, so the sheet must not advertise it there;
// left undefined (platform unknown) every row stays.
export function legacyShortcutsFor(mac) {
  return mac ? LEGACY_SHORTCUTS.filter((s) => !s.notMac) : LEGACY_SHORTCUTS;
}

// platformHints: the two static hints that name a modifier or a key, worded for
// the platform (a Mac has Cmd and no usable Shift+F10 context-menu key).
export function platformHints(mac) {
  return {
    singleKey: 'Single-key shortcuts (letters without ' + (mac ? '⌘' : 'Ctrl') + ')',
    pickerKeys: mac
      ? 'Arrow keys move, Enter opens, right-click or F2 moves to a folder'
      : 'Arrow keys move, Enter opens, Shift+F10 moves to a folder',
  };
}

// The scope names the registry dispatcher offers for the active panel are the
// panel ids (proxy, intercept, repeater, ...); 'drawer' while focus is inside
// the Flow Drawer. Registrations use the same names.
export const KNOWN_SCOPES = ['global', 'proxy', 'intercept', 'repeater', 'intruder', 'scanner', 'findings', 'map', 'notes', 'activity', 'settings', 'drawer'];

// parseDisplayKeys splits a display string into kbd groups for rendering:
// "g p" -> [['g'], ['p']] (chord); "Ctrl+Shift+A" -> [['Ctrl','Shift','A']].
// `mac` (true/false) picks platform labels: ⌘ and ⌥ on macOS, Ctrl and Alt
// elsewhere; left undefined the sheet keeps the both-platforms "Ctrl/⌘" label.
export function parseDisplayKeys(keys, mac) {
  const label = (k) => {
    if (k === 'Ctrl') return mac === undefined ? 'Ctrl/⌘' : (mac ? '⌘' : 'Ctrl');
    if (k === 'Alt' && mac) return '⌥';
    return k;
  };
  return String(keys).split(' / ').map((alt) => alt.trim().split(/\s+/).map((step) => step.split('+').map(label)));
}

// registryKeysToDisplay: keys.js spec ("Mod+Shift+K", "y c") -> display form.
export function registryKeysToDisplay(spec) {
  return String(spec).replace(/\bMod\b/g, 'Ctrl').replace(/\bCtrl\b/gi, 'Ctrl');
}
