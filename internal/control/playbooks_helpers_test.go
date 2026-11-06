package control

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func itoaTest(n int64) string { return strconv.FormatInt(n, 10) }

func writeProjectPlaybook(t *testing.T, projectDir, name, src string) {
	t.Helper()
	dir := filepath.Join(projectDir, "playbooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
