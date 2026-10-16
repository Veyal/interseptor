package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parse limits for hostile specs.
const (
	MaxInputBytes = 32 << 20
	MaxNodes      = 2_000_000
	MaxDepth      = 128
)

// Decode errors.
var (
	ErrTooLarge  = errors.New("openapi: input exceeds 32 MiB")
	ErrTooDeep   = errors.New("openapi: document nesting too deep")
	ErrTooManyN  = errors.New("openapi: document has too many nodes")
	ErrNotObject = errors.New("openapi: document root is not an object")
)

// omap is an insertion-ordered JSON/YAML object. Values are *omap, []any,
// string, json.Number, bool or nil.
type omap struct {
	keys []string
	m    map[string]any
}

func newOmap() *omap { return &omap{m: map[string]any{}} }

func (o *omap) set(k string, v any) {
	if _, ok := o.m[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.m[k] = v
}

func (o *omap) get(k string) any {
	if o == nil {
		return nil
	}
	return o.m[k]
}

func (o *omap) has(k string) bool {
	if o == nil {
		return false
	}
	_, ok := o.m[k]
	return ok
}

// MarshalJSON writes the object in key order without HTML escaping.
func (o *omap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := marshalNoEscape(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := marshalNoEscape(o.m[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// counter bounds the total node count of a decode.
type counter struct {
	n       int
	alias   int // nodes produced by expanding YAML aliases
	inAlias int // >0 while walking an alias target
}

// MaxAliasNodes bounds YAML alias expansion ("billion laughs").
const MaxAliasNodes = 200_000

func (c *counter) tick() error {
	c.n++
	if c.n > MaxNodes {
		return ErrTooManyN
	}
	if c.inAlias > 0 {
		c.alias++
		if c.alias > MaxAliasNodes {
			return ErrTooManyN
		}
	}
	return nil
}

// decode parses JSON or YAML into the ordered tree.
func decode(data []byte) (any, error) {
	if len(data) > MaxInputBytes {
		return nil, ErrTooLarge
	}
	trim := bytes.TrimLeft(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), " \t\r\n")
	if len(trim) > 0 && (trim[0] == '{' || trim[0] == '[') {
		dec := json.NewDecoder(bytes.NewReader(trim))
		dec.UseNumber()
		c := &counter{}
		v, err := decodeJSON(dec, c, 0)
		if err != nil {
			return nil, err
		}
		if _, err := dec.Token(); err != io.EOF {
			return nil, errors.New("openapi: trailing data after JSON document")
		}
		return v, nil
	}
	var n yaml.Node
	if err := yaml.Unmarshal(trim, &n); err != nil {
		return nil, fmt.Errorf("openapi: invalid YAML: %w", err)
	}
	if n.Kind == 0 {
		return nil, errors.New("openapi: empty document")
	}
	return convertYAML(&n, &counter{}, 0)
}

func decodeJSON(dec *json.Decoder, c *counter, depth int) (any, error) {
	if depth > MaxDepth {
		return nil, ErrTooDeep
	}
	if err := c.tick(); err != nil {
		return nil, err
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			o := newOmap()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				v, err := decodeJSON(dec, c, depth+1)
				if err != nil {
					return nil, err
				}
				o.set(k, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return o, nil
		}
		arr := []any{}
		for dec.More() {
			v, err := decodeJSON(dec, c, depth+1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	default:
		return tok, nil // string, json.Number, bool, nil
	}
}

func convertYAML(n *yaml.Node, c *counter, depth int) (any, error) {
	if depth > MaxDepth {
		return nil, ErrTooDeep
	}
	if err := c.tick(); err != nil {
		return nil, err
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return convertYAML(n.Content[0], c, depth+1)
	case yaml.AliasNode:
		if n.Alias == nil {
			return nil, nil
		}
		c.inAlias++
		defer func() { c.inAlias-- }()
		return convertYAML(n.Alias, c, depth+1)
	case yaml.MappingNode:
		return convertYAMLMap(n, c, depth)
	case yaml.SequenceNode:
		arr := make([]any, 0, len(n.Content))
		for _, ch := range n.Content {
			v, err := convertYAML(ch, c, depth+1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	}
	return yamlScalar(n), nil
}

func convertYAMLMap(n *yaml.Node, c *counter, depth int) (any, error) {
	o := newOmap()
	var merges []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		if k.ShortTag() == "!!merge" {
			merges = append(merges, v)
			continue
		}
		val, err := convertYAML(v, c, depth+1)
		if err != nil {
			return nil, err
		}
		o.set(k.Value, val)
	}
	for _, mv := range merges {
		src := []*yaml.Node{mv}
		if mv.Kind == yaml.SequenceNode {
			src = mv.Content
		}
		for _, s := range src {
			v, err := convertYAML(s, c, depth+1)
			if err != nil {
				return nil, err
			}
			if sm, ok := v.(*omap); ok {
				for _, k := range sm.keys {
					if !o.has(k) {
						o.set(k, sm.m[k])
					}
				}
			}
		}
	}
	return o, nil
}

func yamlScalar(n *yaml.Node) any {
	switch n.ShortTag() {
	case "!!null":
		return nil
	case "!!bool":
		return n.Value == "true" || n.Value == "True" || n.Value == "TRUE"
	case "!!int", "!!float":
		s := strings.ReplaceAll(n.Value, "_", "")
		if _, err := strconv.ParseFloat(s, 64); err == nil && !strings.ContainsAny(s, "xXoO") {
			return json.Number(s)
		}
		if i, err := strconv.ParseInt(s, 0, 64); err == nil {
			return json.Number(strconv.FormatInt(i, 10))
		}
	}
	return n.Value
}

// --- typed accessors -------------------------------------------------------

func asMap(v any) *omap {
	o, _ := v.(*omap)
	return o
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}
