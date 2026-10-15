package android

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"strings"
)

// This file adds the Android 14+ ways of trusting the Interseptor CA, which the
// classic `adb remount` path (InstallSystemCA, unchanged) can no longer reach:
//
//   - InstallSystemCATmpfs mounts a tmpfs over the system trust store and, on
//     Android 14+, over the read-only Conscrypt APEX copy in every running mount
//     namespace, so the CA is trusted without a reboot and without a writable
//     /system.
//   - GenerateMagiskModule builds a flashable Magisk / KernelSU module that
//     reinstalls the CA (and reapplies the APEX overlay) on every boot.
//
// Only the command/script construction and the module zip are exercised in
// tests here — there is no Android device in CI — so the real-device steps are
// documented (docs/mobile-testing.md), not claimed to have run.

const (
	// tmpfsStageDir is a scratch dir on the device used to assemble the full set
	// of trusted certs before they are copied onto the tmpfs mounts.
	tmpfsStageDir = "/data/local/tmp/interseptor-cacerts"
	// apexCACertsDir is the Conscrypt APEX trust store that Android 14+ reads from
	// and which is read-only (a separate mount namespace), defeating `adb remount`.
	apexCACertsDir = "/apex/com.android.conscrypt/cacerts"
	// cacertsSELabel is the SELinux context system cacerts files must carry.
	cacertsSELabel = "u:object_r:system_security_cacerts_file:s0"
)

// InstallSystemCATmpfs trusts the CA rebootlessly on modern Android: it roots
// adbd, pushes the cert, and overlays a tmpfs (carrying the existing certs plus
// ours) on the system trust store and the Conscrypt APEX store. It survives
// until the next reboot; GenerateMagiskModule makes it permanent.
func InstallSystemCATmpfs(d Device, certPEM []byte) error {
	name, err := systemCAFilename(certPEM)
	if err != nil {
		return err
	}
	der, err := systemCADER(certPEM)
	if err != nil {
		return err
	}
	path, err := writeTempCert(der, "interseptor-ca-*.0")
	if err != nil {
		return err
	}
	defer os.Remove(path)

	if _, err := runDev(d, true, "root"); err != nil {
		return fmt.Errorf("adb root failed — a rebootless system CA needs a rooted device or emulator: %w", err)
	}
	remoteCert := tmpfsStageDir + "-src/" + name
	if _, err := runDev(d, true, "shell", "mkdir", "-p", tmpfsStageDir+"-src"); err != nil {
		return fmt.Errorf("stage dir: %w", err)
	}
	if _, err := runDev(d, true, "push", path, remoteCert); err != nil {
		return fmt.Errorf("push system CA: %w", err)
	}
	if _, err := runDev(d, true, "shell", "sh", "-c", tmpfsOverlayScript(remoteCert)); err != nil {
		return fmt.Errorf("tmpfs CA overlay: %w", err)
	}
	return nil
}

// tmpfsOverlayScript is the on-device shell that mirrors the current system
// certs plus the freshly pushed one onto a tmpfs over the system trust store,
// then repeats the overlay inside every zygote mount namespace for the Android
// 14+ Conscrypt APEX store. pushedCert is the absolute path of the cert copied
// onto the device.
func tmpfsOverlayScript(pushedCert string) string {
	return strings.Join([]string{
		"set -e",
		"STAGE=" + tmpfsStageDir,
		"SYS=" + systemCACertsDir,
		"APEX=" + apexCACertsDir,
		"rm -rf \"$STAGE\"; mkdir -p \"$STAGE\"",
		"cp -f \"$SYS\"/* \"$STAGE\"/ 2>/dev/null || true",
		"cp -f " + pushedCert + " \"$STAGE\"/",
		// Overlay the system trust store.
		"mount -t tmpfs tmpfs \"$SYS\"",
		"cp -f \"$STAGE\"/* \"$SYS\"/",
		"chown 0:0 \"$SYS\"/*",
		"chmod 644 \"$SYS\"/*",
		"chcon " + cacertsSELabel + " \"$SYS\"/* 2>/dev/null || true",
		// Android 14+: the APEX copy lives in a separate, read-only mount
		// namespace per process, so bind a tmpfs over it inside each zygote's
		// namespace (new apps inherit it).
		"if [ -d \"$APEX\" ]; then",
		"  for pid in 1 $(pidof zygote 2>/dev/null) $(pidof zygote64 2>/dev/null); do",
		"    nsenter --mount=/proc/$pid/ns/mnt -- sh -c 'mount -t tmpfs tmpfs \"" + apexCACertsDir + "\" && cp -f \"" + systemCACertsDir + "\"/* \"" + apexCACertsDir + "\"/ && chown 0:0 \"" + apexCACertsDir + "\"/* && chmod 644 \"" + apexCACertsDir + "\"/* && chcon " + cacertsSELabel + " \"" + apexCACertsDir + "\"/* 2>/dev/null || true' || true",
		"  done",
		"fi",
	}, "\n")
}

