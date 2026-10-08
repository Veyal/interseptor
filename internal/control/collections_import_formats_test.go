package control

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

const insomniaFixture = `{"_type":"export","__export_format":4,"resources":[
 {"_id":"wrk_1","_type":"workspace","name":"Ins Demo","parentId":null},
 {"_id":"req_1","_type":"request","parentId":"wrk_1","name":"Get thing","method":"GET","url":"https://example.com/ins","headers":[],"body":{},"authentication":{}}]}`

const brunoFixture = `meta {
  name: Get bru thing
  type: http
  seq: 1
}

get {
  url: https://example.com/bru
  body: none
  auth: none
}

script:pre-request {
  bru.setVar("x", "1");
}
`

const harFixture = `{"log":{"version":"1.2","creator":{"name":"t","version":"1"},"entries":[
 {"startedDateTime":"2026-01-01T00:00:00Z","time":1,"request":{"method":"GET","url":"https://example.com/har","httpVersion":"HTTP/1.1","headers":[{"name":"Accept","value":"*/*"}],"queryString":[],"cookies":[],"headersSize":-1,"bodySize":0},
  "response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[],"cookies":[],"content":{"size":0,"mimeType":"text/plain"},"redirectURL":"","headersSize":-1,"bodySize":0},"cache":{},"timings":{"send":0,"wait":1,"receive":0}}]}}`

// Every importer is reachable through the same preview and commit routes,
// both auto-detected and by explicit format, and nothing is stored by preview.
func TestImportRoutesAcceptEveryFormat(t *testing.T) {
	cases := []struct {
		name, format, body, wantItem string
	}{
		{"insomnia", "insomnia", insomniaFixture, "Get thing"},
		{"bruno", "bruno", brunoFixture, "Get bru thing"},
		{"har", "har", harFixture, "/har"},
		{"curl", "curl", "curl https://example.com/c -H 'X-A: 1'", ""},
	}
	for _, c := range cases {
		for _, format := range []string{c.format, "auto"} {
			t.Run(c.name+"/"+format, func(t *testing.T) {
				f := newCollFixture(t)
				var pv struct {
					Name        string `json:"name"`
					Quarantined bool   `json:"scriptsQuarantined"`
				}
				f.must("POST", "/api/import/collection/preview?format="+format, c.body, asUI, 200, &pv)
				if !pv.Quarantined {
					t.Fatal("preview must announce quarantine")
				}
				var list struct {
					Collections []store.Collection `json:"collections"`
				}
				f.must("GET", "/api/collections", nil, asUI, 200, &list)
				if len(list.Collections) != 0 {
					t.Fatal("preview must not store anything")
				}
				var out struct {
					CollectionUID string `json:"collectionUid"`
				}
				f.must("POST", "/api/import/collection/commit?format="+format, c.body, asUI, 201, &out)
				if out.CollectionUID == "" {
					t.Fatal("commit returned no collection uid")
				}
				items, _ := f.st.ListItems(out.CollectionUID)
				found := c.wantItem == ""
				for _, it := range items {
					found = found || it.Name == c.wantItem || strings.Contains(string(it.URL), c.wantItem)
				}
				if len(items) == 0 || !found {
					t.Fatalf("items = %d, want one matching %q", len(items), c.wantItem)
				}
			})
		}
	}
}

// Scripts from every format arrive quarantined: no trust row, no capabilities.
func TestImportedBrunoScriptsAreQuarantined(t *testing.T) {
	f := newCollFixture(t)
	var out struct {
		CollectionUID string `json:"collectionUid"`
	}
	f.must("POST", "/api/import/collection/commit?format=bruno", brunoFixture, asUI, 201, &out)
	var sheet struct {
		Scripts []struct {
			Trusted bool `json:"trusted"`
		} `json:"scripts"`
	}
	f.must("GET", "/api/collections/"+out.CollectionUID+"/scripts", nil, asUI, 200, &sheet)
	if len(sheet.Scripts) == 0 {
		t.Fatal("the imported script must appear in the review sheet")
	}
	for _, s := range sheet.Scripts {
		if s.Trusted {
			t.Fatal("an imported script must never arrive trusted")
		}
	}
	co, _ := f.st.GetCollection(out.CollectionUID)
	if len(co.Caps) > 2 { // nil or "[]"
		t.Fatalf("imported collection capabilities = %s", co.Caps)
	}
}

func TestImportUnknownFormatListsSupportedOnes(t *testing.T) {
	f := newCollFixture(t)
	code, body := f.do("POST", "/api/import/collection/preview?format=wsdl", "x", asUI)
	if code != 400 || !strings.Contains(body, "insomnia") || !strings.Contains(body, "bruno") || !strings.Contains(body, "har") {
		t.Fatalf("unsupported format = %d %s", code, body)
	}
}

// The Bruno folder layout arrives as {files:[{path,text}]} (the UI reads every
// selected file); environments and folders are kept.
func TestImportBrunoFolderAsFiles(t *testing.T) {
	f := newCollFixture(t)
	body := `{"files":[{"path":"bruno.json","text":"{\"version\":\"1\",\"name\":\"Bruno Demo\",\"type\":\"collection\"}"},` +
		`{"path":"users/get.bru","text":` + jsonString(brunoFixture) + `}]}`
	var out struct {
		CollectionUID string `json:"collectionUid"`
	}
	f.must("POST", "/api/import/collection/commit?format=bruno-files", body, asUI, 201, &out)
	items, _ := f.st.ListItems(out.CollectionUID)
	folders := 0
	for _, it := range items {
		if it.Kind == "folder" {
			folders++
		}
	}
	if len(items) != 2 || folders != 1 {
		t.Fatalf("items = %d folders = %d", len(items), folders)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
