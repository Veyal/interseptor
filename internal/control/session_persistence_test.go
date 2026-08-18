package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

func TestSetSessionReportsPersistenceFailure(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{
		"enabled":true,
		"headers":"Authorization: Bearer example-token",
		"hostHeaders":{"example.com":"X-Test: one"}
	}`))
	rec := httptest.NewRecorder()
	(&sessionAPI{h}).setSession(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when session settings cannot be persisted; body = %s", rec.Code, rec.Body.String())
	}
}

func TestSetSessionRejectsUnsafeLoginRefreshInterval(t *testing.T) {
	h, _, _ := newHub(t)
	invalid := []int{-1}
	maxInt := int(^uint(0) >> 1)
	maxSafe := int64(^uint64(0)>>1) / int64(time.Second)
	if uint64(maxInt) > uint64(maxSafe) {
		invalid = append(invalid, maxInt)
	}
	for _, refreshSecs := range invalid {
		body, err := json.Marshal(map[string]any{
			"loginMacro": sender.LoginMacro{RefreshSecs: refreshSecs},
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(string(body)))
		rec := httptest.NewRecorder()
		(&sessionAPI{h}).setSession(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("refreshSecs %d: status = %d, want 400; body = %s", refreshSecs, rec.Code, rec.Body.String())
		}
	}
}

func TestLoginMacroFromFlowRejectsMalformedJSON(t *testing.T) {
	h, st, _ := newHub(t)
	id, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com",
		Port: 443, Path: "/login", HTTPVersion: "HTTP/1.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/session/login/from-flow/"+strconv.FormatInt(id, 10), strings.NewReader(`{"enabled":`))
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	(&sessionAPI{h}).loginMacroFromFlow(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestLoginRefreshReportsPersistenceFailure(t *testing.T) {
	h, st, _ := newHub(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "sid=fresh; Path=/")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	if err := h.snd.SetLoginMacro(sender.LoginMacro{
		Enabled: true, Target: upstream.URL,
		Request: "GET /login HTTP/1.1\r\nHost: example.com\r\n\r\n",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.snd.RunLoginMacroNow(); err == nil {
		t.Fatal("RunLoginMacroNow succeeded after session store was closed")
	}
}
