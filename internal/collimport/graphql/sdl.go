package graphql

import (
	"encoding/json"
	"fmt"
	"strings"
)

type tokKind uint8

const (
	tEOF tokKind = iota
	tName
	tPunct
	tString
	tNum
)

type token struct {
	kind        tokKind
	text        string // name / punct / decoded string / number
	line        int
	commaBefore bool
}

type syntaxErr struct {
	msg       string
	line      int
	notFormat bool
}

const maxValueDepth = 64

// parser is a one-token-lookahead SDL parser. It is lenient about ordering
// and repeated definitions (they merge) and strict about token structure.
type parser struct {
	src  string
	pos  int
	line int
	tok  token
	s    *schemaDef
	defs int
}

func (p *parser) fail(format string, a ...any) {
	panic(syntaxErr{msg: fmt.Sprintf(format, a...), line: p.tok.line})
}

// parseSDL parses a GraphQL type-system document.
func parseSDL(src string) (s *schemaDef, err error) {
	p := &parser{src: src, line: 1, s: newSchema()}
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(syntaxErr)
			if !ok {
				panic(r)
			}
			base := ErrSyntax
			if se.notFormat {
				base = ErrNotGraphQL
			}
			s, err = nil, fmt.Errorf("%w: line %d: %s", base, se.line, se.msg)
		}
	}()
	p.advance(true)
	for p.tok.kind != tEOF {
		p.definition()
		p.defs++
	}
	if p.defs == 0 {
		return nil, fmt.Errorf("%w: no definitions", ErrNotGraphQL)
	}
	p.s.finishSDL()
	return p.s, nil
}

// ---- lexer ----

func isNameStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isNameChar(c byte) bool  { return isNameStart(c) || c >= '0' && c <= '9' }

func (p *parser) advance(first bool) {
	comma := false
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == '\n':
			p.line++
			p.pos++
		case c == ' ' || c == '\t' || c == '\r':
			p.pos++
		case c == ',':
			comma = true
			p.pos++
		case c == '#':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		case c == 0xEF && strings.HasPrefix(p.src[p.pos:], "\xef\xbb\xbf"):
			p.pos += 3
		default:
			goto lex
		}
	}
lex:
	p.tok = token{line: p.line, commaBefore: comma}
	if p.pos >= len(p.src) {
		p.tok.kind = tEOF
		return
	}
	c := p.src[p.pos]
	switch {
	case isNameStart(c):
		i := p.pos
		for i < len(p.src) && isNameChar(p.src[i]) {
			i++
		}
		p.tok.kind, p.tok.text = tName, p.src[p.pos:i]
		p.pos = i
	case c == '"':
		p.tok.kind = tString
		p.tok.text = p.lexString()
	case c >= '0' && c <= '9' || c == '-':
		i := p.pos + 1
		for i < len(p.src) && (isNameChar(p.src[i]) || p.src[i] == '.' || p.src[i] == '-' || p.src[i] == '+') {
			i++
		}
		p.tok.kind, p.tok.text = tNum, p.src[p.pos:i]
		p.pos = i
	case c == '.':
		if !strings.HasPrefix(p.src[p.pos:], "...") {
			panic(syntaxErr{msg: "unexpected '.'", line: p.line, notFormat: first})
		}
		p.tok.kind, p.tok.text = tPunct, "..."
		p.pos += 3
	case strings.IndexByte("!$&():=@[]{|}", c) >= 0:
		p.tok.kind, p.tok.text = tPunct, string(c)
		p.pos++
	default:
		panic(syntaxErr{msg: fmt.Sprintf("unexpected character %q", rune(c)), line: p.line, notFormat: first})
	}
}

func (p *parser) lexString() string {
	src := p.src
	if strings.HasPrefix(src[p.pos:], `"""`) {
		i := p.pos + 3
		startLine := p.line
		for i < len(src) {
			switch {
			case strings.HasPrefix(src[i:], `\"""`):
				i += 4
			case strings.HasPrefix(src[i:], `"""`):
				raw := strings.ReplaceAll(src[p.pos+3:i], `\"""`, `"""`)
				p.line += strings.Count(raw, "\n")
				p.pos = i + 3
				return blockString(raw)
			default:
				i++
			}
		}
		panic(syntaxErr{msg: "unterminated block string", line: startLine})
	}
	i := p.pos + 1
	for i < len(src) {
		switch src[i] {
		case '\\':
			i += 2
			continue
		case '\n':
			panic(syntaxErr{msg: "unterminated string", line: p.line})
		case '"':
			lit := src[p.pos : i+1]
			p.pos = i + 1
			var v string
			if json.Unmarshal([]byte(lit), &v) != nil {
				v = lit[1 : len(lit)-1]
			}
			return v
		}
		i++
	}
	panic(syntaxErr{msg: "unterminated string", line: p.line})
}

