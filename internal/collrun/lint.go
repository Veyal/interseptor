package collrun

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/pmsandbox"
	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// Lint finding levels.
const (
	LintError = "error"
	LintWarn  = "warn"
	LintInfo  = "info"
)

// Lint finding codes.
const (
	LintUnresolved   = "unresolved_variable"
	LintUnapproved   = "script_unapproved"
	LintCredential   = "embedded_credential"
	LintUnsupported  = "unsupported_api"
	LintScriptFlag   = "script_flag"
	LintScriptSetVar = "variable_set_by_script"
)

// LintFinding is one lint observation. Messages never contain a credential
// value, only its kind and length.
type LintFinding struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	ItemUID string `json:"itemUid,omitempty"`
	Path    string `json:"path,omitempty"` // "Folder / Request"
	Message string `json:"message"`
	Hash    string `json:"hash,omitempty"` // script hash, for --trust-hash
}

// LintReport is the result of linting a collection.
type LintReport struct {
	CollectionUID  string        `json:"collectionUid"`
	CollectionName string        `json:"collectionName"`
	Requests       int           `json:"requests"`
	Scripts        int           `json:"scripts"`
	Findings       []LintFinding `json:"findings"`
	Errors         int           `json:"errors"`
	Warnings       int           `json:"warnings"`
}

// ExitCode is ExitImportLint when there are errors (or warnings when strict).
func (r *LintReport) ExitCode(strict bool) int {
	if r.Errors > 0 || (strict && r.Warnings > 0) {
		return ExitImportLint
	}
	return ExitPass
}

// LintOptions tune a lint pass.
type LintOptions struct {
	EnvUID string
	// DataColumns are iteration-data column names, which count as defined.
	DataColumns []string
	// Local variables (CLI --env-var) count as defined.
	Local map[string]string
}

var (
	tplRe       = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)
	setVarRe    = regexp.MustCompile("(?:\\.set|setEnvironmentVariable|setGlobalVariable)\\(\\s*['\"`]([^'\"`]+)['\"`]")
	sensitiveH  = regexp.MustCompile(`(?i)^(proxy-)?authorization$|^cookie$|^x-api-key$|^api-key$|^x-auth-token$|^x-access-token$|^x-csrf-token$|^x-xsrf-token$`)
	sensitiveK  = regexp.MustCompile(`(?i)(token|secret|passw(or)?d|pwd|api[_-]?key|access[_-]?key|private[_-]?key|credential)`)
	userinfoRe  = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.\-]*://[^/\s:@{]+:[^/\s@{]+@`)
	sensitiveQS = regexp.MustCompile(`(?i)[?&](access_token|token|api[_-]?key|apikey|secret|password|passwd|auth|sig|signature)=([^&#{\s]+)`)
)

// Lint checks a collection the way a CI gate wants: unresolved variables,
// unapproved scripts, embedded credentials and script APIs the sandbox does
// not ship. It never executes a script or sends a request.
func Lint(b Backend, collectionUID string, opt LintOptions) (*LintReport, error) {
	coll, items, err := b.Load(collectionUID)
	if err != nil {
		return nil, err
	}
	rep := &LintReport{CollectionUID: coll.UID, CollectionName: coll.Name, Findings: []LintFinding{}}
	scripts := CollectionScripts(coll, items)
	rep.Scripts = len(scripts)
	setByScript := scriptSetVars(scripts)
	add := func(f LintFinding) {
		rep.Findings = append(rep.Findings, f)
		switch f.Level {
		case LintError:
			rep.Errors++
		case LintWarn:
			rep.Warnings++
		}
	}

	lintScripts(b, coll, items, scripts, add)
	lintCollectionLevel(coll, add)

	for _, it := range PlanItems(items, "", nil) {
		rep.Requests++
		path := itemPath(items, it)
		chain, err := collexec.ChainFromItems(coll, items, it.UID)
		if err != nil {
			continue
		}
		defined, err := definedNames(b, chain, opt)
		if err != nil {
			add(LintFinding{Level: LintError, Code: LintUnresolved, ItemUID: it.UID, Path: path, Message: "cannot load variables: " + err.Error()})
			continue
		}
		strs := itemStrings(it)
		for _, name := range unresolvedNames(strs, defined) {
			if setByScript[name] {
				add(LintFinding{Level: LintInfo, Code: LintScriptSetVar, ItemUID: it.UID, Path: path,
					Message: fmt.Sprintf("{{%s}} is only set by a script at run time", name)})
				continue
			}
			add(LintFinding{Level: LintError, Code: LintUnresolved, ItemUID: it.UID, Path: path,
				Message: fmt.Sprintf("{{%s}} is not defined in any variable scope", name)})
		}
		for _, msg := range credentialIssues(it) {
			add(LintFinding{Level: LintError, Code: LintCredential, ItemUID: it.UID, Path: path, Message: msg})
		}
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool { return levelRank(rep.Findings[i].Level) < levelRank(rep.Findings[j].Level) })
	return rep, nil
}

