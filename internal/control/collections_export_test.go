package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const exportRoundTripDoc = `{"info":{"name":"Round Trip","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[{"name":"List <items> & more","request":{"method":"GET","header":[{"key":"Accept","value":"application/json"}],"url":{"raw":"https://api.example.com/items?page=1","protocol":"https","host":["api","example","com"],"path":["items"],"query":[{"key":"page","value":"1"}]}}}]}`

func exportReq(f *collFixture, uid, query string, h hdrs) (*http.Response, string) {
	f.t.Helper()
	req := httptest.NewRequest("GET", "/api/collections/"+uid+"/export"+query, nil)
	req.Host = strings.TrimPrefix(f.ts.URL, "http://")
	for k, v := range h {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.h.Handler().ServeHTTP(rec, req)
	return rec.Result(), rec.Body.String()
}

func TestCollectionExportFormatsHaveTypeAndFilename(t *testing.T) {
	f := newCollFixture(t)
	uid := f.importDemo()
	for format, want := range map[string]struct{ ctype, suffix string }{
		"postman": {"application/json", ".postman_collection.json"},
		"curl":    {"text/x-shellscript", ".sh"},
		"native":  {"application/json", ".ixcol.json"},
	} {
		resp, body := exportReq(f, uid, "?format="+format, asUI)
		if resp.StatusCode != 200 || len(body) == 0 {
			t.Fatalf("%s: status %d body %q", format, resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, want.ctype) {
			t.Errorf("%s: content-type %q", format, ct)
		}
		cd := resp.Header.Get("Content-Disposition")
		if !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, want.suffix) || !strings.Contains(cd, "WP7_Demo") {
			t.Errorf("%s: content-disposition %q", format, cd)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff", format)
		}
	}
}

func TestCollectionExportPostmanRoundTripThroughRoutes(t *testing.T) {
	f := newCollFixture(t)
	var out struct {
		CollectionUID string `json:"collectionUid"`
	}
	f.must("POST", "/api/import/collection/commit", exportRoundTripDoc, asUI, 201, &out)
	resp, body := exportReq(f, out.CollectionUID, "?format=postman", asUI)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got, want bytes.Buffer
	if err := json.Compact(&got, []byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = json.Compact(&want, []byte(exportRoundTripDoc))
	if got.String() != want.String() {
		t.Fatalf("round trip not lossless\n got: %s\nwant: %s", got.String(), want.String())
	}
	if !strings.Contains(body, "<items> & more") {
		t.Error("HTML characters were escaped; the export must use postman.Marshal")
	}
}

const exportCanaryDoc = `{"info":{"name":"Canary","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
"auth":{"type":"bearer","bearer":[{"key":"token","value":"CANARY-BEARER-9f3a","type":"string"}]},
"variable":[{"key":"api_key","value":"CANARY-VAR-77c1"}],
"item":[{"name":"Login","request":{"method":"POST","header":[{"key":"X-Api-Key","value":"CANARY-HDR-1d2e"}],
"auth":{"type":"basic","basic":[{"key":"username","value":"u"},{"key":"password","value":"CANARY-PASS-5b8c"}]},
"body":{"mode":"raw","raw":"{\"password\":\"CANARY-BODY-aa01\"}"},
"url":{"raw":"https://api.example.com/login?token=CANARY-QRY-3c4d","protocol":"https","host":["api","example","com"],"path":["login"],"query":[{"key":"token","value":"CANARY-QRY-3c4d"}]}}}]}`

var exportCanaries = []string{"CANARY-BEARER-9f3a", "CANARY-VAR-77c1", "CANARY-HDR-1d2e", "CANARY-PASS-5b8c", "CANARY-BODY-aa01", "CANARY-QRY-3c4d"}

func TestCanaryCollectionExportNeverCarriesLiteralCredentials(t *testing.T) {
	f := newCollFixture(t)
	var out struct {
		CollectionUID string `json:"collectionUid"`
	}
	f.must("POST", "/api/import/collection/commit", exportCanaryDoc, asUI, 201, &out)
	// The user's own copy keeps the credentials (guard against a vacuous test).
	items, _ := f.st.ListItems(out.CollectionUID)
	var stored string
	for _, it := range items {
		stored += string(it.Headers) + string(it.Auth) + string(it.Body) + string(it.URL)
	}
	if !strings.Contains(stored, "CANARY-HDR-1d2e") {
		t.Fatalf("fixture did not store the canary: %s", stored)
	}
	for _, format := range []string{"postman", "curl", "native"} {
		for name, h := range map[string]hdrs{"ui": asUI, "ai": asAI, "bare": nil} {
			_, body := exportReq(f, out.CollectionUID, "?format="+format, h)
			for _, c := range exportCanaries {
				if strings.Contains(body, c) {
					t.Errorf("%s/%s export leaked %s", format, name, c)
				}
			}
		}
	}
}

func TestCollectionExportOffersNoUnscrubbedForm(t *testing.T) {
	f := newCollFixture(t)
	var out struct {
		CollectionUID string `json:"collectionUid"`
	}
	f.must("POST", "/api/import/collection/commit", exportCanaryDoc, asUI, 201, &out)
	for _, q := range []string{"&secrets=1", "&includeSecrets=1", "&reveal=1", "&scrub=0"} {
		for name, h := range map[string]hdrs{"ui": asUI, "ai": asAI} {
			resp, body := exportReq(f, out.CollectionUID, "?format=postman"+q, h)
			if resp.StatusCode != 400 {
				t.Errorf("%s %s: status %d, want 400", name, q, resp.StatusCode)
			}
			for _, c := range exportCanaries {
				if strings.Contains(body, c) {
					t.Errorf("%s %s leaked %s", name, q, c)
				}
			}
		}
	}
}

func TestCollectionExportRejectsUnknownFormat(t *testing.T) {
	f := newCollFixture(t)
	uid := f.importDemo()
	for _, q := range []string{"?format=yaml", "?format=", ""} {
		resp, body := exportReq(f, uid, q, asUI)
		if resp.StatusCode != 400 {
			t.Errorf("%q: status %d", q, resp.StatusCode)
		}
		for _, name := range []string{"postman", "curl", "native"} {
			if !strings.Contains(body, name) {
				t.Errorf("%q: error should list %s: %s", q, name, body)
			}
		}
	}
}

func TestCollectionExportMissingCollectionIs404(t *testing.T) {
	f := newCollFixture(t)
	for _, format := range []string{"postman", "curl", "native"} {
		if resp, body := exportReq(f, "nope", "?format="+format, asUI); resp.StatusCode != 404 {
			t.Errorf("%s: status %d: %s", format, resp.StatusCode, body)
		}
	}
}
