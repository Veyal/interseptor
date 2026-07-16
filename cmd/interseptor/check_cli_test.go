package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckValidateDetectsCompileErrors(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.star")
	os.WriteFile(good, []byte("def check(flow):\n    return []\n"), 0o644)
	bad := filepath.Join(dir, "bad.star")
	os.WriteFile(bad, []byte("def check(flow\n    return []"), 0o644)

	if err := checkValidate([]string{good}, false); err != nil {
		t.Fatalf("good file should validate clean: %v", err)
	}
	if err := checkValidate([]string{bad}, false); err == nil {
		t.Fatal("malformed file should fail validation")
	}
}

func TestCheckValidateActiveUsesActiveEngine(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "ok.star")
	os.WriteFile(f, []byte("def check(point, baseline, probe):\n    return []\n"), 0o644)
	if err := checkValidate([]string{f}, true); err != nil {
		t.Fatalf("active check should validate clean under --active: %v", err)
	}
}

func TestCheckNewRefusesOverwrite(t *testing.T) {
	t.Setenv("INTERSEPTOR_DATA_DIR", t.TempDir())
	if err := checkNew([]string{"mycheck"}, false); err != nil {
		t.Fatalf("first new: %v", err)
	}
	if err := checkNew([]string{"mycheck"}, false); err == nil {
		t.Fatal("second new with same id must refuse overwrite")
	}
}
