// Package curl imports curl command lines into the collection model. It is a
// pure-Go shell tokenizer plus an option interpreter; nothing is executed,
// expanded, fetched or read from disk (@file arguments become needs-asset
// entries). Every command becomes one request item in a new collection.
package curl

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// Limits for hostile input.
const (
	MaxCommands = 5000
	MaxURLLen   = 64 << 10
)

// ErrNoCurl is returned when the input holds no curl command.
var ErrNoCurl = errors.New("curl: no curl command found")

// Options tune a parse. NewID defaults to store.NewUID (tests inject a counter).
type Options struct {
	NewID func() string
	Name  string // collection name, default "curl import"
}

// Result is the parsed import, ready to preview and commit.
type Result struct {
	Collection store.Collection `json:"collection"`
	Items      []store.Item     `json:"items"`
	Report     Report           `json:"report"`
}

// Bundle returns the result as a store.CollectionsBundle.
func (r *Result) Bundle() store.CollectionsBundle {
	return store.CollectionsBundle{Version: store.CollectionsBundleVersion,
		Collections: []store.Collection{r.Collection}, Items: r.Items}
}

// Parse imports every curl command found in text.
func Parse(data []byte, opt Options) (*Result, error) {
	if len(data) > MaxInputBytes {
		return nil, ErrTooLarge
	}
	if opt.NewID == nil {
		opt.NewID = store.NewUID
	}
	if opt.Name == "" {
		opt.Name = "curl import"
	}
	toks, err := Tokenize(string(data))
	if err != nil {
		return nil, err
	}
	res := &Result{}
	res.Report.Format = "curl"
	res.Collection = store.Collection{UID: opt.NewID(), Name: opt.Name, ScopePolicy: store.ScopePolicyBlock,
		Sidecar: mustJSON(map[string]string{"format": "curl"})}
	prev := ""
	nonCurl := 0
	for _, cmd := range splitCommands(toks) {
		args, ok := curlArgs(cmd)
		if !ok {
			if len(cmd) > 0 {
				nonCurl++
			}
			continue
		}
		if len(res.Items) >= MaxCommands {
			res.Report.Entries = append(res.Report.Entries, Entry{Level: Blocked, Feature: "too-many-commands",
				Message: fmt.Sprintf("only the first %d curl commands were imported", MaxCommands)})
			break
		}
		it := buildItem(args, &res.Report, opt.NewID, res.Collection.UID, prev, len(res.Items)+1)
		prev = it.Rank
		res.Items = append(res.Items, it)
		res.Report.Stats.Requests++
	}
	if nonCurl > 0 {
		res.Report.Entries = append(res.Report.Entries, Entry{Level: Degraded, Feature: "non-curl-command",
			Message: fmt.Sprintf("%d non-curl command(s) ignored", nonCurl)})
	}
	finishReport(&res.Report)
	res.Collection.ImportReport = mustJSON(res.Report)
	if len(res.Items) == 0 {
		return res, ErrNoCurl
	}
	return res, nil
}

// ParseCommand imports a single curl command (the "paste curl" path). It
// returns the request item (no collection) and its report.
func ParseCommand(cmd string, opt Options) (*store.Item, *Report, error) {
	res, err := Parse([]byte(cmd), opt)
	if err != nil {
		if res != nil {
			return nil, &res.Report, err
		}
		return nil, nil, err
	}
	it := res.Items[0]
	if len(res.Items) > 1 {
		res.Report.Entries = append(res.Report.Entries, Entry{Level: Degraded, Feature: "multiple-commands",
			Message: "only the first of several curl commands was used"})
		finishReport(&res.Report)
	}
	return &it, &res.Report, nil
}

