package main

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/util"
)

// A small, dependency-free highlighter for the code blocks in the guides. It
// is deliberately conservative: each lexer marks only tokens it is sure of
// and leaves everything else as plain text. Every character is HTML-escaped,
// and tokens become flat <span class="tok-…"> elements of these classes only.
const (
	tokComment = "comment"
	tokString  = "string"
	tokNumber  = "number"
	tokKeyword = "keyword"
	tokKey     = "key"    // JSON, YAML and TOML keys, HTTP header names
	tokPrompt  = "prompt" // leading $ , > or # prompt in console blocks
	tokFlag    = "flag"   // --flag and -f in shell commands
	tokMethod  = "method" // HTTP request methods
	tokStatus  = "status" // HTTP status codes
	tokURL     = "url"
)

type token struct{ class, text string }

// highlightCode returns code as escaped HTML with the tokens of lang wrapped
// in spans. Unknown languages are escaped plain text.
func highlightCode(lang, code string) string {
	var b strings.Builder
	b.Grow(len(code) + len(code)/2)
	for _, t := range lexCode(lang, code) {
		if t.class == "" {
			b.Write(util.EscapeHTML([]byte(t.text)))
			continue
		}
		b.WriteString(`<span class="tok-` + t.class + `">`)
		b.Write(util.EscapeHTML([]byte(t.text)))
		b.WriteString(`</span>`)
	}
	return b.String()
}

func lexCode(lang, code string) []token {
	switch lang {
	case "bash", "sh", "shell", "zsh":
		return lexShell(code, false)
	case "console":
		return lexShell(code, true)
	case "json", "jsonc":
		return lexJSON(code)
	case "http":
		return lexHTTP(code)
	case "yaml", "yml":
		return lexYAML(code)
	case "go":
		return lexCLike(code, goSyntax)
	case "js", "javascript", "ts", "typescript":
		return lexCLike(code, jsSyntax)
	case "python", "py", "starlark":
		return lexPython(code)
	case "toml":
		return lexTOML(code, false)
	case "ini":
		return lexTOML(code, true)
	}
	return []token{{text: code}}
}

// lexer collects tokens over src; text between emitted tokens is plain.
type lexer struct {
	src   string
	pos   int
	plain int // start of the pending plain text
	toks  []token
}

func (l *lexer) skip(n int) { l.pos += n }

func (l *lexer) skipRune() {
	_, n := utf8.DecodeRuneInString(l.src[l.pos:])
	l.pos += max(n, 1)
}

func (l *lexer) skipNewline() {
	if l.pos < len(l.src) && l.src[l.pos] == '\n' {
		l.pos++
	}
}

func (l *lexer) flush() {
	if l.plain < l.pos {
		l.toks = append(l.toks, token{text: l.src[l.plain:l.pos]})
	}
	l.plain = l.pos
}

// emit makes the next n bytes one token of class.
func (l *lexer) emit(class string, n int) {
	if n <= 0 {
		return
	}
	l.flush()
	l.toks = append(l.toks, token{class, l.src[l.pos : l.pos+n]})
	l.pos += n
	l.plain = l.pos
}

// embed adds the tokens of a nested lexer that covered the next n bytes.
func (l *lexer) embed(toks []token, n int) {
	l.flush()
	l.toks = append(l.toks, toks...)
	l.pos += n
	l.plain = l.pos
}

// tokens ends the scan; anything not yet consumed is plain.
func (l *lexer) tokens() []token {
	l.pos = len(l.src)
	l.flush()
	return l.toks
}

func lineLen(s string) int {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return i
	}
	return len(s)
}

