package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A storage failure is the server's fault. Answering 400 tells the UI the
// request was wrong and hides the fault (and used to leak the raw store error).
func TestPruneHandlersAnswer500OnStorageFailure(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		call             func(*flowAPI, http.ResponseWriter, *http.Request)
	}{
		{"undo", "/api/flows/prune/undo", `{"pruneId":1}`, (*flowAPI).undoPrune},
		{"purge", "/api/flows/purge", `{"hosts":["app.example.com"],"mode":"delete"}`, (*flowAPI).purgeFlows},
		{"purge dry run", "/api/flows/purge", `{"hosts":["app.example.com"],"mode":"delete","dryRun":true}`, (*flowAPI).purgeFlows},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, st, _ := newHub(t)
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			tc.call(&flowAPI{h}, rec, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status %d %s, want 500 for a storage failure", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "sql") || strings.Contains(rec.Body.String(), "closed") {
				t.Fatalf("raw storage error leaked: %s", rec.Body.String())
			}
		})
	}
}

// What the caller got wrong stays a 400 with the reason.
func TestPruneHandlersKeep400ForCallerMistakes(t *testing.T) {
	h, _, _ := newHub(t)
	for _, tc := range []struct {
		name, body, want string
		call             func(*flowAPI, http.ResponseWriter, *http.Request)
	}{
		{"unknown prune", `{"pruneId":999}`, "not found", (*flowAPI).undoPrune},
		{"keepOnly without hosts", `{"hosts":[],"mode":"keepOnly"}`, "keepOnly requires", (*flowAPI).purgeFlows},
		{"blank host", `{"hosts":["  "],"mode":"delete"}`, "empty host pattern", (*flowAPI).purgeFlows},
	} {
		rec := httptest.NewRecorder()
		tc.call(&flowAPI{h}, rec, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(tc.body)))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: %d %s, want 400 mentioning %q", tc.name, rec.Code, rec.Body.String(), tc.want)
		}
	}
}
