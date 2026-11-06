package control

import (
	"os/exec"
	"testing"
)

// The decoded pane's shape decision (pretty vs raw, and when to offer the
// toggle) is pure logic, so it runs under node like the other UI models.
func TestCodecDecodedShapeModel(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if out, err := exec.Command("node", "--test", "ui/_js-tests/codec-view-model.test.mjs").CombinedOutput(); err != nil {
		t.Fatalf("codec-view-model.test.mjs failed:\n%s", out)
	}
}

// The retention panel must dry-run before it deletes; the guard is behavioural
// source analysis, so it runs under node with the other UI models.
func TestRetentionPrunePreviewModel(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if out, err := exec.Command("node", "--test", "ui/_js-tests/prune-preview.test.mjs").CombinedOutput(); err != nil {
		t.Fatalf("prune-preview.test.mjs failed:\n%s", out)
	}
}
