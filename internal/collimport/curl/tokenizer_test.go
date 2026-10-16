package curl

import (
	"reflect"
	"strings"
	"testing"
)

func words(t *testing.T, src string) []string {
	t.Helper()
	toks, err := Tokenize(src)
	if err != nil {
		t.Fatalf("Tokenize(%q): %v", src, err)
	}
	var out []string
	for _, tk := range toks {
		if tk.Sep {
			out = append(out, "<"+tk.Text+">")
		} else {
			out = append(out, tk.Text)
		}
	}
	return out
}

func TestTokenizeQuotesAndEscapes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`curl 'a b' "c d" e\ f`, []string{"curl", "a b", "c d", "e f"}},
		{`curl "a\"b" 'c\d'`, []string{"curl", `a"b`, `c\d`}},
		{`curl -H"X: 1"`, []string{"curl", "-HX: 1"}},
		{"curl a \\\n  b \\\r\n  c", []string{"curl", "a", "b", "c"}},
		{"curl a `\n b", []string{"curl", "a", "b"}},
		{`curl $'a\nb\x41é\101'`, []string{"curl", "a\nbAéA"}},
		{`curl '' ""`, []string{"curl", "", ""}},
		{"curl a # comment\ncurl b", []string{"curl", "a", "<\n>", "curl", "b"}},
		{"curl a | jq . ; echo hi && x", []string{"curl", "a", "<|>", "jq", ".", "<;>", "echo", "hi", "<&>", "x"}},
		{`curl 'it'"'"'s'`, []string{"curl", "it's"}},
		{"\n\n curl a\n\n", []string{"curl", "a"}},
	}
	for _, c := range cases {
		if got := words(t, c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Tokenize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTokenizeCmdCaretForm(t *testing.T) {
	in := "curl ^\"https://example.com/a^\" ^\r\n  -H ^\"accept: */*^\" ^\r\n  --data-raw ^\"^{^\\^\"a^\\^\":1^}^\""
	got := words(t, in)
	want := []string{"curl", "https://example.com/a", "-H", "accept: */*", "--data-raw", `{"a":1}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTokenizeDynamicFlag(t *testing.T) {
	toks, err := Tokenize(`curl "$HOST/x" '$LITERAL' "${A}" "$(id)" "cost $5 " "a$"`)
	if err != nil {
		t.Fatal(err)
	}
	dyn := []bool{}
	for _, tk := range toks {
		dyn = append(dyn, tk.Dyn)
	}
	want := []bool{false, true, false, true, true, true, false}
	if !reflect.DeepEqual(dyn, want) {
		t.Fatalf("dyn = %v want %v", dyn, want)
	}
}

func TestTokenizeErrors(t *testing.T) {
	for _, in := range []string{`curl 'abc`, `curl "abc`, `curl $'abc`} {
		if _, err := Tokenize(in); err != ErrUnterminated {
			t.Errorf("%q: err = %v", in, err)
		}
	}
	if _, err := Tokenize(strings.Repeat("a", MaxInputBytes+1)); err != ErrTooLarge {
		t.Errorf("large: %v", err)
	}
	if _, err := Tokenize(strings.Repeat("a ", MaxTokens+1)); err != ErrTooManyToken {
		t.Errorf("many: %v", err)
	}
}

func FuzzTokenize(f *testing.F) {
	for _, s := range []string{`curl 'a' "b" $'c\x41'`, "curl ^\"a^\" ^\n b", "a \\\n b `\n c", `"$(x)`, "$'\\u12", "'", `\`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		toks, err := Tokenize(s)
		if err != nil {
			return
		}
		total := 0
		for k, tk := range toks {
			total += len(tk.Text)
			if tk.Sep && (k == 0 || k == len(toks)-1 || toks[k-1].Sep) {
				t.Fatalf("misplaced separator in %q: %v", s, toks)
			}
		}
		if total > 4*len(s)+16 {
			t.Fatalf("tokens expanded %d bytes from %d", total, len(s))
		}
	})
}
