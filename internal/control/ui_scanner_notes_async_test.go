package control

import (
	"regexp"
	"strings"
	"testing"
)

// These contracts protect the two UI surfaces whose results are shared by
// overlapping requests. The embedded UI has no build-time race checker, so
// keep the ownership rule visible in the source and fail loudly if a future
// edit moves an effect outside its request-generation guard.
func TestUIScannerResultsRejectStaleRequests(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/scanner.js"))
	for _, want := range []string{
		"let scanResultsEpoch=0",
		"const resultsEpoch=++scanResultsEpoch",
		"if(resultsEpoch!==scanResultsEpoch)return",
		"if(resultsEpoch===scanResultsEpoch)renderLoadError",
		"if(resultsEpoch===scanResultsEpoch&&stateEl",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("scanner async result ownership contract missing %q", want)
		}
	}
	if !regexp.MustCompile(`(?s)export async function runScan\(\).*?const resultsEpoch=\+\+scanResultsEpoch`).MatchString(src) {
		t.Error("runScan must claim the shared scanner-results generation")
	}
}

func TestUINotesLoadRejectsStaleRequests(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/notes.js"))
	for _, want := range []string{"let notesLoadGeneration=0", "const loadGeneration=++notesLoadGeneration"} {
		if !strings.Contains(src, want) {
			t.Errorf("notes async load ownership contract missing %q", want)
		}
	}
	if strings.Count(src, "if(loadGeneration!==notesLoadGeneration)return") < 2 {
		t.Error("notes async load ownership contract must guard both success and error effects")
	}
	if !regexp.MustCompile(`(?s)const d=await api\('/api/notes'\);\s*if\(loadGeneration!==notesLoadGeneration\)return`).MatchString(src) {
		t.Error("notes success effects must follow the load-generation guard")
	}
}

func TestUIDecoderRejectsStaleOutputAndTracksPendingState(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/scanner.js"))
	for _, want := range []string{
		"let decRequestEpoch=0",
		"let decModalEpoch=0",
		"const epoch=++decRequestEpoch",
		"const modalEpoch=decModalEpoch",
		"function decCurrent(epoch,modalEpoch,input)",
		"if(!decCurrent(epoch,modalEpoch,input))return",
		"setAttribute('aria-busy',on?'true':'false')",
		"addEventListener('input',()=>decInvalidatePending())",
		"body:JSON.stringify({op,input})",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("decoder async ownership contract missing %q", want)
		}
	}
	if !regexp.MustCompile(`(?s)const epoch=\+\+decRequestEpoch.*?const modalEpoch=decModalEpoch.*?input=\$\('#decIn'\)\.value`).MatchString(src) {
		t.Error("decoder must snapshot the input and modal generation before starting the request")
	}
}
