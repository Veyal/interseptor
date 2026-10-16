package collmatrix

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

const postmanExamples = `[
 {"name":"ok","code":200,"status":"OK","header":[{"key":"Content-Type","value":"application/json"},{"key":"X-Request-Id","value":"r-1"}],"body":"{\"id\":1,\"name\":\"Ada\",\"tags\":[\"a\",\"b\"],\"updatedAt\":\"2026-01-01\"}"},
 {"name":"missing","code":404,"header":{"Content-Type":"text/plain"},"body":"not found"}
]`

func TestParseExamples(t *testing.T) {
	ex := ParseExamples(json.RawMessage(postmanExamples))
	if len(ex) != 2 || ex[0].Code != 200 || ex[0].Name != "ok" || len(ex[0].Headers) != 2 || ex[1].Headers[0].Name != "Content-Type" {
		t.Fatalf("ex = %+v", ex)
	}
	if got := ParseExamples(json.RawMessage(`nope`)); got != nil {
		t.Fatalf("bad json = %+v", got)
	}
	if got := ParseExamples(nil); got != nil {
		t.Fatalf("nil = %+v", got)
	}
}

func TestDiffExampleEqualIgnoresVolatileHeaders(t *testing.T) {
	ex := ParseExamples(json.RawMessage(postmanExamples))[0]
	act := Actual{Status: 200, Headers: []Header{{"content-type", "application/json"}, {"X-Request-Id", "different"}, {"Date", "now"}},
		Body: `{"updatedAt":"2026-01-01","tags":["a","b"],"name":"Ada","id":1}`}
	d := DiffExample(ex, act, DiffOptions{})
	if !d.Equal || d.Status.Changed || len(d.Headers) != 0 || len(d.JSON) != 0 {
		t.Fatalf("diff = %+v", d)
	}
}

func TestDiffExampleReportsStatusHeaderAndJSONChanges(t *testing.T) {
	ex := ParseExamples(json.RawMessage(postmanExamples))[0]
	act := Actual{Status: 201, Headers: []Header{{"Content-Type", "application/json; charset=utf-8"}, {"X-New", "1"}},
		Body: `{"id":2,"name":"Ada","tags":["a"],"role":"admin","updatedAt":"2026-02-02"}`}
	d := DiffExample(ex, act, DiffOptions{})
	if d.Equal || !d.Status.Changed || d.Status.Expected != 200 || d.Status.Actual != 201 {
		t.Fatalf("status = %+v", d.Status)
	}
	kinds := map[string]string{}
	for _, h := range d.Headers {
		kinds[strings.ToLower(h.Name)] = h.Kind
	}
	if kinds["content-type"] != "changed" || kinds["x-new"] != "added" {
		t.Fatalf("headers = %+v", d.Headers)
	}
	paths := map[string]string{}
	for _, c := range d.JSON {
		paths[c.Path] = c.Kind
	}
	want := map[string]string{"id": "changed", "tags[1]": "removed", "role": "added", "updatedAt": "changed"}
	for p, k := range want {
		if paths[p] != k {
			t.Fatalf("path %s = %q want %q (all %+v)", p, paths[p], k, d.JSON)
		}
	}
	if d.BodyFormat != "json" || !strings.Contains(d.ExpectedBody, "\n") {
		t.Fatalf("format=%s body=%q", d.BodyFormat, d.ExpectedBody)
	}
}

func TestDiffExampleIgnorePaths(t *testing.T) {
	ex := Example{Code: 200, Body: `{"items":[{"id":1,"ts":"a"},{"id":2,"ts":"b"}],"meta":{"took":3}}`}
	act := Actual{Status: 200, Body: `{"items":[{"id":1,"ts":"x"},{"id":2,"ts":"y"}],"meta":{"took":99}}`}
	d := DiffExample(ex, act, DiffOptions{IgnorePaths: []string{"$.items[*].ts", "meta"}})
	if !d.Equal || len(d.JSON) != 0 {
		t.Fatalf("diff = %+v", d.JSON)
	}
	d = DiffExample(ex, act, DiffOptions{IgnorePaths: []string{"$.items[*].ts"}})
	if d.Equal || len(d.JSON) != 1 || d.JSON[0].Path != "meta.took" {
		t.Fatalf("diff = %+v", d.JSON)
	}
}

