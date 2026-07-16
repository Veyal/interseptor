// Package starx holds the Starlark builtins shared by every script engine in
// Interseptor (passive checks, active checks, and any future engine). Centralising
// them here means a check author sees one consistent "standard library" and we
// don't re-implement — and re-test — the same helpers in two packages.
//
// Everything here is sandbox-safe by construction: pure functions over their
// arguments, no file/network/clock access, no side effects. The only state is a
// regex compile cache (read-mostly sync.Map) so re_search doesn't recompile a
// pattern on every call inside a loop.
package starx

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"go.starlark.net/starlark"
)

// Predeclared returns the builtins every Interseptor script can call. Engines
// merge this into their own predeclared dict (active scripts additionally bind
// `probe` at run time).
func Predeclared() starlark.StringDict {
	return starlark.StringDict{
		"finding":    starlark.NewBuiltin("finding", FindingBuiltin),
		"re_search":  starlark.NewBuiltin("re_search", ReSearchBuiltin),
		"json_decode": starlark.NewBuiltin("json_decode", JSONDecodeBuiltin),
		"json_encode": starlark.NewBuiltin("json_encode", JSONEncodeBuiltin),
		"b64decode":  starlark.NewBuiltin("b64decode", B64DecodeBuiltin),
		"b64encode":  starlark.NewBuiltin("b64encode", B64EncodeBuiltin),
		"url_decode": starlark.NewBuiltin("url_decode", URLDecodeBuiltin),
		"url_encode": starlark.NewBuiltin("url_encode", URLEncodeBuiltin),
		"hash":       starlark.NewBuiltin("hash", HashBuiltin),
		"hmac":       starlark.NewBuiltin("hmac", HMACBuiltin),
	}
}

// ScriptError formats a script error so the source position is included when the
// Starlark interpreter knows it. EvalError.Error() returns only the message line;
// its Backtrace() carries the file:line:col that makes an authoring typo debuggable.
func ScriptError(label string, err error) error {
	if err == nil {
		return nil
	}
	var ee *starlark.EvalError
	if errors.As(err, &ee) {
		return fmt.Errorf("%s: %s", label, ee.Backtrace())
	}
	return fmt.Errorf("%s: %w", label, err)
}
// rest are optional and default to "".
func FindingBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var severity, title, detail, evidence, fix string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"severity", &severity, "title", &title, "detail?", &detail, "evidence?", &evidence, "fix?", &fix); err != nil {
		return nil, err
	}
	d := starlark.NewDict(5)
	d.SetKey(starlark.String("severity"), starlark.String(severity))
	d.SetKey(starlark.String("title"), starlark.String(title))
	d.SetKey(starlark.String("detail"), starlark.String(detail))
	d.SetKey(starlark.String("evidence"), starlark.String(evidence))
	d.SetKey(starlark.String("fix"), starlark.String(fix))
	return d, nil
}

// reCache memoizes compiled patterns so re_search doesn't recompile on every
// call (a check may call it in a loop). reMaxText caps the input a single call
// scans — the Starlark step limit doesn't tick during a Go regexp call, so an
// unbounded text × a wide pattern could otherwise burn CPU.
var reCache sync.Map // pattern string → *regexp.Regexp | error

const ReMaxText = 256 << 10

func cachedRegexp(pattern string) (*regexp.Regexp, error) {
	if v, ok := reCache.Load(pattern); ok {
		if e, isErr := v.(error); isErr {
			return nil, e
		}
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		reCache.Store(pattern, err)
		return nil, err
	}
	reCache.Store(pattern, re)
	return re, nil
}

// ReSearchBuiltin returns the first regex match as a string, or None.
func ReSearchBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var pattern, text string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "pattern", &pattern, "text", &text); err != nil {
		return nil, err
	}
	re, err := cachedRegexp(pattern)
	if err != nil {
		return nil, fmt.Errorf("re_search: bad pattern: %w", err)
	}
	if len(text) > ReMaxText {
		text = text[:ReMaxText]
	}
	if m := re.FindString(text); m != "" {
		return starlark.String(m), nil
	}
	return starlark.None, nil
}

// ---- JSON ----

// JSONDecodeBuiltin parses a JSON document into a Starlark value
// (dict/list/string/int/float/bool/None). Numbers are kept as exact integers
// when integral, floats otherwise.
func JSONDecodeBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "text", &s); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber() // preserve integer precision instead of forcing float64
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("json_decode: %w", err)
	}
	return toStarlark(v), nil
}

