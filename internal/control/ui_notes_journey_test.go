package control

import (
	"os/exec"
	"strings"
	"testing"
)

func TestUINotesLatestViewSurvivesPendingSave(t *testing.T) {
	src := readUIAsset(t, "js/notes.js")
	start := strings.Index(src, "function applyNotesMode(")
	if start < 0 {
		t.Fatal("Notes view controls missing")
	}
	end := strings.Index(src[start:], "let notesPreviewCache=")
	if end < 0 {
		t.Fatal("Notes view controls missing")
	}
	script := `const buttons=['view','edit'].map(m=>({dataset:{m},classList:{toggle(){}},setAttribute(){},onclick:null}));
const cls=()=>{const s=new Set(['u-hidden']);return {s,toggle(n,on){on?s.add(n):s.delete(n)},has:n=>s.has(n)}};
const editor={value:'Generic unsaved draft',classList:cls(),focus(){},setSelectionRange(){}},preview={classList:cls()},notesState={mode:'edit',chosen:false};
editor.classList.s.clear();
const toggleMode=m=>m==='edit'?'view':'edit';
const $=id=>id==='#notesSeg'?{querySelectorAll:()=>buttons}:id==='#notesEdit'?editor:preview;
let acknowledge;const pending=new Promise(resolve=>acknowledge=resolve);
let renderedDraft,flushes=0;const flushNotesSave=()=>{flushes++;return pending},showNotesPreview=()=>{renderedDraft=editor.value};
` + src[start:start+end] + `
(async()=>{
buttons[0].onclick();
if(notesState.mode!=='view'||!editor.classList.has('u-hidden')||preview.classList.has('u-hidden')||renderedDraft!==editor.value)throw new Error('View waited for persistence instead of rendering the local draft');
if(flushes!==1)throw new Error('switching to View must flush pending edits');
buttons[1].onclick();
acknowledge();await pending;
if(notesState.mode!=='edit'||editor.classList.has('u-hidden')||!preview.classList.has('u-hidden'))throw new Error('pending View save overrode the newer Edit selection');
if(!notesState.chosen)throw new Error('a user choice must be remembered');
toggleNotesMode();
if(notesState.mode!=='view')throw new Error('toggle did not switch to View');
})().catch(e=>{console.error(e);process.exitCode=1});`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("Notes view ownership: %v\n%s", err, out)
	}
}

func TestUINotesViewEditContract(t *testing.T) {
	notes := executableJS(readUIAsset(t, "js/notes.js"))
	requireUIContains(t, notes,
		"from './notes-mode-model.js'",
		"resolveLoadMode({current:notesState.mode,userChosen:notesState.chosen",
		"addEventListener('dblclick'",
		"closest('a,button,summary,input,.note-chip')",
		"export function toggleNotesMode()",
	)
	for _, gone := range []string{"promoteNoteSelection", "notesPromote", "notesPanelWide", "notes-wide", "'preview'"} {
		if strings.Contains(notes, gone) {
			t.Errorf("notes.js still carries removed Preview/Promote code: %q", gone)
		}
	}
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `data-m="view"`, `data-m="edit"`, `aria-label="Notes mode"`)
	if strings.Contains(index, "notesPromote") || strings.Contains(index, "Promote to finding") {
		t.Error("index.html must not offer Promote to finding in Notes")
	}
	requireUIContains(t, readUIAsset(t, "js/app.js"), "activePanel()==='notes'&&isModShortcut(e,'e')", "toggleNotesMode()")
}

// Typing must pin the current mode: the autosave echo reloads notes, and an
// unpinned reload of a non-empty note would flip Edit to View mid-typing.
func TestUINotesTypingPinsModeAgainstAutosaveReload(t *testing.T) {
	src := readUIAsset(t, "js/notes.js")
	start := strings.Index(src, "export function scheduleNotesSave(){")
	if start < 0 {
		t.Fatal("scheduleNotesSave missing")
	}
	end := strings.Index(src[start:], "\n}\n")
	if end < 0 || !strings.Contains(src[start:start+end], "notesState.chosen=true") {
		t.Fatal("scheduleNotesSave must mark the mode as chosen so a save echo cannot switch Edit to View")
	}
}
