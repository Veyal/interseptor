package postman

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// OMap is a JSON object that remembers key order and keeps every value as the
// exact raw bytes it was decoded from. It is what makes the importer/exporter
// pair lossless: unknown keys and key order survive a round trip.
type OMap struct {
	keys []string
	vals map[string]json.RawMessage
}

// NewOMap returns an empty ordered object.
func NewOMap() *OMap { return &OMap{vals: map[string]json.RawMessage{}} }

// ParseOMap decodes a JSON object preserving key order. Duplicate keys keep
// the last value at the first position.
func ParseOMap(raw []byte) (*OMap, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	m := NewOMap()
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected token %v", kt)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		m.Set(k, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return m, nil
}

// Keys returns the keys in order.
func (m *OMap) Keys() []string { return append([]string(nil), m.keys...) }

// Has reports whether the key exists.
func (m *OMap) Has(k string) bool { _, ok := m.vals[k]; return ok }

// Get returns the raw value (nil when absent).
func (m *OMap) Get(k string) json.RawMessage { return m.vals[k] }

// Set stores a value, appending the key when new.
func (m *OMap) Set(k string, v json.RawMessage) {
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
}

// SetValue marshals v and stores it.
func (m *OMap) SetValue(k string, v any) {
	b, err := Marshal(v)
	if err != nil {
		return
	}
	m.Set(k, b)
}

// Marshal is json.Marshal without HTML escaping, so exports keep < > & as
// written in the source collection.
func Marshal(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// MarshalArray joins raw elements into a JSON array without re-escaping them.
func MarshalArray(elems []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, e := range elems {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(e)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// Del removes a key.
func (m *OMap) Del(k string) {
	if _, ok := m.vals[k]; !ok {
		return
	}
	delete(m.vals, k)
	for i, kk := range m.keys {
		if kk == k {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			return
		}
	}
}

// Order rearranges keys so those listed come first in that order (unknown
// listed keys are ignored); the rest keep their relative order after them.
func (m *OMap) Order(first []string) {
	seen := map[string]bool{}
	var out []string
	for _, k := range first {
		if _, ok := m.vals[k]; ok && !seen[k] {
			out = append(out, k)
			seen[k] = true
		}
	}
	for _, k := range m.keys {
		if !seen[k] {
			out = append(out, k)
		}
	}
	m.keys = out
}

// MarshalJSON writes the object compactly in key order.
func (m *OMap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		v := m.vals[k]
		if len(v) == 0 {
			v = json.RawMessage("null")
		}
		var c bytes.Buffer
		if err := json.Compact(&c, v); err != nil {
			return nil, err
		}
		b.Write(c.Bytes())
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Rest returns the entries whose keys are not in known, as a plain map, and
// removes nothing. Used to capture unknown keys into sidecars.
func (m *OMap) Rest(known ...string) map[string]json.RawMessage {
	skip := map[string]bool{}
	for _, k := range known {
		skip[k] = true
	}
	out := map[string]json.RawMessage{}
	for _, k := range m.keys {
		if !skip[k] {
			out[k] = m.vals[k]
		}
	}
	return out
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}