// closingQuote is the length of the quoted string that starts s, including
// both quotes, or 0 when it is not closed (on the same line, if oneLine).
func closingQuote(s string, escapes, oneLine bool) int {
	q := s[0]
	for i := 1; i < len(s); i++ {
		switch {
		case s[i] == '\\' && escapes:
			i++
		case s[i] == q:
			return i + 1
		case s[i] == '\n' && oneLine:
			return 0
		}
	}
	return 0
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(r rune, dollar bool) bool {
	return r == '_' || unicode.IsLetter(r) || (dollar && r == '$')
}

// identLen is the length of the identifier that starts s, or 0.
func identLen(s string, dollar bool) int {
	n := 0
	for n < len(s) {
		r, size := utf8.DecodeRuneInString(s[n:])
		if !isIdentStart(r, dollar) && !(n > 0 && unicode.IsDigit(r)) {
			break
		}
		n += size
	}
	return n
}

// prevNonSpace is the last byte before pos on the same line that is not a
// space or tab, or 0. A comment ending with "." on the line above must not
// make the next line's first word look like a property.
func prevNonSpace(src string, pos int) byte {
	for i := pos - 1; i >= 0; i-- {
		switch c := src[i]; c {
		case ' ', '\t':
		case '\n':
			return 0
		default:
			return c
		}
	}
	return 0
}

// Shell

var (
	shellFlag     = regexp.MustCompile(`^(?:--[A-Za-z0-9][A-Za-z0-9_-]*|-[A-Za-z][A-Za-z0-9_-]*)`)
	shellURL      = regexp.MustCompile(`^(?:https?|wss?)://[^\s'"<>;|&()` + "`" + `]+`)
	shellNumber   = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
	shellHeredoc  = regexp.MustCompile(`^<<(-?)[ \t]*(['"]?)([A-Za-z_][A-Za-z0-9_]*)(['"]?)`)
	shellKeywords = map[string]bool{"if": true, "then": true, "else": true, "elif": true, "fi": true, "for": true, "while": true, "until": true,
		"do": true, "done": true, "case": true, "esac": true, "function": true, "select": true}
	// After these keywords the next word is a command again.
	shellCommandAfter = map[string]bool{"if": true, "then": true, "else": true, "elif": true, "while": true, "until": true, "do": true}
)

func isShellBreak(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\'' || c == '"' || c == '`' || strings.IndexByte(";|&()<>", c) >= 0
}

// lexShell highlights shell. A console block, or a shell block with any
// "$ " line, is a session: prompt lines (and their continuations) are
// commands and every other line is output, left plain.
func lexShell(code string, console bool) []token {
	l := &lexer{src: code}
	prompts := []string{"$ "}
	if console {
		prompts = append(prompts, "> ", "# ")
	}
	if !console && !strings.HasPrefix(code, "$ ") && !strings.Contains(code, "\n$ ") {
		lexShellCommands(l, len(code))
		return l.tokens()
	}
	for l.pos < len(code) {
		s := code[l.pos:]
		n := lineLen(s)
		prompt := ""
		for _, p := range prompts {
			if strings.HasPrefix(s, p) {
				prompt = p
				break
			}
		}
		if prompt == "" {
			l.skip(n)
		} else {
			end := l.pos + n
			for end < len(code) && strings.HasSuffix(strings.TrimRight(code[l.pos:end], "\r"), "\\") {
				end += 1 + lineLen(code[end+1:])
			}
			l.emit(tokPrompt, len(prompt))
			lexShellCommands(l, end)
		}
		l.skipNewline()
	}
	return l.tokens()
}

// lexShellCommands highlights commands up to end.
func lexShellCommands(l *lexer, end int) {
	wordStart, cmdStart := true, true
	heredoc, heredocTabs := "", false
	for l.pos < end {
		s := l.src[l.pos:end]
		c := s[0]
		switch {
		case c == '\n':
			l.skip(1)
			wordStart, cmdStart = true, true
			if heredoc != "" {
				skipHeredoc(l, end, heredoc, heredocTabs)
				heredoc = ""
			}
		case c == ' ' || c == '\t' || c == '\r':
			l.skip(1)
			wordStart = true
		case c == '\\':
			if len(s) > 1 && s[1] == '\n' {
				l.skip(2)
				wordStart = true
				continue
			}
			l.skip(1)
			if len(s) > 1 {
				l.skipRune()
			}
			wordStart = false
		case c == '#' && wordStart:
			l.emit(tokComment, lineLen(s))
		case c == '\'' || c == '"':
			if n := closingQuote(s, c == '"', false); n > 0 {
				l.emit(tokString, n)
			} else {
				l.skip(1)
			}
			wordStart, cmdStart = false, false
		case strings.HasPrefix(s, "<<<"):
			l.skip(3)
			wordStart = true
		case strings.HasPrefix(s, "<<"):
			if m := shellHeredoc.FindStringSubmatch(s); m != nil && m[2] == m[4] {
				heredoc, heredocTabs = m[3], m[1] == "-"
				l.skip(len(m[0]))
			} else {
				l.skip(2)
			}
			wordStart = true
		case c == '`' || strings.IndexByte(";|&()<>", c) >= 0:
			l.skip(1)
			wordStart = true
			if c != ')' && c != '<' && c != '>' {
				cmdStart = true
			}
		default:
			n := shellWordLen(s)
			if n == 0 {
				l.skipRune()
				wordStart = false
				continue
			}
			if wordStart {
				cmdStart = lexShellWord(l, s[:n], cmdStart)
			} else {
				l.skip(n)
			}
			wordStart = false
		}
	}
}

// shellWordLen is the length of the unquoted word that starts s.
func shellWordLen(s string) int {
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] != '\n' {
			i += 2
			continue
		}
		if isShellBreak(s[i]) || s[i] == '\\' {
			break
		}
		i++
	}
	return min(i, len(s))
}

