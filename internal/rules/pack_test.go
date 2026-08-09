package rules

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
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

func TestReadPackRejectsMemberOverExpandedLimit(t *testing.T) {
	// Given
	limits := packReadLimits{entries: 8, fileBytes: 32, totalBytes: 128}
	pack := packWithMembers(t, []member{{name: manifestName, data: []byte(`{"name":"p","entries":[]}`)}, {name: "checks/bomb.star", data: bytes.Repeat([]byte("x"), int(limits.fileBytes+1))}})

	// When
	_, _, err := readPackWithLimits(bytes.NewReader(pack), limits)

	// Then
	if err == nil || !strings.Contains(err.Error(), "expanded size limit") {
		t.Fatalf("ReadPack error=%v, want expanded size limit", err)
	}
}

func TestReadPackAcceptsMemberAtExpandedLimit(t *testing.T) {
	// Given
	limits := packReadLimits{entries: 8, fileBytes: 256, totalBytes: 1024}
	data := bytes.Repeat([]byte("x"), int(limits.fileBytes))
	manifest := Manifest{Name: "p", Entries: []Entry{{Kind: KindPassive, ID: "exact", SHA256: sha256Hex(data)}}}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	pack := packWithMembers(t, []member{{name: manifestName, data: manifestData}, {name: "checks/exact.star", data: data}})

	// When
	_, files, err := readPackWithLimits(bytes.NewReader(pack), limits)

	// Then
	if err != nil || len(files) != 1 || int64(len(files[0].Data)) != limits.fileBytes {
		t.Fatalf("ReadPack files=%d err=%v", len(files), err)
	}
}

func TestReadPackRejectsTooManyMembers(t *testing.T) {
	// Given
	limits := packReadLimits{entries: 4, fileBytes: 64, totalBytes: 256}
	members := []member{{name: manifestName, data: []byte(`{"name":"p","entries":[]}`)}}
	for i := 0; i < limits.entries; i++ {
		members = append(members, member{name: fmt.Sprintf("extra-%d", i), data: []byte("x")})
	}

	// When
	_, _, err := readPackWithLimits(bytes.NewReader(packWithMembers(t, members)), limits)

	// Then
	if err == nil || !strings.Contains(err.Error(), "entry count limit") {
		t.Fatalf("ReadPack error=%v, want entry count limit", err)
	}
}

func TestReadPackRejectsCumulativeExpandedLimit(t *testing.T) {
	// Given
	limits := packReadLimits{entries: 8, fileBytes: 64, totalBytes: 96}
	members := []member{{name: manifestName, data: []byte(`{"name":"p","entries":[]}`)}, {name: "extra-1", data: bytes.Repeat([]byte("x"), 48)}, {name: "extra-2", data: bytes.Repeat([]byte("x"), 48)}}

	// When
	_, _, err := readPackWithLimits(bytes.NewReader(packWithMembers(t, members)), limits)

	// Then
	if err == nil || !strings.Contains(err.Error(), "cumulative expanded size limit") {
		t.Fatalf("ReadPack error=%v, want cumulative expanded size limit", err)
	}
}

type member struct {
	name string
	data []byte
}

func packWithMembers(t *testing.T, members []member) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, item := range members {
		if err := tw.WriteHeader(&tar.Header{Name: item.name, Mode: 0o644, Size: int64(len(item.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(item.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
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
	type member struct {
		name string
		data []byte
	}
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
