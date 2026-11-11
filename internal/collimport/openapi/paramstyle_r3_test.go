package openapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// The expected strings are the rows of the "Style Examples" table of the
// OpenAPI Specification 3.1.1, section 4.8.12.4 (color = "blue",
// ["blue","black","brown"], {"R":100,"G":200,"B":150}).
const styleSpec = `openapi: 3.1.0
info: {title: t, version: "1"}
servers: [{url: https://api.example.com}]
paths:
  /q:
    get:
      parameters:
        - {name: form_s, in: query, required: true, schema: {type: string}, example: blue}
        - {name: form_a, in: query, required: true, explode: false, schema: {type: array, items: {type: string}}, example: [blue, black, brown]}
        - {name: form_ax, in: query, required: true, schema: {type: array, items: {type: string}}, example: [blue, black, brown]}
        - {name: form_o, in: query, required: true, explode: false, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: form_ox, in: query, required: true, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: space_a, in: query, required: true, style: spaceDelimited, schema: {type: array, items: {type: string}}, example: [blue, black, brown]}
        - {name: space_o, in: query, required: true, style: spaceDelimited, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: pipe_a, in: query, required: true, style: pipeDelimited, schema: {type: array, items: {type: string}}, example: [blue, black, brown]}
        - {name: pipe_o, in: query, required: true, style: pipeDelimited, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: deep, in: query, required: true, style: deepObject, explode: true, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: opt_ax, in: query, schema: {type: array, items: {type: string}}, example: [blue, black]}
        - {name: hdr_a, in: header, required: true, schema: {type: array, items: {type: string}}, example: [blue, black, brown]}
        - {name: hdr_o, in: header, required: true, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: hdr_ox, in: header, required: true, explode: true, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: hdr_json, in: header, required: true, content: {application/json: {schema: {type: object}}}, example: {R: 100}}
      responses: {"200": {description: ok}}
  /p/{s}/{sa}/{so}/{sox}/{l}/{la}/{lax}/{lo}/{lox}/{m}/{ma}/{max}/{mo}/{mox}/{lnone}/{mnone}/{snone}:
    get:
      parameters:
        - {name: s, in: path, required: true, schema: {type: string}, example: blue}
        - {name: sa, in: path, required: true, schema: {type: array, items: {type: string}}, example: [blue, black, brown]}
        - {name: so, in: path, required: true, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: sox, in: path, required: true, explode: true, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: l, in: path, required: true, style: label, schema: {type: string}, example: blue}
        - {name: la, in: path, required: true, style: label, schema: {type: array, items: {type: string}}, example: [blue, black, brown]}
        - {name: lax, in: path, required: true, style: label, explode: true, schema: {type: array, items: {type: string}}, example: [blue, black, brown]}
        - {name: lo, in: path, required: true, style: label, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: lox, in: path, required: true, style: label, explode: true, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: m, in: path, required: true, style: matrix, schema: {type: string}, example: blue}
        - {name: ma, in: path, required: true, style: matrix, schema: {type: array, items: {type: string}}, example: [blue, black, brown]}
        - {name: max, in: path, required: true, style: matrix, explode: true, schema: {type: array, items: {type: string}}, example: [blue, black, brown]}
        - {name: mo, in: path, required: true, style: matrix, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: mox, in: path, required: true, style: matrix, explode: true, schema: {type: object}, example: {R: 100, G: 200, B: 150}}
        - {name: lnone, in: path, required: true, style: label, schema: {type: string}}
        - {name: mnone, in: path, required: true, style: matrix, schema: {type: string}}
        - {name: snone, in: path, required: true, schema: {type: string}}
      responses: {"200": {description: ok}}
  /authored:
    get:
      parameters:
        - {name: item_enum, in: query, required: true, explode: false, schema: {type: array, items: {type: string, enum: [x, y]}}}
        - {name: item_default, in: query, required: true, schema: {type: array, items: {type: integer, default: 7}}}
        - {name: none, in: query, required: true, explode: false, schema: {type: array, items: {type: string}}}
        - {name: scalar_none, in: query, required: true, schema: {type: string}}
      responses: {"200": {description: ok}}
`

type styleURL struct {
	Raw   string `json:"raw"`
	Query []struct {
		Key      string `json:"key"`
		Value    string `json:"value"`
		Disabled bool   `json:"disabled,omitempty"`
	} `json:"query"`
	Variable []struct{ Key, Value string } `json:"variable,omitempty"`
}

func styleItems(t *testing.T) map[string]struct {
	u styleURL
	h string
} {
	t.Helper()
	res, err := Parse([]byte(styleSpec), Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]struct {
		u styleURL
		h string
	}{}
	for _, it := range res.Items {
		if it.Kind != "request" {
			continue
		}
		var u styleURL
		if err := json.Unmarshal(it.URL, &u); err != nil {
			t.Fatal(err)
		}
		out[it.Name] = struct {
			u styleURL
			h string
		}{u, string(it.Headers)}
	}
	return out
}

func queryString(u styleURL) string {
	var parts []string
	for _, q := range u.Query {
		if !q.Disabled {
			parts = append(parts, q.Key+"="+q.Value)
		}
	}
	return strings.Join(parts, "&")
}

