package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEvaluateCVSS(t *testing.T) {
	h := &metaAPI{}
	request := func(vector string) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]string{"vector": vector})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/finding-cvss", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.evaluateCVSS(w, r)
		return w
	}

	valid := request("CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N")
	if valid.Code != http.StatusOK {
		t.Fatalf("valid vector status=%d body=%s", valid.Code, valid.Body)
	}
	var got struct {
		Score  float64 `json:"score"`
		Rating string  `json:"rating"`
	}
	if err := json.Unmarshal(valid.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Score != 9.3 || got.Rating != "CRITICAL" {
		t.Fatalf("evaluation=%+v", got)
	}

	none := request("CVSS:4.0/AV:N/AC:H/AT:N/PR:H/UI:N/VC:N/VI:N/VA:N/SC:N/SI:N/SA:N")
	if none.Code != http.StatusOK || !bytes.Contains(none.Body.Bytes(), []byte(`"rating":"INFO"`)) {
		t.Fatalf("NONE should be exposed as Info: status=%d body=%s", none.Code, none.Body)
	}

	invalid := request("9.8")
	if invalid.Code != http.StatusBadRequest || !bytes.Contains(invalid.Body.Bytes(), []byte("invalid CVSS v4.0 vector")) {
		t.Fatalf("invalid vector status=%d body=%s", invalid.Code, invalid.Body)
	}
}
