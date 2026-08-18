package control

import (
	"bytes"
	"database/sql"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func TestPutRetentionRollsBackBothSettingsOnFailure(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.SetSettings(map[string]string{
		retentionMaxAgeHoursKey: "12",
		retentionMaxFlowsKey:    "34",
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(filepath.Dir(st.BodiesDir()), "interseptor.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_retention_flows BEFORE INSERT ON settings
		WHEN NEW.key = 'retention.maxFlows' BEGIN SELECT RAISE(ABORT, 'rejected'); END`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/flows/retention", bytes.NewBufferString(`{"maxAgeHours":1,"maxFlows":2}`))
	(&flowAPI{h}).putRetention(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	for key, want := range map[string]string{retentionMaxAgeHoursKey: "12", retentionMaxFlowsKey: "34"} {
		got, ok, err := st.GetSetting(key)
		if err != nil || !ok || got != want {
			t.Errorf("%s = %q, ok=%v, err=%v; want %q", key, got, ok, err, want)
		}
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