// JSONEncodeBuiltin serializes a Starlark value to a compact JSON string.
func JSONEncodeBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var v starlark.Value
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "value", &v); err != nil {
		return nil, err
	}
	goVal, err := fromStarlark(v)
	if err != nil {
		return nil, fmt.Errorf("json_encode: %w", err)
	}
	out, err := json.Marshal(goVal)
	if err != nil {
		return nil, fmt.Errorf("json_encode: %w", err)
	}
	return starlark.String(string(out)), nil
}

// toStarlark converts a decoded Go value (from encoding/json with UseNumber)
// into the corresponding Starlark value.
func toStarlark(v interface{}) starlark.Value {
	switch x := v.(type) {
	case nil:
		return starlark.None
	case bool:
		return starlark.Bool(x)
	case string:
		return starlark.String(x)
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return starlark.MakeInt64(i)
		}
		if f, err := x.Float64(); err == nil {
			return starlark.Float(f)
		}
		return starlark.String(x.String())
	case []interface{}:
		elems := make([]starlark.Value, len(x))
		for i, e := range x {
			elems[i] = toStarlark(e)
		}
		return starlark.NewList(elems)
	case map[string]interface{}:
		d := starlark.NewDict(len(x))
		for k, val := range x {
			d.SetKey(starlark.String(k), toStarlark(val))
		}
		return d
	}
	return starlark.None
}

// fromStarlark converts a Starlark value into a JSON-marshalable Go value.
func fromStarlark(v starlark.Value) (interface{}, error) {
	switch x := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(x), nil
	case starlark.String:
		return string(x), nil
	case starlark.Int:
		if i, ok := x.Int64(); ok {
			return i, nil
		}
		return x.String(), nil // bignum → string (JSON has no bignum)
	case starlark.Float:
		return float64(x), nil
	case *starlark.List:
		out := make([]interface{}, 0, x.Len())
		it := x.Iterate()
		defer it.Done()
		var e starlark.Value
		for it.Next(&e) {
			gv, err := fromStarlark(e)
			if err != nil {
				return nil, err
			}
			out = append(out, gv)
		}
		return out, nil
	case *starlark.Dict:
		out := make(map[string]interface{}, x.Len())
		for _, item := range x.Items() {
			ks, ok := starlark.AsString(item[0])
			if !ok {
				return nil, fmt.Errorf("dict keys must be strings, got %s", item[0].Type())
			}
			gv, err := fromStarlark(item[1])
			if err != nil {
				return nil, err
			}
			out[ks] = gv
		}
		return out, nil
	}
	return nil, fmt.Errorf("cannot encode value of type %s", v.Type())
}

// ---- base64 ----

func B64DecodeBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "text", &s); err != nil {
		return nil, err
	}
	dec, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("b64decode: %w", err)
	}
	return starlark.String(string(dec)), nil
}

func B64EncodeBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "text", &s); err != nil {
		return nil, err
	}
	return starlark.String(base64.StdEncoding.EncodeToString([]byte(s))), nil
}

// ---- url ----

func URLDecodeBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "text", &s); err != nil {
		return nil, err
	}
	dec, err := url.QueryUnescape(s)
	if err != nil {
		return nil, fmt.Errorf("url_decode: %w", err)
	}
	return starlark.String(dec), nil
}

func URLEncodeBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "text", &s); err != nil {
		return nil, err
	}
	return starlark.String(url.QueryEscape(s)), nil
}

// ---- hashing ----

func newHasher(algo string) (hash.Hash, error) {
	switch algo {
	case "sha256":
		return sha256.New(), nil
	case "sha1":
		return sha1.New(), nil
	case "sha512":
		return sha512.New(), nil
	case "md5":
		return md5.New(), nil
	}
	return nil, fmt.Errorf("unknown algorithm %q (want sha256, sha1, sha512, or md5)", algo)
}

// HashBuiltin: hash(algo, text) → lowercase hex digest.
func HashBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var algo, text string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "algo", &algo, "text", &text); err != nil {
		return nil, err
	}
	h, err := newHasher(algo)
	if err != nil {
		return nil, err
	}
	h.Write([]byte(text))
	return starlark.String(hex.EncodeToString(h.Sum(nil))), nil
}

// HMACBuiltin: hmac(algo, key, message) → lowercase hex digest.
func HMACBuiltin(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var algo, key, msg string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "algo", &algo, "key", &key, "message", &msg); err != nil {
		return nil, err
	}
	var fn func() hash.Hash
	switch algo {
	case "sha256":
		fn = sha256.New
	case "sha1":
		fn = sha1.New
	case "sha512":
		fn = sha512.New
	case "md5":
		fn = md5.New
	default:
		return nil, fmt.Errorf("unknown algorithm %q (want sha256, sha1, sha512, or md5)", algo)
	}
	mac := hmac.New(fn, []byte(key))
	mac.Write([]byte(msg))
	return starlark.String(hex.EncodeToString(mac.Sum(nil))), nil
}