// lexShellWord marks one whole word and reports whether the next word is a
// command.
func lexShellWord(l *lexer, word string, cmdStart bool) bool {
	switch {
	case cmdStart && shellKeywords[word]:
		l.emit(tokKeyword, len(word))
		return shellCommandAfter[word]
	case shellFlag.MatchString(word):
		n := len(shellFlag.FindString(word))
		l.emit(tokFlag, n)
		l.skip(len(word) - n)
	case shellURL.MatchString(word):
		n := len(shellURL.FindString(word))
		l.emit(tokURL, n)
		l.skip(len(word) - n)
	case shellNumber.MatchString(word):
		l.emit(tokNumber, len(word))
	default:
		l.skip(len(word))
	}
	return false
}

// skipHeredoc leaves a here-document body, up to its delimiter line, plain.
func skipHeredoc(l *lexer, end int, delim string, tabs bool) {
	for l.pos < end {
		n := lineLen(l.src[l.pos:end])
		line := strings.TrimRight(l.src[l.pos:l.pos+n], "\r")
		if tabs {
			line = strings.TrimLeft(line, "\t")
		}
		l.skip(n)
		if line == delim {
			return
		}
		if l.pos < end {
			l.skip(1)
		}
	}
}

// JSON (and JSON with comments)

var jsonNumber = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?`)

// valueWordLen is the length of a run of characters that may form a number,
// a date or a bare word.
func valueWordLen(s string) int {
	i := 0
	for i < len(s) {
		c := s[i]
		if !(isDigit(c) || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || strings.IndexByte("_.+-:", c) >= 0) {
			break
		}
		i++
	}
	return i
}

func lexJSON(code string) []token {
	l := &lexer{src: code}
	for l.pos < len(code) {
		s := code[l.pos:]
		c := s[0]
		switch {
		case c == '"':
			n := closingQuote(s, true, true)
			if n == 0 {
				l.skip(1)
				continue
			}
			class := tokString
			if strings.HasPrefix(strings.TrimLeft(s[n:], " \t"), ":") {
				class = tokKey
			}
			l.emit(class, n)
		case strings.HasPrefix(s, "//"):
			l.emit(tokComment, lineLen(s))
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s[2:], "*/"); i >= 0 {
				l.emit(tokComment, i+4)
			} else {
				l.skip(2)
			}
		case c == '-' || isDigit(c):
			w := max(1, valueWordLen(s))
			if n := len(jsonNumber.FindString(s)); n > 0 && n == w {
				l.emit(tokNumber, n)
			} else {
				l.skip(w)
			}
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_':
			w := identLen(s, false)
			if word := s[:w]; word == "true" || word == "false" || word == "null" {
				l.emit(tokKeyword, w)
			} else {
				l.skip(w)
			}
		default:
			l.skipRune()
		}
	}
	return l.tokens()
}

// HTTP messages: a request or status line, headers, a blank line, a body.
// A block may hold several messages, each after a blank line.

var (
	httpRequestLine = regexp.MustCompile(`^(GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS|CONNECT|TRACE)( +)([^ \t\r\n]+)(?: +HTTP/[0-9.]+)?[ \t\r]*$`)
	httpStatusLine  = regexp.MustCompile(`^(HTTP/[0-9.]+ +)([1-5][0-9][0-9])(?:[ \t].*)?\r?$`)
	httpHeaderLine  = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+:")
)

func isHTTPStart(line string) bool {
	return httpRequestLine.MatchString(line) || httpStatusLine.MatchString(line)
}

func lexHTTP(code string) []token {
	l := &lexer{src: code}
	lines := strings.SplitAfter(code, "\n")
	const start, headers, body = 0, 1, 2
	state := start
	for i := 0; i < len(lines); i++ {
		full := lines[i]
		line := strings.TrimSuffix(full, "\n")
		blank := strings.TrimSpace(line) == ""
		switch state {
		case start:
			switch {
			case blank:
				l.skip(len(full))
			case httpRequestLine.MatchString(line):
				m := httpRequestLine.FindStringSubmatch(line)
				l.emit(tokMethod, len(m[1]))
				l.skip(len(m[2]))
				l.emit(tokURL, len(m[3]))
				l.skip(len(full) - len(m[1]) - len(m[2]) - len(m[3]))
				state = headers
			case httpStatusLine.MatchString(line):
				m := httpStatusLine.FindStringSubmatch(line)
				l.skip(len(m[1]))
				l.emit(tokStatus, len(m[2]))
				l.skip(len(full) - len(m[1]) - len(m[2]))
				state = headers
			case httpHeaderLine.MatchString(line):
				state = headers
				i--
			default:
				return l.tokens()
			}
		case headers:
			switch {
			case blank:
				l.skip(len(full))
				state = body
			case httpHeaderLine.MatchString(line):
				name := strings.IndexByte(line, ':')
				l.emit(tokKey, name)
				l.skip(len(full) - name)
			default:
				l.skip(len(full))
			}
		case body:
			if isHTTPStart(line) {
				state = start
				i--
				continue
			}
			j := i + 1
			for j < len(lines) && !(strings.TrimSpace(lines[j-1]) == "" && isHTTPStart(strings.TrimSuffix(lines[j], "\n"))) {
				j++
			}
			text := strings.Join(lines[i:j], "")
			if t := strings.TrimSpace(text); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
				l.embed(lexJSON(text), len(text))
			} else {
				l.skip(len(text))
			}
			i = j - 1
			state = start
		}
	}
	return l.tokens()
}

// YAML

var (
	yamlKey    = regexp.MustCompile(`^("(?:[^"\\\n]|\\.)*"|'[^'\n]*'|[A-Za-z0-9_][^:#\n]*?)[ \t]*:(?:[ \t]|$)`)
	yamlNumber = regexp.MustCompile(`^[-+]?(?:[0-9][0-9_]*(?:\.[0-9]+)?(?:[eE][-+]?[0-9]+)?|0x[0-9A-Fa-f]+|0o[0-7]+|\.inf|\.nan)$`)
	yamlBlock  = regexp.MustCompile(`^[|>][-+0-9]*[ \t]*(?:#.*)?$`)
	yamlWords  = map[string]bool{"true": true, "false": true, "null": true, "~": true}
)

