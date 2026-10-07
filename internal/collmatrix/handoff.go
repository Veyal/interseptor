package collmatrix

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/intruder"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// StarterPayloads are used when a position has no dataset column or explicit
// list. They are a generic starting point for identifier probing; replace them
// with real candidates.
var StarterPayloads = []string{"0", "1", "2", "3", "-1", "99999999", "null", "true", "../", "'"}

// HandoffOptions say how to turn an item into an Intruder attack.
type HandoffOptions struct {
	// Positions are the variable names to fuzz. Empty means every {{var}} that
	// remains unresolved or is not a base URL/host variable of the item.
	Positions []string `json:"positions,omitempty"`
	// Dataset holds payload columns by variable name (for example from the
	// runner's data file). Payloads are used for positions without a column.
	Dataset map[string][]string `json:"dataset,omitempty"`
	// Payloads is the explicit payload list for every position without a column.
	Payloads   []string `json:"payloads,omitempty"`
	AttackType string   `json:"attackType,omitempty"` // sniper | pitchfork | cluster
	// Values resolves non-position variables (current-else-initial). Secret
	// variables are only here when the caller opted in.
	Values map[string]string `json:"-"`
	// Secret names variables whose values must not be written into the
	// template; they stay {{placeholders}} with a note.
	Secret map[string]bool `json:"-"`
}

// Handoff is an Intruder attack built from a collection item.
type Handoff struct {
	Target     string     `json:"target"`
	Template   string     `json:"template"`
	AttackType string     `json:"attackType"`
	Positions  []string   `json:"positions"` // variable name of each section marker, in template order
	Payloads   [][]string `json:"payloads"`
	Notes      []string   `json:"notes,omitempty"`
}

// Spec converts the handoff to an Intruder attack specification.
func (h Handoff) Spec() intruder.Spec {
	return intruder.Spec{Target: h.Target, Template: h.Template, AttackType: h.AttackType, Payloads: h.Payloads}
}

var varRef = regexp.MustCompile(`\{\{\s*([^{}\s|]+)(?:\s*\|[^{}]*)?\s*\}\}`)

type kv struct {
	Key, Value string
	Off        bool
}

