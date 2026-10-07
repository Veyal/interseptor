package curl

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

var credHeader = regexp.MustCompile(`(?i)^(authorization|proxy-authorization|cookie|x-api-key|api-key|x-auth-token|x-access-token|x-csrf-token)$|(?i)(token|secret|api[-_]?key)`)

func buildItem(args []Token, rep *Report, newID func() string, collUID, prevRank string, n int) store.Item {
	r, notes := parseArgs(args)
	it := store.Item{UID: newID(), CollectionUID: collUID, Kind: "request", Rank: store.RankBetween(prevRank, "")}
	rawURL := ""
	if len(r.urls) > 0 {
		rawURL = r.urls[0]
	}
	if len(rawURL) > MaxURLLen {
		rawURL = rawURL[:MaxURLLen]
		notes = append(notes, note{Degraded, "url-truncated", "URL longer than 64 KiB was truncated", ""})
	}
	if rawURL == "" {
		notes = append(notes, note{Unsupported, "no-url", "command has no URL", ""})
	}
	if r.extraURLs > 0 {
		notes = append(notes, note{Degraded, "multiple-urls", fmt.Sprintf("%d extra URL(s) ignored; only the first is imported", r.extraURLs), "Paste each URL as its own curl command"})
	}
	b := &builder{r: r, it: &it, notes: &notes, rep: rep}
	b.method()
	rawURL = b.getData(rawURL)
	b.implied()
	it.URL = urlObject(rawURL)
	it.Name = itemName(it.Method, rawURL, n)
	b.body()
	b.auth()
	b.settings()
	b.credentials(rawURL)
	it.Headers = headerRows(r.headers)
	for _, nt := range notes {
		rep.Entries = append(rep.Entries, Entry{Level: nt.level, Path: it.Name, Item: it.UID, Feature: nt.feature, Message: nt.msg, Suggestion: nt.suggestion})
	}
	if r.dyn {
		rep.Entries = append(rep.Entries, Entry{Level: NeedsReview, Path: it.Name, Item: it.UID, Feature: "shell-expansion",
			Message: "the command uses shell variables, command substitution or backticks that were kept as literal text", Suggestion: "Replace each $VAR with a {{variable}} reference"})
	}
	return it
}

type builder struct {
	r     *request
	it    *store.Item
	notes *[]note
	rep   *Report
}

func (b *builder) note(l Level, f, m, s string) { *b.notes = append(*b.notes, note{l, f, m, s}) }

func (b *builder) method() {
	r := b.r
	switch {
	case r.method != "":
		b.it.Method = r.method
	case r.head:
		b.it.Method = "HEAD"
	case r.upload != "":
		b.it.Method = "PUT"
	case (len(r.data) > 0 || len(r.forms) > 0) && !r.get:
		b.it.Method = "POST"
	default:
		b.it.Method = "GET"
	}
}

// getData appends -G data to the URL query and clears it from the body.
func (b *builder) getData(raw string) string {
	r := b.r
	if !r.get || len(r.data) == 0 {
		return raw
	}
	var parts []string
	for _, d := range r.data {
		if d.Kind == dkText {
			parts = append(parts, d.Val)
		} else {
			b.note(NeedsReview, "needs-asset", "a file-based -d value cannot be appended to the query (files are never read)", "")
		}
	}
	r.data = nil
	if len(parts) == 0 {
		return raw
	}
	q := strings.Join(parts, "&")
	frag := ""
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw, frag = raw[:i], raw[i:]
	}
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
		if strings.HasSuffix(raw, "?") || strings.HasSuffix(raw, "&") {
			sep = ""
		}
	}
	return raw + sep + q + frag
}

// implied adds the headers curl itself would send for the given flags, unless
// the command already set them.
func (b *builder) implied() {
	r := b.r
	set := func(name, val string) {
		if val != "" && !r.hasHeader(name) {
			r.headers = append(r.headers, header{name, val})
		}
	}
	set("User-Agent", r.ua)
	set("Referer", r.referer)
	set("Range", r.rng)
	set("Cookie", r.cookie)
	if r.json {
		set("Content-Type", "application/json")
		set("Accept", "application/json")
	} else if len(r.data) > 0 && len(r.forms) == 0 {
		set("Content-Type", "application/x-www-form-urlencoded")
	}
	if r.compress {
		set("Accept-Encoding", "gzip, deflate")
		b.note(Converted, "compressed", "--compressed became an Accept-Encoding header", "")
	}
	for _, name := range r.removed {
		b.note(Degraded, "header-removal", "curl would drop the built-in header "+name+"; there is no equivalent row", "Disable the matching header in the request instead")
	}
}

func (b *builder) body() {
	r := b.r
	switch {
	case len(r.forms) > 0:
		b.formBody()
	case len(r.data) > 0:
		b.rawBody()
	case r.upload != "":
		b.it.Body = mustJSON(map[string]any{"mode": "file", "file": map[string]string{"src": ""}})
		b.rep.Stats.NeedsAsset++
		b.note(NeedsReview, "needs-asset", "-T upload file must be attached again (local files are never read)", "Attach the file to the request body")
	}
}

