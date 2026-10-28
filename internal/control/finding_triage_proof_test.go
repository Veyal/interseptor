package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A finding whose impact was not demonstrated stays out of "verified", but the
// operator's triage (ignored, false positive, won't fix, fixed) must stick
// instead of being silently reset to needs_verification.
func TestTriageStatusSticksOnUndemonstratedFinding(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/findings", "application/json", strings.NewReader(
		`{"title":"Prereq only","severity":"High","source":"ai","proofReview":{"execution":"prerequisite_only","reason":"needs a rooted device"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ ID int64 }
	_ = json.NewDecoder(resp.Body).Decode(&f)
	resp.Body.Close()

	patch := func(body string) (int, string) {
		req, _ := http.NewRequest(http.MethodPatch, fmt.Sprintf("%s/api/findings/%d", ts.URL, f.ID), strings.NewReader(body))
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		msg, _ := out["error"].(string)
		return r.StatusCode, msg
	}
	for _, status := range []string{"ignored", "false_positive", "wont_fix", "fixed"} {
		body := fmt.Sprintf(`{"status":%q}`, status)
		if status == "ignored" {
			body = `{"status":"ignored","ignoreReason":"Low impact"}`
		}
		if code, msg := patch(body); code != http.StatusOK {
			t.Fatalf("%s: PATCH = %d %s", status, code, msg)
		}
		got, err := st.GetFinding(f.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != status {
			t.Fatalf("status %q was reset to %q", status, got.Status)
		}
	}
	code, msg := patch(`{"status":"verified"}`)
	if code != http.StatusBadRequest || !strings.Contains(msg, "demonstrated") {
		t.Fatalf("verifying an undemonstrated finding = %d %q, want 400 explaining the proof gate", code, msg)
	}
}
