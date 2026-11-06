package control

import (
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// A report needs a wider image than the 1x default; scale must deliver it from
// the same flow with the same content.
func TestPreviewPNGScaleQuery(t *testing.T) {
	h, s, _ := newHub(t)
	flowID, err := s.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "GET", Scheme: "https", Host: "app.example", Port: 443,
		Path: "/me", Status: 200,
		ReqHeaders: map[string][]string{"Host": {"app.example"}},
		ResHeaders: map[string][]string{"Content-Type": {"application/json"}},
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	size := func(query string) (int, int) {
		resp, err := http.Get(ts.URL + "/api/flows/" + strconv.FormatInt(flowID, 10) + "/preview.png" + query)
		if err != nil {
			t.Fatalf("GET %s: %v", query, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", query, resp.StatusCode)
		}
		img, err := png.Decode(resp.Body)
		if err != nil {
			t.Fatalf("decode %s: %v", query, err)
		}
		return img.Bounds().Dx(), img.Bounds().Dy()
	}
	w1, h1 := size("")
	w2, h2 := size("?scale=2")
	if float64(w2)/float64(w1) < 1.9 || float64(h2)/float64(h1) < 1.9 {
		t.Fatalf("scale=2 is %dx%d vs %dx%d, want roughly double", w2, h2, w1, h1)
	}
	if ww, _ := size("?width=1540"); ww != w2 {
		t.Fatalf("width=1540 rendered %d wide, want the same as scale=2 (%d)", ww, w2)
	}
	if resp, err := http.Get(ts.URL + "/api/flows/" + strconv.FormatInt(flowID, 10) + "/preview.png?scale=9"); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("scale=9 = %d, want 400", resp.StatusCode)
		}
	}
}