func levelRank(l string) int {
	switch l {
	case LintError:
		return 0
	case LintWarn:
		return 1
	}
	return 2
}

func itemPath(items []store.Item, it store.Item) string {
	if p := FolderPath(items, it.UID); p != "" {
		return p + " / " + it.Name
	}
	return it.Name
}

// scriptSetVars collects variable names scripts assign by string literal.
func scriptSetVars(scripts []ScriptRef) map[string]bool {
	out := map[string]bool{}
	for _, s := range scripts {
		for _, m := range setVarRe.FindAllStringSubmatch(s.Source, -1) {
			out[m[1]] = true
		}
	}
	return out
}

func lintScripts(b Backend, coll store.Collection, items []store.Item, scripts []ScriptRef, add func(LintFinding)) {
	seen := map[string]bool{}
	for _, s := range scripts {
		path := s.Name
		if s.ItemUID != "" {
			for _, it := range items {
				if it.UID == s.ItemUID {
					path = itemPath(items, it)
				}
			}
		} else {
			path = coll.Name + " (collection)"
		}
		path += " / " + s.Listen
		if !b.Trusted(coll.UID, s.Hash) && !seen["t"+s.Hash] {
			seen["t"+s.Hash] = true
			add(LintFinding{Level: LintWarn, Code: LintUnapproved, ItemUID: s.ItemUID, Path: path, Hash: s.Hash,
				Message: "script is not approved and will not run (trust it in the UI, or pin it with --trust-hash)"})
		}
		an := pmsandbox.Analyze(s.Source)
		for _, f := range an.Unsupported {
			if f.Kind == "stub" {
				continue
			}
			add(LintFinding{Level: LintError, Code: LintUnsupported, ItemUID: s.ItemUID, Path: path, Hash: s.Hash,
				Message: fmt.Sprintf("line %d: %s is not supported (%s)", f.Line, f.API, strings.TrimSpace(f.Note))})
		}
		for _, f := range an.Flags {
			add(LintFinding{Level: LintWarn, Code: LintScriptFlag, ItemUID: s.ItemUID, Path: path, Hash: s.Hash,
				Message: fmt.Sprintf("line %d: %s (%s)", f.Line, f.Name, f.Note)})
		}
	}
}

// lintCollectionLevel checks the collection's own auth for literal credentials.
func lintCollectionLevel(coll store.Collection, add func(LintFinding)) {
	for _, msg := range authCredentialIssues(coll.Auth) {
		add(LintFinding{Level: LintError, Code: LintCredential, Path: coll.Name + " (collection auth)", Message: msg})
	}
}

