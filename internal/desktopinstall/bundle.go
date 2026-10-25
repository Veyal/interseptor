package desktopinstall

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Veyal/interseptor/internal/version"
)

// bundleInfo is the part of Contents/Info.plist the install checks.
type bundleInfo struct {
	ID      string
	Version string
}

// readInfo reads CFBundleIdentifier and CFBundleShortVersionString. An XML
// plist is parsed directly; a binary one is converted with plutil via run.
func readInfo(ctx context.Context, run Runner, app string) (bundleInfo, error) {
	path := filepath.Join(app, "Contents", "Info.plist")
	data, err := os.ReadFile(path)
	if err != nil {
		return bundleInfo{}, err
	}
	if bytes.HasPrefix(data, []byte("bplist")) {
		if run == nil {
			return bundleInfo{}, errors.New("binary Info.plist needs plutil")
		}
		out, code, err := run(ctx, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", path)
		if err != nil || code != 0 {
			return bundleInfo{}, fmt.Errorf("convert Info.plist: exit %d: %v %s", code, err, strings.TrimSpace(string(out)))
		}
		data = out
	}
	return parsePlist(data)
}

// parsePlist pulls top-level string values out of an XML property list.
func parsePlist(data []byte) (bundleInfo, error) {
	var doc struct {
		Dict struct {
			Items []struct {
				XMLName xml.Name
				Value   string `xml:",chardata"`
			} `xml:",any"`
		} `xml:"dict"`
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	if err := dec.Decode(&doc); err != nil {
		return bundleInfo{}, fmt.Errorf("parse Info.plist: %w", err)
	}
	vals := map[string]string{}
	items := doc.Dict.Items
	for k := 0; k+1 < len(items); k++ {
		if items[k].XMLName.Local == "key" && items[k+1].XMLName.Local == "string" {
			vals[strings.TrimSpace(items[k].Value)] = strings.TrimSpace(items[k+1].Value)
		}
	}
	return bundleInfo{ID: vals["CFBundleIdentifier"], Version: vals["CFBundleShortVersionString"]}, nil
}

// extractAndVerify unpacks zip with ditto (which keeps the signature's
// xattrs and symlinks) and runs the checks UpdateVerifier.validateApp does:
// a real bundle directory, our identifier, the release version and an intact
// code signature. It then strips any quarantine attribute defensively.
func (i *Installer) extractAndVerify(ctx context.Context, zipPath, stage, ver string) (string, error) {
	i.logf("Extracting %s…", filepath.Base(zipPath))
	extract := filepath.Join(stage, "extract")
	if err := os.Mkdir(extract, 0o755); err != nil {
		return "", err
	}
	if out, code, err := i.Run(ctx, "/usr/bin/ditto", "-x", "-k", zipPath, extract); err != nil || code != 0 {
		return "", fmt.Errorf("unpack the app: ditto exit %d: %v %s", code, err, strings.TrimSpace(string(out)))
	}
	app := filepath.Join(extract, AppName)
	if fi, err := os.Lstat(app); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("the release zip does not contain %s", AppName)
	}
	i.logf("Verifying bundle identifier, version and code signature…")
	if err := i.validateApp(ctx, app, ver); err != nil {
		return "", err
	}
	_, _, _ = i.Run(ctx, "/usr/bin/xattr", "-dr", "com.apple.quarantine", app)
	return app, nil
}

func (i *Installer) validateApp(ctx context.Context, app, ver string) error {
	info, err := readInfo(ctx, i.Run, app)
	if err != nil {
		return fmt.Errorf("the downloaded app has no readable Info.plist: %w", err)
	}
	if info.ID != BundleID {
		return fmt.Errorf("the downloaded app is a different app (bundle identifier %q)", info.ID)
	}
	if cmp, ok := version.CompareVersions(info.Version, ver); !ok || cmp != 0 {
		return fmt.Errorf("the downloaded app reports version %q, not the release version %s", info.Version, ver)
	}
	out, code, err := i.Run(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", app)
	if err != nil || code != 0 {
		return fmt.Errorf("the downloaded app's code signature is not valid: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// swap moves the current app into the staging folder, renames the new app
// into place, and restores the old one if that fails. keepStage is true only
// when the restore itself failed, so the caller must not delete the backup.
func (i *Installer) swap(newApp, target, stage string) (keepStage bool, err error) {
	rename := i.Rename
	if rename == nil {
		rename = os.Rename
	}
	backup := filepath.Join(stage, "previous-"+AppName)
	_, statErr := os.Lstat(target)
	hadOld := statErr == nil
	if hadOld {
		if err := rename(target, backup); err != nil {
			return false, fmt.Errorf("move the current app aside: %w", err)
		}
	}
	if err := rename(newApp, target); err != nil {
		if !hadOld {
			return false, fmt.Errorf("install the app: %w", err)
		}
		if rerr := rename(backup, target); rerr != nil {
			return true, fmt.Errorf("install the app: %v; restoring the previous app also failed (%v) — it is kept at %s", err, rerr, backup)
		}
		return false, fmt.Errorf("install the app (previous version restored): %w", err)
	}
	return false, nil
}
