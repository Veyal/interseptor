package control

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRenderResponseCarriesAltAndSummaryHeaders(t *testing.T) {
	h, s, _ := newHub(t)
	seedRun(t, s, syntheticRun(testRunID))
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, body := getBytes(t, ts.URL+"/api/intruder/attacks/"+testRunID+"/render.png?kind=timeline")
	requirePNG(t, resp, body)
	alt, err := url.PathUnescape(resp.Header.Get("X-Render-Alt"))
	if err != nil || !strings.Contains(alt, "Intruder run "+testRunID) {
		t.Fatalf("X-Render-Alt = %q (%v)", resp.Header.Get("X-Render-Alt"), err)
	}
	sum, err := url.PathUnescape(resp.Header.Get("X-Render-Summary"))
	if err != nil || !strings.Contains(sum, "returned 2xx") {
		t.Fatalf("X-Render-Summary = %q (%v)", resp.Header.Get("X-Render-Summary"), err)
	}
	for _, v := range resp.Header.Values("X-Render-Alt") {
		for _, r := range v {
			if r > 126 || r < 32 {
				t.Fatalf("header must be ASCII-safe: %q", v)
			}
		}
	}
}

func TestRenderInputValidation(t *testing.T) {
	h, s, _ := newHub(t)
	seedRun(t, s, syntheticRun(testRunID))
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	base := ts.URL + "/api/intruder/attacks/" + testRunID + "/render.png?kind=timeline"
	for _, w := range []string{"100000", "100", "-5", "abc", "639", "1601"} {
		if resp, body := getBytes(t, base+"&width="+w); resp.StatusCode != 400 {
			t.Errorf("width=%s = %d (%s), want 400", w, resp.StatusCode, body)
		}
	}
	for _, w := range []string{"", "0", "640", "1100", "1600"} {
		if resp, body := getBytes(t, base+"&width="+w); resp.StatusCode != 200 {
			t.Errorf("width=%q = %d (%s), want 200", w, resp.StatusCode, body)
		}
	}
	for _, u := range []string{
		"/api/render/flow-diff.png?a=x&b=2",
		"/api/render/flow-diff.png?a=1&b=-4",
		"/api/render/finding-chain.png?findingId=abc",
		"/api/evidence-render?kind=finding_chain&findingIds=abc",
		"/api/evidence-render?kind=flow_diff&flowIdA=1&flowIdB=zz",
		"/api/intruder/attacks/" + testRunID + "/render.png?expected=-1",
	} {
		if resp, body := getBytes(t, ts.URL+u); resp.StatusCode != 400 {
			t.Errorf("%s = %d (%s), want 400", u, resp.StatusCode, body)
		}
	}
	fid := newFindingID(t, s)
	attach := ts.URL + "/api/findings/" + strconv.FormatInt(fid, 10) + "/evidence-render"
	if resp, _ := evPost(t, attach, `{"kind":"timeline","attackId":"latest","width":100000}`); resp.StatusCode != 400 {
		t.Fatalf("attach width = %d", resp.StatusCode)
	}
	if resp, _ := evPost(t, attach, `{"kind":"flow-diff","flowIdA":-1,"flowIdB":2}`); resp.StatusCode != 400 {
		t.Fatalf("attach negative id = %d", resp.StatusCode)
	}
}

func TestRenderConcurrencyLimitReturns503(t *testing.T) {
	h, s, _ := newHub(t)
	seedRun(t, s, syntheticRun(testRunID))
	ev := newEvidenceAPI(h)
	for i := 0; i < cap(ev.sem); i++ {
		ev.sem <- struct{}{}
	}
	_, err := ev.render(evidenceRequest{Kind: "intruder_timeline", RunID: testRunID, Mask: true})
	var se *evidenceStatusError
	if err == nil || !errors.As(err, &se) || se.code != http.StatusServiceUnavailable {
		t.Fatalf("full semaphore must 503, got %v", err)
	}
	<-ev.sem
	if _, err := ev.render(evidenceRequest{Kind: "intruder_timeline", RunID: testRunID, Mask: true}); err != nil {
		t.Fatalf("a free slot must render: %v", err)
	}
	if len(ev.sem) != cap(ev.sem)-1 {
		t.Fatalf("slot leaked: %d held", len(ev.sem))
	}
}

func TestRenderDeadlineMapsToUnavailable(t *testing.T) {
	err := mapRenderError(fmt.Errorf("wrapped: %w", errRenderTimeout))
	var se *evidenceStatusError
	if !errors.As(err, &se) || se.code != http.StatusServiceUnavailable {
		t.Fatalf("got %v", err)
	}
	_ = time.Second
}

func TestRenderJSONVariantEmbedsSmallPNGOnRequest(t *testing.T) {
	h, s, _ := newHub(t)
	seedRun(t, s, syntheticRun(testRunID))
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	_, plain := getBytes(t, ts.URL+"/api/intruder/attacks/latest/render.png?format=json")
	if strings.Contains(string(plain), `"png"`) {
		t.Fatalf("png must be opt-in: %.200s", plain)
	}
	_, body := getBytes(t, ts.URL+"/api/intruder/attacks/latest/render?kind=timeline&format=json&png=1")
	var out struct {
		PNG        string `json:"png"`
		PNGOmitted bool   `json:"pngOmitted"`
		Alt        string `json:"alt"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.PNG == "" || out.PNGOmitted || out.Alt == "" {
		t.Fatalf("body=%.200s err=%v", body, err)
	}
	raw, err := base64.StdEncoding.DecodeString(out.PNG)
	if err != nil || len(raw) == 0 || len(raw) > maxInlineRenderPNG || !strings.HasPrefix(string(raw), "\x89PNG") {
		t.Fatalf("embedded png invalid (%d bytes, %v)", len(raw), err)
	}
}
