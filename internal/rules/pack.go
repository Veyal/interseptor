// Package rules implements Interseptor's rule-pack format: a signed-intent
// tarball of Starlark checks (passive and active) plus a manifest, so a
// community can author, share, install, and remove whole bundles of checks —
// the "OhMy" ecosystem layer over the per-check authoring API.
//
// A pack is a .tar.gz containing:
//
//	manifest.json              — name, version, author, entries[] with per-file sha256
//	checks/<id>.star           — passive checks
//	active-checks/<id>.star    — active checks
//
// Integrity: every file's sha256 is recorded in the manifest at build time and
// verified on read, so a corrupted or tampered pack is rejected before any
// check is written to disk. (Detached minisign/ed25519 signing is the next
// layer; the manifest+sha256 gate is the foundation it rides on.)
package rules

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// KindPassive / KindActive are the two check categories a pack can carry.
const (
	KindPassive = "passive"
	KindActive  = "active"
)

// Entry is one check in a pack manifest: its category, id, and the sha256 of
// the file bytes — verified before install so a tampered file aborts the pack.
type Entry struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

// Manifest is the pack's front matter plus its file index.
type Manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	License     string `json:"license,omitempty"`
	Created     string `json:"created,omitempty"`
	Entries     []Entry `json:"entries"`
}

const manifestName = "manifest.json"

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// BuildPack writes a pack tarball to w from the checks found under srcDir
// (expected layout: srcDir/checks/*.star and srcDir/active-checks/*.star). The
// supplied meta fills the manifest's descriptive fields; entries + hashes are
// computed from the files. Files are written in a stable (sorted) order so two
// builds of the same source are byte-identical.
func BuildPack(srcDir string, meta Manifest, w io.Writer) (Manifest, error) {
	if strings.TrimSpace(meta.Name) == "" {
		return Manifest{}, fmt.Errorf("rules: pack name is required")
	}
	collected, err := collect(srcDir)
	if err != nil {
		return Manifest{}, err
	}
	if len(collected) == 0 {
		return Manifest{}, fmt.Errorf("rules: no .star checks found under %s (expected checks/ and/or active-checks/)", srcDir)
	}
	sort.Slice(collected, func(i, j int) bool {
		if collected[i].Kind != collected[j].Kind {
			return collected[i].Kind < collected[j].Kind
		}
		return collected[i].ID < collected[j].ID
	})

	manifest := meta
	manifest.Created = time.Now().UTC().Format(time.RFC3339)
	manifest.Entries = make([]Entry, 0, len(collected))
	for _, c := range collected {
		manifest.Entries = append(manifest.Entries, Entry{Kind: c.Kind, ID: c.ID, SHA256: sha256Hex(c.Data)})
	}

	gz, _ := gzip.NewWriterLevel(w, gzip.BestCompression)
	tw := tar.NewWriter(gz)
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	if err := writeTar(tw, manifestName, manifestBytes); err != nil {
		tw.Close()
		gz.Close()
		return Manifest{}, err
	}
	for _, c := range collected {
		if err := writeTar(tw, c.archivePath(), c.Data); err != nil {
			tw.Close()
			gz.Close()
			return Manifest{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return Manifest{}, fmt.Errorf("rules: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return Manifest{}, fmt.Errorf("rules: close gzip: %w", err)
	}
	return manifest, nil
}

type collected struct {
	Kind string
	ID   string
	Data []byte
}

func (c collected) archivePath() string {
	if c.Kind == KindActive {
		return "active-checks/" + c.ID + ".star"
	}
	return "checks/" + c.ID + ".star"
}

func collect(srcDir string) ([]collected, error) {
	var out []collected
	for _, sub := range []struct{ dir, kind string }{{"checks", KindPassive}, {"active-checks", KindActive}} {
		names, files, err := readStarDir(srcDir + "/" + sub.dir)
		if err != nil {
			return nil, fmt.Errorf("rules: read %s: %w", sub.dir, err)
		}
		for i, name := range names {
			out = append(out, collected{Kind: sub.kind, ID: name, Data: files[i]})
		}
	}
	return out, nil
}

func writeTar(tw *tar.Writer, name string, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), ModTime: time.Time{}}); err != nil {
		return fmt.Errorf("rules: write header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("rules: write %s: %w", name, err)
	}
	return nil
}

// File is one decoded check from a pack: its category, id, and source bytes.
type File struct {
	Kind string
	ID   string
	Data []byte
}

// ReadPack decodes a pack tarball from r, parses its manifest, and verifies that
// every check file's sha256 matches the manifest. A missing manifest, an unknown
// member, or any hash mismatch is an error — nothing is returned partial.
func ReadPack(r io.Reader) (Manifest, []File, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("rules: not a gzip pack: %w", err)
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	var manifest *Manifest
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Manifest{}, nil, fmt.Errorf("rules: read tar: %w", err)
		}
		if strings.HasPrefix(hdr.Name, "/") || strings.Contains(hdr.Name, "..") {
			return Manifest{}, nil, fmt.Errorf("rules: unsafe member %q", hdr.Name)
		}
		data, err := io.ReadAll(io.LimitReader(tr, 1<<20))
		if err != nil {
			return Manifest{}, nil, fmt.Errorf("rules: read %s: %w", hdr.Name, err)
		}
		if hdr.Name == manifestName {
			var m Manifest
			if err := json.Unmarshal(data, &m); err != nil {
				return Manifest{}, nil, fmt.Errorf("rules: parse manifest: %w", err)
			}
			manifest = &m
			continue
		}
		files[hdr.Name] = data
	}
	if manifest == nil {
		return Manifest{}, nil, fmt.Errorf("rules: pack has no manifest.json")
	}
	out := make([]File, 0, len(manifest.Entries))
	for _, e := range manifest.Entries {
		path := entryPath(e)
		data, ok := files[path]
		if !ok {
			return Manifest{}, nil, fmt.Errorf("rules: manifest lists %s but the pack is missing it", path)
		}
		if got := sha256Hex(data); got != e.SHA256 {
			return Manifest{}, nil, fmt.Errorf("rules: integrity check failed for %s (manifest %s, file %s)", path, e.SHA256, got)
		}
		out = append(out, File{Kind: e.Kind, ID: e.ID, Data: data})
	}
	return *manifest, out, nil
}

func entryPath(e Entry) string {
	if e.Kind == KindActive {
		return "active-checks/" + e.ID + ".star"
	}
	return "checks/" + e.ID + ".star"
}
