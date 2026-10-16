// Package bruno imports Bruno collections (bruno.json folders of .bru files,
// or a single .bru file) into the collection model. The .bru markup is parsed
// by a hand-written, data-only parser; nothing is executed, fetched or read
// from disk by this package (callers hand it file contents). Scripts and tests
// are recorded and quarantined, dotenv files are never read, symlinks and
// path traversal are refused, and unsupported features are reported.
package bruno

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

// Limits for hostile input.
const (
	MaxFiles      = 20000
	MaxTotalBytes = impkit.MaxInputBytes
	MaxDepth      = impkit.MaxDepth
)

// Errors.
var (
	ErrTooLarge = errors.New("bruno: input exceeds 64 MiB")
	ErrNoBru    = errors.New("bruno: no .bru requests found")
)

// File is one input file; Path is relative to the collection root with
// forward slashes ("users/get.bru", "environments/dev.bru", "bruno.json").
type File struct {
	Path string
	Data []byte
}

// Options tune a parse. NewID defaults to store.NewUID.
type Options struct {
	NewID func() string
	Name  string
}

// Result is the parsed import, ready to preview and commit.
type Result struct {
	Collection   store.Collection      `json:"collection"`
	Items        []store.Item          `json:"items"`
	Variables    []store.Variable      `json:"variables"`
	Environments []postman.EnvImport   `json:"environments,omitempty"`
	Report       impkit.Report         `json:"report"`
	SecretValues []postman.SecretValue `json:"-"`
}

// Bundle returns the result as a store.CollectionsBundle.
func (r *Result) Bundle() store.CollectionsBundle {
	b := store.CollectionsBundle{Version: store.CollectionsBundleVersion,
		Collections: []store.Collection{r.Collection}, Items: append([]store.Item(nil), r.Items...),
		Variables: append([]store.Variable(nil), r.Variables...)}
	for _, e := range r.Environments {
		b.Environments = append(b.Environments, e.Environment)
		b.Variables = append(b.Variables, e.Variables...)
	}
	return b
}

// dirNode is a folder discovered from the file paths.
type dirNode struct {
	path   string
	meta   *bru // folder.bru
	dirs   map[string]*dirNode
	files  []File
	seq    float64
	hasSeq bool
}

// Parse imports a single .bru request file.
func Parse(data []byte, opt Options) (*Result, error) {
	return ParseFiles([]File{{Path: "request.bru", Data: data}}, opt)
}

// ParseFiles imports a Bruno collection from its files.
func ParseFiles(files []File, opt Options) (*Result, error) {
	if opt.NewID == nil {
		opt.NewID = store.NewUID
	}
	res := &Result{}
	res.Report.Format = "bruno"
	c := &conv{res: res, opt: opt}
	if len(files) > MaxFiles {
		c.add(impkit.Blocked, "", "", "too-many-files", fmt.Sprintf("only the first %d files were read", MaxFiles), "")
		files = files[:MaxFiles]
	}
	total := 0
	for _, f := range files {
		total += len(f.Data)
		if total > MaxTotalBytes {
			return nil, ErrTooLarge
		}
	}
	root, collFile, envFiles, name := c.layout(files)
	if opt.Name != "" {
		name = opt.Name
	}
	if name == "" {
		name = "Bruno import"
	}
	res.Collection = store.Collection{UID: opt.NewID(), Name: name, ScopePolicy: store.ScopePolicyBlock,
		Sidecar: impkit.MustJSON(postman.CollectionSidecar{Format: "bruno"})}
	c.collection(collFile)
	prev := ""
	for _, ch := range c.children(root) {
		prev = c.emit(ch, "", prev, "", 0, c.inherit)
	}
	c.environments(envFiles)
	impkit.Finish(&res.Report, "Bruno")
	res.Collection.ImportReport = impkit.MustJSON(res.Report)
	if res.Report.Stats.Requests == 0 {
		return res, ErrNoBru
	}
	return res, nil
}