func splitCommands(toks []Token) [][]Token {
	var out [][]Token
	var cur []Token
	for _, t := range toks {
		if t.Sep {
			if len(cur) > 0 {
				out = append(out, cur)
			}
			cur = nil
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// curlArgs strips a shell prompt and VAR=value prefixes and returns the
// arguments after the curl word.
func curlArgs(cmd []Token) ([]Token, bool) {
	i := 0
	for i < len(cmd) && (cmd[i].Text == "$" || cmd[i].Text == ">" || isAssignment(cmd[i].Text)) {
		i++
	}
	if i >= len(cmd) {
		return nil, false
	}
	base := strings.ToLower(path.Base(strings.ReplaceAll(cmd[i].Text, `\`, "/")))
	if base != "curl" && base != "curl.exe" {
		return nil, false
	}
	return cmd[i+1:], true
}

func isAssignment(s string) bool {
	i := strings.IndexByte(s, '=')
	if i <= 0 {
		return false
	}
	for j := 0; j < i; j++ {
		c := s[j]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || (j > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// note collects report entries for one command until its item exists.
type note struct {
	level      Level
	feature    string
	msg        string
	suggestion string
}

func parseArgs(args []Token) (*request, []note) {
	r := &request{}
	var notes []note
	add := func(l Level, f, m, s string) { notes = append(notes, note{l, f, m, s}) }
	endOpts := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case endOpts || a.Text == "-" || a.Text == "" || a.Text[0] != '-':
			r.takeURL(a)
		case a.Text == "--":
			endOpts = true
		case strings.HasPrefix(a.Text, "--"):
			name, val, hasVal := strings.Cut(a.Text[2:], "=")
			spec, ok := longFlags[name]
			if !ok {
				if strings.HasPrefix(name, "no-") {
					continue
				}
				add(Degraded, "unknown-flag", "unknown option --"+name+" ignored", "Check the curl documentation; the request may differ from the original")
				continue
			}
			dyn := a.Dyn
			if spec.arg && !hasVal {
				if i+1 >= len(args) {
					add(Degraded, "missing-value", "option --"+name+" has no value", "")
					continue
				}
				i++
				val, dyn = args[i].Text, args[i].Dyn
			}
			r.apply(spec, val, dyn, add)
		default:
			a2 := a.Text
			for j := 1; j < len(a2); j++ {
				c := a2[j]
				long, known := shortFlags[c]
				if !known {
					add(Degraded, "unknown-flag", "unknown option -"+string(c)+" ignored", "")
					continue
				}
				spec := longFlags[long]
				if spec.name == "" {
					spec = flagSpec{shortWithArg[c], fIgnored, long}
				}
				if shortWithArg[c] {
					spec.arg = true
					val, dyn := a2[j+1:], a.Dyn
					if val == "" {
						if i+1 >= len(args) {
							add(Degraded, "missing-value", "option -"+string(c)+" has no value", "")
							break
						}
						i++
						val, dyn = args[i].Text, args[i].Dyn
					}
					r.apply(spec, val, dyn, add)
					break
				}
				spec.arg = false
				r.apply(spec, "", a.Dyn, add)
			}
		}
	}
	return r, notes
}

func (r *request) takeURL(t Token) {
	if t.Dyn {
		r.dyn = true
	}
	if len(r.urls) == 0 {
		r.urls = []string{t.Text}
		return
	}
	r.extraURLs++
}

func (r *request) apply(spec flagSpec, val string, dyn bool, add func(Level, string, string, string)) {
	if dyn {
		r.dyn = true
	}
	switch spec.name {
	case "request":
		r.method = val
	case "header":
		if !r.addHeader(val) {
			add(Degraded, "header-file", "header argument is not a Name: value pair (file headers are never read)", "")
		}
	case "data", "data-ascii", "data-raw", "data-binary", "data-urlencode", "json":
		r.addData(spec.name, val)
		if spec.name == "json" {
			r.json = true
		}
	case "form", "form-string":
		if !r.addForm(val, spec.name == "form-string") {
			add(Degraded, "form-syntax", "form argument is not name=value", "")
		}
	case "user":
		r.user, r.hasUser = val, true
	case "cookie":
		if strings.Contains(val, "=") {
			if r.cookie != "" {
				r.cookie += "; "
			}
			r.cookie += val
		} else {
			add(Unsupported, "cookie-file", "cookie jar files are never read", "Paste the cookies as name=value pairs or use the collection cookie jar")
		}
	case "get":
		r.get = true
	case "head":
		r.head = true
	case "insecure":
		r.insecure = true
	case "location":
		r.location = true
	case "compressed":
		r.compress = true
	case "digest", "ntlm", "basic":
		r.authKind = spec.name
	case "user-agent":
		r.ua = val
	case "referer":
		r.referer = val
	case "range":
		r.rng = val
	case "url":
		r.takeURL(Token{Text: val, Dyn: dyn})
	case "max-time":
		if r.timeoutMs = parseSeconds(val); r.timeoutMs == nil {
			add(Degraded, "max-time", "invalid --max-time value ignored", "")
		}
	case "max-redirs":
		r.maxRedirs = parseInt(val)
	case "upload-file":
		r.upload = val
	case "oauth2-bearer":
		r.bearer, r.hasBearer = val, true
	default:
		switch spec.class {
		case fIgnored:
			add(Degraded, "flag:"+spec.name, "option --"+spec.name+" has no effect on a collection request and was ignored", "")
		case fUnsupp:
			add(Unsupported, "flag:"+spec.name, "option --"+spec.name+" cannot be represented and was dropped", "Configure it in Interseptor settings (proxy, client certificates, resolver)")
		}
	}
}
