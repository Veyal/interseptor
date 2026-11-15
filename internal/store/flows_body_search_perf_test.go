package store

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
	"time"
)

func writeGzipTestBody(t *testing.T, s *Store, plain string) string {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write([]byte(plain)); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	w, err := s.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func Test_FlowSearch_unicode_case_fold_in_metadata_and_body(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	metaDottedI, err := s.InsertFlow(&Flow{
		TS:         time.UnixMilli(1),
		Method:     "GET",
		Scheme:     "https",
		Host:       "example.com",
		Path:       "/istanbul",
		Status:     200,
		ReqHeaders: map[string][]string{"X-Token": {"İzmir-marker"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bodyDottedI, err := s.InsertFlow(&Flow{
		TS:          time.UnixMilli(2),
		Method:      "POST",
		Scheme:      "https",
		Host:        "example.com",
		Path:        "/body-i",
		Status:      200,
		ResBodyHash: writeAnywhereTestBody(t, s, "İZMİR-BODY-MARKER"),
	})
	if err != nil {
		t.Fatal(err)
	}
	metaKelvin, err := s.InsertFlow(&Flow{
		TS:         time.UnixMilli(3),
		Method:     "GET",
		Scheme:     "https",
		Host:       "example.com",
		Path:       "/kelvin",
		Status:     200,
		ResHeaders: map[string][]string{"X-Unit": {"300KELVIN"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bodyKelvin, err := s.InsertFlow(&Flow{
		TS:          time.UnixMilli(4),
		Method:      "POST",
		Scheme:      "https",
		Host:        "example.com",
		Path:        "/body-k",
		Status:      200,
		ResBodyHash: writeAnywhereTestBody(t, s, "300KELVIN-BODY"),
	})
	if err != nil {
		t.Fatal(err)
	}
	bodyCafe, err := s.InsertFlow(&Flow{
		TS:          time.UnixMilli(5),
		Method:      "POST",
		Scheme:      "https",
		Host:        "example.com",
		Path:        "/cafe",
		Status:      200,
		ResBodyHash: writeAnywhereTestBody(t, s, "ORDER CAFÉ MENU"),
	})
	if err != nil {
		t.Fatal(err)
	}
	metaCafe, err := s.InsertFlow(&Flow{
		TS:     time.UnixMilli(6),
		Method: "GET",
		Scheme: "https",
		Host:   "example.com",
		Path:   "/menu-CAFÉ",
		Status: 200,
	})
	if err != nil {
		t.Fatal(err)
	}

	gotBodyI, _, _, err := s.FlowIDsBodySearch(FlowFilter{Search: "izmir-body-marker"}, 8000)
	if err != nil || len(gotBodyI) != 1 || gotBodyI[0] != bodyDottedI {
		t.Fatalf("body search İ->i got=%v err=%v, want [%d]", gotBodyI, err, bodyDottedI)
	}
	gotAnyI, _, err := anywhereSearchIDs(s, FlowFilter{Search: "izmir-"}, 8000)
	if err != nil || len(gotAnyI) != 2 || gotAnyI[0] != bodyDottedI || gotAnyI[1] != metaDottedI {
		t.Fatalf("anywhere search İ->i got=%v err=%v, want [%d %d]", gotAnyI, err, bodyDottedI, metaDottedI)
	}

	gotBodyK, _, _, err := s.FlowIDsBodySearch(FlowFilter{Search: "kelvin-body"}, 8000)
	if err != nil || len(gotBodyK) != 1 || gotBodyK[0] != bodyKelvin {
		t.Fatalf("body search K->k got=%v err=%v, want [%d]", gotBodyK, err, bodyKelvin)
	}
	gotAnyK, _, err := anywhereSearchIDs(s, FlowFilter{Search: "300kelvin"}, 8000)
	if err != nil || len(gotAnyK) != 2 || gotAnyK[0] != bodyKelvin || gotAnyK[1] != metaKelvin {
		t.Fatalf("anywhere search K->k got=%v err=%v, want [%d %d]", gotAnyK, err, bodyKelvin, metaKelvin)
	}

	gotBodyCafe, _, _, err := s.FlowIDsBodySearch(FlowFilter{Search: "café"}, 8000)
	if err != nil || len(gotBodyCafe) != 1 || gotBodyCafe[0] != bodyCafe {
		t.Fatalf("body search non-ASCII got=%v err=%v, want [%d]", gotBodyCafe, err, bodyCafe)
	}
	gotAnyCafe, _, err := anywhereSearchIDs(s, FlowFilter{Search: "café"}, 8000)
	if err != nil || len(gotAnyCafe) != 2 || gotAnyCafe[0] != metaCafe || gotAnyCafe[1] != bodyCafe {
		t.Fatalf("anywhere search non-ASCII got=%v err=%v, want [%d %d]", gotAnyCafe, err, metaCafe, bodyCafe)
	}
}

func Test_FlowSearch_gzip_body_exclusions_and_256k_cap(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	gzipHeaders := map[string][]string{"Content-Encoding": {"gzip"}}
	keepID, err := s.InsertFlow(&Flow{
		TS:          time.UnixMilli(1),
		Method:      "GET",
		Scheme:      "https",
		Host:        "example.com",
		Path:        "/gz-keep",
		Status:      200,
		ResHeaders:  gzipHeaders,
		ResBodyHash: writeGzipTestBody(t, s, "hello gzip-needle-token cleanly"),
	})
	if err != nil {
		t.Fatal(err)
	}
	dropID, err := s.InsertFlow(&Flow{
		TS:          time.UnixMilli(2),
		Method:      "GET",
		Scheme:      "https",
		Host:        "example.com",
		Path:        "/gz-drop",
		Status:      200,
		ResHeaders:  gzipHeaders,
		ResBodyHash: writeGzipTestBody(t, s, "hello gzip-needle-token with exclude-noise-term"),
	})
	if err != nil {
		t.Fatal(err)
	}

	bigPayload := "WITHIN-CAP-TOKEN " + strings.Repeat("a", maxEndpointBodyReadBytes) + " PAST-CAP-TOKEN"
	plainCapID, err := s.InsertFlow(&Flow{
		TS:          time.UnixMilli(3),
		Method:      "GET",
		Scheme:      "https",
		Host:        "example.com",
		Path:        "/plain-cap",
		Status:      200,
		ResBodyHash: writeAnywhereTestBody(t, s, bigPayload),
	})
	if err != nil {
		t.Fatal(err)
	}
	gzipCapID, err := s.InsertFlow(&Flow{
		TS:          time.UnixMilli(4),
		Method:      "GET",
		Scheme:      "https",
		Host:        "example.com",
		Path:        "/gzip-cap",
		Status:      200,
		ResHeaders:  gzipHeaders,
		ResBodyHash: writeGzipTestBody(t, s, bigPayload),
	})
	if err != nil {
		t.Fatal(err)
	}

	both, _, _, err := s.FlowIDsBodySearch(FlowFilter{Search: "gzip-needle-token"}, 8000)
	if err != nil || len(both) != 2 || both[0] != dropID || both[1] != keepID {
		t.Fatalf("plain gzip body search got=%v err=%v, want [%d %d]", both, err, dropID, keepID)
	}
	filtered, _, _, err := s.FlowIDsBodySearch(FlowFilter{Search: "gzip-needle-token -EXCLUDE-NOISE-TERM"}, 8000)
	if err != nil || len(filtered) != 1 || filtered[0] != keepID {
		t.Fatalf("gzip body exclusion got=%v err=%v, want [%d]", filtered, err, keepID)
	}

	within, _, _, err := s.FlowIDsBodySearch(FlowFilter{Search: "within-cap-token"}, 8000)
	if err != nil || len(within) != 2 || within[0] != gzipCapID || within[1] != plainCapID {
		t.Fatalf("within-cap got=%v err=%v, want [%d %d]", within, err, gzipCapID, plainCapID)
	}
	past, _, _, err := s.FlowIDsBodySearch(FlowFilter{Search: "past-cap-token"}, 8000)
	if err != nil || len(past) != 0 {
		t.Fatalf("past-cap got=%v err=%v, want empty", past, err)
	}
}

func Test_Endpoints_shared_body_hash_soft404_and_empty_body_search(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	soft404Hash := writeAnywhereTestBody(t, s, "<html><body>Sorry, the requested page was not found.</body></html>")
	okHash := writeAnywhereTestBody(t, s, `{"ok":true}`)

	for i, path := range []string{"/missing-a", "/missing-b", "/missing-c"} {
		if _, err := s.InsertFlow(&Flow{
			TS:          time.UnixMilli(int64(i + 1)),
			Method:      "GET",
			Scheme:      "https",
			Host:        "example.com",
			Path:        path,
			Status:      200,
			ResBodyHash: soft404Hash,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.InsertFlow(&Flow{
		TS:          time.UnixMilli(9),
		Method:      "GET",
		Scheme:      "https",
		Host:        "example.com",
		Path:        "/noise-404",
		Status:      404,
		ResBodyHash: soft404Hash,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertFlow(&Flow{
		TS:          time.UnixMilli(10),
		Method:      "GET",
		Scheme:      "https",
		Host:        "example.com",
		Path:        "/api/health",
		Status:      200,
		ResBodyHash: okHash,
	}); err != nil {
		t.Fatal(err)
	}

	eps, _, err := s.Endpoints(EndpointFilter{HideNoiseOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 4 {
		t.Fatalf("HideNoiseOnly endpoints count = %d (%+v), want 4 (dropping /noise-404)", len(eps), eps)
	}
	for _, ep := range eps {
		wantSoft := ep.Path != "/api/health"
		if ep.Soft404 != wantSoft {
			t.Fatalf("endpoint %s Soft404 = %v, want %v", ep.Path, ep.Soft404, wantSoft)
		}
	}

	emptyBody, _, err := s.Endpoints(EndpointFilter{
		Search:        "never-matched-token",
		SearchScope:   EndpointSearchBody,
		HideNoiseOnly: true,
	})
	if err != nil || len(emptyBody) != 0 {
		t.Fatalf("empty body search endpoints = %+v err=%v, want empty", emptyBody, err)
	}
}
