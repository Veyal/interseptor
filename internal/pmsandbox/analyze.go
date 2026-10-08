package pmsandbox

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed pm_coverage.json
var coverageJSON []byte

type objectCov struct {
	Members []string `json:"members"`
	Chain   string   `json:"chain"`
}

// Coverage is the parsed pm_coverage.json.
type Coverage struct {
	Objects       map[string]objectCov `json:"objects"`
	ChainWords    []string             `json:"chain_words"`
	Chai          []string             `json:"chai"`
	ResponseChain []string             `json:"response_chain"`
	Unsupported   map[string]string    `json:"unsupported"`
	Stubs         map[string]string    `json:"stubs"`
	Globals       struct {
		Supported   []string          `json:"supported"`
		Unsupported map[string]string `json:"unsupported"`
	} `json:"globals"`
	Modules struct {
		Supported []string          `json:"supported"`
		Deferred  map[string]string `json:"deferred"`
		Forbidden map[string]string `json:"forbidden"`
	} `json:"modules"`

	sets map[string]map[string]bool
}

var (
	covOnce sync.Once
	cov     *Coverage
)

// LoadCoverage returns the embedded coverage data (parsed once).
func LoadCoverage() *Coverage {
	covOnce.Do(func() {
		c := &Coverage{sets: map[string]map[string]bool{}}
		if err := json.Unmarshal(coverageJSON, c); err != nil {
			panic("pmsandbox: bad pm_coverage.json: " + err.Error())
		}
		set := func(name string, l []string) {
			m := map[string]bool{}
			for _, s := range l {
				m[s] = true
			}
			c.sets[name] = m
		}
		set("chain", c.ChainWords)
		set("chai", c.Chai)
		set("resp", c.ResponseChain)
		set("globals", c.Globals.Supported)
		set("modules", c.Modules.Supported)
		for k, o := range c.Objects {
			set("obj:"+k, o.Members)
		}
		cov = c
	})
	return cov
}

// Finding is one analyser observation.
type Finding struct {
	API  string `json:"api"`
	Line int    `json:"line"`
	Kind string `json:"kind"` // unsupported|stub|module-deferred|module-forbidden
	Note string `json:"note,omitempty"`
}

// Flag marks a risky construct reviewers should look at.
type Flag struct {
	Name string `json:"name"` // eval|function-constructor|infinite-loop|obfuscation|dynamic-require|timer-loop
	Line int    `json:"line"`
	Note string `json:"note,omitempty"`
}

// HostRef is a literal host found in a string.
type HostRef struct {
	Host string `json:"host"`
	Line int    `json:"line"`
}

// Report is the result of analysing one script without running it.
type Report struct {
	APIs        []string  `json:"apis"`        // supported pm.*/global APIs used (deduplicated)
	Modules     []string  `json:"modules"`     // require() literals
	Unsupported []Finding `json:"unsupported"` // would fail at run time with status "unsupported"
	Hosts       []HostRef `json:"hosts"`
	Flags       []Flag    `json:"flags"`
	Bytes       int       `json:"bytes"`
	Lines       int       `json:"lines"`
}

// HasUnsupported reports whether running the script would hit an API the
// sandbox does not ship (stubs do not count: they run as no-ops).
func (r Report) HasUnsupported() bool {
	for _, f := range r.Unsupported {
		if f.Kind != "stub" {
			return true
		}
	}
	return false
}

type tok struct {
	kind string // ident|str|punct|num
	text string
	line int
}

var hostRe = regexp.MustCompile(`(?i)\b(?:https?|wss?)://([a-z0-9._\-\[\]:]+)`)