func lexYAML(code string) []token {
	l := &lexer{src: code}
	blockIndent := -1 // the key indentation while inside a block scalar
	for l.pos < len(code) {
		line := code[l.pos : l.pos+lineLen(code[l.pos:])]
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if blockIndent >= 0 {
			if strings.TrimSpace(line) == "" || indent > blockIndent {
				l.skip(len(line))
				l.skipNewline()
				continue
			}
			blockIndent = -1
		}
		l.skip(indent)
		rest := line[indent:]
		column := indent
		for strings.HasPrefix(rest, "- ") || rest == "-" {
			n := len(rest) - len(strings.TrimLeft(rest[1:], " "))
			l.skip(n)
			rest, column = rest[n:], column+n
		}
		switch {
		case strings.HasPrefix(rest, "#"):
			l.emit(tokComment, len(rest))
		case strings.TrimRight(rest, "\r") == "---" || strings.TrimRight(rest, "\r") == "...":
			l.skip(len(rest))
		default:
			if m := yamlKey.FindStringSubmatchIndex(rest); m != nil {
				l.emit(tokKey, m[3]-m[2])
				l.skip(m[1] - m[3])
				rest = rest[m[1]:]
			}
			if lexYAMLValue(l, rest) {
				blockIndent = column
			}
		}
		l.skipToLineEnd()
		l.skipNewline()
	}
	return l.tokens()
}