type conv struct {
	res     *Result
	opt     Options
	inherit []impkit.Row // collection-level headers copied into requests
	ignored []string
}

func (c *conv) add(l impkit.Level, p, uid, feature, msg, sugg string) {
	c.res.Report.Entries = append(c.res.Report.Entries, impkit.Entry{Level: l, Path: p, Item: uid, Feature: feature, Message: msg, Suggestion: sugg})
}

// layout sorts the files into the directory tree, the collection file and
// environment files, refusing unsafe paths.
func (c *conv) layout(files []File) (root *dirNode, coll *bru, envs []File, name string) {
	root = &dirNode{dirs: map[string]*dirNode{}}
	var bruJSON struct {
		Name string `json:"name"`
	}
	ignored := 0
	for _, f := range files {
		p := strings.ReplaceAll(f.Path, "\\", "/")
		if p == "" || strings.HasPrefix(p, "/") || hasDotDot(p) || strings.ContainsRune(p, 0) {
			c.add(impkit.Blocked, f.Path, "", "unsafe-path", "file path escapes the collection root and was skipped", "")
			continue
		}
		p = path.Clean(p)
		base := path.Base(p)
		switch {
		case p == "bruno.json":
			_ = json.Unmarshal(f.Data, &bruJSON)
			name = bruJSON.Name
		case p == "collection.bru":
			coll = c.parseOrWarn(f)
		case strings.HasPrefix(p, "environments/") && strings.HasSuffix(base, ".bru"):
			envs = append(envs, File{Path: p, Data: f.Data})
		case strings.HasSuffix(base, ".bru"):
			if segmentIgnored(p) {
				ignored++
				continue
			}
			dir := root
			parts := strings.Split(path.Dir(p), "/")
			if path.Dir(p) == "." {
				parts = nil
			}
			for _, seg := range parts {
				nd := dir.dirs[seg]
				if nd == nil {
					nd = &dirNode{path: path.Join(dir.path, seg), dirs: map[string]*dirNode{}}
					dir.dirs[seg] = nd
				}
				dir = nd
			}
			if base == "folder.bru" {
				dir.meta = c.parseOrWarn(f)
				continue
			}
			dir.files = append(dir.files, File{Path: p, Data: f.Data})
		default:
			ignored++
		}
	}
	if ignored > 0 {
		c.add(impkit.Degraded, "", "", "ignored-files", fmt.Sprintf("%d non-.bru file(s) ignored (dotenv, node_modules and other files are never read)", ignored), "")
	}
	return
}

func hasDotDot(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if s == ".." {
			return true
		}
	}
	return false
}

func segmentIgnored(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if s == "node_modules" || s == ".git" {
			return true
		}
	}
	return false
}

func (c *conv) parseOrWarn(f File) *bru {
	if len(f.Data) > MaxFileBytes {
		c.add(impkit.Blocked, f.Path, "", "file-too-large", ".bru file larger than 8 MiB was skipped", "")
		return nil
	}
	b, err := parseBru(string(f.Data))
	if err != nil && !errors.Is(err, ErrNotBru) {
		c.add(impkit.Degraded, f.Path, "", "parse", err.Error(), "")
	}
	if b == nil || len(b.blocks) == 0 {
		c.add(impkit.Degraded, f.Path, "", "not-bru", "file holds no .bru blocks and was skipped", "")
		return nil
	}
	for _, w := range b.warns {
		c.add(impkit.Degraded, f.Path, "", "parse", w, "")
	}
	return b
}

// child is a folder or request ready to emit.
type child struct {
	dir  *dirNode
	req  *bru
	file File
	name string
	seq  float64
}

