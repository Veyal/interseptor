package scanner

import (
	"net/http"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// url.ParseQuery returns every valid pair alongside a decoding error, but the
// helpers used to discard the whole result when any one pair was malformed.
// One stray "%" in an unrelated parameter then hid every other parameter from
// the checks that enumerate them — a silent false negative.
func TestJSONPFindsCallbackWhenASiblingQueryPairIsMalformed(t *testing.T) {
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com",
		Path: "/api?callback=renderData&bad=%zz", Status: 200, Mime: "application/javascript",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/javascript"},
			"Strict-Transport-Security": {"max-age=63072000"},
		}),
	}
	got := Analyze(flow, nil, []byte(`renderData({"secret":42})`))
	if !has(got, "JSONP endpoint reflects callback") {
		t.Fatalf("expected JSONP finding despite the malformed sibling pair; got: %s", titles(got))
	}
}

func TestQueryParamKeepsValidPairsNextToAMalformedOne(t *testing.T) {
	if got := queryParam("/x?u=admin&bad=%zz", "u"); got != "admin" {
		t.Fatalf("queryParam = %q, want admin", got)
	}
	if got := queryParam("/x?bad=%zz", "u"); got != "" {
		t.Fatalf("queryParam = %q, want empty", got)
	}
}
