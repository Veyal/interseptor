package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestEngagementBriefRoundTripAndReportHeader(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	put := func(body string) (*http.Response, string) {
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/engagement-brief", strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	resp, body := put(`{"scope":"api.example.com","doNotTouch":"billing","credentialPolicy":"never print values"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", resp.StatusCode, body)
	}
	var got store.EngagementBrief
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Version != 1 || got.DoNotTouch != "billing" {
		t.Fatalf("PUT body = %s (%v)", body, err)
	}

	resp, err := http.Get(ts.URL + "/api/engagement-brief")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"version":1`) {
		t.Fatalf("GET body = %s", b)
	}

	if _, err := st.CreateFinding(&store.Finding{Title: "t", Severity: "Low"}); err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	resp, err = http.Get(ts.URL + "/api/findings/report?statuses=all")
	if err != nil {
		t.Fatal(err)
	}
	md, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(md), "engagement brief v1") || !strings.Contains(string(md), "api.example.com") {
		t.Fatalf("report header missing brief citation:\n%s", md)
	}

	resp, err = http.Get(ts.URL + "/api/findings/report?format=json&statuses=all")
	if err != nil {
		t.Fatal(err)
	}
	jb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var rep struct {
		BriefVersion int `json:"engagementBriefVersion"`
	}
	if err := json.Unmarshal(jb, &rep); err != nil || rep.BriefVersion != 1 {
		t.Fatalf("json report brief version = %d (%v): %s", rep.BriefVersion, err, jb)
	}
}

func TestEngagementBriefRejectsTrailingJSONAndOversize(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	for _, body := range []string{
		`{"scope":"x"}{}`,
		`{"scope":"` + strings.Repeat("a", store.MaxEngagementFieldBytes+1) + `"}`,
	} {
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/engagement-brief", strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	}
	if b, _ := st.GetEngagementBrief(); b.Version != 0 {
		t.Fatalf("rejected writes changed brief: %+v", b)
	}
}