// definedNames returns every variable name resolvable for the chain.
func definedNames(b Backend, chain collexec.Chain, opt LintOptions) (map[string]bool, error) {
	layers, local, err := b.Layers(chain, opt.EnvUID)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, n := range varstore.NewStack(layers...).Names() {
		out[n] = true
	}
	for k := range local {
		out[k] = true
	}
	for k := range opt.Local {
		out[k] = true
	}
	for _, c := range opt.DataColumns {
		out[c] = true
	}
	return out, nil
}

// itemStrings flattens every string a request template can carry.
func itemStrings(it store.Item) []string {
	var out []string
	for _, raw := range []json.RawMessage{it.URL, it.Headers, it.Params, it.Body, it.Auth} {
		out = append(out, jsonStrings(raw)...)
	}
	return out
}

func jsonStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			out = append(out, t)
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k])
			}
		}
	}
	walk(v)
	return out
}

// unresolvedNames lists {{names}} used in strs that nothing defines. Dynamic
// variables ({{$guid}}) are always defined; pipes are stripped.
func unresolvedNames(strs []string, defined map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range strs {
		for _, m := range tplRe.FindAllStringSubmatch(s, -1) {
			name := strings.TrimSpace(strings.SplitN(m[1], "|", 2)[0])
			if name == "" || strings.HasPrefix(name, "$") || strings.ContainsAny(name, " ()") {
				continue
			}
			if !defined[name] && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func hasTemplate(s string) bool { return strings.Contains(s, "{{") }

// credentialIssues finds literal credentials in a request. Messages describe
// the location and kind only, never the value.
func credentialIssues(it store.Item) []string {
	var out []string
	var hs []struct {
		Key      string `json:"key"`
		Value    any    `json:"value"`
		Disabled bool   `json:"disabled"`
	}
	if json.Unmarshal(it.Headers, &hs) == nil {
		for _, h := range hs {
			v, _ := h.Value.(string)
			if h.Disabled || v == "" || hasTemplate(v) {
				continue
			}
			if sensitiveH.MatchString(h.Key) {
				out = append(out, fmt.Sprintf("header %s carries a literal credential (%d chars); use a secret variable", h.Key, len(v)))
			}
		}
	}
	for _, s := range jsonStrings(it.URL) {
		if userinfoRe.MatchString(s) && !hasTemplate(s[:strings.Index(s, "@")]) {
			out = append(out, "URL contains user:password@ credentials")
		}
		for _, m := range sensitiveQS.FindAllStringSubmatch(s, -1) {
			if !hasTemplate(m[2]) {
				out = append(out, fmt.Sprintf("query parameter %s carries a literal credential (%d chars)", m[1], len(m[2])))
			}
		}
	}
	out = append(out, authCredentialIssues(it.Auth)...)
	for _, s := range itemStrings(it) {
		for _, h := range redact.Scan(s) {
			out = append(out, fmt.Sprintf("a %s (%d chars) is embedded in the request", h.Kind, h.Len))
		}
	}
	return dedupe(out)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// authCredentialIssues checks an auth block ({"type":"bearer","bearer":[{key,value}]}
// or an object form) for literal secret fields.
func authCredentialIssues(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var o map[string]json.RawMessage
	if json.Unmarshal(raw, &o) != nil {
		return nil
	}
	var typ string
	_ = json.Unmarshal(o["type"], &typ)
	var out []string
	check := func(key, val string) {
		if val == "" || hasTemplate(val) || !sensitiveK.MatchString(key) {
			return
		}
		out = append(out, fmt.Sprintf("auth (%s) field %q holds a literal credential (%d chars); use a secret variable", typ, key, len(val)))
	}
	sub, ok := o[strings.ToLower(typ)]
	if !ok {
		return nil
	}
	var arr []struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	if json.Unmarshal(sub, &arr) == nil {
		for _, kv := range arr {
			v, _ := kv.Value.(string)
			check(kv.Key, v)
		}
		return out
	}
	var m map[string]any
	if json.Unmarshal(sub, &m) == nil {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, _ := m[k].(string)
			check(k, v)
		}
	}
	return out
}
