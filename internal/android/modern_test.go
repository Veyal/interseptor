package android

import (
	"archive/zip"
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestTmpfsOverlayScriptCoversSystemAndApex(t *testing.T) {
	script := tmpfsOverlayScript("/data/local/tmp/interseptor-cacerts-src/abcd1234.0")
	for _, want := range []string{
		"mount -t tmpfs tmpfs",
		systemCACertsDir,
		apexCACertsDir,
		"nsenter --mount=/proc/$pid/ns/mnt",
		cacertsSELabel,
		"/data/local/tmp/interseptor-cacerts-src/abcd1234.0",
		"pidof zygote64",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("overlay script missing %q\n---\n%s", want, script)
		}
	}
}

func TestInstallSystemCATmpfsCommandConstruction(t *testing.T) {
	pemBytes := testCAPEM(t)
	name, err := systemCAFilename(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	if err := InstallSystemCATmpfs(Device{Serial: "dev", State: "device"}, pemBytes); err != nil {
		t.Fatal(err)
	}

	wantPush := "/data/local/tmp/interseptor-cacerts-src/" + name
	var rooted, pushed, ranScript bool
	for _, c := range calls {
		switch {
		case len(c) >= 3 && c[2] == "root":
			rooted = true
		case len(c) >= 5 && c[2] == "push" && c[len(c)-1] == wantPush:
			pushed = true
		case len(c) >= 5 && c[2] == "shell" && c[3] == "sh" && c[4] == "-c" && strings.Contains(c[len(c)-1], "mount -t tmpfs tmpfs"):
			ranScript = true
		}
	}
	if !rooted {
		t.Errorf("expected an adb root call, calls=%v", calls)
	}
	if !pushed {
		t.Errorf("expected push to %s, calls=%v", wantPush, calls)
	}
	if !ranScript {
		t.Errorf("expected a shell sh -c with the overlay script, calls=%v", calls)
	}
}

func TestInstallSystemCATmpfsRejectsBadPEM(t *testing.T) {
	stubADB(t, func(args ...string) ([]byte, error) { return nil, nil })
	if err := InstallSystemCATmpfs(Device{Serial: "dev", State: "device"}, []byte("nope")); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestGenerateMagiskModuleContents(t *testing.T) {
	pemBytes := testCAPEM(t)
	name, err := systemCAFilename(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pemBytes)
	wantDER := block.Bytes
	if _, err := x509.ParseCertificate(wantDER); err != nil {
		t.Fatal(err)
	}

	zipBytes, err := GenerateMagiskModule(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("output is not a valid zip: %v", err)
	}
	got := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = b
	}

	certEntry := "system/etc/security/cacerts/" + name
	wantEntries := []string{
		"module.prop",
		"META-INF/com/google/android/update-binary",
		"META-INF/com/google/android/updater-script",
		"customize.sh",
		"post-fs-data.sh",
		certEntry,
	}
	for _, e := range wantEntries {
		if _, ok := got[e]; !ok {
			t.Errorf("module zip missing entry %q", e)
		}
	}
	if !reflect.DeepEqual(got[certEntry], wantDER) {
		t.Error("staged system CA content does not match the certificate DER")
	}
	if !strings.Contains(string(got["module.prop"]), "id="+magiskModuleID) {
		t.Errorf("module.prop missing id: %s", got["module.prop"])
	}
	if !strings.Contains(string(got["META-INF/com/google/android/update-binary"]), "install_module") {
		t.Error("update-binary should defer to Magisk install_module")
	}
	if !strings.Contains(string(got["META-INF/com/google/android/updater-script"]), "#MAGISK") {
		t.Error("updater-script must carry the #MAGISK marker")
	}
	post := string(got["post-fs-data.sh"])
	if !strings.Contains(post, "nsenter") || !strings.Contains(post, apexCACertsDir) {
		t.Error("post-fs-data.sh should reapply the APEX overlay via nsenter")
	}
}

func TestGenerateMagiskModuleRejectsBadPEM(t *testing.T) {
	if _, err := GenerateMagiskModule([]byte("not a cert")); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}
