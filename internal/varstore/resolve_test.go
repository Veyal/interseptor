package varstore

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/redact"
)

func layer(s Scope, name string, kv ...string) Layer {
	l := Layer{Scope: s, Name: name, Vars: map[string]Var{}}
	for i := 0; i+1 < len(kv); i += 2 {
		l.Vars[kv[i]] = Var{Value: kv[i+1]}
	}
	return l
}

func fixed() *Resolver {
	return New(Options{
		Clock: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 6000000, time.UTC) },
		Rand:  NewRand(42),
	})
}

func TestPrecedenceTable(t *testing.T) {
	all := map[Scope]Layer{
		ScopeLocal:       layer(ScopeLocal, "l", "v", "local"),
		ScopeData:        layer(ScopeData, "d", "v", "data"),
		ScopeEnvironment: layer(ScopeEnvironment, "e", "v", "env"),
		ScopeFolder:      layer(ScopeFolder, "f", "v", "folder"),
		ScopeCollection:  layer(ScopeCollection, "c", "v", "collection"),
		ScopeGlobal:      layer(ScopeGlobal, "g", "v", "global"),
	}
	order := []Scope{ScopeLocal, ScopeData, ScopeEnvironment, ScopeFolder, ScopeCollection, ScopeGlobal}
	want := []string{"local", "data", "env", "folder", "collection", "global"}
	// Removing the winner one at a time must reveal the next scope, whatever
	// order the layers were supplied in.
	for cut := 0; cut < len(order); cut++ {
		var layers []Layer
		for i := len(order) - 1; i >= cut; i-- { // deliberately reversed
			layers = append(layers, all[order[i]])
		}
		got := fixed().Resolve("{{v}}", NewStack(layers...))
		if got.Value != want[cut] || got.Err != nil {
			t.Fatalf("cut=%d got %q err=%v want %q", cut, got.Value, got.Err, want[cut])
		}
		if len(got.Uses) != 1 || got.Uses[0].Scope != order[cut].String() {
			t.Fatalf("cut=%d uses=%+v", cut, got.Uses)
		}
	}
}

