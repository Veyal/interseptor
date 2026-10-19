package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostNotesImageRejectsNonImagePayload(t *testing.T) {
	h, _, _ := newHub(t)
	post := func(body string) int {
		rec := httptest.NewRecorder()
		(&projectAPI{h}).postNotesImage(rec, httptest.NewRequest(http.MethodPost, "/api/notes/images", strings.NewReader(body)))
		return rec.Code
	}
	// "hello" declared as PNG, an HTML payload, and a payload with no declared type.
	for _, body := range []string{
		`{"mime":"image/png","data":"aGVsbG8="}`,
		`{"mime":"text/html","data":"PGh0bWw+PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg=="}`,
		`{"data":"aGVsbG8="}`,
	} {
		if c := post(body); c != http.StatusUnsupportedMediaType {
			t.Fatalf("%s -> %d, want 415", body, c)
		}
	}
	png := `{"mime":"application/octet-stream","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg=="}`
	if c := post(png); c != http.StatusOK {
		t.Fatalf("real PNG -> %d, want 200", c)
	}
}
