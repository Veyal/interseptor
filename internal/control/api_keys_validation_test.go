package control

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestCreateAPIKeyRejectsUnsafeMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "unknown scope", body: `{"scope":"readonly"}`},
		{name: "negative expiry", body: `{"expiresIn":-1}`},
		{name: "overflowing expiry", body: `{"expiresIn":` + strconv.FormatInt(math.MaxInt64, 10) + `}`},
		{name: "oversized label", body: `{"label":"` + strings.Repeat("x", 257) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, st, _ := newHub(t)
			req := httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			(&metaAPI{Hub: h}).createKey(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			keys, err := st.ListAPIKeys()
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) != 0 {
				t.Fatalf("invalid request created %d keys", len(keys))
			}
		})
	}
}
