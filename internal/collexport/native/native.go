// Package native reads and writes Interseptor's own portable collection
// format (".ixcol.json"): one collection with its folders, requests,
// environments and variable declarations. It is lossless for every column the
// store models.
//
// Secrets: Export expects a bundle obtained from
// store.ExportCollectionsBundle with the default (scrubbed) options, which is
// the single scrub function; it additionally blanks secret-type variable
// values unless IncludeSecrets. Decode treats the file as hostile: bounded
// size and counts, validated structure, secret values blanked, collection
// capabilities cleared and the scope policy reset to "block" (a file can never
// widen what an imported collection may reach).
package native

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Veyal/interseptor/internal/store"
)

// Format identifies the file type; Version is the schema version.
const (
	Format  = "interseptor.collection"
	Version = 1
)

// Limits applied by Decode.
const (
	MaxBytes = 64 << 20
	MaxItems = 100000
	MaxDepth = 64
)

// Errors.
var (
	ErrNoCollection = errors.New("native export: collection not found")
	ErrFormat       = errors.New("native: not an Interseptor collection file")
	ErrVersion      = errors.New("native: unsupported file version")
)

// Options for an export.
type Options struct {
	// IncludeSecrets keeps secret-type variable values present in the input.
	// The caller must have obtained explicit confirmation.
	IncludeSecrets bool
	// Compact writes minified JSON instead of tab-indented.
	Compact bool
}

// Warning notes content that does not travel in this format.
type Warning struct {
	Feature string `json:"feature"`
	Message string `json:"message"`
}

// Output is an exported file.
type Output struct {
	Data     []byte    `json:"-"`
	Warnings []Warning `json:"warnings,omitempty"`
}

type file struct {
	Format       string              `json:"format"`
	Version      int                 `json:"version"`
	Collection   store.Collection    `json:"collection"`
	Items        []store.Item        `json:"items"`
	Environments []store.Environment `json:"environments"`
	Variables    []store.Variable    `json:"variables"`
}

