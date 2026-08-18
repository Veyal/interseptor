package control

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestPassiveCheckRejectsMissingBodyEvidence(t *testing.T) {
	h, st, _ := newHub(t)
	id, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/submit",
		ReqBodyHash: strings.Repeat("a", 64), ReqLen: 9,
	})
	if err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	source := "def check(flow):\n    return []\n"
	body := `{"source":` + strconv.Quote(source) + `,"flowId":` + strconv.FormatInt(id, 10) + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/checks/test", strings.NewReader(body))
	rec := httptest.NewRecorder()
	(&checksAPI{Hub: h}).testCheck(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q, want 404", rec.Code, rec.Body.String())
	}
}
