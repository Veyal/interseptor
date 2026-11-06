// Package msgcodec runs project-scoped Starlark message codecs that decode
// (and optionally re-encode) application-layer request/response bodies for
// display and editing. Codecs never run on the proxy hot path — only on
// explicit UI/API/MCP decode requests — so failures cannot break forwarding.
package msgcodec

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.starlark.net/starlark"

	"github.com/Veyal/interseptor/internal/starx"
)

// MaxSteps is the Starlark step budget for one compile/match/decode/encode
// call. Exported so the codec editor can publish it and a budget failure can
// name what it hit instead of a bare "computation cancelled".
const MaxSteps = 5_000_000

const maxSteps = MaxSteps

// budgetError annotates a Starlark cancellation with the budget it hit. The
// library's own text ("Starlark computation cancelled: too many steps") gives no
// scale, so sizing a codec was guesswork.
func budgetError(err error, steps uint64) error {
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), "too many steps") {
		return err
	}
	return fmt.Errorf("%w (used %d of %d steps)", err, steps, MaxSteps)
}

// budgetFormatted is budgetError for an error already rendered by
// starx.ScriptError: an EvalError's backtrace replaces the wrapped text, so the
// hint has to be appended to the formatted message instead.
func budgetFormatted(err error, steps uint64) error {
	if err == nil || !strings.Contains(err.Error(), "too many steps") {
		return err
	}
	if strings.Contains(err.Error(), " steps)") {
		return err
	}
	return fmt.Errorf("%w (used %d of %d steps)", err, steps, MaxSteps)
}

// Meta is codec metadata from the script's `meta` dict (or defaults from id).
type Meta struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	ApplyOnSend bool   `json:"applyOnSend"`
	Enabled     bool   `json:"enabled"`
}

