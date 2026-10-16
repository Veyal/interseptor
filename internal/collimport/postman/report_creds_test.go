package postman

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

const credReportFixture = `{"info":{"name":"Creds","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
"auth":{"type":"bearer","bearer":[{"key":"token","value":"SECRET-COLL","type":"string"}]},
"item":[{"name":"Login","request":{"method":"POST",
"header":[{"key":"X-Api-Key","value":"SECRET-HDR1"},{"key":"X-Auth-Token","value":"SECRET-HDR2"},{"key":"X-Trace","value":"fine"}],
"auth":{"type":"basic","basic":[{"key":"username","value":"u"},{"key":"password","value":"SECRET-BASIC"}]},
"body":{"mode":"raw","raw":"{\"password\":\"SECRET-BODY\",\"user\":\"u\"}"},
"url":{"raw":"https://api.example.com/login?access_token=SECRET-Q","query":[{"key":"access_token","value":"SECRET-Q"}]}}}]}`

func parseInline(t *testing.T, src string) *Result {
	t.Helper()
	r, err := Parse([]byte(src), Options{NewID: counterID()})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Every literal credential gets a report entry that names the field and
// suggests lifting it to a variable; the value itself is never in the report.
func TestEveryLiteralCredentialIsReportedByFieldName(t *testing.T) {
	r := parseInline(t, credReportFixture)
	var msgs []string
	for _, e := range r.Report.Entries {
		if e.Feature == "embedded-credential" {
			msgs = append(msgs, e.Message)
			if !strings.Contains(strings.ToLower(e.Suggestion), "variable") {
				t.Errorf("entry %q has no lift-to-variable suggestion: %q", e.Message, e.Suggestion)
			}
		}
	}
	all := strings.Join(msgs, "\n")
	for _, field := range []string{"X-Api-Key", "X-Auth-Token", "basic.password", "bearer.token", "access_token", "password"} {
		if !strings.Contains(all, field) {
			t.Errorf("no embedded-credential entry names %q:\n%s", field, all)
		}
	}
	whole := fmt.Sprint(r.Report)
	for _, v := range []string{"SECRET-COLL", "SECRET-HDR1", "SECRET-HDR2", "SECRET-BASIC", "SECRET-BODY", "SECRET-Q"} {
		if strings.Contains(whole, v) {
			t.Errorf("report leaked credential value %s", v)
		}
	}
	if r.Report.Stats.EmbeddedCredentials != len(msgs) {
		t.Errorf("EmbeddedCredentials=%d, entries=%d", r.Report.Stats.EmbeddedCredentials, len(msgs))
	}
}

// digest, awsv4 and jwt are applied when sending, so the report must not call
// them inert; hawk/ntlm/oauth1/edgegrid/asap really are not applied.
func TestAppliedAuthTypesAreNotReportedInert(t *testing.T) {
	for typ, inert := range map[string]bool{"digest": false, "awsv4": false, "jwt": false, "hawk": true, "ntlm": true, "oauth1": true} {
		src := fmt.Sprintf(`{"info":{"name":"A","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
"auth":{"type":%q,%q:[{"key":"user","value":"{{u}}"}]},"item":[]}`, typ, typ)
		r := parseInline(t, src)
		if got := r.Report.Has(PreservedInert, "auth:"+typ); got != inert {
			t.Errorf("auth %s: preserved-inert=%v want %v", typ, got, inert)
		}
		if !inert && !r.Report.Has(Converted, "auth:"+typ) {
			t.Errorf("auth %s should be reported converted", typ)
		}
		if inert {
			for _, e := range r.Report.Entries {
				if e.Feature == "auth:"+typ && !strings.Contains(e.Message, "sent without") {
					t.Errorf("inert auth message should say the request is sent without it: %q", e.Message)
				}
			}
		}
	}
}

func TestV1WithoutInfoIsReportedAsV1(t *testing.T) {
	_, err := Parse([]byte(`{"id":"x","name":"old","order":["a"],"requests":[{"id":"a","name":"r","url":"https://example.com","method":"GET"}]}`), Options{})
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "v1") {
		t.Fatalf("want an unsupported v1 error, got %v", err)
	}
}

func TestDataDumpGetsAHelpfulError(t *testing.T) {
	_, err := Parse([]byte(`{"version":1,"collections":[{"id":"x","name":"c","requests":[]}],"environments":[]}`), Options{})
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(strings.ToLower(err.Error()), "data dump") {
		t.Fatalf("want a data dump error, got %v", err)
	}
}

func TestBOMIsStripped(t *testing.T) {
	src := "\ufeff" + `{"info":{"name":"B","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[]}`
	r, err := Parse([]byte(src), Options{})
	if err != nil || r.Collection.Name != "B" {
		t.Fatalf("BOM prefixed file: %v", err)
	}
}

func TestSiblingRanksStayShort(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"info":{"name":"Flat","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[`)
	for i := 0; i < 3000; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"name":"r%d","request":{"method":"GET","url":"https://example.com/%d"}}`, i, i)
	}
	sb.WriteString(`]}`)
	r := parseInline(t, sb.String())
	prev := ""
	for _, it := range r.Items {
		if len(it.Rank) > 8 {
			t.Fatalf("rank %q of %s is %d chars", it.Rank, it.Name, len(it.Rank))
		}
		if it.Rank <= prev {
			t.Fatalf("ranks not strictly increasing at %s: %q <= %q", it.Name, it.Rank, prev)
		}
		prev = it.Rank
	}
	// room to insert between any two neighbours without growing much
	mid := store.RankBetween(r.Items[10].Rank, r.Items[11].Rank)
	if !(r.Items[10].Rank < mid && mid < r.Items[11].Rank) {
		t.Fatalf("no room between neighbours: %q", mid)
	}
}