func (c *conv) children(d *dirNode) []child {
	var out []child
	dirNames := make([]string, 0, len(d.dirs))
	for n := range d.dirs {
		dirNames = append(dirNames, n)
	}
	sort.Strings(dirNames)
	var folders []child
	for _, n := range dirNames {
		nd := d.dirs[n]
		name, seq := n, float64(1<<30)
		if nd.meta != nil {
			if m := nd.meta.get("meta"); m != nil {
				if v := pair(m, "name"); v != "" {
					name = v
				}
				if s, ok := seqOf(m); ok {
					seq = s
				}
			}
		}
		folders = append(folders, child{dir: nd, name: name, seq: seq})
	}
	sort.SliceStable(folders, func(i, j int) bool { return folders[i].seq < folders[j].seq })
	out = append(out, folders...)
	var reqs []child
	for _, f := range d.files {
		b := c.parseOrWarn(f)
		if b == nil {
			continue
		}
		name, seq := strings.TrimSuffix(path.Base(f.Path), ".bru"), float64(1<<30)
		if m := b.get("meta"); m != nil {
			if v := pair(m, "name"); v != "" {
				name = v
			}
			if s, ok := seqOf(m); ok {
				seq = s
			}
		}
		reqs = append(reqs, child{req: b, file: f, name: name, seq: seq})
	}
	sort.SliceStable(reqs, func(i, j int) bool {
		if reqs[i].seq != reqs[j].seq {
			return reqs[i].seq < reqs[j].seq
		}
		return reqs[i].file.Path < reqs[j].file.Path
	})
	return append(out, reqs...)
}

func seqOf(m *block) (float64, bool) {
	v := pair(m, "seq")
	if v == "" {
		return 0, false
	}
	var f float64
	if _, err := fmt.Sscanf(v, "%g", &f); err != nil {
		return 0, false
	}
	return f, true
}

func pair(b *block, key string) string {
	if b == nil {
		return ""
	}
	for _, p := range b.Pairs {
		if p.Key == key && !p.Disabled {
			return p.Value
		}
	}
	return ""
}

func (c *conv) emit(ch child, parent, prevRank, parentPath string, depth int, inherit []impkit.Row) string {
	if len(c.res.Items) >= impkit.MaxItems {
		return prevRank
	}
	if depth >= MaxDepth {
		c.add(impkit.Blocked, parentPath, "", "too-deep", "folder nesting deeper than 64 levels was not imported", "")
		return prevRank
	}
	name := strings.TrimSpace(ch.name)
	if name == "" {
		name = "Untitled"
	}
	p := parentPath + "/" + name
	it := store.Item{UID: c.opt.NewID(), CollectionUID: c.res.Collection.UID, ParentUID: parent,
		Rank: store.RankBetween(prevRank, ""), Name: name}
	if ch.dir != nil {
		it.Kind = "folder"
		c.res.Report.Stats.Folders++
		sub := inherit
		if m := ch.dir.meta; m != nil {
			sub = c.folder(m, &it, p, inherit)
		}
		it.Sidecar = impkit.MustJSON(postman.ItemSidecar{})
		c.res.Items = append(c.res.Items, it)
		prev := ""
		for _, k := range c.children(ch.dir) {
			prev = c.emit(k, it.UID, prev, p, depth+1, sub)
		}
		return it.Rank
	}
	it.Kind = "request"
	c.res.Report.Stats.Requests++
	c.request(ch, &it, p, inherit)
	it.Sidecar = impkit.MustJSON(postman.ItemSidecar{})
	c.res.Items = append(c.res.Items, it)
	return it.Rank
}

// folder applies folder.bru; folder headers are returned for inheritance
// because the execution pipeline only applies request headers.
func (c *conv) folder(b *bru, it *store.Item, p string, inherit []impkit.Row) []impkit.Row {
	ctx := &blkCtx{c: c, path: p, uid: it.UID}
	it.Auth = ctx.auth(b)
	if d := b.get("docs"); d != nil {
		it.DescriptionMD = d.Text
	}
	it.Events = ctx.scripts(b, true)
	out := inherit
	if h := b.get("headers"); h != nil {
		rows := ctx.rows(h)
		impkit.ScanHeaderCredentials(c.rep(), p, it.UID, rows)
		c.add(impkit.Degraded, p, it.UID, "inherited-headers", "folder headers are copied into each request below because folders do not apply headers when sending", "")
		out = append(append([]impkit.Row(nil), inherit...), rows...)
	}
	ctx.vars(b, store.VarOwnerFolder, it.UID)
	ctx.unknownBlocks(b)
	return out
}

