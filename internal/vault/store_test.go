package vault

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func writeMiniZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("interceptor.db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("SQLite format 3\x00fake")); err != nil {
		t.Fatal(err)
	}
	bw, err := zw.Create("bodies/ab/cd")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bw.Write([]byte("blob")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPutListPrune(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	z := writeMiniZip(t)
	for i := 0; i < 4; i++ {
		if _, err := st.Put("acme", "lab", "host", bytes.NewReader(z)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := st.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	if list[0].Revisions != 2 || list[0].LatestRev != 4 {
		t.Fatalf("got %+v", list[0])
	}
	revs, err := st.Revisions("acme")
	if err != nil || len(revs) != 2 {
		t.Fatalf("revs=%v err=%v", revs, err)
	}
	rc, info, err := st.OpenRev("acme", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if info.Rev != 4 || len(b) == 0 {
		t.Fatalf("open latest=%+v len=%d", info, len(b))
	}
}

func TestRejectBadZip(t *testing.T) {
	st, err := Open(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put("x", "", "", bytes.NewReader([]byte("not-a-zip"))); err == nil {
		t.Fatal("expected error")
	}
}

func TestInvalidSlug(t *testing.T) {
	st, err := Open(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put("../evil", "", "", bytes.NewReader(writeMiniZip(t))); err == nil {
		t.Fatal("expected invalid slug")
	}
}

func TestAuthBootstrap(t *testing.T) {
	dir := t.TempDir()
	a, err := OpenAuth(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, created, err := a.EnsureBootstrap()
	if err != nil || !created || raw == "" {
		t.Fatalf("bootstrap raw=%q created=%v err=%v", raw, created, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "vault.token"))
	if err != nil || !bytes.Contains(b, []byte(raw)) {
		t.Fatalf("vault.token=%q err=%v", b, err)
	}
	scope, err := a.Check("Bearer " + raw)
	if err != nil || scope != ScopeFull {
		t.Fatalf("check scope=%v err=%v", scope, err)
	}
	_, created2, err := a.EnsureBootstrap()
	if err != nil || created2 {
		t.Fatal("second bootstrap should not create")
	}
}