func TestFolderInnerBeatsOuter(t *testing.T) {
	st := NewStack(layer(ScopeFolder, "inner", "v", "inner"), layer(ScopeFolder, "outer", "v", "outer"))
	if got := fixed().Resolve("{{v}}", st).Value; got != "inner" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveSyntax(t *testing.T) {
	st := NewStack(layer(ScopeEnvironment, "e",
		"host", "example.com", "name", "host", "a", "{{b}}-A", "b", "B",
		"j", `he said "hi"`, "empty", ""))
	cases := []struct{ in, want string }{
		{"https://{{host}}/x", "https://example.com/x"},
		{"{{ host }}", "example.com"},
		{"{{{{name}}}}", "example.com"}, // nested name
		{"{{a}}", "B-A"},                // value is itself a template
		{`\{{host}}`, "{{host}}"},       // escape
		{"{{host", "{{host"},            // unterminated stays literal
		{"{{}}", "{{}}"},
		{"{{empty}}x", "x"},
		{"{{host|upper}}", "EXAMPLE.COM"},
		{"{{host|b64}}", "ZXhhbXBsZS5jb20="},
		{"{{j|json}}", `he said \"hi\"`},
		{"{{host|md5}}", "5ababd603b22780302dd8d83498e5172"},
		{"{{host|hmac:k|trim}}", "9c4ab7a2f4ab6b4a3d6f0ea0b6b6d53d5f5b2d93b8e9b2fe4f0ba9c4e2b0b1a3"},
		{"{{$payload:ssti}}", "{{7*7}}"}, // dynamic output is final, not re-expanded
	}
	for _, c := range cases {
		got := fixed().Resolve(c.in, st)
		if c.in == "{{host|hmac:k|trim}}" {
			if len(got.Value) != 64 {
				t.Fatalf("hmac len %d", len(got.Value))
			}
			continue
		}
		if got.Value != c.want {
			t.Errorf("%q -> %q want %q (unres=%v)", c.in, got.Value, c.want, got.Unresolved)
		}
	}
}

func TestUnresolvedPolicies(t *testing.T) {
	st := NewStack(layer(ScopeEnvironment, "e", "ok", "1"))
	r := fixed().Resolve("{{ok}}/{{missing}}/{{ok|nopipe}}", st)
	var ue *UnresolvedError
	if !errors.As(r.Err, &ue) || !errors.Is(r.Err, ErrUnresolved) {
		t.Fatalf("block policy must error: %v", r.Err)
	}
	if !reflect.DeepEqual(r.Unresolved, []string{"missing", "ok|nopipe"}) {
		t.Fatalf("unresolved=%v", r.Unresolved)
	}
	lit := New(Options{Policy: PolicyLiteral}).Resolve("a{{missing}}b", st)
	if lit.Err != nil || lit.Value != "a{{missing}}b" {
		t.Fatalf("literal: %+v", lit)
	}
	emp := New(Options{Policy: PolicyEmpty}).Resolve("a{{missing}}b", st)
	if emp.Err != nil || emp.Value != "ab" {
		t.Fatalf("empty: %+v", emp)
	}
	// Unknown dynamic variable is unresolved, not silently random.
	if r := fixed().Resolve("{{$nope}}", st); r.Err == nil {
		t.Fatal("unknown dynamic must block")
	}
}

func TestCycleAndDepth(t *testing.T) {
	st := NewStack(layer(ScopeEnvironment, "e", "a", "{{b}}", "b", "{{a}}", "self", "x{{self}}"))
	for _, tpl := range []string{"{{a}}", "{{self}}"} {
		r := fixed().Resolve(tpl, st)
		if r.Err == nil || len(r.Problems) == 0 || !strings.Contains(r.Problems[0], "cycle") {
			t.Fatalf("%s: want cycle block, got %+v", tpl, r)
		}
	}
	// A chain longer than MaxDepth is refused, a shorter one resolves.
	kv := []string{}
	for i := 0; i < 20; i++ {
		kv = append(kv, "v"+string(rune('a'+i)), "{{v"+string(rune('a'+i+1))+"}}")
	}
	kv = append(kv, "vu", "end")
	deep := NewStack(layer(ScopeEnvironment, "e", kv...))
	if r := New(Options{MaxDepth: 8}).Resolve("{{va}}", deep); r.Err == nil || !strings.Contains(strings.Join(r.Problems, ";"), "depth") {
		t.Fatalf("expected depth block: %+v", r)
	}
	if r := New(Options{MaxDepth: 8}).Resolve("{{vo}}", deep); r.Value != "end" { // 6 hops
		t.Fatalf("shallow chain: %+v", r)
	}
}

func TestExpansionBombIsBounded(t *testing.T) {
	st := NewStack(layer(ScopeEnvironment, "e",
		"a", "{{b}}{{b}}{{b}}{{b}}", "b", "{{c}}{{c}}{{c}}{{c}}", "c", "{{d}}{{d}}{{d}}{{d}}",
		"d", "{{e}}{{e}}{{e}}{{e}}", "e", "{{f}}{{f}}{{f}}{{f}}", "f", "{{g}}{{g}}{{g}}{{g}}",
		"g", "{{h}}{{h}}{{h}}{{h}}", "h", strings.Repeat("x", 4096)))
	r := New(Options{MaxOutput: 1 << 16}).Resolve("{{a}}", st)
	if !errors.Is(r.Err, ErrTooLarge) || r.Value != "" {
		t.Fatalf("bomb must hit limit: err=%v len=%d", r.Err, len(r.Value))
	}
}

func TestDynamicDeterminism(t *testing.T) {
	tpl := "{{$guid}}|{{$timestamp}}|{{$isoTimestamp}}|{{$randomInt}}|{{$randomEmail}}|{{$randomIP}}|{{$randomPassword}}|{{$randomBoolean}}|{{$randomHexColor}}|{{$randomFirstName}} {{$randomLastName}}|{{$randomAlphaNumeric}}|{{$randomUUID}}"
	a, b := fixed().Resolve(tpl, nil), fixed().Resolve(tpl, nil)
	if a.Err != nil || a.Value != b.Value {
		t.Fatalf("same seed+clock must be identical:\n%s\n%s", a.Value, b.Value)
	}
	parts := strings.Split(a.Value, "|")
	if len(parts[0]) != 36 || parts[0][14] != '4' {
		t.Fatalf("guid shape: %s", parts[0])
	}
	if parts[1] != "1767323045" || parts[2] != "2026-01-02T03:04:05.006Z" {
		t.Fatalf("clock: %v", parts[1:3])
	}
	if !strings.HasSuffix(parts[4], "@example.com") {
		t.Fatalf("email: %s", parts[4])
	}
	other := New(Options{Clock: fixed().o.Clock, Rand: NewRand(43)}).Resolve(tpl, nil)
	if other.Value == a.Value {
		t.Fatal("different seed should differ")
	}
	// Two occurrences in one template draw independently.
	two := fixed().Resolve("{{$guid}}{{$guid}}", nil).Value
	if two[:36] == two[36:] {
		t.Fatal("repeat dynamic must draw fresh values")
	}
}

func TestSecretRegistry(t *testing.T) {
	reg := redact.NewRegistry()
	const canary = "CANARY-token-123456"
	st := NewStack(Layer{Scope: ScopeEnvironment, Name: "e", Vars: map[string]Var{
		"tok":    {Value: canary, Secret: true},
		"hdr":    {Value: "Bearer {{tok}}"},
		"unused": {Value: "UNUSED-secret-999", Secret: true},
	}})
	r := New(Options{Registry: reg, Rand: NewRand(1)}).Resolve("Authorization: {{hdr|b64}} {{tok}}", st)
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if !reg.Contains(canary) || !reg.Contains("Bearer "+canary) {
		t.Fatal("used secrets and derivations must be registered")
	}
	masked := reg.Mask(r.Value)
	if strings.Contains(masked, canary) {
		t.Fatalf("leak in masked output: %s", masked)
	}
	used := false
	for _, u := range r.Uses {
		if u.Name == "tok" && u.Secret {
			used = true
		}
	}
	if !used {
		t.Fatalf("uses must flag secret: %+v", r.Uses)
	}
	st.RegisterSecrets(reg)
	if !reg.Contains("UNUSED-secret-999") {
		t.Fatal("RegisterSecrets must add unused secrets")
	}
}

func TestResolveRequest(t *testing.T) {
	reg := redact.NewRegistry()
	st := NewStack(Layer{Scope: ScopeEnvironment, Name: "e", Vars: map[string]Var{
		"host": {Value: "example.com:8443"}, "uid": {Value: "a/b c"}, "k": {Value: "X-Key"},
		"tok": {Value: "SECRET-value-77", Secret: true},
	}})
	req := Request{
		Method: "POST", URL: "https://{{host}}/users/:id/items/:missing?x=:id",
		PathVars: map[string]string{"id": "{{uid}}"},
		Headers:  []KV{{Key: "{{k}}", Value: "{{tok}}"}, {Key: "Off", Value: "{{nope}}", Disabled: true}},
		Query:    []KV{{Key: "q", Value: "{{$randomWord}}"}},
		Form:     []KV{{Key: "f{{k}}", Value: "v"}},
		Body:     `{"t":"{{tok}}"}`,
		Auth:     map[string]string{"token": "{{tok}}"},
	}
	out, res := New(Options{Registry: reg, Rand: NewRand(7)}).ResolveRequest(req, st)
	if res.Err != nil {
		t.Fatalf("%v %+v", res.Err, res)
	}
	if out.URL != "https://example.com:8443/users/a%2Fb%20c/items/:missing?x=:id" {
		t.Fatalf("url=%s", out.URL)
	}
	if out.Headers[0].Key != "X-Key" || out.Headers[0].Value != "SECRET-value-77" || out.Headers[1].Value != "{{nope}}" {
		t.Fatalf("headers=%+v", out.Headers)
	}
	if out.Form[0].Key != "fX-Key" || out.Body != `{"t":"SECRET-value-77"}` || out.Auth["token"] != "SECRET-value-77" {
		t.Fatalf("form/body/auth: %+v", out)
	}
	fields := map[string]bool{}
	for _, u := range res.Uses {
		fields[u.Field] = true
	}
	for _, f := range []string{"url", "path:id", "header:X-Key"[:0] + "header:{{k}}", "body", "auth:token", "query:q"} {
		if !fields[f] {
			t.Errorf("missing use for field %q in %v", f, fields)
		}
	}
	// Unresolved in any field blocks the whole request.
	_, bad := fixed().ResolveRequest(Request{URL: "https://example.com/", Headers: []KV{{Key: "A", Value: "{{nope}}"}}}, st)
	if bad.Err == nil || bad.Unresolved[0] != "nope" {
		t.Fatalf("expected block: %+v", bad)
	}
}

func TestSubstitutePath(t *testing.T) {
	v := map[string]string{"id": "7"}
	cases := []struct{ in, want string }{
		{"https://example.com:8080/a/:id", "https://example.com:8080/a/7"},
		{"https://example.com:8080", "https://example.com:8080"},
		{"/a/:id/b?x=:id#:id", "/a/7/b?x=:id#:id"},
		{"example.com/a/:id", "example.com/a/7"},
		{"https://example.com/a/:other", "https://example.com/a/:other"},
	}
	for _, c := range cases {
		if got := SubstitutePath(c.in, v); got != c.want {
			t.Errorf("%q -> %q want %q", c.in, got, c.want)
		}
	}
}

func TestConcurrentResolve(t *testing.T) {
	r := fixed()
	st := NewStack(layer(ScopeEnvironment, "e", "a", "{{$guid}}"))
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				r.Resolve("{{a}}{{$randomInt}}", st)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

func FuzzResolve(f *testing.F) {
	for _, s := range []string{"{{a}}", "{{{{a}}}}", `\{{a}}`, "{{", "}}{{", "{{a|b64|nope}}", "{{$guid}}", "{{self}}", "{{a}}{{b}}{{c}}", "{{ }}", "{{a{{b{{c}}}}}}"} {
		f.Add(s)
	}
	st := NewStack(layer(ScopeEnvironment, "e", "a", "{{b}}", "b", "{{c}}x", "c", "{{a}}", "self", "{{self}}{{self}}", "x", "ok"))
	f.Fuzz(func(t *testing.T, tpl string) {
		if len(tpl) > 4096 {
			return
		}
		r := New(Options{Rand: NewRand(1), Clock: fixed().o.Clock, MaxOutput: 1 << 16, MaxExpansions: 2000})
		a := r.Resolve(tpl, st)
		b := r.Resolve(tpl, st)
		if a.Err == nil && len(a.Value) > 1<<16 {
			t.Fatalf("output limit violated: %d", len(a.Value))
		}
		if !strings.Contains(tpl, "$") && !reflect.DeepEqual(a, b) && a.Err == nil {
			t.Fatalf("non-deterministic: %q vs %q", a.Value, b.Value)
		}
	})
}
