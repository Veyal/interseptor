package bruno

import (
	"errors"
	"strings"
)

// Limits for hostile .bru input.
const (
	MaxFileBytes  = 8 << 20
	MaxBlocks     = 200
	MaxBlockLines = 200000
)

// ErrNotBru is returned for input that holds no recognizable .bru block.
var ErrNotBru = errors.New("bruno: not a .bru file")

// kv is one dictionary entry; Disabled is set by the "~" prefix.
type kv struct {
	Key, Value string
	Disabled   bool
	Line       int
}

// block is one top-level block of a .bru file. Dictionary blocks fill Pairs,
// text blocks (body:*, script:*, tests, docs) fill Text, array blocks
// (vars:secret) fill List.
type block struct {
	Name  string
	Pairs []kv
	Text  string
	List  []string
	Line  int
}

type bru struct {
	blocks []block
	warns  []string
}

func (b *bru) get(name string) *block {
	for i := range b.blocks {
		if b.blocks[i].Name == name {
			return &b.blocks[i]
		}
	}
	return nil
}

func isTextBlock(name string) bool {
	return strings.HasPrefix(name, "body:") && name != "body:form-urlencoded" && name != "body:multipart-form" && name != "body:file" ||
		strings.HasPrefix(name, "script:") || name == "tests" || name == "docs" || name == "example"
}

// parseBru parses the .bru markup. It never evaluates anything; unknown
// blocks are kept (as text) so the caller can report them.
func parseBru(src string) (*bru, error) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")
	out := &bru{}
	for i := 0; i < len(lines); i++ {
		ln := strings.TrimRight(lines[i], " \t")
		if strings.TrimSpace(ln) == "" {
			continue
		}
		name, kind, ok := blockStart(ln)
		if !ok {
			out.warns = append(out.warns, "line "+itoa(i+1)+": text outside any block ignored")
			continue
		}
		if len(out.blocks) >= MaxBlocks {
			return out, errors.New("bruno: too many blocks in one file")
		}
		start := i + 1
		end := start
		closer := "}"
		if kind == '[' {
			closer = "]"
		}
		for end < len(lines) && strings.TrimRight(lines[end], " \t") != closer {
			end++
		}
		if end-start > MaxBlockLines {
			return out, errors.New("bruno: block too large")
		}
		body := lines[start:end]
		if end >= len(lines) {
			out.warns = append(out.warns, "line "+itoa(i+1)+": block "+name+" is not closed")
		}
		blk := block{Name: name, Line: i + 1}
		switch {
		case kind == '[':
			for _, l := range body {
				for _, p := range strings.Split(strings.TrimSpace(l), ",") {
					if p = strings.TrimSpace(p); p != "" {
						blk.List = append(blk.List, p)
					}
				}
			}
		case isTextBlock(name):
			blk.Text = dedent(body)
		default:
			blk.Pairs = parsePairs(body, start+1)
		}
		out.blocks = append(out.blocks, blk)
		i = end
	}
	if len(out.blocks) == 0 {
		return out, ErrNotBru
	}
	return out, nil
}

// blockStart recognizes "name {" and "name [" at column 0.
func blockStart(ln string) (name string, kind byte, ok bool) {
	if ln == "" || ln[0] == ' ' || ln[0] == '\t' {
		return
	}
	last := ln[len(ln)-1]
	if last != '{' && last != '[' {
		return
	}
	name = strings.TrimSpace(ln[:len(ln)-1])
	if name == "" {
		return
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ':' || c == '-' || c == '_') {
			return "", 0, false
		}
	}
	return name, last, true
}

// dedent strips the two-space block indent .bru adds to text bodies.
func dedent(lines []string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "  "):
			out[i] = l[2:]
		default:
			out[i] = strings.TrimLeft(l, " ")
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// parsePairs reads "key: value" lines; "~" disables a row and ”' opens a
// multi-line value.
func parsePairs(lines []string, firstLine int) []kv {
	var out []kv
	for i := 0; i < len(lines); i++ {
		raw := strings.TrimSpace(lines[i])
		if raw == "" {
			continue
		}
		disabled := false
		if strings.HasPrefix(raw, "~") {
			disabled, raw = true, strings.TrimSpace(raw[1:])
		}
		key, val, found := strings.Cut(raw, ":")
		if !found {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		line := firstLine + i
		if strings.HasPrefix(val, "'''") {
			rest := val[3:]
			if j := strings.Index(rest, "'''"); j >= 0 {
				val = rest[:j]
			} else {
				ml := []string{rest}
				i++
				for ; i < len(lines); i++ {
					t := strings.TrimSpace(lines[i])
					if t == "'''" {
						break
					}
					ml = append(ml, strings.TrimPrefix(lines[i], "  "))
				}
				val = strings.TrimLeft(strings.Join(ml, "\n"), "\n")
			}
		}
		out = append(out, kv{Key: key, Value: val, Disabled: disabled, Line: line})
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
