package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// When the server starts in a fallback project (the remembered external folder
// was unavailable), the reason must be readable by the UI, not only by whoever
// reads the log.
func TestProjectReportsTheStartupNotice(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	get := func() map[string]any {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/project")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if _, present := get()["startupNotice"]; present {
		t.Fatal("no notice must mean no field, so a normal start shows nothing")
	}
	h.StartupNotice = "the project folder /Volumes/client is unavailable; opened the default project instead"
	if got := get()["startupNotice"]; got != h.StartupNotice {
		t.Fatalf("startupNotice = %v", got)
	}
}
