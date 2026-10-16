package curl

import (
	"net/url"
	"strconv"
	"strings"
)

type header struct{ Name, Value string }

type dataKind int

const (
	dkText  dataKind = iota
	dkFile           // @file: never read, becomes needs-asset
	dkStdin          // @- : unsupported
)

type dataPart struct {
	Kind dataKind
	Val  string // text, or the file name for dkFile
}

type formPart struct {
	Name        string
	Value       string
	File        bool // @file (upload) or <file (content from file)
	ContentType string
	Filename    string
}

// request is the parsed meaning of one curl command line.
type request struct {
	method    string
	urls      []string
	headers   []header
	removed   []string // "Name:" headers curl would drop
	data      []dataPart
	forms     []formPart
	user      string
	hasUser   bool
	authKind  string // basic | digest | ntlm
	bearer    string
	hasBearer bool
	cookie    string
	get       bool
	head      bool
	insecure  bool
	location  bool
	compress  bool
	json      bool
	ua        string
	referer   string
	rng       string
	upload    string
	maxRedirs *int
	timeoutMs *int64
	dyn       bool
	extraURLs int
}

func (r *request) headerIndex(name string) int {
	for i, h := range r.headers {
		if strings.EqualFold(h.Name, name) {
			return i
		}
	}
	return -1
}

func (r *request) hasHeader(name string) bool { return r.headerIndex(name) >= 0 }

func (r *request) headerValue(name string) string {
	if i := r.headerIndex(name); i >= 0 {
		return r.headers[i].Value
	}
	return ""
}

// addHeader parses a -H value: "Name: value", "Name;" (send empty) or
// "Name:" (remove). Returns false when the value is not a header (e.g. @file).
func (r *request) addHeader(v string) bool {
	if strings.HasPrefix(v, "@") {
		return false
	}
	if i := strings.IndexByte(v, ':'); i >= 0 {
		name, val := strings.TrimSpace(v[:i]), strings.TrimSpace(v[i+1:])
		if name == "" {
			return false
		}
		if val == "" {
			r.removed = append(r.removed, name)
			return true
		}
		r.headers = append(r.headers, header{name, val})
		return true
	}
	if strings.HasSuffix(v, ";") {
		name := strings.TrimSpace(strings.TrimSuffix(v, ";"))
		if name != "" {
			r.headers = append(r.headers, header{name, ""})
			return true
		}
	}
	return false
}

// addData records -d style data. kind is data|data-ascii|data-raw|data-binary|
// data-urlencode|json.
func (r *request) addData(kind, v string) {
	switch kind {
	case "data-raw", "json":
		r.data = append(r.data, dataPart{dkText, v})
	case "data-urlencode":
		r.data = append(r.data, urlencodePart(v))
	default:
		switch {
		case v == "@-":
			r.data = append(r.data, dataPart{dkStdin, ""})
		case strings.HasPrefix(v, "@"):
			r.data = append(r.data, dataPart{dkFile, v[1:]})
		default:
			r.data = append(r.data, dataPart{dkText, v})
		}
	}
}

// urlencodePart implements --data-urlencode's content, =content, name=content,
// @file and name@file forms.
func urlencodePart(v string) dataPart {
	if i := strings.IndexByte(v, '='); i >= 0 {
		name, content := v[:i], v[i+1:]
		enc := url.QueryEscape(content)
		if name == "" {
			return dataPart{dkText, enc}
		}
		return dataPart{dkText, name + "=" + enc}
	}
	if i := strings.IndexByte(v, '@'); i >= 0 {
		if v[i+1:] == "-" {
			return dataPart{dkStdin, ""}
		}
		return dataPart{dkFile, v[i+1:]}
	}
	return dataPart{dkText, url.QueryEscape(v)}
}

// addForm parses -F "name=value[;type=..][;filename=..]", "name=@file" and
// "name=<file". stringOnly (--form-string) disables the @ and < forms.
func (r *request) addForm(v string, stringOnly bool) bool {
	i := strings.IndexByte(v, '=')
	if i <= 0 {
		return false
	}
	p := formPart{Name: v[:i]}
	val := v[i+1:]
	if !stringOnly && (strings.HasPrefix(val, "@") || strings.HasPrefix(val, "<")) {
		p.File = true
		val = val[1:]
	}
	val, p.ContentType, p.Filename = splitFormAttrs(val, stringOnly)
	p.Value = val
	r.forms = append(r.forms, p)
	return true
}

// splitFormAttrs strips ";type=" and ";filename=" suffixes and surrounding
// quotes from a form value.
func splitFormAttrs(val string, stringOnly bool) (value, ctype, filename string) {
	if strings.HasPrefix(val, `"`) {
		if j := strings.Index(val[1:], `"`); j >= 0 {
			value = val[1 : 1+j]
			rest := val[2+j:]
			ctype, filename = parseAttrs(rest)
			return
		}
	}
	if stringOnly {
		return val, "", ""
	}
	parts := strings.Split(val, ";")
	value = parts[0]
	if len(parts) > 1 {
		ctype, filename = parseAttrs(";" + strings.Join(parts[1:], ";"))
	}
	return
}

func parseAttrs(s string) (ctype, filename string) {
	for _, a := range strings.Split(s, ";") {
		a = strings.TrimSpace(a)
		switch {
		case strings.HasPrefix(a, "type="):
			ctype = strings.Trim(a[5:], `"`)
		case strings.HasPrefix(a, "filename="):
			filename = strings.Trim(a[9:], `"`)
		}
	}
	return
}

func parseSeconds(v string) *int64 {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > 86400*7 {
		return nil
	}
	ms := int64(f * 1000)
	return &ms
}

func parseInt(v string) *int {
	n, err := strconv.Atoi(v)
	if err != nil || n < -1 || n > 1000 {
		return nil
	}
	return &n
}