// Magisk module identity. KernelSU reads the same layout.
const (
	magiskModuleID   = "interseptor_ca"
	magiskModuleName = "Interseptor System CA"
)

// GenerateMagiskModule builds a flashable Magisk / KernelSU module zip that
// installs the Interseptor CA into the system trust store and reapplies the
// Android 14+ APEX overlay on every boot. The returned bytes are a complete zip.
func GenerateMagiskModule(certPEM []byte) ([]byte, error) {
	name, err := systemCAFilename(certPEM)
	if err != nil {
		return nil, err
	}
	der, err := systemCADER(certPEM)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := []struct {
		name string
		body []byte
	}{
		{"module.prop", []byte(magiskModuleProp())},
		{"META-INF/com/google/android/update-binary", []byte(magiskUpdateBinary())},
		{"META-INF/com/google/android/updater-script", []byte("#MAGISK\n")},
		{"customize.sh", []byte(magiskCustomize())},
		{"post-fs-data.sh", []byte(magiskPostFsData())},
		{systemCACertsModulePath + "/" + name, der},
	}
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.body); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// systemCACertsModulePath is where a Magisk module stages the system trust store
// so magic-mount overlays it onto /system/etc/security/cacerts.
const systemCACertsModulePath = "system/etc/security/cacerts"

func magiskModuleProp() string {
	return strings.Join([]string{
		"id=" + magiskModuleID,
		"name=" + magiskModuleName,
		"version=v1",
		"versionCode=1",
		"author=Interseptor",
		"description=Trusts the Interseptor interception CA as a system CA (rebootless on Android 14+ via a tmpfs overlay of the Conscrypt APEX trust store).",
		"",
	}, "\n")
}

// magiskUpdateBinary is the standard Magisk module installer stub: it defers to
// Magisk's own util_functions.sh install_module, which copies the module files
// and runs customize.sh.
func magiskUpdateBinary() string {
	return strings.Join([]string{
		"#!/sbin/sh",
		"##################",
		"# Magisk module installer stub",
		"##################",
		"umask 022",
		"OUTFD=$2",
		"ZIPFILE=$3",
		"mount /data 2>/dev/null",
		"[ -f /data/adb/magisk/util_functions.sh ] || { echo \"! Install Magisk first\"; exit 1; }",
		". /data/adb/magisk/util_functions.sh",
		"install_module",
		"exit 0",
		"",
	}, "\n")
}

func magiskCustomize() string {
	return strings.Join([]string{
		"#!/sbin/sh",
		"# Ensure the staged system CA carries the permissions Android expects.",
		"set_perm_recursive $MODPATH/" + systemCACertsModulePath + " 0 0 0755 0644",
		"",
	}, "\n")
}

// magiskPostFsData reapplies the Conscrypt APEX overlay at each boot (Android
// 14+), mirroring the magic-mounted system cacerts (which already include this
// module's cert) over the read-only APEX trust store.
func magiskPostFsData() string {
	return strings.Join([]string{
		"#!/sbin/sh",
		"MODDIR=${0%/*}",
		"SYS=" + systemCACertsDir,
		"APEX=" + apexCACertsDir,
		"[ -d \"$APEX\" ] || exit 0",
		"STAGE=" + tmpfsStageDir,
		"rm -rf \"$STAGE\"; mkdir -p \"$STAGE\"",
		"cp -f \"$SYS\"/* \"$STAGE\"/ 2>/dev/null || true",
		"cp -f \"$MODDIR/" + systemCACertsModulePath + "\"/* \"$STAGE\"/ 2>/dev/null || true",
		"for pid in 1 $(pidof zygote 2>/dev/null) $(pidof zygote64 2>/dev/null); do",
		"  nsenter --mount=/proc/$pid/ns/mnt -- sh -c 'mount -t tmpfs tmpfs \"" + apexCACertsDir + "\" && cp -f \"" + tmpfsStageDir + "\"/* \"" + apexCACertsDir + "\"/ && chown 0:0 \"" + apexCACertsDir + "\"/* && chmod 644 \"" + apexCACertsDir + "\"/* && chcon " + cacertsSELabel + " \"" + apexCACertsDir + "\"/* 2>/dev/null || true' || true",
		"done",
		"",
	}, "\n")
}
