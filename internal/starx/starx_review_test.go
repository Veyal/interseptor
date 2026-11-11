package starx

import (
	"testing"

	"go.starlark.net/starlark"
)

// json_decode documents exact integers; one past int64 used to become a float.
func TestJSONDecodeKeepsBigIntegersExact(t *testing.T) {
	v, err := starlark.Call(&starlark.Thread{}, starlark.NewBuiltin("json_decode", JSONDecodeBuiltin),
		starlark.Tuple{starlark.String(`{"id": 12345678901234567890123}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _, _ := v.(*starlark.Dict).Get(starlark.String("id"))
	if _, ok := got.(starlark.Int); !ok || got.String() != "12345678901234567890123" {
		t.Fatalf("id = %s (%s), want the exact integer", got.String(), got.Type())
	}
}

// A codec that builds its pattern from flow data (re_search("id=" + x, body))
// compiles a new pattern per flow. The cache kept every one for the life of the
// process; it must stay bounded while still serving and caching new patterns.
func TestRegexCacheIsBounded(t *testing.T) {
	re := starlark.NewBuiltin("re_search", ReSearchBuiltin)
	for i := 0; i < 3*reCacheMax; i++ {
		pat := starlark.String("id=" + starlark.MakeInt(i).String() + "x")
		if _, err := starlark.Call(&starlark.Thread{}, re, starlark.Tuple{pat, starlark.String("id=5x")}, nil); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	reCache.Range(func(_, _ any) bool { n++; return true })
	if n > reCacheMax {
		t.Fatalf("regex cache holds %d patterns, want at most %d", n, reCacheMax)
	}
	v, err := starlark.Call(&starlark.Thread{}, re, starlark.Tuple{starlark.String("b+"), starlark.String("abbc")}, nil)
	if err != nil || v != starlark.String("bb") {
		t.Fatalf("re_search after the cache filled = %v, %v", v, err)
	}
}
