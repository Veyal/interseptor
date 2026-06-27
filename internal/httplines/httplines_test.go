package httplines

import "testing"

func TestNormalizeArgString(t *testing.T) {
	m, err := NormalizeArg("User-Agent: test\nCookie: a=b")
	if err != nil {
		t.Fatal(err)
	}
	if m["User-Agent"][0] != "test" || m["Cookie"][0] != "a=b" {
		t.Fatalf("%v", m)
	}
}

func TestNormalizeArgObject(t *testing.T) {
	m, err := NormalizeArg(map[string]any{"User-Agent": "bot", "X-Test": []any{"1", "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if m["User-Agent"][0] != "bot" || len(m["X-Test"]) != 2 {
		t.Fatalf("%v", m)
	}
}

func TestNormalizeArgRejectsGarbage(t *testing.T) {
	_, err := NormalizeArg(42)
	if err == nil {
		t.Fatal("expected error for non-string non-object")
	}
}

func TestToLinesRoundTrip(t *testing.T) {
	in := map[string][]string{"B": {"2"}, "A": {"1"}}
	s := ToLines(in)
	m := ToMap(s)
	if m["A"][0] != "1" || m["B"][0] != "2" {
		t.Fatalf("%v", m)
	}
}
