package control

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/msgcodec"
	"github.com/Veyal/interseptor/internal/store"
)

func putBodyR2(t *testing.T, st *store.Store, body []byte) (string, int64) {
	t.Helper()
	bw, err := st.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bw.Write(body); err != nil {
		t.Fatal(err)
	}
	hash, n, err := bw.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return hash, n
}

func allocatedBy(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// Playbook matching keeps 64 KiB of the response and the request's parameter
// names. Readiness runs it for the newest flow on every refresh, so it must not
// load a large download or upload into memory to do that.
func TestObservedForFlowReadsBoundedBodies(t *testing.T) {
	h, st, _ := newHub(t)
	const size = 32 << 20
	res := bytes.Repeat([]byte{'r'}, size)
	copy(res, "MARKER-IN-RESPONSE ")
	req := append([]byte(`{"user_id":1,"redirect_url":"x","blob":"`), bytes.Repeat([]byte{'q'}, size)...)
	req = append(req, `","tail":2}`...)
	resHash, resLen := putBodyR2(t, st, res)
	reqHash, reqLen := putBodyR2(t, st, req)
	f := &store.Flow{Method: "POST", Host: "example.com", Path: "/upload?next=/home", ReqBodyHash: reqHash, ReqLen: reqLen, ResBodyHash: resHash, ResLen: resLen}

	var obs = h.observedForFlow(f)
	got := allocatedBy(func() { obs = h.observedForFlow(f) })
	t.Logf("observedForFlow on a %d MiB request and response allocated %.1f MiB", size>>20, float64(got)/(1<<20))
	if got > 8<<20 {
		t.Fatalf("observedForFlow allocated %d MiB for two %d MiB bodies", got>>20, size>>20)
	}
	if len(obs.Body) != 64*1024 || !strings.HasPrefix(obs.Body, "MARKER-IN-RESPONSE ") {
		t.Fatalf("response signal = %d bytes starting %q, want the first 64 KiB", len(obs.Body), obs.Body[:min(len(obs.Body), 20)])
	}
	sort.Strings(obs.Params)
	// The keys before the oversized value are still reported; "tail" lies past
	// the read limit and "blob" is the key whose value was cut.
	if want := "blob,next,redirect_url,user_id"; strings.Join(obs.Params, ",") != want {
		t.Fatalf("params = %v, want %s", obs.Params, want)
	}
}

// A truncated form body yields the complete pairs only, never a name invented
// from the cut point.
func TestParamNamesFromTruncatedBody(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"a=1&b=2&cccc", "a,b"},
		{"a=1&b=2&c=3", "a,b"},
		{"onlyone", ""},
		{`{"a":1,"b":{"x":[1,2`, "a,b"},
		{`{"a":1,"b":"cut`, "a,b"},
		{`{"a":1,"b`, "a"},
		{`{"a":1,`, "a"},
		{`[{"a":1},`, ""},
	} {
		got := paramNamesFromTruncatedBody(tc.body)
		sort.Strings(got)
		if strings.Join(got, ",") != tc.want {
			t.Errorf("paramNamesFromTruncatedBody(%q) = %v, want %s", tc.body, got, tc.want)
		}
	}
}

// Complete bodies keep the names they always produced, and a compressed
// response is matched on its text rather than on the compressed bytes.
func TestObservedForFlowSmallBodiesUnchanged(t *testing.T) {
	h, st, _ := newHub(t)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte("Warning: mysql_fetch_array() expects parameter"))
	zw.Close()
	resHash, resLen := putBodyR2(t, st, gz.Bytes())
	for body, want := range map[string]string{
		`{"id":7,"role":"user"}`: "id,q,role",
		"id=7&role=user":         "id,q,role",
		"":                       "q",
	} {
		f := &store.Flow{Method: "POST", Host: "Example.com", Path: "/a?q=1", ResBodyHash: resHash, ResLen: resLen,
			ReqHeaders: map[string][]string{"Content-Type": {"application/json"}},
			ResHeaders: map[string][]string{"Content-Encoding": {"gzip"}}}
		if body != "" {
			f.ReqBodyHash, f.ReqLen = putBodyR2(t, st, []byte(body))
		}
		obs := h.observedForFlow(f)
		sort.Strings(obs.Params)
		if got := strings.Join(obs.Params, ","); got != want {
			t.Errorf("body %q: params = %s, want %s", body, got, want)
		}
		if obs.Host != "example.com" || obs.Path != "/a" || obs.Headers["Content-Type"] != "application/json" {
			t.Errorf("body %q: observed = %+v", body, obs)
		}
		if !strings.Contains(obs.Body, "mysql_fetch_array") {
			t.Errorf("body %q: response signal is not the decoded text: %q", body, obs.Body)
		}
	}
}

