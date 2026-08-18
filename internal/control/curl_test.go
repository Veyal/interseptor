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

func TestFlowCurlClassifiesLookupFailures(t *testing.T) {
	h, st, _ := newHub(t)
	api := &flowAPI{h}

	missingReq := httptest.NewRequest(http.MethodGet, "/api/flows/1/curl", nil)
	missingReq.SetPathValue("id", "1")
	missingRec := httptest.NewRecorder()
	api.flowCurl(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d, want 404", missingRec.Code)
	}

	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	failureReq := httptest.NewRequest(http.MethodGet, "/api/flows/1/curl", nil)
	failureReq.SetPathValue("id", "1")
	failureRec := httptest.NewRecorder()
	api.flowCurl(failureRec, failureReq)
	if failureRec.Code != http.StatusInternalServerError || !strings.Contains(failureRec.Body.String(), "internal server error") {
		t.Fatalf("storage failure status=%d body=%q, want scrubbed 500", failureRec.Code, failureRec.Body.String())
	}
}

func TestFlowCurlRejectsMissingRequestBody(t *testing.T) {
	h, st, _ := newHub(t)
	id, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/submit",
		ReqBodyHash: strings.Repeat("a", 64), ReqLen: 9,
	})
	if err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/flows/1/curl", nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	(&flowAPI{h}).flowCurl(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q, want 404", rec.Code, rec.Body.String())
	}
}