func (b *builder) rawBody() {
	r := b.r
	var texts []string
	files := 0
	for _, d := range r.data {
		switch d.Kind {
		case dkText:
			texts = append(texts, d.Val)
		case dkFile:
			files++
		case dkStdin:
			b.note(Unsupported, "stdin-body", "data from stdin (@-) is not available", "Paste the body into the request")
		}
	}
	if files > 0 {
		b.rep.Stats.NeedsAsset += files
		b.note(NeedsReview, "needs-asset", fmt.Sprintf("%d body value(s) read from files; files are never read at import", files), "Attach the file or paste its content")
	}
	if len(texts) == 0 {
		if files == 1 && len(r.data) == 1 {
			b.it.Body = mustJSON(map[string]any{"mode": "file", "file": map[string]string{"src": ""}})
		}
		return
	}
	m := postman.NewOMap()
	m.SetValue("mode", "raw")
	m.SetValue("raw", strings.Join(texts, "&"))
	if lang := rawLanguage(r.headerValue("Content-Type")); lang != "" {
		m.SetValue("options", map[string]any{"raw": map[string]string{"language": lang}})
	}
	b.it.Body = mustJSON(m)
}

func rawLanguage(ct string) string {
	ct = strings.ToLower(ct)
	switch {
	case strings.Contains(ct, "json"):
		return "json"
	case strings.Contains(ct, "xml"):
		return "xml"
	case strings.Contains(ct, "html"):
		return "html"
	case strings.Contains(ct, "javascript"):
		return "javascript"
	case strings.HasPrefix(ct, "text/"):
		return "text"
	}
	return ""
}

func (b *builder) formBody() {
	var rows []map[string]any
	for _, f := range b.r.forms {
		row := map[string]any{"key": f.Name}
		if f.File {
			row["type"] = "file"
			row["src"] = ""
			if f.Filename != "" {
				row["fileName"] = f.Filename
			}
			b.rep.Stats.NeedsAsset++
			b.note(NeedsReview, "needs-asset", "form field "+f.Name+" uploads a local file; files are never read at import", "Attach the file in the form editor")
		} else {
			row["type"] = "text"
			row["value"] = f.Value
		}
		if f.ContentType != "" {
			row["contentType"] = f.ContentType
		}
		rows = append(rows, row)
	}
	b.it.Body = mustJSON(map[string]any{"mode": "formdata", "formdata": rows})
	if len(b.r.data) > 0 {
		b.note(Degraded, "form-and-data", "both -F and -d were given; -d data was dropped", "")
	}
}

func (b *builder) auth() {
	r := b.r
	switch {
	case r.hasBearer:
		b.it.Auth = authObj("bearer", [][2]string{{"token", r.bearer}})
	case r.hasUser:
		user, pass, _ := strings.Cut(r.user, ":")
		kind := r.authKind
		if kind == "" {
			kind = "basic"
		}
		b.it.Auth = authObj(kind, [][2]string{{"username", user}, {"password", pass}})
		if !strings.Contains(r.user, ":") {
			b.note(Degraded, "auth-prompt", "-u had no password (curl would prompt); an empty password was used", "")
		}
		if kind == "ntlm" {
			b.note(PreservedInert, "auth:ntlm", "ntlm auth is kept but not applied when sending", "")
		}
	}
}

func authObj(typ string, kv [][2]string) []byte {
	m := postman.NewOMap()
	m.SetValue("type", typ)
	var rows []map[string]string
	for _, p := range kv {
		rows = append(rows, map[string]string{"key": p[0], "value": p[1], "type": "string"})
	}
	m.SetValue(typ, rows)
	return mustJSON(m)
}

func (b *builder) settings() {
	r := b.r
	s := postman.NewOMap()
	s.SetValue("followRedirects", r.location)
	if r.insecure {
		s.SetValue("verifyTls", false)
	}
	if r.maxRedirs != nil && r.location {
		s.SetValue("maxRedirects", *r.maxRedirs)
	}
	if r.timeoutMs != nil {
		s.SetValue("timeoutMs", *r.timeoutMs)
	}
	b.it.Settings = mustJSON(s)
}

// credentials counts literal secrets so the UI can offer "lift to secret
// variable". Values are never put in the report.
func (b *builder) credentials(rawURL string) {
	r := b.r
	flag := func(what string) {
		b.rep.Stats.EmbeddedCredentials++
		b.note(NeedsReview, "embedded-credential", what+" holds a literal credential (value not shown)", "Lift it to a secret variable and reference it as {{name}}")
	}
	if r.hasUser && !strings.Contains(r.user, "{{") {
		flag("-u")
	}
	if r.hasBearer && !strings.Contains(r.bearer, "{{") {
		flag("--oauth2-bearer")
	}
	for _, h := range r.headers {
		if credHeader.MatchString(h.Name) && h.Value != "" && !strings.Contains(h.Value, "{{") {
			flag("header " + h.Name)
		}
	}
	if u, err := url.Parse(rawURL); err == nil && u.User != nil && !strings.Contains(rawURL, "{{") {
		flag("URL userinfo")
	}
	if n := store.CountURLBodyCredentials(b.it.URL, b.it.Body); n > 0 {
		for i := 0; i < n; i++ {
			flag("URL query or body")
		}
	}
}

func headerRows(hs []header) []byte {
	rows := make([]map[string]string, 0, len(hs))
	for _, h := range hs {
		rows = append(rows, map[string]string{"key": h.Name, "value": h.Value})
	}
	return mustJSON(rows)
}

func itemName(method, rawURL string, n int) string {
	s := method + " " + hostAndPath(rawURL)
	if rawURL == "" {
		s = fmt.Sprintf("curl command %d", n)
	}
	if len(s) > 120 {
		s = s[:120]
		for len(s) > 0 && s[len(s)-1]&0xC0 == 0x80 {
			s = s[:len(s)-1]
		}
	}
	return strings.TrimSpace(s)
}