// Export writes collection uid from the bundle.
func Export(b store.CollectionsBundle, uid string, opt Options) (*Output, error) {
	f := file{Format: Format, Version: Version, Items: []store.Item{}, Environments: []store.Environment{}, Variables: []store.Variable{}}
	found := false
	for _, c := range b.Collections {
		if c.UID == uid {
			f.Collection, found = c, true
		}
	}
	if !found {
		return nil, ErrNoCollection
	}
	owners := map[string]bool{store.VarOwnerCollection + "/" + uid: true}
	for _, it := range b.Items {
		if it.CollectionUID != uid {
			continue
		}
		f.Items = append(f.Items, it)
		kind := store.VarOwnerRequest
		if it.Kind == "folder" {
			kind = store.VarOwnerFolder
		}
		owners[kind+"/"+it.UID] = true
	}
	for _, e := range b.Environments {
		if e.CollectionUID == uid {
			f.Environments = append(f.Environments, e)
			owners[store.VarOwnerEnvironment+"/"+e.UID] = true
		}
	}
	for _, v := range b.Variables {
		if !owners[v.OwnerKind+"/"+v.OwnerUID] {
			continue
		}
		if v.Type == store.VarTypeSecret && !opt.IncludeSecrets {
			v.InitialValue = ""
		}
		f.Variables = append(f.Variables, v)
	}
	sortFile(&f)
	out := &Output{}
	if !opt.IncludeSecrets {
		out.Warnings = append(out.Warnings, Warning{"secrets", "secret variable values, current values, cookies and tokens are not included"})
	}
	if len(f.Collection.Caps) > 0 {
		out.Warnings = append(out.Warnings, Warning{"capabilities", "collection capabilities are carried but cleared again on import"})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if !opt.Compact {
		enc.SetIndent("", "\t")
	}
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	out.Data = buf.Bytes()
	return out, nil
}

func sortFile(f *file) {
	sort.SliceStable(f.Items, func(i, j int) bool {
		a, b := f.Items[i], f.Items[j]
		if a.ParentUID != b.ParentUID {
			return a.ParentUID < b.ParentUID
		}
		if a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
		return a.UID < b.UID
	})
	sort.SliceStable(f.Environments, func(i, j int) bool { return f.Environments[i].UID < f.Environments[j].UID })
	sort.SliceStable(f.Variables, func(i, j int) bool {
		a, b := f.Variables[i], f.Variables[j]
		if a.OwnerKind != b.OwnerKind {
			return a.OwnerKind < b.OwnerKind
		}
		if a.OwnerUID != b.OwnerUID {
			return a.OwnerUID < b.OwnerUID
		}
		return a.Key < b.Key
	})
}

// Decode parses a native file into a bundle. The result is safe to hand to a
// store commit: structure validated, secrets blanked, capabilities cleared.
func Decode(data []byte) (store.CollectionsBundle, error) {
	var b store.CollectionsBundle
	if len(data) > MaxBytes {
		return b, errors.New("native: file exceeds 64 MiB")
	}
	var f file
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&f); err != nil {
		return b, fmt.Errorf("native: invalid JSON: %w", err)
	}
	if f.Format != Format {
		return b, ErrFormat
	}
	if f.Version != Version {
		return b, ErrVersion
	}
	if f.Collection.UID == "" || f.Collection.Name == "" {
		return b, errors.New("native: collection needs a uid and a name")
	}
	if len(f.Items) > MaxItems {
		return b, fmt.Errorf("native: more than %d items", MaxItems)
	}
	byUID := make(map[string]*store.Item, len(f.Items))
	for i := range f.Items {
		it := &f.Items[i]
		if it.UID == "" || byUID[it.UID] != nil {
			return b, fmt.Errorf("native: item %d has a missing or duplicate uid", i+1)
		}
		if it.Kind != "folder" && it.Kind != "request" {
			return b, fmt.Errorf("native: item %q has unknown kind %q", it.Name, it.Kind)
		}
		it.CollectionUID = f.Collection.UID
		byUID[it.UID] = it
	}
	for _, it := range f.Items {
		if err := checkParents(&it, byUID); err != nil {
			return b, err
		}
	}
	envs := map[string]bool{}
	for i := range f.Environments {
		e := &f.Environments[i]
		if e.UID == "" || envs[e.UID] {
			return b, fmt.Errorf("native: environment %d has a missing or duplicate uid", i+1)
		}
		envs[e.UID] = true
		e.CollectionUID = f.Collection.UID
	}
	for i := range f.Variables {
		v := &f.Variables[i]
		switch v.OwnerKind {
		case store.VarOwnerCollection:
			if v.OwnerUID != f.Collection.UID {
				return b, errors.New("native: variable owned by another collection")
			}
		case store.VarOwnerFolder, store.VarOwnerRequest:
			if byUID[v.OwnerUID] == nil {
				return b, errors.New("native: variable owner item not found")
			}
		case store.VarOwnerEnvironment:
			if !envs[v.OwnerUID] {
				return b, errors.New("native: variable owner environment not found")
			}
		default:
			return b, fmt.Errorf("native: variable owner kind %q is not allowed in a file", v.OwnerKind)
		}
		if v.Type == store.VarTypeSecret {
			v.InitialValue = ""
		}
	}
	f.Collection.Caps = nil
	f.Collection.ScopePolicy = store.ScopePolicyBlock
	b = store.CollectionsBundle{Version: store.CollectionsBundleVersion,
		Collections: []store.Collection{f.Collection}, Items: f.Items, Environments: f.Environments, Variables: f.Variables}
	return b, nil
}

func checkParents(it *store.Item, byUID map[string]*store.Item) error {
	cur := it
	for depth := 0; cur.ParentUID != ""; depth++ {
		if depth > MaxDepth {
			return fmt.Errorf("native: item %q is nested deeper than %d levels or in a cycle", it.Name, MaxDepth)
		}
		p := byUID[cur.ParentUID]
		if p == nil {
			return fmt.Errorf("native: item %q has an unknown parent", it.Name)
		}
		if p.Kind != "folder" {
			return fmt.Errorf("native: item %q has a non-folder parent", it.Name)
		}
		cur = p
	}
	return nil
}