// Analyze reads src (never executes it) and lists what it touches.
func Analyze(src string) Report {
	c := LoadCoverage()
	rep := Report{Bytes: len(src), Lines: strings.Count(src, "\n") + 1}
	toks, strs := lex(src)
	apis := map[string]bool{}
	mods := map[string]bool{}
	seenUnsup := map[string]bool{}
	addUnsup := func(f Finding) {
		k := f.Kind + "|" + f.API
		if !seenUnsup[k] {
			seenUnsup[k] = true
			rep.Unsupported = append(rep.Unsupported, f)
		}
	}

	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind != "ident" {
			continue
		}
		// Skip identifiers that are themselves property names (handled by the chain that owns them).
		if i > 0 && toks[i-1].kind == "punct" && (toks[i-1].text == "." || toks[i-1].text == "?.") {
			continue
		}
		path := []string{t.text}
		j := i + 1
		for j+1 < len(toks) && toks[j].kind == "punct" && (toks[j].text == "." || toks[j].text == "?.") && toks[j+1].kind == "ident" {
			path = append(path, toks[j+1].text)
			j += 2
		}
		call := j < len(toks) && toks[j].kind == "punct" && toks[j].text == "("
		head := path[0]

		switch {
		case head == "eval" && len(path) == 1 && call:
			rep.Flags = append(rep.Flags, Flag{Name: "eval", Line: t.line, Note: "eval() runs dynamically built code"})
		case head == "Function" && (call || (i > 0 && toks[i-1].text == "new")):
			rep.Flags = append(rep.Flags, Flag{Name: "function-constructor", Line: t.line, Note: "Function constructor builds code from strings"})
		case head == "while" && matchSeq(toks, i+1, "(", "true", ")"):
			rep.Flags = append(rep.Flags, Flag{Name: "infinite-loop", Line: t.line, Note: "while(true) only ends by break or timeout"})
		case head == "for" && matchSeq(toks, i+1, "(", ";", ";", ")"):
			rep.Flags = append(rep.Flags, Flag{Name: "infinite-loop", Line: t.line, Note: "for(;;) only ends by break or timeout"})
		case head == "setInterval":
			rep.Flags = append(rep.Flags, Flag{Name: "timer-loop", Line: t.line, Note: "setInterval repeats until the virtual-time cap"})
		case head == "fromCharCode" || (len(path) > 1 && path[len(path)-1] == "fromCharCode"):
			rep.Flags = append(rep.Flags, Flag{Name: "obfuscation", Line: t.line, Note: "String.fromCharCode building text"})
		}

		if head == "require" && len(path) == 1 && call {
			if j+2 < len(toks) && toks[j+1].kind == "str" && toks[j+2].text == ")" {
				name := strings.TrimPrefix(toks[j+1].text, "node:")
				mods[name] = true
				switch {
				case c.sets["modules"][name]:
				case c.Modules.Deferred[name] != "":
					addUnsup(Finding{API: "require(" + jsonStr(name) + ")", Line: t.line, Kind: "module-deferred", Note: c.Modules.Deferred[name]})
				case c.Modules.Forbidden[name] != "":
					addUnsup(Finding{API: "require(" + jsonStr(name) + ")", Line: t.line, Kind: "module-forbidden", Note: c.Modules.Forbidden[name]})
				default:
					addUnsup(Finding{API: "require(" + jsonStr(name) + ")", Line: t.line, Kind: "unsupported", Note: "module is not shipped"})
				}
			} else {
				rep.Flags = append(rep.Flags, Flag{Name: "dynamic-require", Line: t.line, Note: "require() with a non-literal argument cannot be checked"})
				addUnsup(Finding{API: "require(<dynamic>)", Line: t.line, Kind: "unsupported", Note: "dynamic require is not available"})
			}
		}

		if note, ok := c.Globals.Unsupported[head]; ok && head != "require" {
			addUnsup(Finding{API: head, Line: t.line, Kind: "unsupported", Note: note})
		}

		if head == "pm" || head == "isp" {
			checkPM(c, path, t.line, apis, addUnsup)
			if head == "pm" && len(path) == 2 && path[1] == "expect" && call {
				checkChain(c, toks, j, t.line, addUnsup)
			}
		} else if c.sets["globals"][head] {
			apis[head] = true
		}
		i = j - 1
	}

	for _, s := range strs {
		for _, m := range hostRe.FindAllStringSubmatch(s.text, -1) {
			h := strings.ToLower(m[1])
			if strings.Contains(h, "{{") {
				continue
			}
			rep.Hosts = append(rep.Hosts, HostRef{Host: h, Line: s.line})
		}
		if len(s.text) >= 500 && isBlobLike(s.text) {
			rep.Flags = append(rep.Flags, Flag{Name: "obfuscation", Line: s.line, Note: "long encoded string literal"})
		}
		if n := strings.Count(s.text, `\x`) + strings.Count(s.text, `\u`); n >= 20 {
			rep.Flags = append(rep.Flags, Flag{Name: "obfuscation", Line: s.line, Note: "many escape sequences in one string"})
		}
	}

	rep.APIs = sortedKeys(apis)
	rep.Modules = sortedKeys(mods)
	sort.SliceStable(rep.Unsupported, func(i, j int) bool { return rep.Unsupported[i].Line < rep.Unsupported[j].Line })
	rep.Hosts = dedupHosts(rep.Hosts)
	rep.Flags = dedupFlags(rep.Flags)
	return rep
}

func checkPM(c *Coverage, path []string, line int, apis map[string]bool, addUnsup func(Finding)) {
	// Explicitly unsupported or stubbed prefixes win.
	for n := 1; n <= len(path); n++ {
		p := strings.Join(path[:n], ".")
		if note, ok := c.Unsupported[p]; ok {
			addUnsup(Finding{API: p, Line: line, Kind: "unsupported", Note: note})
			return
		}
		if note, ok := c.Stubs[p]; ok {
			addUnsup(Finding{API: p, Line: line, Kind: "stub", Note: note})
			return
		}
	}
	if path[0] == "isp" {
		apis[strings.Join(path[:min(len(path), 2)], ".")] = true
		return
	}
	// Longest known object prefix, then the next segment must be a member.
	for n := len(path) - 1; n >= 1; n-- {
		obj := strings.Join(path[:n], ".")
		o, ok := c.Objects[obj]
		if !ok {
			continue
		}
		next := path[n]
		if o.Chain != "" {
			for _, seg := range path[n:] {
				if !c.sets["chain"][seg] && !c.sets["chai"][seg] && !c.sets["resp"][seg] {
					addUnsup(Finding{API: obj + "." + seg, Line: line, Kind: "unsupported", Note: "assertion is not in the Chai subset"})
					return
				}
			}
		} else if !c.sets["obj:"+obj][next] {
			addUnsup(Finding{API: obj + "." + next, Line: line, Kind: "unsupported", Note: "member is not shipped"})
			return
		}
		apis[strings.Join(path[:min(len(path), n+1)], ".")] = true
		return
	}
	if len(path) == 1 {
		apis["pm"] = true
	}
}

