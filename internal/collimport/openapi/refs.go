package openapi

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// MaxRefChain bounds $ref -> $ref -> ... chains.
const MaxRefChain = 32

// Ref errors.
var (
	ErrExternalRef = errors.New("external $ref (never fetched)")
	ErrBadRef      = errors.New("unresolvable $ref")
	ErrRefLoop     = errors.New("$ref loop")
)

// lookupRef resolves a local JSON pointer such as "#/components/schemas/A".
func lookupRef(root any, ref string) (any, error) {
	if !strings.HasPrefix(ref, "#") {
		return nil, ErrExternalRef
	}
	ptr := ref[1:]
	if ptr == "" {
		return root, nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, ErrBadRef
	}
	cur := root
	for _, seg := range strings.Split(ptr[1:], "/") {
		if un, err := url.PathUnescape(seg); err == nil {
			seg = un
		}
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		switch t := cur.(type) {
		case *omap:
			if !t.has(seg) {
				return nil, ErrBadRef
			}
			cur = t.m[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(t) {
				return nil, ErrBadRef
			}
			cur = t[i]
		default:
			return nil, ErrBadRef
		}
	}
	return cur, nil
}

// deref follows a chain of "$ref" objects to the first non-reference value and
// returns it with the name of the last reference followed ("" when v was not a
// reference). Loops and over-long chains are errors.
func deref(root any, v any) (any, string, error) {
	last := ""
	seen := map[string]bool{}
	for n := 0; ; n++ {
		o := asMap(v)
		ref, ok := o.get("$ref").(string)
		if !ok {
			return v, last, nil
		}
		if n >= MaxRefChain || seen[ref] {
			return nil, ref, ErrRefLoop
		}
		seen[ref] = true
		next, err := lookupRef(root, ref)
		if err != nil {
			return nil, ref, err
		}
		v, last = next, ref
	}
}

func refName(ref string) string {
	if i := strings.LastIndexByte(ref, '/'); i >= 0 {
		return ref[i+1:]
	}
	return ref
}
