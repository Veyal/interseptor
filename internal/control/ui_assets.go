package control

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// uiAsset is one embedded UI file prepared for serving: its strong validator
// and, for text types, a gzip copy built once on first request. The embedded
// FS is immutable for the life of the process, so both are computed lazily and
// kept forever (about 0.7 MB of gzip copies for the whole UI).
type uiAsset struct {
	data   []byte
	etag   string // `"<sha256 prefix>"`
	gz     []byte // nil when gzip does not help or the type is not compressible
	gzETag string // `"<sha256 prefix>-gz"`
}

var uiAssets sync.Map // name -> *uiAsset

// uiGenerated holds assets the shell needs that are not embedded files: the
// concatenated stylesheet. It is written once, inside initUIBuild.
var uiGenerated = map[string]*uiAsset{}

// loadUIAsset returns the prepared asset for an embedded path (or for a file the
// shell generates from them), or ok=false when it does not exist.
func loadUIAsset(name string) (*uiAsset, bool) {
	if v, ok := uiAssets.Load(name); ok {
		return v.(*uiAsset), true
	}
	if a, ok := generatedUIAsset(name); ok {
		return a, true
	}
	data, err := uiFS.ReadFile("ui/" + name)
	if err != nil {
		return nil, false
	}
	a := newUIAsset(name, data)
	v, _ := uiAssets.LoadOrStore(name, a)
	return v.(*uiAsset), true
}

// newUIAsset prepares one file for serving: its validator and, for text types
// that shrink, the gzip copy.
func newUIAsset(name string, data []byte) *uiAsset {
	sum := sha256.Sum256(data)
	tag := hex.EncodeToString(sum[:12])
	a := &uiAsset{data: data, etag: `"` + tag + `"`}
	if uiCompressible(name) && len(data) > 512 {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if _, err := zw.Write(data); err == nil && zw.Close() == nil && buf.Len() < len(data) {
			a.gz = buf.Bytes()
			a.gzETag = `"` + tag + `-gz"`
		}
	}
	return a
}

// generatedUIAsset returns a shell-generated asset, preparing the build first.
// initUIBuild does not call back into loadUIAsset, so the once never re-enters.
func generatedUIAsset(name string) (*uiAsset, bool) {
	uiBuild.once.Do(initUIBuild)
	a, ok := uiGenerated[name]
	return a, ok
}

// uiVersionPrefix is the path segment under which the shell links its modules
// and stylesheets: /_v/<build id>/js/app.js. The build id is a digest of every
// embedded UI file, so a URL under the current id names content that cannot
// change for the life of that URL and is served as immutable. The browser then
// revalidates one response on reload (the shell) instead of every asset.
const uiVersionPrefix = "_v/"

// Cache policies for UI responses. The shell and any unversioned path must be
// revalidated so an upgrade is picked up at once; an asset under the current
// build id never needs to be asked for again.
const (
	uiCacheRevalidate = "no-cache"
	uiCacheImmutable  = "private, max-age=31536000, immutable"
)

var uiBuild struct {
	once  sync.Once
	id    string
	shell *uiAsset
}

// uiBuildID returns the digest that identifies this binary's embedded UI.
func uiBuildID() string {
	uiBuild.once.Do(initUIBuild)
	return uiBuild.id
}

// uiShell returns index.html with its asset links moved under the build prefix,
// or nil when the embedded UI has no index.
func uiShell() *uiAsset {
	uiBuild.once.Do(initUIBuild)
	return uiBuild.shell
}

func initUIBuild() {
	sum := sha256.New()
	_ = fs.WalkDir(uiFS, "ui", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := uiFS.ReadFile(path)
		if err != nil {
			return err
		}
		// Length-prefix both parts so moving bytes between a name and a body, or
		// between two files, cannot produce the same digest.
		sum.Write([]byte(strconv.Itoa(len(path)) + ":" + path + ":" + strconv.Itoa(len(data)) + ":"))
		sum.Write(data)
		return nil
	})
	uiBuild.id = hex.EncodeToString(sum.Sum(nil)[:8])
	if index, err := uiFS.ReadFile("ui/index.html"); err == nil {
		// Hints, then the stylesheet bundle, then the prefix rewrite, so the
		// injected links get the same /_v/<build id>/ URLs as the ones written
		// in index.html. The validator is taken from the final bytes and
		// therefore covers all of it.
		hinted := preloadUIShell(index, readEmbeddedUIModule)
		bundled, css := bundleUIShellCSS(hinted, readEmbeddedUIModule)
		if css != nil {
			uiGenerated[uiBundleCSSPath] = newUIAsset(uiBundleCSSPath, css)
		}
		uiBuild.shell = newUIAsset("index.html", versionUIShell(bundled, uiBuild.id))
	}
}

