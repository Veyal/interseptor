package curl

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Tokenizer limits (hostile-input bounds).
const (
	MaxInputBytes = 4 << 20
	MaxTokens     = 200000
)

// Tokenizer errors.
var (
	ErrTooLarge     = errors.New("curl: input exceeds 4 MiB")
	ErrTooManyToken = errors.New("curl: too many tokens")
	ErrUnterminated = errors.New("curl: unterminated quote")
)

// Token is one shell word, or a command separator (Sep) such as a newline,
// ';', '&&', '||' or '|'. Dyn marks a word that contains a shell expansion
// ($VAR, ${..}, $(..), backticks) which this package never evaluates.
type Token struct {
	Text string
	Sep  bool
	Dyn  bool
}

// Tokenize splits a shell command line the way bash does for the subset that
// matters for curl: single and double quotes, $'..' ANSI-C quoting, backslash
// and PowerShell-backtick line continuations, comments, and command
// separators. cmd.exe "^" escapes (Chrome "Copy as cURL (cmd)") are undone
// first. Nothing is executed or expanded.
func Tokenize(src string) ([]Token, error) {
	if len(src) > MaxInputBytes {
		return nil, ErrTooLarge
	}
	if looksLikeCmd(src) {
		src = unCaret(src)
	}
	t := &tokenizer{s: src}
	return t.run()
}

func looksLikeCmd(s string) bool {
	return strings.Contains(s, "^\"") || strings.Contains(s, "^\n") || strings.Contains(s, "^\r\n")
}

