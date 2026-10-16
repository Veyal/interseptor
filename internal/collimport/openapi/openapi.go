// Package openapi imports OpenAPI 3.x and Swagger 2.0 documents (JSON or YAML)
// into the collection model: operations become requests, tags become folders,
// declared response examples become saved examples, security schemes become
// auth configuration plus secret variables, and servers become environments.
// Parsing is data-only: nothing is fetched (external $refs are reported, never
// followed), executed or sent.
package openapi

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

// Limits for hostile input (see also node.go and schema.go).
const (
	MaxOperations      = 20000
	MaxParamsPerOp     = 500
	MaxResponsesPerOp  = 100
	MaxExamplesPerResp = 20
)

// Errors.
var (
	ErrNotOpenAPI  = errors.New("openapi: not an OpenAPI 3.x or Swagger 2.0 document")
	ErrUnsupported = errors.New("openapi: unsupported specification version")
)

// Report types are shared with the Postman importer.
type (
	Report    = postman.Report
	Entry     = postman.Entry
	Level     = postman.Level
	EnvImport = postman.EnvImport
)

const (
	Converted      = postman.Converted
	Degraded       = postman.Degraded
	PreservedInert = postman.PreservedInert
	Unsupported    = postman.Unsupported
	Blocked        = postman.Blocked
	NeedsReview    = postman.NeedsReview
)

// Options tune a parse. NewID defaults to store.NewUID (tests inject a counter).
type Options struct {
	NewID func() string
}

// Result is a parsed specification, ready to preview and commit.
type Result struct {
	Collection   store.Collection `json:"collection"`
	Items        []store.Item     `json:"items"`
	Variables    []store.Variable `json:"variables"`
	Environments []EnvImport      `json:"environments,omitempty"`
	Report       Report           `json:"report"`
}

// Bundle returns the result as a store.CollectionsBundle.
func (r *Result) Bundle() store.CollectionsBundle {
	b := store.CollectionsBundle{Version: store.CollectionsBundleVersion,
		Collections: []store.Collection{r.Collection}}
	b.Items = append(b.Items, r.Items...)
	b.Variables = append(b.Variables, r.Variables...)
	for _, e := range r.Environments {
		b.Environments = append(b.Environments, e.Environment)
		b.Variables = append(b.Variables, e.Variables...)
	}
	return b
}

type importer struct {
	opt          Options
	root         *omap
	v2           bool
	res          *Result
	seen         map[string]bool // dedupe key for repeated report entries
	vars         map[string]bool
	schemes      *omap
	globalSec    []any
	hasGlobalSec bool
	baseURL      string
}

// Parse imports an OpenAPI / Swagger document.
func Parse(data []byte, opt Options) (*Result, error) {
	if opt.NewID == nil {
		opt.NewID = store.NewUID
	}
	tree, err := decode(data)
	if err != nil {
		if errors.Is(err, ErrTooLarge) || errors.Is(err, ErrTooDeep) || errors.Is(err, ErrTooManyN) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrNotOpenAPI, err)
	}
	root := asMap(tree)
	if root == nil {
		return nil, ErrNotObject
	}
	im := &importer{opt: opt, root: root, res: &Result{}, seen: map[string]bool{}, vars: map[string]bool{}}
	im.res.Report.Format = "openapi"
	switch ver := asString(root.get("openapi")); {
	case strings.HasPrefix(ver, "3."):
	case asString(root.get("swagger")) == "2.0":
		im.v2 = true
	case ver != "" || root.has("swagger"):
		return nil, fmt.Errorf("%w: %s%s", ErrUnsupported, ver, asString(root.get("swagger")))
	default:
		return nil, ErrNotOpenAPI
	}
	if err := im.run(); err != nil {
		return im.res, err
	}
	im.finish()
	return im.res, nil
}

func (im *importer) report(l Level, path, feature, msg, suggestion string) {
	im.res.Report.Entries = append(im.res.Report.Entries, Entry{Level: l, Path: path, Feature: feature, Message: msg, Suggestion: suggestion})
}

// reportOnce records an entry once per (feature, key).
func (im *importer) reportOnce(key string, l Level, path, feature, msg, suggestion string) {
	if im.seen[feature+"|"+key] {
		return
	}
	im.seen[feature+"|"+key] = true
	im.report(l, path, feature, msg, suggestion)
}

func (im *importer) finish() {
	r := &im.res.Report
	r.Counts = map[Level]int{}
	for _, e := range r.Entries {
		r.Counts[e.Level]++
	}
	sort.SliceStable(r.Entries, func(i, j int) bool { return levelRank(r.Entries[i].Level) < levelRank(r.Entries[j].Level) })
	st := r.Stats
	r.Headline = fmt.Sprintf("%d folders, %d requests, %d saved examples; %d variables (%d secret), %d environments; %d need review",
		st.Folders, st.Requests, st.Examples, st.Variables, st.SecretVariables, len(im.res.Environments), r.Counts[NeedsReview])
	im.res.Collection.ImportReport = mustJSON(*r)
}

func levelRank(l Level) int {
	switch l {
	case Unsupported:
		return 0
	case Blocked:
		return 1
	case NeedsReview:
		return 2
	case Degraded:
		return 3
	case PreservedInert:
		return 4
	}
	return 5
}

func (im *importer) run() error {
	info := asMap(im.root.get("info"))
	c := &im.res.Collection
	c.UID = im.opt.NewID()
	c.Name = asString(info.get("title"))
	if c.Name == "" {
		c.Name = "Imported API"
	}
	c.ScopePolicy = store.ScopePolicyBlock
	c.Description = asString(info.get("description"))
	if v := asString(info.get("version")); v != "" {
		if c.Description != "" {
			c.Description += "\n\n"
		}
		c.Description += "Version: " + v
	}
	side := newOmap()
	if im.v2 {
		side.set("format", "swagger-"+asString(im.root.get("swagger")))
	} else {
		side.set("format", "openapi-"+asString(im.root.get("openapi")))
	}
	if info != nil {
		side.set("info", info)
	}
	if ed := im.root.get("externalDocs"); ed != nil {
		side.set("externalDocs", ed)
	}
	c.Sidecar = mustJSON(side)
	im.servers()
	im.loadSecurity()
	if err := im.operations(); err != nil {
		return err
	}
	for _, k := range []string{"webhooks", "callbacks"} {
		if im.root.has(k) {
			im.report(Unsupported, "", k, k+" describe calls made to a client and cannot be sent as requests", "")
		}
	}
	return nil
}

func (im *importer) addVar(owner, uid, key, typ, value string) {
	id := owner + "|" + uid + "|" + key
	if im.vars[id] {
		return
	}
	im.vars[id] = true
	v := store.Variable{OwnerKind: owner, OwnerUID: uid, Key: key, Type: typ, InitialValue: value, Enabled: true}
	im.res.Variables = append(im.res.Variables, v)
	im.res.Report.Stats.Variables++
	if typ == store.VarTypeSecret {
		im.res.Report.Stats.SecretVariables++
	}
}
