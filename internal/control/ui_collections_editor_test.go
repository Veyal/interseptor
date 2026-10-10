package control

import (
	"io/fs"
	"strings"
	"testing"
)

// Static guards for the "do not lose the user's work" behaviour of the
// Collections editor: Send must go through the sent-over safety net, an
// unsaved edit must be visible and revertible, and delete must offer an
// honest in-session Undo.
func TestCollectionsEditorKeepsWorkRecoverable(t *testing.T) {
	read := func(name string) string {
		b, err := fs.ReadFile(uiFS, "ui/"+name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	editor, resp := read("js/collections-editor.js"), read("js/collections-response.js")

	if strings.Contains(resp, "saveCurrent") {
		t.Error("Send must save through saveForSend so the replaced stored version is kept, not call saveCurrent directly")
	}
	if !strings.Contains(resp, "saveForSend") {
		t.Error("collections-response.js lost the saveForSend call before Send")
	}
	for _, want := range []string{
		"Unsaved changes: differs from the stored request",
		"Matches the stored request",
		"Restore previous version",
		"is lost if you reload",
		"This request changed elsewhere", // the 409 conflict path must survive
		"id = 'collRevert'",
	} {
		if !strings.Contains(editor, want) {
			t.Errorf("collections-editor.js lost %q", want)
		}
	}
	for _, f := range []string{"js/collections-editor.js", "js/collections-safety.js"} {
		src := read(f)
		for _, bad := range []string{".outerHTML =", "insertAdjacentHTML"} {
			if strings.Contains(src, bad) {
				t.Errorf("%s uses %s", f, bad)
			}
		}
	}
}