// Result is what Decode returns for a matching codec.
type Result struct {
	CodecID   string            `json:"codecId"`
	Title     string            `json:"title,omitempty"`
	Plaintext string            `json:"plaintext"`
	Fields    map[string]string `json:"fields,omitempty"`
	Note      string            `json:"note,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// Flow is the read-only view handed to match/decode/encode.
type Flow struct {
	Method     string
	Scheme     string
	Host       string
	Port       int
	Path       string
	Status     int
	Mime       string
	ReqHeaders map[string][]string
	ResHeaders map[string][]string
	ReqBody    string
	ResBody    string
}

// Codec is a compiled Starlark codec.
type Codec struct {
	Meta Meta
	Src  string
	fnM  starlark.Value // match
	fnD  starlark.Value // decode
	fnE  starlark.Value // encode (optional)
	fnS  starlark.Value // sign (optional)
}

func predeclared() starlark.StringDict {
	return starx.Predeclared()
}

// Compile parses a codec script. Requires match(flow, side) and decode(flow, side, raw).
// encode(flow, side, plaintext) is optional unless apply_on_send is true.
func Compile(id, src string) (*Codec, error) {
	thread := &starlark.Thread{Name: "compile-codec:" + id}
	thread.SetMaxExecutionSteps(maxSteps)
	globals, err := starlark.ExecFile(thread, id+".star", src, predeclared())
	if err != nil {
		return nil, budgetFormatted(starx.ScriptError(fmt.Sprintf("codec %q", id), err), thread.ExecutionSteps())
	}
	meta := Meta{ID: id, Title: id, Enabled: true}
	if mv, ok := globals["meta"]; ok {
		if d, ok := mv.(*starlark.Dict); ok {
			meta = parseMeta(id, d)
		}
	}
	fnM, ok := globals["match"]
	if !ok {
		return nil, fmt.Errorf("codec %q: missing match(flow, side)", id)
	}
	if _, ok := fnM.(starlark.Callable); !ok {
		return nil, fmt.Errorf("codec %q: match must be a function", id)
	}
	fnD, ok := globals["decode"]
	if !ok {
		return nil, fmt.Errorf("codec %q: missing decode(flow, side, raw)", id)
	}
	if _, ok := fnD.(starlark.Callable); !ok {
		return nil, fmt.Errorf("codec %q: decode must be a function", id)
	}
	var fnE starlark.Value
	if v, ok := globals["encode"]; ok {
		if _, ok := v.(starlark.Callable); !ok {
			return nil, fmt.Errorf("codec %q: encode must be a function", id)
		}
		fnE = v
	}
	if meta.ApplyOnSend && fnE == nil {
		return nil, fmt.Errorf("codec %q: apply_on_send requires encode(flow, side, plaintext)", id)
	}
	var fnS starlark.Value
	if v, ok := globals["sign"]; ok {
		if _, ok := v.(starlark.Callable); !ok {
			return nil, fmt.Errorf("codec %q: sign must be a function", id)
		}
		fnS = v
		// A sign() without encode() would claim request signing it cannot apply:
		// headers can only be recomputed after the body is final.
		if !meta.ApplyOnSend {
			return nil, fmt.Errorf("codec %q: sign(flow, side, raw) requires apply_on_send = true", id)
		}
	}
	globals.Freeze()
	return &Codec{Meta: meta, Src: src, fnM: fnM, fnD: fnD, fnE: fnE, fnS: fnS}, nil
}

// parseMeta reads the script's meta dict. ID is always the file id (<id>.star):
// encode, repeater and collections load codecs by file, so a copied codec that
// kept the original meta "id" must not report the other file's id. The meta
// "id" only serves as the display title when no "title" is given.
func parseMeta(id string, d *starlark.Dict) Meta {
	m := Meta{ID: id, Title: id, Enabled: true}
	if v, ok, _ := d.Get(starlark.String("id")); ok {
		if s, ok := starlark.AsString(v); ok && s != "" {
			m.Title = s
		}
	}
	if v, ok, _ := d.Get(starlark.String("title")); ok {
		if s, ok := starlark.AsString(v); ok {
			m.Title = s
		}
	}
	if v, ok, _ := d.Get(starlark.String("apply_on_send")); ok {
		if b, ok := v.(starlark.Bool); ok {
			m.ApplyOnSend = bool(b)
		}
	}
	if v, ok, _ := d.Get(starlark.String("enabled")); ok {
		if b, ok := v.(starlark.Bool); ok {
			m.Enabled = bool(b)
		}
	}
	return m
}

// Match reports whether this codec applies to the flow/side. side accepts the
// same aliases as Decode/TryDecode ("req"/"request", "res"/"resp"/"response");
// the script always sees the canonical "req" or "res".
func (c *Codec) Match(flow Flow, side string) (bool, error) {
	side = normalizeSide(side)
	thread := &starlark.Thread{Name: "match:" + c.Meta.ID}
	thread.SetMaxExecutionSteps(maxSteps)
	v, err := starlark.Call(thread, c.fnM, starlark.Tuple{flowValue(flow), starlark.String(side)}, nil)
	if err != nil {
		return false, budgetFormatted(starx.ScriptError("match", err), thread.ExecutionSteps())
	}
	if b, ok := v.(starlark.Bool); ok {
		return bool(b), nil
	}
	return v != starlark.None && v != starlark.False, nil
}

// Decode runs decode(flow, side, raw) where raw is the body for that side.
func (c *Codec) Decode(flow Flow, side string) (Result, error) {
	side = normalizeSide(side)
	raw := bodyFor(flow, side)
	thread := &starlark.Thread{Name: "decode:" + c.Meta.ID}
	thread.SetMaxExecutionSteps(maxSteps)
	v, err := starlark.Call(thread, c.fnD, starlark.Tuple{flowValue(flow), starlark.String(side), starlark.String(raw)}, nil)
	if err != nil {
		return Result{CodecID: c.Meta.ID, Title: c.Meta.Title, Error: budgetError(err, thread.ExecutionSteps()).Error()},
			budgetFormatted(starx.ScriptError("decode", err), thread.ExecutionSteps())
	}
	return parseDecodeResult(c, v)
}

// HasSign reports whether the codec can recompute request signatures.
func (c *Codec) HasSign() bool { return c.fnS != nil }

// Sign runs sign(flow, side, raw) where raw is the final outgoing body and
// returns the header overrides the caller must apply. It is invoked only on an
// explicit send with a decoded body: intercept-forward and passive traffic never
// reach it, so a captured flow is never silently rewritten.
func (c *Codec) Sign(flow Flow, side, raw string) (map[string]string, error) {
	if c.fnS == nil {
		return nil, nil
	}
	side = normalizeSide(side)
	thread := &starlark.Thread{Name: "sign:" + c.Meta.ID}
	thread.SetMaxExecutionSteps(maxSteps)
	v, err := starlark.Call(thread, c.fnS, starlark.Tuple{flowValue(flow), starlark.String(side), starlark.String(raw)}, nil)
	if err != nil {
		return nil, budgetFormatted(starx.ScriptError("sign", err), thread.ExecutionSteps())
	}
	headers := map[string]string{}
	if v == starlark.None {
		return headers, nil
	}
	d, ok := v.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("sign must return a dict of header names to values")
	}
	for _, item := range d.Items() {
		name, okName := starlark.AsString(item[0])
		value, okVal := starlark.AsString(item[1])
		if !okName || !okVal {
			return nil, fmt.Errorf("sign must return string header names and values")
		}
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("sign returned an empty header name")
		}
		headers[name] = value
	}
	return headers, nil
}

// Encode rebuilds a wire body from edited plaintext.
func (c *Codec) Encode(flow Flow, side, plaintext string) (string, error) {
	side = normalizeSide(side)
	if c.fnE == nil {
		return "", fmt.Errorf("codec %q has no encode()", c.Meta.ID)
	}
	thread := &starlark.Thread{Name: "encode:" + c.Meta.ID}
	thread.SetMaxExecutionSteps(maxSteps)
	v, err := starlark.Call(thread, c.fnE, starlark.Tuple{flowValue(flow), starlark.String(side), starlark.String(plaintext)}, nil)
	if err != nil {
		return "", budgetFormatted(starx.ScriptError("encode", err), thread.ExecutionSteps())
	}
	s, ok := starlark.AsString(v)
	if !ok {
		return "", fmt.Errorf("encode must return a string")
	}
	return s, nil
}

func parseDecodeResult(c *Codec, v starlark.Value) (Result, error) {
	r := Result{CodecID: c.Meta.ID, Title: c.Meta.Title}
	if s, ok := starlark.AsString(v); ok {
		r.Plaintext = s
		return r, nil
	}
	d, ok := v.(*starlark.Dict)
	if !ok {
		return r, fmt.Errorf("decode must return a string or dict")
	}
	if pv, ok, _ := d.Get(starlark.String("plaintext")); ok {
		if s, ok := starlark.AsString(pv); ok {
			r.Plaintext = s
		}
	}
	if nv, ok, _ := d.Get(starlark.String("note")); ok {
		if s, ok := starlark.AsString(nv); ok {
			r.Note = s
		}
	}
	if fv, ok, _ := d.Get(starlark.String("fields")); ok {
		if fd, ok := fv.(*starlark.Dict); ok {
			r.Fields = map[string]string{}
			for _, item := range fd.Items() {
				ks, _ := starlark.AsString(item[0])
				vs, _ := starlark.AsString(item[1])
				if ks != "" {
					r.Fields[ks] = vs
				}
			}
			if r.Plaintext == "" && len(r.Fields) > 0 {
				// Prefer a single-field plaintext for the editor when only fields returned.
				for _, k := range sortedKeys(r.Fields) {
					r.Plaintext = r.Fields[k]
					break
				}
			}
		}
	}
	return r, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func bodyFor(f Flow, side string) string {
	if normalizeSide(side) == "res" {
		return f.ResBody
	}
	return f.ReqBody
}

// TryDecode returns the first matching enabled codec's decode result.
func TryDecode(codecs []*Codec, flow Flow, side string) (Result, bool) {
	side = normalizeSide(side)
	for _, c := range codecs {
		if c == nil || !c.Meta.Enabled {
			continue
		}
		ok, err := c.Match(flow, side)
		if err != nil || !ok {
			continue
		}
		r, err := c.Decode(flow, side)
		if err != nil {
			r.Error = err.Error()
			r.CodecID = c.Meta.ID
			r.Title = c.Meta.Title
			return r, true
		}
		return r, true
	}
	return Result{}, false
}

func normalizeSide(side string) string {
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "res", "response", "resp":
		return "res"
	default:
		return "req"
	}
}

// LoadDir compiles every *.star codec in dir.
func LoadDir(dir string) ([]*Codec, map[string]error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".star") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []*Codec
	errs := map[string]error{}
	for _, name := range names {
		id := strings.TrimSuffix(name, ".star")
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			errs[name] = err
			continue
		}
		c, err := Compile(id, string(b))
		if err != nil {
			errs[name] = err
			continue
		}
		out = append(out, c)
	}
	return out, errs
}