// uiShellAssetRef matches a src/href in the shell that names a stylesheet or a
// module by absolute path. API downloads, the manifest and data: URLs are not
// matched and stay as written.
var uiShellAssetRef = regexp.MustCompile(`((?:src|href)=")/([A-Za-z0-9_./-]+\.(?:js|css))"`)

// versionUIShell rewrites every reference to an embedded stylesheet or module
// to the build-prefixed URL. Relative imports inside a module resolve against
// the module's own URL, so the whole graph follows the entry points. Each file
// must be reachable by exactly one URL: a module loaded through two is
// evaluated twice.
func versionUIShell(index []byte, id string) []byte {
	return uiShellAssetRef.ReplaceAllFunc(index, func(m []byte) []byte {
		sub := uiShellAssetRef.FindSubmatch(m)
		name := string(sub[2])
		if !hasUIAsset(name) {
			return m // not a file this build serves: leave the link alone
		}
		return []byte(string(sub[1]) + "/" + uiVersionPrefix + id + "/" + name + `"`)
	})
}

// hasUIAsset reports whether name is an embedded UI file or one the shell
// generates from them (the stylesheet bundle).
func hasUIAsset(name string) bool {
	if _, ok := uiGenerated[name]; ok {
		return true
	}
	_, err := fs.Stat(uiFS, "ui/"+name)
	return err == nil
}

// ---- stylesheet bundle ----
//
// index.html links thirty-eight stylesheets. Each is one request, and a cold
// load issues them over the same six connections the module graph is already
// using, so the count, not the bytes (576 KB), is what costs. The shell is
// served with them concatenated in document order, which preserves the cascade
// exactly (later sheets still override earlier ones) because no sheet uses
// @import or a relative url().

// uiBundleCSSPath is where the concatenated stylesheets are served. It must not
// collide with a real file.
const uiBundleCSSPath = "css/_all.css"

// uiStandaloneSheets keep their own <link>: collections-matrix.js appends that
// sheet itself unless it finds its URL already linked, and an appended copy
// would move its rules to the end of the cascade.
var uiStandaloneSheets = map[string]bool{"css/collections-matrix.css": true}

var uiShellStylesheetLink = regexp.MustCompile(`[ \t]*<link rel="stylesheet" href="/([A-Za-z0-9_./-]+\.css)">[ \t]*\r?\n?`)

// bundleUIShellCSS replaces the plain stylesheet links of the shell with one
// link to the concatenation, in document order, of the files they name. Links
// that are not the plain form, name a file that is not embedded, or name a
// standalone sheet are left exactly as they are. It returns nil for the bundle
// when there is nothing to concatenate.
func bundleUIShellCSS(index []byte, read func(string) ([]byte, bool)) ([]byte, []byte) {
	var bundle bytes.Buffer
	replaced := 0
	for _, m := range uiShellStylesheetLink.FindAllSubmatchIndex(index, -1) {
		name := string(index[m[2]:m[3]])
		if uiStandaloneSheets[name] {
			continue
		}
		data, ok := read(name)
		if !ok {
			continue
		}
		bundle.Write(data)
		bundle.WriteByte('\n')
		replaced++
	}
	if replaced < 2 {
		return index, nil
	}
	// Drop every bundled link but the first, which becomes the bundle link.
	out := make([]byte, 0, len(index))
	prev := 0
	seen := 0
	for _, m := range uiShellStylesheetLink.FindAllSubmatchIndex(index, -1) {
		name := string(index[m[2]:m[3]])
		if uiStandaloneSheets[name] {
			continue
		}
		if _, ok := read(name); !ok {
			continue
		}
		out = append(out, index[prev:m[0]]...)
		if seen == 0 {
			out = append(out, `<link rel="stylesheet" href="/`+uiBundleCSSPath+`">`...)
			out = append(out, '\n')
		}
		prev = m[1]
		seen++
	}
	out = append(out, index[prev:]...)
	return out, bundle.Bytes()
}

