package control

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestListFlowsHideBrowserNoise(t *testing.T) {
	h, s, _ := newHub(t)
	for _, f := range []*store.Flow{
		{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "app.example.com", Path: "/login", Status: 200},
		{TS: time.UnixMilli(2), Method: "POST", Scheme: "https", Host: "content-autofill.googleapis.com", Path: "/v1/forms:vote", Status: 200},
		{TS: time.UnixMilli(3), Method: "GET", Scheme: "https", Host: "accounts.google.com", Path: "/ListAccounts?gpsia=1", Status: 200},
	} {
		if _, err := s.InsertFlow(f); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	if n := flowCount(t, ts.URL+"/api/flows"); n != 3 {
		t.Fatalf("default list = %d flows, want all 3: the filter is opt-in", n)
	}
	if n := flowCount(t, ts.URL+"/api/flows?hideBrowserNoise=1&search=example"); n != 1 {
		t.Fatalf("hideBrowserNoise=1 with a search = %d flows, want 1", n)
	}
	if n := flowCount(t, ts.URL+"/api/flows?hideBrowserNoise=1"); n != 1 {
		t.Fatalf("hideBrowserNoise=1 = %d flows, want only the target flow", n)
	}
}

func TestUIHistoryBrowserNoiseToggle(t *testing.T) {
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `id="hideNoiseFilter"`, `aria-pressed="false"`, "Hide browser noise")
	proxy := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, proxy,
		"if(state.hideBrowserNoise)q.set('hideBrowserNoise','1');",
		"if(state.hideBrowserNoise&&isBrowserNoise(f.host,f.path))return false;",
		"[state.hideBrowserNoise,'#hideNoiseFilter','hiding browser noise']",
		"hideBrowserNoise:!!state.hideBrowserNoise",
		// saved views carry it like the other History filters
		"hideTlsFailed:state.hideTlsFailed,hideBrowserNoise:!!state.hideBrowserNoise};",
		"state.hideBrowserNoise=!!f.hideBrowserNoise;",
	)
	if !strings.Contains(proxy, "state.hideBrowserNoise=localStorage.getItem(HIDE_NOISE_KEY)==='1';") {
		t.Error("browser noise must stay visible until the viewer opts in")
	}
}