// unCaret removes cmd.exe caret escapes: "^<newline>" is a continuation and
// "^c" is the literal c.
func unCaret(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '^' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			break
		}
		switch {
		case s[i+1] == '\n':
			i++
		case s[i+1] == '\r':
			i++
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
		default:
			i++
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

type tokenizer struct {
	s      string
	i      int
	cur    strings.Builder
	inWord bool
	dyn    bool
	out    []Token
}

func (t *tokenizer) push(tok Token) error {
	if len(t.out) >= MaxTokens {
		return ErrTooManyToken
	}
	t.out = append(t.out, tok)
	return nil
}

func (t *tokenizer) endWord() error {
	if !t.inWord {
		return nil
	}
	tok := Token{Text: t.cur.String(), Dyn: t.dyn}
	t.cur.Reset()
	t.inWord, t.dyn = false, false
	return t.push(tok)
}

func (t *tokenizer) sep(text string) error {
	if err := t.endWord(); err != nil {
		return err
	}
	if n := len(t.out); n > 0 && t.out[n-1].Sep {
		return nil // collapse runs of separators
	}
	if len(t.out) == 0 {
		return nil
	}
	return t.push(Token{Text: text, Sep: true})
}

func (t *tokenizer) add(c byte) { t.cur.WriteByte(c); t.inWord = true }

func (t *tokenizer) run() ([]Token, error) {
	s := t.s
	for t.i < len(s) {
		c := s[t.i]
		var err error
		switch {
		case c == ' ' || c == '\t':
			err = t.endWord()
			t.i++
		case c == '\n' || c == '\r':
			err = t.sep("\n")
			t.i++
		case c == ';':
			err = t.sep(";")
			t.i++
		case c == '|':
			err = t.sep("|")
			t.i++
			if t.i < len(s) && s[t.i] == '|' {
				t.i++
			}
		case c == '&':
			err = t.sep("&")
			t.i++
			if t.i < len(s) && s[t.i] == '&' {
				t.i++
			}
		case c == '#' && !t.inWord:
			for t.i < len(s) && s[t.i] != '\n' {
				t.i++
			}
		case c == '\\':
			t.backslash()
		case c == '`':
			err = t.backtick()
		case c == '\'':
			err = t.single()
		case c == '"':
			err = t.double()
		case c == '$':
			err = t.dollar()
		default:
			t.add(c)
			t.i++
		}
		if err != nil {
			return nil, err
		}
	}
	if err := t.endWord(); err != nil {
		return nil, err
	}
	if n := len(t.out); n > 0 && t.out[n-1].Sep {
		t.out = t.out[:n-1]
	}
	return t.out, nil
}

// eol reports the length of a newline at s[i:] (0 when none).
func eol(s string, i int) int {
	if i < len(s) && s[i] == '\n' {
		return 1
	}
	if i+1 < len(s) && s[i] == '\r' && s[i+1] == '\n' {
		return 2
	}
	return 0
}

func (t *tokenizer) backslash() {
	s := t.s
	if n := eol(s, t.i+1); n > 0 {
		t.i += 1 + n // line continuation
		return
	}
	if t.i+1 >= len(s) {
		t.i++
		return
	}
	t.add(s[t.i+1])
	t.i += 2
}

func (t *tokenizer) backtick() error {
	if n := eol(t.s, t.i+1); n > 0 {
		t.i += 1 + n // PowerShell continuation
		return nil
	}
	t.dyn = true
	t.add('`')
	t.i++
	return nil
}

func (t *tokenizer) single() error {
	t.i++
	t.inWord = true
	end := strings.IndexByte(t.s[t.i:], '\'')
	if end < 0 {
		return ErrUnterminated
	}
	t.cur.WriteString(t.s[t.i : t.i+end])
	t.i += end + 1
	return nil
}

func (t *tokenizer) double() error {
	s := t.s
	t.i++
	t.inWord = true
	for t.i < len(s) {
		c := s[t.i]
		switch {
		case c == '"':
			t.i++
			return nil
		case c == '\\' && t.i+1 < len(s):
			n := s[t.i+1]
			if k := eol(s, t.i+1); k > 0 {
				t.i += 1 + k
				continue
			}
			if n == '$' || n == '`' || n == '"' || n == '\\' {
				t.cur.WriteByte(n)
				t.i += 2
				continue
			}
			t.cur.WriteByte(c)
			t.i++
		case c == '$' || c == '`':
			if t.expansionAt(t.i) {
				t.dyn = true
			}
			t.cur.WriteByte(c)
			t.i++
		default:
			t.cur.WriteByte(c)
			t.i++
		}
	}
	return ErrUnterminated
}

// expansionAt reports whether s[i] begins a shell expansion.
func (t *tokenizer) expansionAt(i int) bool {
	s := t.s
	if s[i] == '`' {
		return true
	}
	if i+1 >= len(s) {
		return false
	}
	n := s[i+1]
	return n == '(' || n == '{' || n == '_' || (n >= 'a' && n <= 'z') || (n >= 'A' && n <= 'Z') || n == '@' || n == '*' || (n >= '0' && n <= '9')
}

func (t *tokenizer) dollar() error {
	s := t.s
	if t.i+1 < len(s) && s[t.i+1] == '\'' {
		t.i += 2
		t.inWord = true
		return t.ansiC()
	}
	if t.i+1 < len(s) && s[t.i+1] == '"' {
		t.i++ // locale string: treat as a plain double-quoted string
		return nil
	}
	if t.expansionAt(t.i) {
		t.dyn = true
	}
	t.add('$')
	t.i++
	return nil
}

// ansiC reads the body of $'...' (the opening quote is consumed).
func (t *tokenizer) ansiC() error {
	s := t.s
	for t.i < len(s) {
		c := s[t.i]
		if c == '\'' {
			t.i++
			return nil
		}
		if c != '\\' || t.i+1 >= len(s) {
			t.cur.WriteByte(c)
			t.i++
			continue
		}
		t.i++
		e := s[t.i]
		t.i++
		switch e {
		case 'n':
			t.cur.WriteByte('\n')
		case 't':
			t.cur.WriteByte('\t')
		case 'r':
			t.cur.WriteByte('\r')
		case 'a':
			t.cur.WriteByte(7)
		case 'b':
			t.cur.WriteByte(8)
		case 'e', 'E':
			t.cur.WriteByte(27)
		case 'f':
			t.cur.WriteByte(12)
		case 'v':
			t.cur.WriteByte(11)
		case '\\', '\'', '"', '?':
			t.cur.WriteByte(e)
		case 'x':
			t.hexEscape(2, false)
		case 'u':
			t.hexEscape(4, true)
		case 'U':
			t.hexEscape(8, true)
		case 'c':
			if t.i < len(s) {
				t.cur.WriteByte(s[t.i] & 0x1f)
				t.i++
			}
		case '0', '1', '2', '3', '4', '5', '6', '7':
			t.octalEscape(e)
		default:
			t.cur.WriteByte('\\')
			t.cur.WriteByte(e)
		}
	}
	return ErrUnterminated
}

func (t *tokenizer) hexEscape(max int, uni bool) {
	s := t.s
	j := t.i
	for j < len(s) && j-t.i < max && isHex(s[j]) {
		j++
	}
	if j == t.i {
		t.cur.WriteByte('\\')
		return
	}
	v, _ := strconv.ParseUint(s[t.i:j], 16, 32)
	t.i = j
	if uni {
		if v > utf8.MaxRune || (v >= 0xD800 && v <= 0xDFFF) {
			t.cur.WriteRune(utf8.RuneError)
			return
		}
		t.cur.WriteRune(rune(v))
		return
	}
	t.cur.WriteByte(byte(v))
}

func (t *tokenizer) octalEscape(first byte) {
	s := t.s
	v := int(first - '0')
	for n := 0; n < 2 && t.i < len(s) && s[t.i] >= '0' && s[t.i] <= '7'; n++ {
		v = v*8 + int(s[t.i]-'0')
		t.i++
	}
	t.cur.WriteByte(byte(v))
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