// ---- module preload hints ----
//
// A cold load otherwise discovers the ~150 modules one import wave at a time:
// a module's imports are only known once it has been fetched and parsed, so
// the depth of the import graph, not its size, sets the load time. The shell
// therefore hints every module the entry points reach through static imports,
// and the browser can request them all as soon as it has the document.
// Modules reached only through import() are left out: hinting them would
// fetch code a boot may never run.

var (
	uiShellModuleScript = regexp.MustCompile(`<script\b[^>]*\btype="module"[^>]*\bsrc="/([A-Za-z0-9_./-]+\.js)"`)
	uiShellPreloadHint  = regexp.MustCompile(`<link rel="modulepreload" href="/([A-Za-z0-9_./-]+\.js)">[ \t]*\r?\n?`)
)

func readEmbeddedUIModule(name string) ([]byte, bool) {
	data, err := uiFS.ReadFile("ui/" + name)
	return data, err == nil
}

// preloadUIShell adds a modulepreload hint for every module in the static
// import graph of the shell's module scripts that index.html does not already
// hint. The new hints follow the existing ones (or, when there are none,
// precede the first module script), each module once.
//
// The control server speaks HTTP/1.1, where the browser opens six connections
// and a cold load is bound by the number of requests, not by how early they
// are discovered: hinting the whole graph in one flat list measured no faster
// than the seven root hints. What does help is the order. The first module
// script is the application entry; the scripts after it run only once it has
// evaluated. Its graph is therefore hinted first, and the modules that only
// the later scripts need are hinted with fetchpriority="low" so they queue
// behind it and behind the first API reads.
func preloadUIShell(index []byte, read func(name string) ([]byte, bool)) []byte {
	var roots []string
	for _, m := range uiShellModuleScript.FindAllSubmatch(index, -1) {
		roots = append(roots, string(m[1]))
	}
	hinted := map[string]bool{}
	at := -1
	for _, m := range uiShellPreloadHint.FindAllSubmatchIndex(index, -1) {
		hinted[string(index[m[2]:m[3]])] = true
		at = m[1] // after the last existing hint
	}
	if at < 0 {
		if loc := uiShellModuleScript.FindIndex(index); loc != nil {
			at = loc[0]
		}
	}
	if at < 0 {
		return index
	}
	entry := map[string]bool{}
	var hints bytes.Buffer
	hint := func(name, attrs string) {
		if !hinted[name] {
			hinted[name] = true
			hints.WriteString(`<link rel="modulepreload" href="/` + name + `"` + attrs + `>` + "\n")
		}
	}
	if len(roots) > 0 {
		for _, name := range uiStaticModuleGraph(roots[:1], read) {
			entry[name] = true
			hint(name, "")
		}
	}
	for _, name := range uiStaticModuleGraph(roots, read) {
		if !entry[name] {
			hint(name, ` fetchpriority="low"`)
		}
	}
	if hints.Len() == 0 {
		return index
	}
	out := make([]byte, 0, len(index)+hints.Len())
	out = append(out, index[:at]...)
	out = append(out, hints.Bytes()...)
	return append(out, index[at:]...)
}