func decodeKVs(raw json.RawMessage) []kv {
	var in []struct {
		Key      string `json:"key"`
		Value    any    `json:"value"`
		Disabled bool   `json:"disabled"`
		Enabled  *bool  `json:"enabled"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &in) != nil {
		return nil
	}
	out := make([]kv, 0, len(in))
	for _, k := range in {
		v := ""
		switch x := k.Value.(type) {
		case nil:
		case string:
			v = x
		default:
			b, _ := json.Marshal(x)
			v = string(b)
		}
		out = append(out, kv{k.Key, v, k.Disabled || (k.Enabled != nil && !*k.Enabled)})
	}
	return out
}

func itemURL(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Raw string `json:"raw"`
	}
	_ = json.Unmarshal(raw, &o)
	return o.Raw
}

type handoffBuilder struct {
	o      HandoffOptions
	pos    map[string]bool
	order  []string // variable name of every marker in emission order
	notes  []string
	noted  map[string]bool
	unres  map[string]bool
	secret map[string]bool
}

func (b *handoffBuilder) note(s string) {
	if !b.noted[s] {
		b.noted[s] = true
		b.notes = append(b.notes, s)
	}
}

// subst resolves {{var}} references in s. Position variables become a §value§
// section; other known variables are inlined; the rest stay as they are.
func (b *handoffBuilder) subst(s string) string {
	return varRef.ReplaceAllStringFunc(s, func(m string) string {
		name := varRef.FindStringSubmatch(m)[1]
		if strings.HasPrefix(name, "$") {
			b.note("dynamic variable {{" + name + "}} is not resolved by Intruder")
			return m
		}
		val, have := b.o.Values[name]
		if b.pos[name] {
			b.order = append(b.order, name)
			if !have {
				val = ""
			}
			return "§" + strings.ReplaceAll(val, "§", "") + "§"
		}
		if b.o.Secret[name] {
			b.note("secret variable {{" + name + "}} left as a placeholder; add the credential in the template yourself")
			return m
		}
		if have {
			return val
		}
		b.unres[name] = true
		b.note("variable {{" + name + "}} is not resolved; it will be sent literally")
		return m
	})
}

// BuildHandoff turns a request item into an Intruder attack. The request is
// assembled from the item (method, URL, query, headers, raw/urlencoded/GraphQL
// body, basic/bearer/apikey auth); multipart and file bodies are not supported.
func BuildHandoff(it store.Item, o HandoffOptions) (*Handoff, error) {
	if it.Kind == "folder" {
		return nil, errors.New("collmatrix: a folder cannot be sent to Intruder")
	}
	b := &handoffBuilder{o: o, pos: map[string]bool{}, noted: map[string]bool{}, unres: map[string]bool{}}
	for _, p := range o.Positions {
		if p = strings.TrimSpace(strings.Trim(strings.TrimSpace(p), "{}")); p != "" {
			b.pos[p] = true
		}
	}
	rawURL := itemURL(it.URL)
	if rawURL == "" {
		return nil, errors.New("collmatrix: the request has no URL")
	}
	if len(b.pos) == 0 {
		// Default: fuzz every variable of the URL path/query and body that is
		// not the base of the URL and has no declared header role.
		for _, n := range defaultPositions(it, rawURL) {
			b.pos[n] = true
		}
	}
	if len(b.pos) == 0 {
		return nil, errors.New("collmatrix: no variable to fuzz; pass positions")
	}

	// Resolve the target from the URL with only the non-position variables.
	probe := varRef.ReplaceAllStringFunc(rawURL, func(m string) string {
		name := varRef.FindStringSubmatch(m)[1]
		if v, ok := o.Values[name]; ok && !o.Secret[name] {
			return v
		}
		return m
	})
	u, err := url.Parse(strings.ReplaceAll(strings.ReplaceAll(probe, "§", ""), "{{", "%7B%7B"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("collmatrix: cannot determine the target host; resolve the base URL variable first")
	}
	if strings.Contains(u.Host, "%7B") {
		return nil, errors.New("collmatrix: the host contains an unresolved variable; resolve it first")
	}
	target := u.Scheme + "://" + u.Host

	// Build the path+query text from the original template so positions land in it.
	rest := rawURL
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
		if j := strings.IndexAny(rest, "/?#"); j >= 0 {
			rest = rest[j:]
		} else {
			rest = "/"
		}
	} else if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		// {{baseUrl}}/path: the leading variable is the target; keep its own
		// path prefix (for example /v1) in front of the rest.
		basePath := ""
		if bu, perr := url.Parse(varRef.ReplaceAllStringFunc(rest[:j], func(m string) string { return o.Values[varRef.FindStringSubmatch(m)[1]] })); perr == nil {
			basePath = strings.TrimRight(bu.Path, "/")
		}
		rest = rest[j:]
		if strings.HasPrefix(rest, "?") {
			rest = "/" + rest
		}
		rest = basePath + rest
	}
	rest = strings.SplitN(rest, "#", 2)[0]
	path := rest
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if params := decodeKVs(it.Params); len(params) > 0 {
		path = strings.SplitN(path, "?", 2)[0]
		var q []string
		for _, p := range params {
			if !p.Off && p.Key != "" {
				q = append(q, p.Key+"="+p.Value)
			}
		}
		if len(q) > 0 {
			path += "?" + strings.Join(q, "&")
		}
	}
	method := strings.ToUpper(strings.TrimSpace(it.Method))
	if method == "" {
		method = "GET"
	}

	var lines []string
	lines = append(lines, method+" "+b.subst(path)+" HTTP/1.1", "Host: "+u.Host)
	haveCT := false
	for _, h := range decodeKVs(it.Headers) {
		if h.Off || h.Key == "" || strings.EqualFold(h.Key, "host") || strings.EqualFold(h.Key, "content-length") {
			continue
		}
		haveCT = haveCT || strings.EqualFold(h.Key, "content-type")
		lines = append(lines, h.Key+": "+b.subst(h.Value))
	}
	if al, ok := b.authLine(it.Auth); ok {
		lines = append(lines, al)
	}
	body, ct := b.body(it.Body)
	if ct != "" && !haveCT {
		lines = append(lines, "Content-Type: "+ct)
	}
	tpl := strings.Join(lines, "\r\n") + "\r\n\r\n" + body
	if len(b.order) == 0 {
		return nil, errors.New("collmatrix: none of the chosen variables appears in the request")
	}
	h := &Handoff{Target: target, Template: tpl, Positions: b.order, Notes: b.notes}
	h.AttackType = normaliseAttack(o.AttackType, len(b.order))
	h.Payloads = payloadsFor(h, o)
	for _, n := range h.Positions {
		if len(o.Dataset[n]) == 0 && len(o.Payloads) == 0 {
			h.Notes = append(h.Notes, "no payloads for {{"+n+"}}: using the starter list")
			break
		}
	}
	sort.Strings(h.Notes)
	return h, nil
}

func normaliseAttack(t string, positions int) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "pitchfork", "cluster":
		if positions > 1 {
			return strings.ToLower(strings.TrimSpace(t))
		}
	}
	return "sniper"
}

// payloadsFor builds the payload lists: sniper uses one list (the union of the
// positions' columns, else explicit, else starter); pitchfork and cluster use
// one list per position.
func payloadsFor(h *Handoff, o HandoffOptions) [][]string {
	pick := func(name string) []string {
		if c := o.Dataset[name]; len(c) > 0 {
			return c
		}
		if len(o.Payloads) > 0 {
			return o.Payloads
		}
		return StarterPayloads
	}
	if h.AttackType == "sniper" {
		seen := map[string]bool{}
		var list []string
		for _, n := range h.Positions {
			for _, v := range pick(n) {
				if !seen[v] {
					seen[v] = true
					list = append(list, v)
				}
			}
		}
		return [][]string{list}
	}
	var out [][]string
	for _, n := range h.Positions {
		out = append(out, pick(n))
	}
	return out
}

// defaultPositions picks every variable that appears after the host in the URL
// and in the body (not headers, not the host/base part).
func defaultPositions(it store.Item, rawURL string) []string {
	var names []string
	seen := map[string]bool{}
	add := func(s string) {
		for _, m := range varRef.FindAllStringSubmatch(s, -1) {
			n := m[1]
			if !strings.HasPrefix(n, "$") && !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	rest := rawURL
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if j := strings.IndexAny(rest, "/?"); j >= 0 {
		add(rest[j:])
	}
	for _, p := range decodeKVs(it.Params) {
		if !p.Off {
			add(p.Value)
		}
	}
	add(string(it.Body))
	return names
}

func (b *handoffBuilder) authLine(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var a struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &a) != nil {
		return "", false
	}
	field := func(typ, key string) string {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		var arr []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		if json.Unmarshal(m[typ], &arr) == nil {
			for _, f := range arr {
				if f.Key == key {
					return fmt.Sprint(orEmpty(f.Value))
				}
			}
			return ""
		}
		var obj map[string]any
		if json.Unmarshal(m[typ], &obj) == nil {
			return fmt.Sprint(orEmpty(obj[key]))
		}
		return ""
	}
	switch strings.ToLower(a.Type) {
	case "bearer":
		return "Authorization: Bearer " + b.subst(field("bearer", "token")), true
	case "apikey":
		if in := field("apikey", "in"); in == "" || in == "header" {
			return field("apikey", "key") + ": " + b.subst(field("apikey", "value")), true
		}
		b.note("API key in the query is not applied; add it to the template")
	case "basic":
		b.note("basic auth is not applied; add the Authorization header to the template")
	case "", "inherit", "none", "noauth":
	default:
		b.note("auth type " + a.Type + " is not applied; add the credential to the template")
	}
	return "", false
}

func (b *handoffBuilder) body(raw json.RawMessage) (string, string) {
	if len(raw) == 0 {
		return "", ""
	}
	var o struct {
		Mode       string          `json:"mode"`
		Raw        string          `json:"raw"`
		URLEncoded json.RawMessage `json:"urlencoded"`
		GraphQL    json.RawMessage `json:"graphql"`
		Options    struct {
			Raw struct {
				Language string `json:"language"`
			} `json:"raw"`
		} `json:"options"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return "", ""
	}
	switch strings.ToLower(o.Mode) {
	case "raw":
		ct := "text/plain"
		switch strings.ToLower(o.Options.Raw.Language) {
		case "json":
			ct = "application/json"
		case "xml":
			ct = "application/xml"
		case "html":
			ct = "text/html"
		}
		return b.subst(o.Raw), ct
	case "urlencoded":
		var parts []string
		for _, p := range decodeKVs(o.URLEncoded) {
			if !p.Off && p.Key != "" {
				parts = append(parts, url.QueryEscape(p.Key)+"="+b.subst(p.Value))
			}
		}
		return strings.Join(parts, "&"), "application/x-www-form-urlencoded"
	case "graphql":
		var g struct {
			Query     string `json:"query"`
			Variables any    `json:"variables"`
		}
		_ = json.Unmarshal(o.GraphQL, &g)
		payload := map[string]any{"query": g.Query}
		if g.Variables != nil {
			payload["variables"] = g.Variables
		}
		j, _ := json.Marshal(payload)
		return b.subst(string(j)), "application/json"
	case "formdata", "file":
		b.note("multipart and file bodies are not supported; the body was left out")
	}
	return "", ""
}

