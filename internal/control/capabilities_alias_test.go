package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCapabilitiesEndpointReportsTargetsSupport(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		SchemaVersion string `json:"schemaVersion"`
		Finding       struct {
			CreateFields      []string `json:"createFields"`
			TargetsSupported  bool     `json:"targetsSupported"`
			LegacyScalarField string   `json:"legacyScalarField"`
		} `json:"finding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion == "" || !containsString(got.Finding.CreateFields, "targets") || !got.Finding.TargetsSupported || got.Finding.LegacyScalarField != "target" {
		t.Fatalf("capabilities = %+v", got)
	}
}
