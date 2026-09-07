package control

import (
	"os/exec"
	"strings"
	"testing"
)

func TestUINotesLatestViewSurvivesPendingSave(t *testing.T) {
	src := readUIAsset(t, "js/notes.js")
	start := strings.Index(src, "$('#notesSeg')&&$('#notesSeg').querySelectorAll")
	if start < 0 {
		t.Fatal("Notes view controls missing")
	}
	end := strings.Index(src[start:], "let notesPreviewCache=")
	if end < 0 {
		t.Fatal("Notes view controls missing")
	}
	script := `const buttons=['edit','preview'].map(m=>({dataset:{m},classList:{toggle(){}},setAttribute(){}}));
	const editor={value:'Generic unsaved draft',style:{display:'block'}},preview={style:{display:'none'}},notesState={mode:'edit'};
const $=id=>id==='#notesSeg'?{querySelectorAll:()=>buttons}:id==='#notesEdit'?editor:preview;
let acknowledge;const pending=new Promise(resolve=>acknowledge=resolve);
let renderedDraft;const flushNotesSave=()=>pending,showNotesPreview=()=>{renderedDraft=editor.value};
` + src[start:start+end] + `
(async()=>{
const changing=buttons[1].onclick();
if(notesState.mode!=='preview'||editor.style.display!=='none'||preview.style.display!=='block'||renderedDraft!==editor.value)throw new Error('Preview waited for persistence instead of rendering the local draft');
await buttons[0].onclick();
acknowledge();await changing;
if(notesState.mode!=='edit'||editor.style.display!=='block'||preview.style.display!=='none')throw new Error('pending Preview save overrode the newer Edit selection');
})().catch(e=>{console.error(e);process.exitCode=1});`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("Notes view ownership: %v\n%s", err, out)
	}
}