// A codec sees at most the body the inspector would display. A capture far
// larger than that used to be read whole (and copied again into a string) for
// each side, whichever side was being decoded.
func TestFlowForCodecReadsBoundedBodies(t *testing.T) {
	h, st, _ := newHub(t)
	const size = 96 << 20
	big := bytes.Repeat([]byte{'d'}, size)
	resHash, resLen := putBodyR2(t, st, big)
	big = nil
	reqHash, reqLen := putBodyR2(t, st, []byte(`{"enc":"abc"}`))
	f := &store.Flow{Method: "POST", Host: "example.com", Path: "/api", ReqBodyHash: reqHash, ReqLen: reqLen, ResBodyHash: resHash, ResLen: resLen}

	var flowLen int
	got := allocatedBy(func() {
		flow, err := h.flowForCodec(f)
		if err != nil {
			t.Fatal(err)
		}
		flowLen = len(flow.ResBody)
		if flow.ReqBody != `{"enc":"abc"}` {
			t.Fatalf("request body = %q", flow.ReqBody)
		}
	})
	t.Logf("flowForCodec on a %d MiB response allocated %.1f MiB (codec sees %d MiB)", size>>20, float64(got)/(1<<20), flowLen>>20)
	if flowLen != decodeMax {
		t.Fatalf("codec response body = %d bytes, want the %d-byte display limit", flowLen, decodeMax)
	}
	if got > 128<<20 {
		t.Fatalf("flowForCodec allocated %d MiB for a %d MiB body", got>>20, size>>20)
	}
}

// Bodies inside the limit reach the codec complete and decompressed, as before.
func TestFlowForCodecSmallBodiesUnchanged(t *testing.T) {
	h, st, _ := newHub(t)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(`{"data":"plain"}`))
	zw.Close()
	resHash, resLen := putBodyR2(t, st, gz.Bytes())
	reqHash, reqLen := putBodyR2(t, st, []byte("a=1"))
	f := &store.Flow{Method: "POST", Host: "example.com", Path: "/api", Status: 200, ReqBodyHash: reqHash, ReqLen: reqLen, ResBodyHash: resHash, ResLen: resLen,
		ResHeaders: map[string][]string{"Content-Encoding": {"gzip"}}}
	flow, truncated, err := h.flowForCodecLimit(f, decodeMax)
	if err != nil || truncated {
		t.Fatalf("flowForCodecLimit: truncated=%v err=%v", truncated, err)
	}
	if flow.ReqBody != "a=1" || flow.ResBody != `{"data":"plain"}` || flow.Status != 200 || flow.Host != "example.com" {
		t.Fatalf("codec flow = %+v", flow)
	}
	if _, truncated, err = h.flowForCodecLimit(f, 8); err != nil || !truncated {
		t.Fatalf("limit 8: truncated=%v err=%v, want truncated", truncated, err)
	}
	if _, err := h.flowForCodec(&store.Flow{ReqBodyHash: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("a missing body must still be an error")
	}
}

// The Decoded view says when the codec was given a prefix of the capture.
func TestGetFlowDecodedReportsTruncatedInput(t *testing.T) {
	h, st, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	if err := msgcodec.Save(h.CodecsDir(), "len", "meta = {\"id\": \"len\", \"title\": \"Len\"}\n\ndef match(flow, side):\n    return True\n\ndef decode(flow, side, raw):\n    return {\"plaintext\": str(len(raw))}\n"); err != nil {
		t.Fatal(err)
	}
	decoded := func(size int) map[string]any {
		hash, n := putBodyR2(t, st, bytes.Repeat([]byte{'x'}, size))
		id, err := st.InsertFlow(&store.Flow{Method: "GET", Scheme: "https", Host: "example.com", Path: "/big", Status: 200, ResBodyHash: hash, ResLen: n})
		if err != nil {
			t.Fatal(err)
		}
		rec := getDecodePreview(t, h, "/api/flows/"+strconv.FormatInt(id, 10)+"/decoded?side=res")
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("status %d body %s: %v", rec.Code, rec.Body.String(), err)
		}
		return out
	}
	if out := decoded(1024); out["matched"] != true || out["plaintext"] != "1024" || out["inputTruncated"] != nil {
		t.Fatalf("small body: %v", out)
	}
	if out := decoded(decodeMax + 5); out["plaintext"] != strconv.Itoa(decodeMax) || out["inputTruncated"] != true {
		t.Fatalf("oversized body: plaintext=%v inputTruncated=%v error=%v", out["plaintext"], out["inputTruncated"], out["error"])
	}
}
