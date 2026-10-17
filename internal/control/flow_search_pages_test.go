package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// A History search whose body scan stopped early must say where it stopped, so
// the list pages on from there instead of ending at a short page.
func TestListFlowsBodySearchReportsWhereTheScanStopped(t *testing.T) {
	h, s, _ := newHub(t)
	w, err := s.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("deep-page-needle")); err != nil {
		t.Fatal(err)
	}
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "app.example.com", Path: "/old", Status: 200, ResBodyHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFlowAnywhereCandidates+5; i++ {
		if _, err := s.InsertFlow(&store.Flow{TS: time.UnixMilli(int64(10 + i)), Method: "GET", Scheme: "https", Host: "app.example.com", Path: "/newer", Status: 200}); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	type page struct {
		Flows []struct {
			ID int64 `json:"id"`
		} `json:"flows"`
		SearchMore   bool  `json:"searchMore"`
		SearchCursor int64 `json:"searchCursor"`
	}
	get := func(q string) page {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/flows?limit=50&sort=id&dir=desc&" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var p page
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	for _, scope := range []string{"anywhere", "body"} {
		first := get("search=deep-page-needle&searchScope=" + scope)
		if len(first.Flows) != 0 || !first.SearchMore || first.SearchCursor <= old {
			t.Fatalf("%s first page = %+v, want no rows, searchMore and a cursor above the old flow", scope, first)
		}
		next := get(fmt.Sprintf("search=deep-page-needle&searchScope=%s&curId=%d", scope, first.SearchCursor))
		if len(next.Flows) != 1 || next.Flows[0].ID != old || next.SearchMore {
			t.Fatalf("%s next page = %+v, want the old flow and no more", scope, next)
		}
	}
}

func TestUIHistoryPagesOnFromASearchCursor(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, proxy,
		"flowSearchCursor=d&&d.searchMore?Number(d.searchCursor)||0:0;",
		"if(flowSearchCursor)q.set('curId',String(flowSearchCursor));",
		"if(!flowPageError)continueShortSearch();",
		"show(flowSearchCursor?'Searching older flows…':'Loading older flows…');",
	)
}
