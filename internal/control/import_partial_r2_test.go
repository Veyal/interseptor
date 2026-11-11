package control

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// failInsertOnPath makes the store reject any flow whose path is `path`, so an
// import fails on a chosen entry after earlier ones were stored.
func failInsertOnPath(t *testing.T, st *store.Store, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(filepath.Dir(st.BodiesDir()), "interseptor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER r2_fail_insert BEFORE INSERT ON flows WHEN NEW.path = '` + path + `'
		BEGIN SELECT RAISE(ABORT, 'r2 injected storage failure'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
}

const partialHAR = `{"log":{"version":"1.2","entries":[
 {"startedDateTime":"2026-01-01T00:00:00Z","request":{"method":"GET","url":"https://app.example.com/one","headers":[]},"response":{"status":200,"headers":[],"content":{"text":""}}},
 {"startedDateTime":"2026-01-01T00:00:01Z","request":{"method":"GET","url":"https://app.example.com/two","headers":[]},"response":{"status":200,"headers":[],"content":{"text":""}}},
 {"startedDateTime":"2026-01-01T00:00:02Z","request":{"method":"GET","url":"https://app.example.com/boom","headers":[]},"response":{"status":200,"headers":[],"content":{"text":""}}},
 {"startedDateTime":"2026-01-01T00:00:03Z","request":{"method":"GET","url":"https://app.example.com/four","headers":[]},"response":{"status":200,"headers":[],"content":{"text":""}}}
]}}`

type partialImportResult struct {
	Error    string `json:"error"`
	Imported *int   `json:"imported"`
	Partial  bool   `json:"partial"`
}

// checkPartialImport asserts the contract shared by the three flow importers: a
// storage failure after N flows keeps them, says so, and refreshes the views.
func checkPartialImport(t *testing.T, h *Hub, st *store.Store, events chan string, code int, body []byte) {
	t.Helper()
	if code != http.StatusInternalServerError {
		t.Fatalf("status %d %s, want 500", code, body)
	}
	if strings.Contains(string(body), "r2 injected storage failure") {
		t.Fatalf("raw storage error leaked to the client: %s", body)
	}
	var res partialImportResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	n, err := st.FlowCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("%d flows stored, want the 2 that preceded the failure", n)
	}
	if res.Imported == nil || *res.Imported != 2 || !res.Partial {
		t.Fatalf("failure response %s does not report the 2 flows that were imported", body)
	}
	nudged := false
	for len(events) > 0 {
		if strings.Contains(<-events, `"type":"flow.new"`) {
			nudged = true
		}
	}
	if !nudged {
		t.Fatal("no flow.new nudge after a partial import: History keeps hiding the imported flows")
	}
	// The Map aggregate cached before the import must not survive it.
	if hosts := endpointHosts(t, h); len(hosts) == 0 {
		t.Fatal("endpoints still served from the pre-import cache")
	}
}

// C12: HAR import.
func TestImportHARPartialFailureIsReported(t *testing.T) {
	h, st, _ := newHub(t)
	failInsertOnPath(t, st, "/boom")
	if hosts := endpointHosts(t, h); len(hosts) != 0 { // prime the (empty) Map cache
		t.Fatalf("unexpected endpoints %v", hosts)
	}
	events, unsub := subscribeRaw(h, 64)
	defer unsub()

	rec := httptest.NewRecorder()
	(&projectAPI{h}).importHAR(rec, httptest.NewRequest(http.MethodPost, "/api/import/har", strings.NewReader(partialHAR)))
	checkPartialImport(t, h, st, events, rec.Code, rec.Body.Bytes())
}

// C12: portable project import (its flows section is a HAR).
func TestImportProjectPartialFlowFailureIsReported(t *testing.T) {
	h, st, _ := newHub(t)
	failInsertOnPath(t, st, "/boom")
	endpointHosts(t, h)
	events, unsub := subscribeRaw(h, 64)
	defer unsub()

	code, _ := 0, 0
	rec := httptest.NewRecorder()
	(&projectAPI{h}).importProject(rec, httptest.NewRequest(http.MethodPost, "/api/project/import", strings.NewReader(`{"version":"1","har":`+partialHAR+`}`)))
	code = rec.Code
	checkPartialImport(t, h, st, events, code, rec.Body.Bytes())
}

// C12: Burp XML import.
func TestImportBurpPartialFailureIsReported(t *testing.T) {
	h, st, _ := newHub(t)
	failInsertOnPath(t, st, "/boom")
	endpointHosts(t, h)
	events, unsub := subscribeRaw(h, 64)
	defer unsub()

	item := func(path string) string {
		return `<item><url><![CDATA[https://app.example.com` + path + `]]></url><host>app.example.com</host><port>443</port><protocol>https</protocol><method><![CDATA[GET]]></method><path><![CDATA[` + path + `]]></path><request base64="false"><![CDATA[GET ` + path + ` HTTP/1.1
Host: app.example.com

]]></request><status>200</status><response base64="false"><![CDATA[HTTP/1.1 200 OK

]]></response></item>`
	}
	xml := `<?xml version="1.0"?><items burpVersion="2024.1">` + item("/one") + item("/two") + item("/boom") + item("/four") + `</items>`
	rec := httptest.NewRecorder()
	(&projectAPI{h}).importBurp(rec, httptest.NewRequest(http.MethodPost, "/api/import/burp", strings.NewReader(xml)))
	checkPartialImport(t, h, st, events, rec.Code, rec.Body.Bytes())
}

// C13: GET /api/flows/prune/pending answered a storage failure with the raw
// error text; every other handler scrubs it.
func TestListPendingPrunesScrubsStorageError(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&flowAPI{h}).listPendingPrunes(rec, httptest.NewRequest(http.MethodGet, "/api/flows/prune/pending", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "internal server error") {
		t.Fatalf("status %d body %s, want the scrubbed 500", rec.Code, rec.Body.String())
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "sql") || strings.Contains(rec.Body.String(), "closed") {
		t.Fatalf("raw storage error leaked: %s", rec.Body.String())
	}
}