// skipToLineEnd leaves the rest of the current line plain.
func (l *lexer) skipToLineEnd() {
	l.pos += lineLen(l.src[l.pos:])
}

// lexYAMLValue marks a scalar value and reports a block scalar indicator.
func lexYAMLValue(l *lexer, rest string) (block bool) {
	for len(rest) > 0 {
		c := rest[0]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			l.skip(1)
			rest = rest[1:]
		case c == '#':
			l.emit(tokComment, len(rest))
			return block
		case c == '"' || c == '\'':
			n := closingQuote(rest, c == '"', true)
			if n == 0 {
				return block
			}
			l.emit(tokString, n)
			rest = rest[n:]
		case (c == '|' || c == '>') && yamlBlock.MatchString(rest):
			l.skip(1)
			return true
		case strings.IndexByte("[{&*!@`%", c) >= 0:
			return block
		default:
			end := strings.Index(rest, " #")
			if end < 0 {
				end = len(rest)
			}
			scalar := strings.TrimRight(rest[:end], " \t\r")
			switch {
			case yamlNumber.MatchString(scalar):
				l.emit(tokNumber, len(scalar))
			case yamlWords[strings.ToLower(scalar)]:
				l.emit(tokKeyword, len(scalar))
			default:
				l.skip(len(scalar))
			}
			if len(scalar) == 0 {
				return block
			}
			rest = rest[len(scalar):]
		}
	}
	return block
}

// Go, JavaScript and TypeScript

type cSyntax struct {
	keywords map[string]bool
	js       bool // template literals, $ in names, object keys are not keywords
}

func wordSet(words string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(words) {
		set[w] = true
	}
	return set
}

var (
	goSyntax = cSyntax{keywords: wordSet(`break case chan const continue default defer else fallthrough for func go goto if import
		interface map package range return select struct switch type var true false nil iota`)}
	// Contextual words such as type, as, of, from, get and set are left out:
	// they are usually plain names.
	jsSyntax = cSyntax{js: true, keywords: wordSet(`async await break case catch class const continue debugger default delete do else
		export extends false finally for function if import in instanceof let new null return static super switch this throw true try
		typeof undefined var void while with yield abstract declare enum implements interface keyof namespace private protected public
		readonly satisfies`)}
	cNumber = regexp.MustCompile(`^(?:0[xX][0-9a-fA-F_]+|0[bB][01_]+|0[oO][0-7_]+|(?:[0-9][0-9_]*(?:\.[0-9_]*)?|\.[0-9][0-9_]*)(?:[eE][-+]?[0-9_]+)?)`)
)

// numberLen is the length of the number that starts s when it stands alone
// (not followed by a letter, digit or dot), or 0.
func numberLen(pattern *regexp.Regexp, s, suffixes string) int {
	n := len(pattern.FindString(s))
	if n == 0 {
		return 0
	}
	if n < len(s) && strings.IndexByte(suffixes, s[n]) >= 0 {
		n++
	}
	if n < len(s) {
		if r, _ := utf8.DecodeRuneInString(s[n:]); r == '.' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			return 0
		}
	}
	return n
}

