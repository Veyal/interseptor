package control

import (
	"strings"
	"testing"
)

// WCAG 2.1.4: with the "Single-key shortcuts" switch off, the app.js global
// handler must not act on a bare character key (g-chords, r/i/c/a/x, f/d,
// /, ?), and the History walker must ignore j/k. The gate has to come before
// every plain-letter branch of the handler.
func TestUIGlobalSingleKeyShortcutsHonourTheSwitch(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app,
		"import { singleKeyShortcutsOn } from './keyboard.js'",
		"function isBareCharacterKey(e){return e.key.length===1&&e.key!==' '&&!e.ctrlKey&&!e.metaKey&&!e.altKey;}",
		"if((e.key==='j'||e.key==='k')&&!singleKeyShortcutsOn())return;",
	)
	gate := strings.Index(app, "if(isBareCharacterKey(e)&&!singleKeyShortcutsOn()){resetGoto();return;}")
	if gate < 0 {
		t.Fatal("global handler has no single-key gate")
	}
	for _, branch := range []string{
		"isPlainShortcut(e,'f')||isPlainShortcut(e,'d')",
		"if(isHelpShortcut(e))",
		"GO_MNEMONICS[e.key.toLowerCase()]",
		"if(isPlainShortcut(e,'g'))",
		"isPlainShortcut(e,'x')",
		"isPlainShortcut(e,'/')",
	} {
		at := strings.Index(app, branch)
		if at < 0 {
			t.Errorf("handler branch %q not found", branch)
		} else if at < gate {
			t.Errorf("handler branch %q runs before the single-key gate", branch)
		}
	}
}