func TestQueryParameterStylesFollowTheSpecTable(t *testing.T) {
	it, ok := styleItems(t)["GET /q"]
	if !ok {
		t.Fatal("GET /q not imported")
	}
	want := strings.Join([]string{
		"form_s=blue",
		"form_a=blue,black,brown",
		"form_ax=blue&form_ax=black&form_ax=brown",
		"form_o=R,100,G,200,B,150",
		"R=100&G=200&B=150",
		"space_a=blue%20black%20brown",
		"space_o=R%20100%20G%20200%20B%20150",
		"pipe_a=blue%7Cblack%7Cbrown",
		"pipe_o=R%7C100%7CG%7C200%7CB%7C150",
		"deep%5BR%5D=100&deep%5BG%5D=200&deep%5BB%5D=150",
	}, "&")
	if got := queryString(it.u); got != want {
		t.Errorf("query rows\n got %s\nwant %s", got, want)
	}
	// what the pipeline sends is the row list as written (store.EffectiveURL):
	// the delimiters the style added stay encoded exactly once
	eff := store.EffectiveURL(mustJSON(it.u))
	for _, part := range []string{"space_a=blue%20black%20brown", "pipe_a=blue%7Cblack%7Cbrown", "deep%5BR%5D=100", "form_ax=blue&form_ax=black&form_ax=brown"} {
		if !strings.Contains(eff, part) || !strings.Contains(it.u.Raw, part) {
			t.Errorf("effective URL %q / raw %q lack %q", eff, it.u.Raw, part)
		}
	}
	// an optional exploded array keeps every row, each disabled
	n := 0
	for _, q := range it.u.Query {
		if q.Key == "opt_ax" {
			n++
			if !q.Disabled {
				t.Errorf("optional row %s=%s is enabled", q.Key, q.Value)
			}
		}
	}
	if n != 2 {
		t.Errorf("optional exploded array produced %d rows, want 2", n)
	}
	for _, h := range []string{
		`{"key":"hdr_a","value":"blue,black,brown"}`,
		`{"key":"hdr_o","value":"R,100,G,200,B,150"}`,
		`{"key":"hdr_ox","value":"R=100,G=200,B=150"}`,
		`{"key":"hdr_json","value":"{\"R\":100}"}`,
	} {
		if !strings.Contains(it.h, h) {
			t.Errorf("headers %s lack %s", it.h, h)
		}
	}
}

func TestPathParameterStylesFollowTheSpecTable(t *testing.T) {
	var u styleURL
	for name, it := range styleItems(t) {
		if strings.HasPrefix(name, "GET /p/") {
			u = it.u
		}
	}
	want := map[string]string{
		"s": "blue", "sa": "blue,black,brown", "so": "R,100,G,200,B,150", "sox": "R=100,G=200,B=150",
		"l": ".blue", "la": ".blue,black,brown", "lax": ".blue.black.brown", "lo": ".R,100,G,200,B,150", "lox": ".R=100.G=200.B=150",
		"m": ";m=blue", "ma": ";ma=blue,black,brown", "max": ";max=blue;max=black;max=brown",
		"mo": ";mo=R,100,G,200,B,150", "mox": ";R=100;G=200;B=150",
		"lnone": ".{{lnone}}", "mnone": ";mnone={{mnone}}", "snone": "{{snone}}",
	}
	if len(u.Variable) != len(want) {
		t.Fatalf("%d path variables, want %d", len(u.Variable), len(want))
	}
	for _, v := range u.Variable {
		if v.Value != want[v.Key] {
			t.Errorf("path %s = %q, want %q", v.Key, v.Value, want[v.Key])
		}
	}
}

// A value authored on the items schema (enum, default) is an authored value of
// the array; a parameter with nothing authored stays empty (never a made-up
// sample), whatever its style.
func TestArrayParameterUsesValuesAuthoredOnItems(t *testing.T) {
	it, ok := styleItems(t)["GET /authored"]
	if !ok {
		t.Fatal("GET /authored not imported")
	}
	if got, want := queryString(it.u), "item_enum=x&item_default=7&none=&scalar_none="; got != want {
		t.Errorf("query rows\n got %s\nwant %s", got, want)
	}
}

func TestSwagger2CollectionFormats(t *testing.T) {
	spec := `swagger: "2.0"
info: {title: t, version: "1"}
host: api.example.com
paths:
  /c:
    get:
      parameters:
        - {name: csv, in: query, required: true, type: array, items: {type: string}, default: [a, b]}
        - {name: ssv, in: query, required: true, type: array, items: {type: string}, collectionFormat: ssv, default: [a, b]}
        - {name: tsv, in: query, required: true, type: array, items: {type: string}, collectionFormat: tsv, default: [a, b]}
        - {name: pipes, in: query, required: true, type: array, items: {type: string}, collectionFormat: pipes, default: [a, b]}
        - {name: multi, in: query, required: true, type: array, items: {type: string}, collectionFormat: multi, default: [a, b]}
        - {name: X-Pipes, in: header, required: true, type: array, items: {type: string}, collectionFormat: pipes, default: [a, b]}
      responses: {"200": {description: ok}}
`
	res, err := Parse([]byte(spec), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range res.Items {
		if it.Kind != "request" {
			continue
		}
		var u styleURL
		if err := json.Unmarshal(it.URL, &u); err != nil {
			t.Fatal(err)
		}
		if got, want := queryString(u), "csv=a,b&ssv=a%20b&tsv=a%09b&pipes=a%7Cb&multi=a&multi=b"; got != want {
			t.Errorf("query rows\n got %s\nwant %s", got, want)
		}
		if !strings.Contains(string(it.Headers), `{"key":"X-Pipes","value":"a|b"}`) {
			t.Errorf("headers: %s", it.Headers)
		}
	}
}