func lexCLike(code string, syn cSyntax) []token {
	l := &lexer{src: code}
	for l.pos < len(code) {
		s := code[l.pos:]
		c := s[0]
		r, _ := utf8.DecodeRuneInString(s)
		switch {
		case strings.HasPrefix(s, "//"):
			l.emit(tokComment, lineLen(s))
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s[2:], "*/"); i >= 0 {
				l.emit(tokComment, i+4)
			} else {
				l.skip(2)
			}
		case c == '"' || c == '\'':
			n := closingQuote(s, true, true)
			if c == '\'' && !syn.js && n > 12 {
				n = 0 // a Go rune literal is short
			}
			if n > 0 {
				l.emit(tokString, n)
			} else {
				l.skip(1)
			}
		case c == '`' && syn.js:
			lexTemplateLiteral(l)
		case c == '`':
			if i := strings.IndexByte(s[1:], '`'); i >= 0 {
				l.emit(tokString, i+2)
			} else {
				l.skip(1)
			}
		case isDigit(c) || (c == '.' && len(s) > 1 && isDigit(s[1])):
			suffix := "i"
			if syn.js {
				suffix = "n"
			}
			if n := numberLen(cNumber, s, suffix); n > 0 {
				l.emit(tokNumber, n)
			} else {
				l.skip(max(1, valueWordLen(s)))
			}
		case isIdentStart(r, syn.js):
			w := identLen(s, syn.js)
			word := s[:w]
			isKey := syn.js && strings.HasPrefix(strings.TrimLeft(s[w:], " \t"), ":")
			if syn.keywords[word] && prevNonSpace(code, l.pos) != '.' && !isKey {
				l.emit(tokKeyword, w)
			} else {
				l.skip(w)
			}
		default:
			l.skipRune()
		}
	}
	return l.tokens()
}

// lexTemplateLiteral marks a JavaScript template literal as a string, leaving
// each ${…} substitution plain. An unterminated literal stays plain.
func lexTemplateLiteral(l *lexer) {
	s := l.src[l.pos:]
	type part struct {
		class string
		n     int
	}
	var parts []part
	from := 0
	for i := 1; i < len(s); i++ {
		switch {
		case s[i] == '\\':
			i++
		case s[i] == '`':
			parts = append(parts, part{tokString, i + 1 - from})
			for _, p := range parts {
				if p.class == "" {
					l.skip(p.n)
				} else {
					l.emit(p.class, p.n)
				}
			}
			return
		case strings.HasPrefix(s[i:], "${"):
			end := matchingBrace(s, i+1)
			if end < 0 {
				l.skip(1)
				return
			}
			parts = append(parts, part{tokString, i - from}, part{"", end + 1 - i})
			from = end + 1
			i = end
		}
	}
	l.skip(1)
}

// matchingBrace is the index of the } that closes the { at s[open], or -1.
func matchingBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// Python and Starlark

var (
	pythonKeywords = wordSet(`False None True and as assert async await break class continue def del elif else except finally for
		from global if import in is lambda nonlocal not or pass raise return try while with yield`)
	pythonPrefixes = wordSet(`r u b f rb br fr rf`)
	pythonNumber   = regexp.MustCompile(`^(?:0[xX][0-9a-fA-F_]+|0[bB][01_]+|0[oO][0-7_]+|(?:[0-9][0-9_]*(?:\.[0-9_]*)?|\.[0-9][0-9_]*)(?:[eE][-+]?[0-9_]+)?)`)
)

// pythonString is the length of the string literal whose quote is s[at],
// including the prefix s[:at], or 0 when it is not closed.
func pythonString(s string, at int) int {
	q := s[at : at+1]
	if triple := strings.Repeat(q, 3); strings.HasPrefix(s[at:], triple) {
		for i := at + 3; i < len(s); i++ {
			if s[i] == '\\' {
				i++
				continue
			}
			if strings.HasPrefix(s[i:], triple) {
				return i + 3
			}
		}
		return 0
	}
	if n := closingQuote(s[at:], true, true); n > 0 {
		return at + n
	}
	return 0
}

