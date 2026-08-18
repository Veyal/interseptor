package control

import (
	"bytes"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestPutRetentionRejectsAgeThatOverflowsDuration(t *testing.T) {
	h, _, _ := newHub(t)
	req := httptest.NewRequest(http.MethodPut, "/api/flows/retention",
		bytes.NewBufferString(`{"maxAgeHours":9223372036854775807,"maxFlows":0}`))
	rec := httptest.NewRecorder()
	(&flowAPI{h}).putRetention(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestRetentionRunRefusesPersistedOverflowWithoutDeleting(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.InsertFlow(&store.Flow{
		TS: time.Now().Add(-time.Hour), Method: "GET", Scheme: "https", Host: "example.com", Path: "/keep",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(retentionMaxAgeHoursKey, strconv.FormatInt(math.MaxInt64, 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runRetentionOnce(); err == nil {
		t.Fatal("runRetentionOnce succeeded with an overflowing persisted age")
	}
	if count, err := st.FlowCount(); err != nil || count != 1 {
		t.Fatalf("flow count = %d, err = %v; overflow must not delete history", count, err)
	}
}