// Handoff builds an Intruder attack from a collection item, resolving the
// non-position variables from the stored layers (current-else-initial values).
// Secret variables stay placeholders unless includeSecrets is set; the control
// layer must never set it for AI callers.
func (s *Service) Handoff(collectionUID, itemUID, envUID string, o HandoffOptions, includeSecrets bool) (*Handoff, error) {
	coll, items, err := s.d.Backend.Load(collectionUID)
	if err != nil {
		return nil, err
	}
	chain, err := collexec.ChainFromItems(coll, items, itemUID)
	if err != nil {
		return nil, err
	}
	layers, local, err := s.d.Backend.Layers(chain, envUID)
	if err != nil {
		return nil, err
	}
	stack := varstore.NewStack(layers...)
	o.Values, o.Secret = map[string]string{}, map[string]bool{}
	for _, n := range stack.Names() {
		v, _, ok := stack.Lookup(n)
		if !ok {
			continue
		}
		if v.Secret && !includeSecrets {
			o.Secret[n] = true
			continue
		}
		o.Values[n] = v.Value
	}
	for k, v := range local {
		o.Values[k] = v
	}
	h, err := BuildHandoff(chain.Item, o)
	if err != nil {
		return nil, err
	}
	h.Target = s.scrub(h.Target)
	if !includeSecrets {
		h.Template = s.scrub(h.Template)
	}
	return h, nil
}