func lexPython(code string) []token {
	l := &lexer{src: code}
	for l.pos < len(code) {
		s := code[l.pos:]
		c := s[0]
		r, _ := utf8.DecodeRuneInString(s)
		switch {
		case c == '#':
			l.emit(tokComment, lineLen(s))
		case c == '"' || c == '\'':
			if n := pythonString(s, 0); n > 0 {
				l.emit(tokString, n)
			} else {
				l.skip(1)
			}
		case isDigit(c) || (c == '.' && len(s) > 1 && isDigit(s[1])):
			if n := numberLen(pythonNumber, s, "jJ"); n > 0 {
				l.emit(tokNumber, n)
			} else {
				l.skip(max(1, valueWordLen(s)))
			}
		case isIdentStart(r, false):
			w := identLen(s, false)
			word := s[:w]
			if pythonPrefixes[strings.ToLower(word)] && w < len(s) && (s[w] == '"' || s[w] == '\'') {
				if n := pythonString(s, w); n > 0 {
					l.emit(tokString, n)
					continue
				}
			}
			if pythonKeywords[word] && prevNonSpace(code, l.pos) != '.' {
				l.emit(tokKeyword, w)
			} else {
				l.skip(w)
			}
		default:
			l.skipRune()
		}
	}
	return l.tokens()
}

// TOML and INI

var (
	tomlSection = regexp.MustCompile(`^\[\[?[^\[\]\n]+\]\]?`)
	tomlKey     = regexp.MustCompile(`^([A-Za-z0-9_-]+(?:[ \t]*\.[ \t]*[A-Za-z0-9_-]+)*|"(?:[^"\\\n]|\\.)*"|'[^'\n]*')[ \t]*=`)
	iniKey      = regexp.MustCompile(`^([^=;#\[\s][^=\n]*?)[ \t]*=`)
	tomlNumber  = regexp.MustCompile(`^[-+]?(?:0x[0-9A-Fa-f_]+|0o[0-7_]+|0b[01_]+|inf|nan|[0-9][0-9_]*(?:\.[0-9_]+)?(?:[eE][-+]?[0-9_]+)?)$`)
	iniNumber   = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
)

func lexTOML(code string, ini bool) []token {
	l := &lexer{src: code}
	for l.pos < len(code) {
		lineEnd := l.pos + lineLen(code[l.pos:])
		line := code[l.pos:lineEnd]
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		l.skip(indent)
		rest := line[indent:]
		switch {
		case rest == "":
		case rest[0] == '#' || (ini && rest[0] == ';'):
			l.emit(tokComment, len(rest))
		case rest[0] == '[' && tomlSection.MatchString(rest):
			l.emit(tokKeyword, len(tomlSection.FindString(rest)))
			if !ini {
				lexTOMLValues(l, lineEnd)
			}
		default:
			key := tomlKey
			if ini {
				key = iniKey
			}
			if m := key.FindStringSubmatchIndex(rest); m != nil {
				l.emit(tokKey, m[3]-m[2])
				l.skip(m[1] - m[3])
				rest = rest[m[1]:]
			}
			if ini {
				value := strings.TrimSpace(rest)
				l.skip(len(rest) - len(strings.TrimLeft(rest, " \t")))
				if iniNumber.MatchString(value) {
					l.emit(tokNumber, len(value))
				}
			} else {
				lexTOMLValues(l, lineEnd)
			}
		}
		l.skipToLineEnd()
		l.skipNewline()
	}
	return l.tokens()
}

// lexTOMLValues marks the values on the rest of a line; a multi-line string
// may run past lineEnd.
func lexTOMLValues(l *lexer, lineEnd int) {
	for l.pos < lineEnd {
		s := l.src[l.pos:lineEnd]
		c := s[0]
		switch {
		case strings.IndexByte(" \t\r,[]{}=", c) >= 0:
			l.skip(1)
		case c == '#':
			l.emit(tokComment, len(s))
			return
		case strings.HasPrefix(s, `"""`) || strings.HasPrefix(s, `'''`):
			if i := strings.Index(l.src[l.pos+3:], s[:3]); i >= 0 {
				l.emit(tokString, i+6)
				return
			}
			l.skip(3)
		case c == '"' || c == '\'':
			if n := closingQuote(s, c == '"', true); n > 0 {
				l.emit(tokString, n)
			} else {
				l.skip(1)
			}
		default:
			w := valueWordLen(s)
			if w == 0 {
				l.skipRune()
				continue
			}
			switch word := s[:w]; {
			case word == "true" || word == "false":
				l.emit(tokKeyword, w)
			case tomlNumber.MatchString(word):
				l.emit(tokNumber, w)
			default:
				l.skip(w)
			}
		}
	}
}