// checkChain validates the assertion chain after pm.expect(...). toks[open]
// is the "(" of the expect call.
func checkChain(c *Coverage, toks []tok, open, line int, addUnsup func(Finding)) {
	depth := 0
	k := open
	for ; k < len(toks); k++ {
		if toks[k].kind != "punct" {
			continue
		}
		switch toks[k].text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
		}
		if depth == 0 {
			break
		}
	}
	k++
	for k+1 < len(toks) && toks[k].kind == "punct" && (toks[k].text == "." || toks[k].text == "?.") && toks[k+1].kind == "ident" {
		seg := toks[k+1].text
		if !c.sets["chain"][seg] && !c.sets["chai"][seg] && !c.sets["resp"][seg] {
			addUnsup(Finding{API: "chai ." + seg, Line: toks[k+1].line, Kind: "unsupported", Note: "assertion is not in the Chai subset"})
		}
		k += 2
		if k < len(toks) && toks[k].kind == "punct" && toks[k].text == "(" { // skip call arguments
			d := 0
			for ; k < len(toks); k++ {
				if toks[k].kind == "punct" {
					if toks[k].text == "(" {
						d++
					} else if toks[k].text == ")" {
						d--
						if d == 0 {
							k++
							break
						}
					}
				}
			}
		}
	}
}

func matchSeq(toks []tok, at int, want ...string) bool {
	for i, w := range want {
		if at+i >= len(toks) || toks[at+i].text != w {
			return false
		}
	}
	return true
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedupHosts(h []HostRef) []HostRef {
	seen := map[string]bool{}
	var out []HostRef
	for _, x := range h {
		if !seen[x.Host] {
			seen[x.Host] = true
			out = append(out, x)
		}
	}
	return out
}

func dedupFlags(f []Flag) []Flag {
	seen := map[string]bool{}
	var out []Flag
	for _, x := range f {
		k := x.Name + "|" + x.Note
		if !seen[k] {
			seen[k] = true
			out = append(out, x)
		}
	}
	return out
}

func isBlobLike(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// lex is a small JavaScript tokenizer: enough to find identifier chains,
// string literals and punctuation without being fooled by comments, strings
// or regex literals. It never fails; malformed input yields fewer tokens.
func lex(src string) (toks []tok, strs []tok) {
	line := 1
	n := len(src)
	prevSignificant := func() *tok {
		if len(toks) == 0 {
			return nil
		}
		return &toks[len(toks)-1]
	}
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			i += 2
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
				if src[i] == '\n' {
					line++
				}
				i++
			}
			i += 2
		case c == '"' || c == '\'' || c == '`':
			start, l0 := i+1, line
			q := c
			i++
			for i < n && src[i] != q {
				if src[i] == '\\' {
					i++
				}
				if i < n && src[i] == '\n' {
					line++
				}
				i++
			}
			if start > n {
				start = n
			}
			end := min(i, n)
			t := tok{kind: "str", text: src[start:end], line: l0}
			toks = append(toks, t)
			strs = append(strs, t)
			i++
		case isIdentStart(c):
			s := i
			for i < n && isIdentPart(src[i]) {
				i++
			}
			toks = append(toks, tok{kind: "ident", text: src[s:i], line: line})
		case c >= '0' && c <= '9':
			s := i
			for i < n && (isIdentPart(src[i]) || src[i] == '.') {
				i++
			}
			toks = append(toks, tok{kind: "num", text: src[s:i], line: line})
		case c == '/':
			p := prevSignificant()
			regexOK := p == nil || (p.kind == "punct" && p.text != ")" && p.text != "]" && p.text != "}") || (p.kind == "ident" && (p.text == "return" || p.text == "typeof" || p.text == "case"))
			if regexOK {
				i++
				inClass := false
				for i < n && src[i] != '\n' {
					if src[i] == '\\' {
						i += 2
						continue
					}
					if src[i] == '[' {
						inClass = true
					} else if src[i] == ']' {
						inClass = false
					} else if src[i] == '/' && !inClass {
						break
					}
					i++
				}
				i++
				for i < n && isIdentPart(src[i]) {
					i++
				}
				toks = append(toks, tok{kind: "regex", text: "/re/", line: line})
			} else {
				toks = append(toks, tok{kind: "punct", text: "/", line: line})
				i++
			}
		case c == '?' && i+1 < n && src[i+1] == '.' && !(i+2 < n && src[i+2] >= '0' && src[i+2] <= '9'):
			toks = append(toks, tok{kind: "punct", text: "?.", line: line})
			i += 2
		default:
			toks = append(toks, tok{kind: "punct", text: string(c), line: line})
			i++
		}
	}
	return toks, strs
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}
func isIdentPart(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }
