package rules

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestSignAndInstallRejectsUnsignedAndBadSig(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	keyDir := filepath.Join(root, "trusted-pack-keys")
	if err := os.MkdirAll(keyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WritePublicKeyFile(filepath.Join(keyDir, "dev.pub"), pub); err != nil {
		t.Fatal(err)
	}
	kr := DefaultKeyring(root)
	if _, ok := kr["dev"]; !ok {
		t.Fatal("trusted key not loaded")
	}

	src := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(src, "checks"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "checks", "a.star"), []byte("def check(flow):\n    return []\n"), 0o644))

	var unsigned bytes.Buffer
	if _, err := BuildPack(src, Manifest{Name: "p", Version: "1.0.0"}, &unsigned); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(root)
	checksDir := filepath.Join(root, "checks")
	activeDir := filepath.Join(root, "active-checks")
	if _, _, err := reg.InstallStreamOpts(bytes.NewReader(unsigned.Bytes()), checksDir, activeDir, "t", InstallOpts{}); err == nil {
		t.Fatal("unsigned pack must be rejected by default")
	}

	var signed bytes.Buffer
	m, err := BuildPackOpts(src, Manifest{Name: "p", Version: "1.0.0"}, &signed, BuildOpts{PrivateKey: priv, KeyID: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Signature == nil || m.Signature.KeyID != "dev" {
		t.Fatalf("expected signature, got %+v", m.Signature)
	}
	got, n, err := reg.InstallStreamOpts(bytes.NewReader(signed.Bytes()), checksDir, activeDir, "t", InstallOpts{Keys: kr})
	if err != nil {
		t.Fatalf("signed install: %v", err)
	}
	if n != 1 || got.Name != "p" {
		t.Fatalf("install: %s %d", got.Name, n)
	}
	rec, ok, _ := reg.Get("p")
	if !ok || rec.Signed != "dev" {
		t.Fatalf("record signed field: %+v", rec)
	}

	// Tamper: rebuild with different content but reuse old signature bytes.
	must(t, os.WriteFile(filepath.Join(src, "checks", "a.star"), []byte("def check(flow):\n    return [Finding()]\n"), 0o644))
	var tampered bytes.Buffer
	bad, err := BuildPackOpts(src, Manifest{Name: "p", Version: "1.0.0"}, &tampered, BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_ = bad
	// Re-pack signed then flip signature to wrong key material via ReadPack/manual is hard;
	// instead verify VerifyManifestSignature fails on wrong digest.
	badM := m
	badM.Entries[0].SHA256 = "deadbeef"
	if err := VerifyManifestSignature(badM, kr); err == nil {
		t.Fatal("tampered digest must fail verify")
	}
}

func TestCatalogInstallTrustBuiltin(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(root)
	var buf bytes.Buffer
	if _, err := BuildCatalogPack("secrets", &buf); err != nil {
		t.Fatal(err)
	}
	_, _, err := reg.InstallStreamOpts(bytes.NewReader(buf.Bytes()),
		filepath.Join(root, "checks"), filepath.Join(root, "active-checks"), "catalog",
		InstallOpts{TrustBuiltin: true})
	if err != nil {
		t.Fatal(err)
	}
	rec, ok, _ := reg.Get("secrets")
	if !ok || rec.Signed != "builtin" {
		t.Fatalf("catalog should be builtin: %+v", rec)
	}
}
