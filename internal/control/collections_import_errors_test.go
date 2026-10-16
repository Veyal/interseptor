package control

import (
	"strings"
	"testing"
)

const bomCollection = "\ufeff" + `{"info":{"name":"Bommed","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[{"name":"r","request":{"method":"GET","url":"https://example.com/x"}}]}`

// A UTF-8 byte order mark (Windows editors, PowerShell exports) must not turn a
// valid file into "not valid JSON".
func TestImportAcceptsUTF8BOM(t *testing.T) {
	for _, format := range []string{"", "auto", "postman"} {
		f := newCollFixture(t)
		f.must("POST", "/api/import/collection/preview?format="+format, bomCollection, asUI, 200, nil)
		f.must("POST", "/api/import/collection/commit?format="+format, bomCollection, asUI, 201, nil)
	}
	f := newCollFixture(t)
	f.must("POST", "/api/import/collection/commit", "\ufeffcurl https://example.com/c", asUI, 201, nil)
}

// A file no importer recognises says what was tried instead of blaming only Postman.
func TestUnrecognisedJSONListsWhatWasTried(t *testing.T) {
	f := newCollFixture(t)
	body := f.must("POST", "/api/import/collection/preview", `{"foo":1}`, asUI, 415, nil)
	for _, want := range []string{"Postman", "OpenAPI", "Insomnia", "HAR"} {
		if !strings.Contains(body, want) {
			t.Errorf("error should mention %s: %s", want, body)
		}
	}
}

// A Postman-looking file with a real problem still gets the Postman error.
func TestPostmanLookingJSONGetsThePostmanError(t *testing.T) {
	f := newCollFixture(t)
	body := f.must("POST", "/api/import/collection/preview", `{"info":{"name":"x"}}`, asUI, 415, nil)
	if !strings.Contains(body, "Postman") || strings.Contains(body, "Insomnia") {
		t.Fatalf("want the Postman error only: %s", body)
	}
}

// A malformed file whose format was recognised surfaces that importer's error.
func TestRecognisedButBrokenFileSurfacesItsOwnImporterError(t *testing.T) {
	f := newCollFixture(t)
	body := f.must("POST", "/api/import/collection/preview", `{"log":{"entries":"bad"}}`, asUI, 400, nil)
	if !strings.Contains(body, "HAR") || strings.Contains(strings.ToLower(body), "postman") {
		t.Fatalf("want the HAR error: %s", body)
	}
}

// The Postman data dump explains how to import instead of "not a Postman collection".
func TestPostmanDataDumpMessage(t *testing.T) {
	f := newCollFixture(t)
	body := f.must("POST", "/api/import/collection/preview", `{"version":1,"collections":[{"id":"x","name":"c","requests":[]}],"environments":[]}`, asUI, 415, nil)
	if !strings.Contains(strings.ToLower(body), "data dump") {
		t.Fatalf("%s", body)
	}
}

// Re-importing never duplicates, and what was skipped is named in the report.
func TestReimportReportNamesSkippedRequests(t *testing.T) {
	f := newCollFixture(t)
	f.must("POST", "/api/import/collection/commit", credCollectionFixture, asUI, 201, nil)
	var out struct {
		Report struct {
			Entries []struct {
				Feature string `json:"feature"`
				Path    string `json:"path"`
			} `json:"entries"`
		} `json:"report"`
		Stats struct {
			ItemsAdded   int `json:"itemsAdded"`
			ItemsSkipped int `json:"itemsSkipped"`
		} `json:"stats"`
		Quarantined bool `json:"scriptsQuarantined"`
	}
	f.must("POST", "/api/import/collection/commit", credCollectionFixture, asUI, 201, &out)
	if out.Stats.ItemsAdded != 0 || out.Stats.ItemsSkipped != 1 {
		t.Fatalf("stats %+v", out.Stats)
	}
	named := false
	for _, e := range out.Report.Entries {
		if e.Feature == "item-skipped-duplicate" && e.Path == "Login" {
			named = true
		}
	}
	if !named {
		t.Fatalf("skip not named in report: %+v", out.Report.Entries)
	}
	if out.Quarantined {
		t.Fatal("a file with no scripts has nothing quarantined")
	}
}

func TestScriptsQuarantinedReflectsReality(t *testing.T) {
	f := newCollFixture(t)
	withScript := `{"info":{"name":"S","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[{"name":"r","event":[{"listen":"prerequest","script":{"exec":["pm.variables.set('a','1')"]}}],"request":{"method":"GET","url":"https://example.com/s"}}]}`
	var pv struct {
		Quarantined bool `json:"scriptsQuarantined"`
	}
	f.must("POST", "/api/import/collection/preview", withScript, asUI, 200, &pv)
	if !pv.Quarantined {
		t.Fatal("scripts present: must report quarantined")
	}
	f.must("POST", "/api/import/collection/preview", bearerCollectionFixture, asUI, 200, &pv)
	if pv.Quarantined {
		t.Fatal("no scripts: must not claim quarantine")
	}
}