// uiStaticModuleGraph returns the roots and every module they reach through
// static imports, each once, breadth first. Names are embedded paths
// ("js/app.js"); a name read cannot find is dropped.
func uiStaticModuleGraph(roots []string, read func(name string) ([]byte, bool)) []string {
	seen := map[string]bool{}
	var order []string
	queue := append([]string(nil), roots...)
	for _, r := range roots {
		seen[r] = true
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		src, ok := read(name)
		if !ok {
			continue
		}
		order = append(order, name)
		for _, spec := range jsStaticImports(src) {
			if dep, ok := resolveUIImport(name, spec); ok && !seen[dep] {
				seen[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	return order
}

// resolveUIImport resolves a relative module specifier against the importing
// module's embedded path. Bare and absolute specifiers are not embedded
// modules under the build prefix and are not followed.
func resolveUIImport(from, spec string) (string, bool) {
	if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
		return "", false
	}
	if strings.ContainsAny(spec, `?#\`) {
		return "", false // a query makes a second URL for the file; never hint it
	}
	name := path.Join(path.Dir(from), spec)
	if !strings.HasSuffix(name, ".js") || !fs.ValidPath(name) {
		return "", false
	}
	return name, true
}

// jsStaticImports returns the module specifiers of the static import and
// re-export declarations in a module's source, in source order:
//
//	import './x.js'            import a, {b as c} from './x.js'
//	import * as ns from './x.js'
//	export * from './x.js'     export {a, b} from './x.js'
//
// Comments, string and template contents, regular expression literals,
// import() calls and import.meta are skipped.
func jsStaticImports(src []byte) []string {
	p := &jsCursor{l: jsLexer{src: src}}
	var out []string
	afterDot := false
	tok := p.advance()
	for tok.kind != jsEOF {
		if tok.kind == jsIdent && !afterDot && (tok.text == "import" || tok.text == "export") {
			if spec, ok := p.declarationSpecifier(tok.text == "import"); ok {
				out = append(out, spec)
			}
			afterDot = false
			tok = p.cur // the first token the declaration did not consume
			continue
		}
		afterDot = tok.is(".")
		tok = p.advance()
	}
	return out
}

const (
	jsEOF byte = iota
	jsIdent
	jsPunct
	jsString
	jsOther // number, template or regular expression literal
)

type jsTok struct {
	kind byte
	text string // identifier name, punctuator byte, or raw string contents
}

func (t jsTok) is(punct string) bool { return t.kind == jsPunct && t.text == punct }

type jsCursor struct {
	l   jsLexer
	cur jsTok
}

func (p *jsCursor) advance() jsTok {
	p.cur = p.l.next()
	return p.cur
}

// declarationSpecifier is called with the cursor on `import` or `export` and
// reports the declaration's module specifier, if it has one. It leaves p.cur
// on the first token that is not part of the declaration.
func (p *jsCursor) declarationSpecifier(isImport bool) (string, bool) {
	tok := p.advance()
	if isImport && tok.kind == jsString { // import './x.js'
		p.advance()
		return tok.text, true
	}
	// An import clause starts with a binding, `{` or `*`; a re-export with `{`
	// or `*`. Anything else is import(), import.meta, a property named import,
	// or an export of a local declaration.
	if !(tok.is("{") || tok.is("*") || (isImport && tok.kind == jsIdent)) {
		return "", false
	}
	depth, closed := 0, false
	for tok.kind != jsEOF {
		switch {
		case tok.is("{"):
			depth++
		case tok.is("}"):
			if depth--; depth < 0 {
				return "", false
			}
			closed = depth == 0
			tok = p.advance()
			continue
		case depth > 0: // names inside the braces, including string names
		case tok.kind == jsIdent && tok.text == "from":
			next := p.advance()
			if next.kind == jsString {
				p.advance()
				return next.text, true
			}
			tok = next // a binding that happens to be called from
			continue
		case closed && !isImport:
			return "", false // `export {a, b}` with no source
		case tok.kind == jsIdent || tok.is(",") || tok.is("*") || tok.kind == jsString:
		default:
			return "", false
		}
		closed = false
		tok = p.advance()
	}
	return "", false
}

// jsLexer is the small part of a JavaScript tokenizer needed to find module
// declarations without being misled by text that only looks like one.
type jsLexer struct {
	src  []byte
	pos  int
	prev jsTok
}

func (l *jsLexer) next() jsTok {
	l.prev = l.scan()
	return l.prev
}

func (l *jsLexer) scan() jsTok {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			l.pos++
		case c == '/' && l.peek(1) == '/':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
		case c == '/' && l.peek(1) == '*':
			end := bytes.Index(l.src[l.pos+2:], []byte("*/"))
			if end < 0 {
				l.pos = len(l.src)
			} else {
				l.pos += end + 4
			}
		case c == '\'' || c == '"':
			return jsTok{jsString, l.stringLiteral(c)}
		case c == '`':
			l.template()
			return jsTok{kind: jsOther}
		case c == '/':
			if end, ok := l.regexEnd(); ok {
				l.pos = end
				return jsTok{kind: jsOther}
			}
			l.pos++
			return jsTok{jsPunct, "/"}
		case jsIdentByte(c) && (c < '0' || c > '9'):
			start := l.pos
			for l.pos < len(l.src) && jsIdentByte(l.src[l.pos]) {
				l.pos++
			}
			return jsTok{jsIdent, string(l.src[start:l.pos])}
		case c >= '0' && c <= '9':
			for l.pos < len(l.src) && (jsIdentByte(l.src[l.pos]) || l.src[l.pos] == '.') {
				l.pos++
			}
			return jsTok{kind: jsOther}
		default:
			l.pos++
			return jsTok{jsPunct, string(c)}
		}
	}
	return jsTok{kind: jsEOF}
}

func (l *jsLexer) peek(n int) byte {
	if l.pos+n < len(l.src) {
		return l.src[l.pos+n]
	}
	return 0
}

func jsIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// stringLiteral consumes a quoted string and returns its raw contents. An
// unterminated string ends at the line break, as it does for the parser.
func (l *jsLexer) stringLiteral(quote byte) string {
	l.pos++
	start := l.pos
	for l.pos < len(l.src) {
		switch c := l.src[l.pos]; {
		case c == '\\':
			l.pos += 2
			continue
		case c == quote:
			l.pos++
			return string(l.src[start : l.pos-1])
		case c == '\n':
			return string(l.src[start:l.pos])
		}
		l.pos++
	}
	l.pos = len(l.src)
	return string(l.src[start:])
}

// template consumes a template literal, including the code in its ${…}
// substitutions (which may hold strings, comments and further templates).
func (l *jsLexer) template() {
	l.pos++
	for l.pos < len(l.src) {
		switch c := l.src[l.pos]; {
		case c == '\\':
			l.pos += 2
		case c == '`':
			l.pos++
			return
		case c == '$' && l.peek(1) == '{':
			l.pos += 2
			for depth := 1; depth > 0; {
				tok := l.next()
				if tok.kind == jsEOF {
					return
				}
				if tok.is("{") {
					depth++
				} else if tok.is("}") {
					depth--
				}
			}
		default:
			l.pos++
		}
	}
}

// regexEnd reports where the regular expression literal starting at l.pos
// ends. A slash is a division when the previous token ends an operand, or
// when no closing slash follows on the same line.
func (l *jsLexer) regexEnd() (int, bool) {
	switch p := l.prev; p.kind {
	case jsOther, jsString:
		return 0, false
	case jsPunct:
		if p.text == ")" || p.text == "]" || p.text == "}" {
			return 0, false
		}
	case jsIdent:
		switch p.text {
		case "return", "typeof", "instanceof", "in", "of", "new", "delete", "void", "throw", "case", "do", "else", "yield", "await":
		default:
			return 0, false
		}
	}
	inClass := false
	for i := l.pos + 1; i < len(l.src); i++ {
		switch c := l.src[i]; {
		case c == '\\':
			i++
		case c == '\n':
			return 0, false
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			for i++; i < len(l.src) && jsIdentByte(l.src[i]); i++ {
			}
			return i, true
		}
	}
	return 0, false
}

func uiCompressible(name string) bool {
	for _, ext := range []string{".html", ".js", ".css", ".svg", ".json", ".webmanifest", ".txt", ".md"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// acceptsGzip reports whether the Accept-Encoding header allows gzip (an
// explicit q=0 refuses it).
func acceptsGzip(h string) bool {
	for _, part := range strings.Split(h, ",") {
		fields := strings.Split(part, ";")
		coding := strings.ToLower(strings.TrimSpace(fields[0]))
		if coding != "gzip" && coding != "*" {
			continue
		}
		refused := false
		for _, p := range fields[1:] {
			p = strings.ToLower(strings.ReplaceAll(p, " ", ""))
			if strings.HasPrefix(p, "q=") {
				if q, err := strconv.ParseFloat(p[2:], 64); err == nil && q == 0 {
					refused = true
				}
			}
		}
		if !refused {
			return true
		}
	}
	return false
}

// etagMatches implements the weak comparison If-None-Match uses: any listed
// tag (with or without the W/ prefix) equal to etag, or "*", matches.
func etagMatches(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimSpace(t)
		if t == "*" || strings.TrimPrefix(t, "W/") == etag {
			return true
		}
	}
	return false
}

// writeUIAsset serves a prepared asset with gzip negotiation under the given
// cache policy. uiCacheRevalidate makes the browser ask before reusing a
// response whose URL carries no version, and a matching validator turns the
// answer into a body-less 304; uiCacheImmutable is for build-prefixed URLs.
func writeUIAsset(w http.ResponseWriter, r *http.Request, name string, a *uiAsset, cache string) {
	h := w.Header()
	h.Set("Content-Type", uiContentType(name))
	h.Set("Cache-Control", cache)
	body, etag := a.data, a.etag
	if a.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r.Header.Get("Accept-Encoding")) {
			body, etag = a.gz, a.gzETag
			h.Set("Content-Encoding", "gzip")
		}
	}
	h.Set("ETag", etag)
	if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatches(inm, etag) {
		h.Del("Content-Encoding")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}
