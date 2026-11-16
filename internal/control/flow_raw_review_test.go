package control

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/msgcodec"
	"github.com/Veyal/interseptor/internal/store"
)

func gzipBytesForVariantTest(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// GET /api/flows/{id}/raw treated every side other than "res" as the request,
// so side=response (or a typo) answered 200 with the wrong message and nothing
// to tell the caller. An unknown side is now a 400; the two documented values
// and the omitted default are unchanged.
func TestFlowRawRejectsUnknownSide(t *testing.T) {
	h, s, _ := newHub(t)
	id, err := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/a",
		HTTPVersion: "HTTP/1.1", Status: 204,
		ReqHeaders: map[string][]string{"Host": {"example.com"}},
		ResHeaders: map[string][]string{"X-Marker": {"response-side"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	get := func(query string) (int, string) {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/flows/" + itoa(id) + "/raw" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, readAll(resp.Body)
	}
	for _, q := range []string{"", "?side=req"} {
		if code, body := get(q); code != http.StatusOK || !strings.HasPrefix(body, "GET /a HTTP/1.1") {
			t.Errorf("raw%s: status %d body %q, want the request", q, code, body)
		}
	}
	if code, body := get("?side=res"); code != http.StatusOK || !strings.Contains(body, "X-Marker: response-side") {
		t.Errorf("raw?side=res: status %d body %q, want the response", code, body)
	}
	for _, side := range []string{"response", "request", "RES", "both", "resp"} {
		code, body := get("?side=" + side)
		if code != http.StatusBadRequest {
			t.Errorf("raw?side=%s: status %d body %q, want 400", side, code, body)
		}
		if strings.HasPrefix(body, "GET /a HTTP/1.1") {
			t.Errorf("raw?side=%s answered with the request", side)
		}
	}
}

func TestFlowVariantsRawBodyDecodedAndDetail(t *testing.T) {
	h, s, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	get := func(ts *httptest.Server, path string) (int, string) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, readAll(resp.Body)
	}
	if err := msgcodec.Save(h.CodecsDir(), "echo-body", `meta = {"id": "echo-body", "title": "Echo Body"}

def match(flow, side):
    return True

def decode(flow, side, raw):
    return {"plaintext": "decoded:" + flow.method + ":" + flow.path + ":" + str(raw)}
`); err != nil {
		t.Fatalf("save codec: %v", err)
	}

	modBody := []byte(`{"role":"admin"}`)
	modHash, modLen := putBodyR2(t, s, modBody)
	emptyOrigID, err := s.InsertFlow(&store.Flow{
		TS:                 time.UnixMilli(2),
		Method:             "POST",
		OriginalMethod:     "GET",
		Scheme:             "https",
		Host:               "example.com",
		Port:               443,
		Path:               "/after?x=2",
		OriginalPath:       "/before?x=1",
		HTTPVersion:        "HTTP/1.1",
		Status:             200,
		ReqHeaders:         map[string][]string{"Host": {"example.com"}, "X-Step": {"after"}, "Content-Type": {"application/json"}},
		OriginalReqHeaders: map[string][]string{"Host": {"example.com"}, "X-Step": {"before"}},
		ReqBodyHash:        modHash,
		ReqLen:             modLen,
		Flags:              store.FlagIntercepted | store.FlagEdited,
	})
	if err != nil {
		t.Fatal(err)
	}

	origGzipReq := gzipBytesForVariantTest(t, `{"token":"orig-req"}`)
	modGzipReq := gzipBytesForVariantTest(t, `{"token":"mod-req"}`)
	origGzipRes := gzipBytesForVariantTest(t, `orig-res-body`)
	modGzipRes := gzipBytesForVariantTest(t, `mod-res-body`)
	origReqH, _ := putBodyR2(t, s, origGzipReq)
	modReqH, modReqLen := putBodyR2(t, s, modGzipReq)
	origResH, _ := putBodyR2(t, s, origGzipRes)
	modResH, modResLen := putBodyR2(t, s, modGzipRes)

	gzipID, err := s.InsertFlow(&store.Flow{
		TS:                  time.UnixMilli(3),
		Method:              "POST",
		OriginalMethod:      "PUT",
		Scheme:              "https",
		Host:                "example.com",
		Port:                443,
		Path:                "/submit?v=mod",
		OriginalPath:        "/submit?v=orig",
		HTTPVersion:         "HTTP/1.1",
		Status:              200,
		OriginalStatus:      403,
		ReqHeaders:          map[string][]string{"Host": {"example.com"}, "Content-Encoding": {"gzip"}, "Content-Type": {"application/json"}, "X-Tag": {"modified"}},
		OriginalReqHeaders:  map[string][]string{"Host": {"example.com"}, "Content-Encoding": {"gzip"}, "Content-Type": {"application/json"}, "X-Tag": {"original"}},
		ResHeaders:          map[string][]string{"Content-Encoding": {"gzip"}, "Content-Type": {"text/plain"}, "X-Resp": {"modified"}},
		OriginalResHeaders:  map[string][]string{"Content-Encoding": {"gzip"}, "Content-Type": {"text/plain"}, "X-Resp": {"original"}},
		ReqBodyHash:         modReqH,
		OriginalReqBodyHash: origReqH,
		ResBodyHash:         modResH,
		OriginalResBodyHash: origResH,
		ReqLen:              modReqLen,
		ResLen:              modResLen,
		Mime:                "text/plain",
		Flags:               store.FlagIntercepted | store.FlagEdited | store.FlagResponseEdited,
	})
	if err != nil {
		t.Fatal(err)
	}

	sharedResHash, sharedResLen := putBodyR2(t, s, []byte("unchanged-body"))
	headerRuleID, err := s.InsertFlow(&store.Flow{
		TS:                 time.UnixMilli(4),
		Method:             "GET",
		Scheme:             "https",
		Host:               "example.com",
		Port:               443,
		Path:               "/hdr-rule",
		HTTPVersion:        "HTTP/1.1",
		Status:             200,
		ResHeaders:         map[string][]string{"X-Rule": {"after"}},
		OriginalResHeaders: map[string][]string{"X-Rule": {"before"}},
		ResBodyHash:        sharedResHash,
		ResLen:             sharedResLen,
		Mime:               "text/plain",
	})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// 1) Detail DTO includes original method/path/status and truthful original length (including 0).
	code, detailRaw := get(ts, "/api/flows/"+itoa(emptyOrigID))
	if code != http.StatusOK {
		t.Fatalf("detail status=%d", code)
	}
	var detail struct {
		Method         string `json:"method"`
		Path           string `json:"path"`
		OriginalMethod string `json:"originalMethod"`
		OriginalPath   string `json:"originalPath"`
		OriginalReqLen *int64 `json:"originalReqLen"`
	}
	if err := json.Unmarshal([]byte(detailRaw), &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail.OriginalMethod != "GET" || detail.OriginalPath != "/before?x=1" {
		t.Fatalf("detail original start line = (%q %q), want (GET /before?x=1)", detail.OriginalMethod, detail.OriginalPath)
	}
	if detail.OriginalReqLen == nil || *detail.OriginalReqLen != 0 {
		t.Fatalf("detail originalReqLen = %v, want 0", detail.OriginalReqLen)
	}

	// 2) Originally empty request edited to carry a body must NOT show the modified body under variant=original.
	if code, body := get(ts, "/api/flows/"+itoa(emptyOrigID)+"/raw?side=req&variant=original"); code != http.StatusOK || !strings.HasPrefix(body, "GET /before?x=1 HTTP/1.1") || !strings.Contains(body, "X-Step: before") || strings.Contains(body, "admin") {
		t.Fatalf("orig empty req raw: status=%d body=%q", code, body)
	}
	if code, body := get(ts, "/api/flows/"+itoa(emptyOrigID)+"/raw?side=req"); code != http.StatusOK || !strings.HasPrefix(body, "POST /after?x=2 HTTP/1.1") || !strings.Contains(body, "X-Step: after") || !strings.Contains(body, "admin") {
		t.Fatalf("mod req raw: status=%d body=%q", code, body)
	}
	if code, body := get(ts, "/api/flows/"+itoa(emptyOrigID)+"/body?side=req&variant=original"); code != http.StatusOK || body != "" {
		t.Fatalf("orig empty req body download: status=%d body=%q, want empty", code, body)
	}
	if code, body := get(ts, "/api/flows/"+itoa(emptyOrigID)+"/body?side=req"); code != http.StatusOK || body != string(modBody) {
		t.Fatalf("mod req body download: status=%d body=%q", code, body)
	}

	// 3) Gzipped request + response support variant=original across raw, body, decoded, and status line.
	if code, body := get(ts, "/api/flows/"+itoa(gzipID)+"/raw?side=req&variant=original"); code != http.StatusOK || !strings.HasPrefix(body, "PUT /submit?v=orig HTTP/1.1") || !strings.Contains(body, "X-Tag: original") || !strings.Contains(body, "orig-req") || strings.Contains(body, "mod-req") {
		t.Fatalf("orig gzip req raw: status=%d body=%q", code, body)
	}
	if code, body := get(ts, "/api/flows/"+itoa(gzipID)+"/raw?side=res&variant=original"); code != http.StatusOK || !strings.HasPrefix(body, "HTTP/1.1 403 Forbidden") || !strings.Contains(body, "X-Resp: original") || !strings.Contains(body, "orig-res-body") || strings.Contains(body, "mod-res-body") {
		t.Fatalf("orig gzip res raw: status=%d body=%q", code, body)
	}
	if code, body := get(ts, "/api/flows/"+itoa(gzipID)+"/body?side=req&variant=original"); code != http.StatusOK || body != `{"token":"orig-req"}` {
		t.Fatalf("orig gzip req body: status=%d body=%q", code, body)
	}
	if code, body := get(ts, "/api/flows/"+itoa(gzipID)+"/body?side=res&variant=original"); code != http.StatusOK || body != "orig-res-body" {
		t.Fatalf("orig gzip res body: status=%d body=%q", code, body)
	}
	if code, body := get(ts, "/api/flows/"+itoa(gzipID)+"/decoded?side=req&variant=original"); code != http.StatusOK || !strings.Contains(body, `"decoded:PUT:/submit?v=orig:{\"token\":\"orig-req\"}"`) {
		t.Fatalf("orig decoded req: status=%d body=%q", code, body)
	}
	if code, body := get(ts, "/api/flows/"+itoa(gzipID)+"/decoded?side=req"); code != http.StatusOK || !strings.Contains(body, `"decoded:POST:/submit?v=mod:{\"token\":\"mod-req\"}"`) {
		t.Fatalf("mod decoded req: status=%d body=%q", code, body)
	}

	// 4) Header-only response rules (without FlagResponseEdited) keep the shared unchanged body.
	if code, body := get(ts, "/api/flows/"+itoa(headerRuleID)+"/raw?side=res&variant=original"); code != http.StatusOK || !strings.Contains(body, "X-Rule: before") || !strings.Contains(body, "unchanged-body") {
		t.Fatalf("header-rule orig res raw: status=%d body=%q", code, body)
	}
}