// blockString applies the GraphQL common-indent and blank-line rules.
func blockString(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	indent := -1
	for _, l := range lines[1:] {
		t := strings.TrimLeft(l, " \t")
		if t == "" {
			continue
		}
		if n := len(l) - len(t); indent < 0 || n < indent {
			indent = n
		}
	}
	if indent > 0 {
		for i := 1; i < len(lines); i++ {
			if len(lines[i]) >= indent {
				lines[i] = lines[i][indent:]
			} else {
				lines[i] = strings.TrimLeft(lines[i], " \t")
			}
		}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// ---- grammar ----

func (p *parser) isPunct(s string) bool { return p.tok.kind == tPunct && p.tok.text == s }

func (p *parser) skipPunct(s string) bool {
	if p.isPunct(s) {
		p.advance(false)
		return true
	}
	return false
}

func (p *parser) expect(s string) {
	if !p.skipPunct(s) {
		p.fail("expected %q, found %s", s, p.describe())
	}
}

func (p *parser) describe() string {
	switch p.tok.kind {
	case tEOF:
		return "end of input"
	case tString:
		return "a string"
	}
	return fmt.Sprintf("%q", p.tok.text)
}

func (p *parser) name() string {
	if p.tok.kind != tName {
		p.fail("expected a name, found %s", p.describe())
	}
	n := p.tok.text
	p.advance(false)
	return n
}

func (p *parser) optDesc() string {
	if p.tok.kind == tString {
		d := clip(p.tok.text)
		p.advance(false)
		return d
	}
	return ""
}

func (p *parser) definition() {
	first := p.defs == 0
	desc := p.optDesc()
	if p.tok.kind != tName {
		if first {
			panic(syntaxErr{msg: "not a GraphQL type-system document", line: p.tok.line, notFormat: true})
		}
		p.fail("expected a definition, found %s", p.describe())
	}
	kw := p.tok.text
	line := p.tok.line
	switch kw {
	case "schema", "scalar", "type", "interface", "union", "enum", "input", "directive", "extend":
	default:
		if first {
			panic(syntaxErr{msg: fmt.Sprintf("not a GraphQL type-system document (%q)", kw), line: line, notFormat: true})
		}
		p.fail("unknown definition keyword %q", kw)
	}
	p.advance(false)
	if kw == "extend" {
		kw = p.name()
	}
	switch kw {
	case "schema":
		p.directives()
		if p.skipPunct("{") {
			for !p.isPunct("}") {
				op := p.name()
				p.expect(":")
				t := p.name()
				switch op {
				case "query":
					p.s.query = t
				case "mutation":
					p.s.mutation = t
				case "subscription":
					p.s.subscription = t
				default:
					p.fail("unknown operation type %q", op)
				}
			}
			p.expect("}")
		}
	case "scalar":
		td := p.s.get(p.name(), kScalar)
		if td.desc == "" {
			td.desc = desc
		}
		p.directives()
	case "type", "interface":
		kind := kObject
		if kw == "interface" {
			kind = kInterface
		}
		td := p.s.get(p.name(), kind)
		if td.desc == "" {
			td.desc = desc
		}
		if p.tok.kind == tName && p.tok.text == "implements" {
			p.advance(false)
			p.skipPunct("&")
			for p.tok.kind == tName {
				td.ifaces = append(td.ifaces, p.tok.text)
				p.advance(false)
				if !p.skipPunct("&") && !p.tok.commaBefore {
					break
				}
			}
		}
		p.directives()
		if p.skipPunct("{") {
			for !p.isPunct("}") {
				td.fields = append(td.fields, p.fieldDef())
				p.s.totalFields++
				if p.s.totalFields > maxFieldsTotal {
					p.fail("more than %d fields", maxFieldsTotal)
				}
			}
			p.expect("}")
		}
	case "input":
		td := p.s.get(p.name(), kInput)
		if td.desc == "" {
			td.desc = desc
		}
		p.directives()
		if p.skipPunct("{") {
			for !p.isPunct("}") {
				p.optDesc()
				td.inputs = append(td.inputs, p.inputValue())
			}
			p.expect("}")
		}
	case "union":
		td := p.s.get(p.name(), kUnion)
		p.directives()
		if p.skipPunct("=") {
			p.skipPunct("|")
			td.possible = append(td.possible, p.name())
			for p.skipPunct("|") {
				td.possible = append(td.possible, p.name())
			}
		}
	case "enum":
		td := p.s.get(p.name(), kEnum)
		p.directives()
		if p.skipPunct("{") {
			for !p.isPunct("}") {
				p.optDesc()
				td.enums = append(td.enums, p.name())
				p.directives()
			}
			p.expect("}")
		}
	case "directive":
		p.expect("@")
		p.name()
		if p.isPunct("(") {
			p.argsDef()
		}
		if p.tok.kind == tName && p.tok.text == "repeatable" {
			p.advance(false)
		}
		if n := p.name(); n != "on" {
			p.fail("expected \"on\", found %q", n)
		}
		p.skipPunct("|")
		p.name()
		for p.skipPunct("|") {
			p.name()
		}
	default:
		p.fail("cannot extend %q", kw)
	}
	p.s.countType(p)
}

func (s *schemaDef) countType(p *parser) {
	if len(s.order) > maxTypes {
		p.fail("more than %d types", maxTypes)
	}
}

func (p *parser) argsDef() []inputValue {
	p.expect("(")
	var out []inputValue
	for !p.isPunct(")") {
		p.optDesc()
		out = append(out, p.inputValue())
	}
	p.expect(")")
	return out
}

func (p *parser) inputValue() inputValue {
	iv := inputValue{name: p.name()}
	p.expect(":")
	iv.typ = p.typeRef(0)
	if p.skipPunct("=") {
		iv.hasDefault = true
		p.skipValue(0)
	}
	p.directives()
	return iv
}

func (p *parser) fieldDef() *fieldDef {
	f := &fieldDef{desc: p.optDesc(), name: p.name()}
	if p.isPunct("(") {
		f.args = p.argsDef()
	}
	p.expect(":")
	f.typ = p.typeRef(0)
	f.deprecated, f.reason = p.directives()
	return f
}

func (p *parser) typeRef(depth int) *tref {
	if depth > maxTypeRefDepth {
		p.fail("type nested deeper than %d", maxTypeRefDepth)
	}
	var t *tref
	if p.skipPunct("[") {
		t = &tref{kind: refList, elem: p.typeRef(depth + 1)}
		p.expect("]")
	} else {
		t = &tref{kind: refNamed, name: p.name()}
	}
	if p.skipPunct("!") {
		t = &tref{kind: refNonNull, elem: t}
	}
	return t
}

// directives consumes directive applications and reports @deprecated.
func (p *parser) directives() (deprecated bool, reason string) {
	for p.isPunct("@") {
		p.advance(false)
		n := p.name()
		isDep := n == "deprecated"
		if isDep {
			deprecated = true
		}
		if p.skipPunct("(") {
			for !p.isPunct(")") {
				an := p.name()
				p.expect(":")
				v := p.skipValue(0)
				if isDep && an == "reason" {
					reason = v
				}
			}
			p.expect(")")
		}
	}
	return
}

// skipValue consumes a GraphQL value; it returns the string value of a plain
// string literal (used for deprecation reasons) and "" for everything else.
func (p *parser) skipValue(depth int) string {
	if depth > maxValueDepth {
		p.fail("value nested deeper than %d", maxValueDepth)
	}
	switch {
	case p.tok.kind == tString:
		v := clip(p.tok.text)
		p.advance(false)
		return v
	case p.tok.kind == tName || p.tok.kind == tNum:
		p.advance(false)
	case p.isPunct("$"):
		p.advance(false)
		p.name()
	case p.isPunct("["):
		p.advance(false)
		for !p.isPunct("]") {
			p.skipValue(depth + 1)
		}
		p.advance(false)
	case p.isPunct("{"):
		p.advance(false)
		for !p.isPunct("}") {
			p.name()
			p.expect(":")
			p.skipValue(depth + 1)
		}
		p.advance(false)
	default:
		p.fail("expected a value, found %s", p.describe())
	}
	return ""
}
