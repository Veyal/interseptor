package starx

import (
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

// A self-referencing value must fail with an error, not overflow the stack
// (a fatal, unrecoverable crash that would take the proxy down).
func TestJSONEncodeRejectsCyclicList(t *testing.T) {
	l := starlark.NewList(nil)
	if err := l.Append(l); err != nil {
		t.Fatal(err)
	}
	_, err := runBuiltin(t, "json_encode", l)
	if err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("want nesting-depth error, got %v", err)
	}
}

func TestJSONEncodeRejectsCyclicDict(t *testing.T) {
	d := starlark.NewDict(1)
	if err := d.SetKey(starlark.String("self"), d); err != nil {
		t.Fatal(err)
	}
	if _, err := runBuiltin(t, "json_encode", d); err == nil {
		t.Fatal("cyclic dict encoded without error")
	}
}

func TestJSONEncodeAllowsModerateNesting(t *testing.T) {
	var v starlark.Value = starlark.String("leaf")
	for i := 0; i < 100; i++ {
		v = starlark.NewList([]starlark.Value{v})
	}
	if _, err := runBuiltin(t, "json_encode", v); err != nil {
		t.Fatalf("100 levels rejected: %v", err)
	}
}