func (c *conv) rep() *impkit.Report { return &c.res.Report }

// collection applies collection.bru to the collection row.
func (c *conv) collection(b *bru) {
	if b == nil {
		return
	}
	ctx := &blkCtx{c: c, path: "collection.bru", uid: ""}
	c.res.Collection.Auth = ctx.auth(b)
	c.res.Collection.Events = ctx.scripts(b, true)
	if d := b.get("docs"); d != nil {
		c.res.Collection.Description = d.Text
	}
	if h := b.get("headers"); h != nil {
		rows := ctx.rows(h)
		impkit.ScanHeaderCredentials(c.rep(), "collection.bru", "", rows)
		c.inherit = rows
		c.add(impkit.Degraded, "collection.bru", "", "inherited-headers", "collection headers are copied into each request because collections do not apply headers when sending", "")
	}
	ctx.vars(b, store.VarOwnerCollection, c.res.Collection.UID)
	ctx.unknownBlocks(b)
}

func (c *conv) environments(files []File) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, f := range files {
		b := c.parseOrWarn(f)
		if b == nil {
			continue
		}
		name := strings.TrimSuffix(path.Base(f.Path), ".bru")
		im := postman.EnvImport{Environment: store.Environment{UID: c.opt.NewID(), Name: name, Kind: "env", CollectionUID: c.res.Collection.UID}}
		secrets := map[string]bool{}
		if sb := b.get("vars:secret"); sb != nil {
			for _, k := range sb.List {
				secrets[k] = true
			}
		}
		if vb := b.get("vars"); vb != nil {
			for _, kv := range vb.Pairs {
				c.envVar(&im, kv, secrets, name)
			}
		}
		for k := range secrets {
			if !hasVar(im.Variables, k) {
				im.Variables = append(im.Variables, store.Variable{OwnerKind: store.VarOwnerEnvironment, OwnerUID: im.Environment.UID, Key: k, Type: store.VarTypeSecret, Enabled: true})
				c.res.Report.Stats.SecretVariables++
			}
		}
		c.res.Report.Stats.EnvironmentVariables += len(im.Variables)
		c.res.Environments = append(c.res.Environments, im)
	}
}

func hasVar(vs []store.Variable, k string) bool {
	for _, v := range vs {
		if v.Key == k {
			return true
		}
	}
	return false
}

func (c *conv) envVar(im *postman.EnvImport, kv kv, secrets map[string]bool, env string) {
	v := store.Variable{OwnerKind: store.VarOwnerEnvironment, OwnerUID: im.Environment.UID, Key: kv.Key, Type: store.VarTypeDefault, Enabled: !kv.Disabled}
	if kv.Disabled {
		c.res.Report.Stats.DisabledRows++
	}
	if secrets[kv.Key] || (impkit.IsSecretKey(kv.Key) && kv.Value != "" && !strings.Contains(kv.Value, "{{")) {
		v.Type = store.VarTypeSecret
		c.res.Report.Stats.SecretVariables++
		if kv.Value != "" {
			c.res.SecretValues = append(c.res.SecretValues, postman.SecretValue{OwnerKind: v.OwnerKind, OwnerUID: v.OwnerUID, Key: kv.Key, Value: kv.Value})
			c.add(impkit.NeedsReview, "environment "+env, "", "secret-variable", "variable "+kv.Key+" holds a secret value; it was moved out of the shareable initial value (offered as a local current value only)", "")
		}
	} else {
		v.InitialValue = kv.Value
	}
	c.res.Report.Stats.Variables++
	im.Variables = append(im.Variables, v)
}
