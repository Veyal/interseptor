package rules

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"strings"
	"testing"
)

func writeCheckDir(t *testing.T, root string) {
	t.Helper()
	mkdir := func(p string) { must(t, os.MkdirAll(p, 0o755)) }
	mustWrite := func(p, body string) { must(t, os.WriteFile(p, []byte(body), 0o644)) }
	mkdir(root + "/checks")
	mkdir(root + "/active-checks")
	mustWrite(root+"/checks/hsts.star", "# name: HSTS\ndef check(flow):\n    return []\n")
	mustWrite(root+"/checks/jwt.star", "def check(flow):\n    return []\n")
	mustWrite(root+"/active-checks/sqli.star", "def check(point, baseline, probe):\n    return []\n")
}

func TestBuildAndReadPackRoundTrip(t *testing.T) {
	src := t.TempDir()
	writeCheckDir(t, src)

	var buf bytes.Buffer
	m, err := BuildPack(src, Manifest{Name: "owasp-top", Version: "1.0.0", Author: "Priya"}, &buf)
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if len(m.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(m.Entries))
	}

	got, files, err := ReadPack(&buf)
	if err != nil {
		t.Fatalf("ReadPack: %v", err)
	}
	if got.Name != "owasp-top" || got.Version != "1.0.0" {
		t.Fatalf("manifest identity wrong: %+v", got)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
}

func TestBuildPackRequiresName(t *testing.T) {
	src := t.TempDir()
	writeCheckDir(t, src)
	var buf bytes.Buffer
	if _, err := BuildPack(src, Manifest{}, &buf); err == nil {
		t.Fatal("BuildPack without a name must fail")
	}
}

func TestReadPackRejectsTamperedFile(t *testing.T) {
	src := t.TempDir()
	writeCheckDir(t, src)
	var buf bytes.Buffer
	if _, err := BuildPack(src, Manifest{Name: "p", Version: "1"}, &buf); err != nil {
		t.Fatal(err)
	}
	// Rewrite the pack with one file's contents altered (sha256 now mismatches).
	tampered := tamperMember(t, buf.Bytes(), "checks/hsts.star", []byte("def check(flow):\n    return [finding('high','tampered')]\n"))
	if _, _, err := ReadPack(bytes.NewReader(tampered)); err == nil {
		t.Fatal("ReadPack must reject a pack whose file hash no longer matches the manifest")
	}
}

func TestReadPackRejectsMissingManifest(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "checks/x.star", Size: 4})
	tw.Write([]byte("x=1\n"))
	tw.Close()
	gz.Close()
	if _, _, err := ReadPack(&buf); err == nil || !strings.Contains(err.Error(), "no manifest") {
		t.Fatalf("expected missing-manifest error, got %v", err)
	}
}

// tamperMember rebuilds the gzip+tar with one member's bytes replaced, leaving
// the manifest untouched — so the integrity check is what catches the swap.
func tamperMember(t *testing.T, pack []byte, target string, replacement []byte) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(pack))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	type member struct{ name string; data []byte }
	var members []member
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		data := make([]byte, h.Size)
		_, _ = tr.Read(data)
		members = append(members, member{h.Name, data})
	}
	var out bytes.Buffer
	gzw := gzip.NewWriter(&out)
	tw := tar.NewWriter(gzw)
	for _, m := range members {
		data := m.data
		if m.name == target {
			data = replacement
		}
		tw.WriteHeader(&tar.Header{Name: m.name, Mode: 0o644, Size: int64(len(data))})
		tw.Write(data)
	}
	tw.Close()
	gzw.Close()
	return out.Bytes()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