func TestDiffExampleTypeChangeAndText(t *testing.T) {
	d := DiffExample(Example{Code: 200, Body: `{"a":1}`}, Actual{Status: 200, Body: `{"a":"1"}`}, DiffOptions{})
	if len(d.JSON) != 1 || d.JSON[0].Kind != "type" {
		t.Fatalf("type change = %+v", d.JSON)
	}
	d = DiffExample(Example{Code: 404, Body: "not found"}, Actual{Status: 404, Body: "Not Found"}, DiffOptions{})
	if d.Equal || d.BodyFormat != "text" || !d.BodyChanged {
		t.Fatalf("text = %+v", d)
	}
	d = DiffExample(Example{Code: 204}, Actual{Status: 204}, DiffOptions{})
	if !d.Equal || d.BodyFormat != "empty" {
		t.Fatalf("empty = %+v", d)
	}
}

func TestDiffExampleBounds(t *testing.T) {
	big := `{"k":"` + strings.Repeat("x", 3000) + `"}`
	d := DiffExample(Example{Code: 200, Body: big}, Actual{Status: 200, Body: `{"k":"` + strings.Repeat("y", 3000) + `"}`}, DiffOptions{MaxBody: 1000})
	if !d.Truncated {
		t.Fatalf("truncated flag missing: %+v", d)
	}
	var sb strings.Builder
	sb.WriteString("{")
	for i := 0; i < 500; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`"k` + strings.Repeat("a", i%7) + string(rune('A'+i%26)) + string(rune('a'+(i/26)%26)) + `":` + "1")
	}
	sb.WriteString("}")
	d = DiffExample(Example{Code: 200, Body: "{}"}, Actual{Status: 200, Body: sb.String()}, DiffOptions{})
	if len(d.JSON) > MaxJSONChanges || !d.ChangesCapped {
		t.Fatalf("changes = %d capped=%v", len(d.JSON), d.ChangesCapped)
	}
}

type fakeBodies map[int64]FlowBody

func (f fakeBodies) FlowBody(id int64, _ int64) (FlowBody, error) {
	b, ok := f[id]
	if !ok {
		return FlowBody{}, errNotFound
	}
	return b, nil
}

func TestServiceDiffExampleScrubsAndSelectsByName(t *testing.T) {
	be := newFake("get user")
	be.reg.Add("canary-tok-123")
	be.items[0].Examples = json.RawMessage(postmanExamples)
	svc := New(Deps{Backend: be, Bodies: fakeBodies{9: {Status: 200, Headers: []Header{{"Content-Type", "application/json"}}, Body: []byte(`{"id":1,"name":"canary-tok-123","tags":["a","b"],"updatedAt":"2026-01-01"}`)}}})
	d, err := svc.DiffExample(context.Background(), "c1", "i-get user", "ok", 9, DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "canary-tok-123") {
		t.Fatalf("secret leaked: %s", b)
	}
	if d.Example != "ok" || len(d.JSON) != 1 || d.JSON[0].Path != "name" {
		t.Fatalf("diff = %+v", d)
	}
	if _, err := svc.DiffExample(context.Background(), "c1", "i-get user", "nope", 9, DiffOptions{}); err == nil {
		t.Fatal("unknown example must error")
	}
	if _, err := svc.DiffExample(context.Background(), "c1", "i-get user", "ok", 77, DiffOptions{}); err == nil {
		t.Fatal("unknown flow must error")
	}
	svc2 := New(Deps{Backend: be})
	if _, err := svc2.DiffExample(context.Background(), "c1", "i-get user", "ok", 9, DiffOptions{}); err == nil {
		t.Fatal("no body reader must error")
	}
}

var _ = store.Item{}
