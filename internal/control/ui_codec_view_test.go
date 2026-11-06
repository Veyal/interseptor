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

// The suppressed-traffic banner's wording is pure logic; it tells "we filtered
// it" apart from "the app sent nothing".
func TestSuppressedBannerModel(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if out, err := exec.Command("node", "--test", "ui/_js-tests/suppressed-model.test.mjs").CombinedOutput(); err != nil {
		t.Fatalf("suppressed-model.test.mjs failed:\n%s", out)
	}
}

// The Playbooks panel's filter, trust badge and coverage wording are pure logic;
// a broken model would mislabel untrusted project content as bundled.
func TestPlaybookViewModel(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if out, err := exec.Command("node", "--test", "ui/_js-tests/playbook-view-model.test.mjs").CombinedOutput(); err != nil {
		t.Fatalf("playbook-view-model.test.mjs failed:\n%s", out)
	}
}
