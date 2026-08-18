package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectMetadataMutationsRejectTrailingJSONBeforeChangingState(t *testing.T) {
	t.Run("saved view", func(t *testing.T) {
		h, st, _ := newHub(t)
		ts := httptest.NewServer(h.Handler())
		defer ts.Close()

		resp, err := http.Post(ts.URL+"/api/views", "application/json",
			strings.NewReader(`{"name":"new view","data":{"host":"example.com"}}{}`))
		if err != nil {
			t.Fatalf("POST view: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
		views, err := st.ListViews()
		if err != nil {
			t.Fatalf("ListViews: %v", err)
		}
		if len(views) != 0 {
			t.Fatalf("views changed after rejected create: %+v", views)
		}
	})

	t.Run("OOB base URL", func(t *testing.T) {
		h, st, _ := newHub(t)
		if err := st.SetSetting("oob.enabled", "1"); err != nil {
			t.Fatalf("enable OOB: %v", err)
		}
		if err := st.SetSetting("oob.baseUrl", "https://old.example.com/oob"); err != nil {
			t.Fatalf("seed OOB base: %v", err)
		}
		ts := httptest.NewServer(h.Handler())
		defer ts.Close()

		resp, err := http.Post(ts.URL+"/api/oob/base", "application/json",
			strings.NewReader(`{"baseUrl":"https://new.example.com/oob"}{}`))
		if err != nil {
			t.Fatalf("POST OOB base: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
		base, ok, err := st.GetSetting("oob.baseUrl")
		if err != nil {
			t.Fatalf("GetSetting: %v", err)
		}
		if !ok || base != "https://old.example.com/oob" {
			t.Fatalf("base URL = %q (ok=%v), want old value", base, ok)
		}
	})
}
