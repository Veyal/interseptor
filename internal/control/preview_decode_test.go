package control

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/msgcodec"
	"github.com/Veyal/interseptor/internal/store"
)

func TestDecodeFormBody(t *testing.T) {
	got, ok := decodeFormBody([]byte("name=Ada+Lovelace&email=ada%40example.com&empty=&flag&n=1%262"), "application/x-www-form-urlencoded; charset=utf-8", true)
	if !ok {
		t.Fatal("form with content-type should decode")
	}
	want := "name = Ada Lovelace\nemail = ada@example.com\nempty = \nflag = \nn = 1&2"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// Without a content-type only a clean request body qualifies.
	if _, ok := decodeFormBody([]byte("a=1&b=two"), "", true); !ok {
		t.Fatal("clean k=v&k=v request body should decode without content-type")
	}
	for _, body := range []string{"a=1&b=two", "token=abc", ""} {
		if _, ok := decodeFormBody([]byte(body), "", false); ok {
			t.Fatalf("response %q must need a content-type", body)
		}
	}
	for _, body := range []string{`{"a":"b=c"}`, "hello world=1", "no pairs here", "a=1&&", "QUJDREVGR0hJSktMTU5PUA==", "x=1\ny=2", "=v&=w"} {
		if _, ok := decodeFormBody([]byte(body), "", true); ok {
			t.Fatalf("%q must not be treated as form without content-type", body)
		}
	}
	if _, ok := decodeFormBody([]byte("a=1"), "application/json", true); ok {
		t.Fatal("explicit non-form content-type wins over sniffing")
	}
	if got, ok := decodeFormBody([]byte("a=%zz&b=%41"), "application/x-www-form-urlencoded", true); !ok || got != "a = %zz\nb = A" {
		t.Fatalf("bad escape kept verbatim: %q %v", got, ok)
	}
}

func TestDecodeBase64Body(t *testing.T) {
	jsonPlain := `{"user":"ada","role":"admin"}`
	for name, enc := range map[string]string{
		"std":        base64.StdEncoding.EncodeToString([]byte(jsonPlain)),
		"rawstd":     base64.RawStdEncoding.EncodeToString([]byte(jsonPlain)),
		"url":        base64.URLEncoding.EncodeToString([]byte(jsonPlain + "??>>")),
		"rawurl":     base64.RawURLEncoding.EncodeToString([]byte(jsonPlain + "??>>")),
		"whitespace": "\n  " + base64.StdEncoding.EncodeToString([]byte(jsonPlain)) + " \r\n",
	} {
		got, ok := decodeBase64Body([]byte(enc))
		if !ok || !strings.HasPrefix(got, `{"user":"ada"`) {
			t.Fatalf("%s: got %q ok=%v", name, got, ok)
		}
	}
	if got, ok := decodeBase64Body([]byte(base64.StdEncoding.EncodeToString([]byte("plain text secret = 42")))); !ok || got != "plain text secret = 42" {
		t.Fatalf("plain text: %q %v", got, ok)
	}
	bad := map[string]string{
		"binary":   base64.StdEncoding.EncodeToString([]byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0x80, 0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0}),
		"short":    base64.StdEncoding.EncodeToString([]byte("hi")),
		"mixed":    "ab+/_-cdefghijklmnop",
		"word":     "internationalization",
		"not b64":  `{"a":1}` + strings.Repeat(" ", 20),
		"empty":    "",
		"digits":   "1234567890123456",
		"inner ws": "QUJDREVG R0hJSktM TU5PUA==",
		"bad pad":  "QUJDREVGR0hJSktMTU5PUA=",
	}
	for name, body := range bad {
		if got, ok := decodeBase64Body([]byte(body)); ok {
			t.Fatalf("%s must stay raw, decoded to %q", name, got)
		}
	}
}

const testPreviewCodec = `
meta = {"id": "xor-lite", "title": "Example Envelope"}

def match(flow, side):
    raw = flow.req_body if side == "req" else flow.res_body
    return flow.host == "example.com" and raw.startswith("ENC:")

def decode(flow, side, raw):
    return {"plaintext": raw[4:], "fields": {}, "note": ""}
`

const testBrokenCodec = `
meta = {"id": "broken", "title": "Broken Example"}

def match(flow, side):
    return side == "res"

def decode(flow, side, raw):
    fail("cannot decrypt example payload")
`

func saveTestCodec(t *testing.T, h *Hub, id, src string) {
	t.Helper()
	h.ProjectDir = t.TempDir()
	if err := msgcodec.Save(h.CodecsDir(), id, src); err != nil {
		t.Fatal(err)
	}
}

func insertDecodeFlow(t *testing.T, st *store.Store, h *Hub, reqCT string, reqBody, resBody string) *store.Flow {
	t.Helper()
	hdr := map[string][]string{}
	if reqCT != "" {
		hdr["Content-Type"] = []string{reqCT}
	}
	f := &store.Flow{
		Method: "POST", Scheme: "https", Host: "example.com", Path: "/api/login", Status: 200, HTTPVersion: "HTTP/1.1",
		ReqHeaders: hdr, ResHeaders: map[string][]string{"Content-Type": {"text/plain"}},
	}
	if reqBody != "" {
		f.ReqBodyHash, f.ReqLen = putTestBody(t, st, []byte(reqBody)), int64(len(reqBody))
	}
	if resBody != "" {
		f.ResBodyHash, f.ResLen = putTestBody(t, st, []byte(resBody)), int64(len(resBody))
	}
	id, err := st.InsertFlow(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetFlow(id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPreviewSideDecodeCodecThenFallbacks(t *testing.T) {
	h, st, _ := newHub(t)
	saveTestCodec(t, h, "xor-lite", testPreviewCodec)
	f := insertDecodeFlow(t, st, h, "", `ENC:{"role":"admin","id":7}`, base64.StdEncoding.EncodeToString([]byte(`{"ok":true,"user":"ada"}`)))

	d := h.newPreviewDecoder(f, decodeAuto)
	raw, _, got, err := h.flowSideForPreview(f, true, d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `{"role":"admin","id":7}`) || strings.Contains(string(raw), "ENC:") {
		t.Fatalf("codec plaintext expected:\n%s", raw)
	}
	if got.Tag != "codec:xor-lite" || got.Notice != "Request decoded: codec “Example Envelope”" {
		t.Fatalf("decode info = %+v", got)
	}
	// Request line and headers stay as captured.
	if !strings.HasPrefix(string(raw), "POST /api/login HTTP/1.1\r\n") {
		t.Fatalf("request line changed:\n%s", raw)
	}

	// Response: no codec matches, falls to base64.
	raw, _, got, err = h.flowSideForPreview(f, false, d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `{"ok":true,"user":"ada"}`) || got.Tag != "base64" || got.Notice != "Response decoded: base64" {
		t.Fatalf("base64 fallback: %+v\n%s", got, raw)
	}
}

func TestPreviewSideDecodeCodecErrorFallsBackWithNote(t *testing.T) {
	h, st, _ := newHub(t)
	saveTestCodec(t, h, "broken", testBrokenCodec)
	b64 := base64.StdEncoding.EncodeToString([]byte(`{"secret":"example"}`))
	f := insertDecodeFlow(t, st, h, "", "", b64)
	raw, _, got, err := h.flowSideForPreview(f, false, h.newPreviewDecoder(f, decodeAuto))
	if err != nil {
		t.Fatal(err)
	}
	if got.Tag != "base64" || !strings.Contains(string(raw), `{"secret":"example"}`) {
		t.Fatalf("expected base64 fallback: %+v\n%s", got, raw)
	}
	if !strings.Contains(got.Notice, "codec “Broken Example” failed") || !strings.Contains(got.Notice, "cannot decrypt") || !strings.Contains(got.Notice, "base64") {
		t.Fatalf("notice should name the codec error and the fallback: %q", got.Notice)
	}
}

func TestPreviewSideDecodeFormAndRawMode(t *testing.T) {
	h, st, _ := newHub(t)
	f := insertDecodeFlow(t, st, h, "application/x-www-form-urlencoded", "user=ada%40example.com&pass=p+w", "")
	raw, _, got, err := h.flowSideForPreview(f, true, h.newPreviewDecoder(f, decodeAuto))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\r\n\r\nuser = ada@example.com\npass = p w") || got.Tag != "form" || got.Notice != "Request decoded: form-urlencoded" {
		t.Fatalf("form: %+v\n%q", got, raw)
	}
	// Content-Length stays as captured: headers are not rewritten.
	f.ReqHeaders["Content-Length"] = []string{"31"}
	raw, _, _, _ = h.flowSideForPreview(f, true, h.newPreviewDecoder(f, decodeAuto))
	if !strings.Contains(string(raw), "Content-Length: 31\r\n") {
		t.Fatalf("Content-Length rewritten:\n%s", raw)
	}

	raw, size, got, err := h.flowSideForPreview(f, true, h.newPreviewDecoder(f, decodeRaw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "user=ada%40example.com&pass=p+w") || got.Tag != "" || got.Notice != "" || size != int64(len("user=ada%40example.com&pass=p+w")) {
		t.Fatalf("raw mode must leave the body: %+v size=%d\n%q", got, size, raw)
	}
}

func TestPreviewSideDecodeLeavesBase64LookingBinaryRaw(t *testing.T) {
	h, st, _ := newHub(t)
	b64 := base64.StdEncoding.EncodeToString([]byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0x80, 0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0})
	f := insertDecodeFlow(t, st, h, "", "", b64)
	raw, _, got, err := h.flowSideForPreview(f, false, h.newPreviewDecoder(f, decodeAuto))
	if err != nil {
		t.Fatal(err)
	}
	if got.Tag != "" || !strings.HasSuffix(string(raw), b64) {
		t.Fatalf("binary base64 should stay raw: %+v", got)
	}
}

func TestPreviewDecodedBodyKeepsCutAndSizeSemantics(t *testing.T) {
	h, st, _ := newHub(t)
	saveTestCodec(t, h, "xor-lite", testPreviewCodec)
	// Plaintext beyond the 256 KiB read limit: cut to 64 KiB with the full size reported.
	plain := strings.Repeat("line of example text\n", 15000)
	f := insertDecodeFlow(t, st, h, "", "ENC:"+plain, "")
	raw, size, got, err := h.flowSideForPreview(f, true, h.newPreviewDecoder(f, decodeAuto))
	if err != nil {
		t.Fatal(err)
	}
	if got.Tag != "codec:xor-lite" || size != int64(len(plain)) || len(raw) > previewBodyCap+1024 {
		t.Fatalf("tag=%q size=%d (want %d) rawlen=%d", got.Tag, size, len(plain), len(raw))
	}

	// Decoded text under the read limit is passed whole so JSON pretty-prints before the cut.
	mid := strings.Repeat("line of example text\n", 9000)
	f = insertDecodeFlow(t, st, h, "", "", base64.StdEncoding.EncodeToString([]byte(mid)))
	raw, size, got, err = h.flowSideForPreview(f, false, h.newPreviewDecoder(f, decodeAuto))
	if err != nil {
		t.Fatal(err)
	}
	if got.Tag != "base64" || size != int64(len(mid)) || len(raw) < len(mid) {
		t.Fatalf("tag=%q size=%d rawlen=%d", got.Tag, size, len(raw))
	}
}

func TestPreviewSkipsDecodeForTruncatedBody(t *testing.T) {
	h, st, _ := newHub(t)
	body := strings.Repeat("QUJD", previewPrettyLimit/4+10)
	f := insertDecodeFlow(t, st, h, "", "", body)
	_, _, got, err := h.flowSideForPreview(f, false, h.newPreviewDecoder(f, decodeAuto))
	if err != nil {
		t.Fatal(err)
	}
	if got.Tag != "" {
		t.Fatalf("a body cut at the read limit must not be decoded: %+v", got)
	}
}

func getDecodePreview(t *testing.T, h *Hub, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:9966"
	h.Handler().ServeHTTP(rec, req)
	return rec
}

func TestFlowPreviewDecodeParamAndHeader(t *testing.T) {
	h, st, _ := newHub(t)
	saveTestCodec(t, h, "xor-lite", testPreviewCodec)
	f := insertDecodeFlow(t, st, h, "", `ENC:{"role":"admin"}`, base64.StdEncoding.EncodeToString([]byte(`{"ok":true,"token":"abc"}`)))
	base := "/api/flows/" + strconv.FormatInt(f.ID, 10) + "/preview.png"

	rec := getDecodePreview(t, h, base)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Interseptor-Decoded-Body"); got != "req=codec:xor-lite,res=base64" {
		t.Fatalf("header = %q", got)
	}

	// Focus matches the decoded text, not the raw bytes.
	rec = getDecodePreview(t, h, base+"?focus=%22token%22%3A+%22abc%22&focusSide=res")
	if got := rec.Header().Get("X-Interseptor-Focus-Matches"); got != "req=0,res=1" {
		t.Fatalf("decoded focus matches = %q", got)
	}
	// With decode=raw the same text lives nowhere, no decoded header.
	rec = getDecodePreview(t, h, base+"?decode=raw&focus=%22token%22%3A+%22abc%22&focusSide=res")
	if got := rec.Header().Get("X-Interseptor-Focus-Matches"); got != "req=0,res=0" {
		t.Fatalf("raw focus matches = %q", got)
	}
	if rec.Header().Get("X-Interseptor-Decoded-Body") != "" {
		t.Fatal("decode=raw must not report decoding")
	}

	// A flow with nothing decodable omits the header.
	plain := insertDecodeFlow(t, st, h, "", "", "hello example")
	rec = getDecodePreview(t, h, "/api/flows/"+strconv.FormatInt(plain.ID, 10)+"/preview.png")
	if rec.Code != http.StatusOK || rec.Header().Get("X-Interseptor-Decoded-Body") != "" {
		t.Fatalf("status %d header %q", rec.Code, rec.Header().Get("X-Interseptor-Decoded-Body"))
	}

	rec = getDecodePreview(t, h, base+"?decode=rot13")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "decode") {
		t.Fatalf("invalid decode: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFlowPreviewDecodedStillMasksCredentialHeaders(t *testing.T) {
	h, st, _ := newHub(t)
	f := insertDecodeFlow(t, st, h, "application/x-www-form-urlencoded", "a=1", "")
	f.ReqHeaders["Authorization"] = []string{"Bearer example-secret-token"}
	opts, err := previewOptsFromQuery(httptest.NewRequest(http.MethodGet, "/x?side=req&focus=example-secret-token", nil))
	if err != nil {
		t.Fatal(err)
	}
	res, info, err := h.renderFlowPreview(f, opts, true, decodeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if !res.MaskedMatch || info.Header() != "req=form" {
		t.Fatalf("masked=%v header=%q", res.MaskedMatch, info.Header())
	}
}

func TestAttachFlowPreviewAcceptsDecodeAndReportsHeader(t *testing.T) {
	h, st, _ := newHub(t)
	f := insertDecodeFlow(t, st, h, "application/x-www-form-urlencoded", "user=ada&role=admin", "")
	fid, err := st.CreateFinding(&store.Finding{Title: "Example finding"})
	if err != nil {
		t.Fatal(err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/findings/"+strconv.FormatInt(fid, 10)+"/flow-preview", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:50000"
		req.Host = "127.0.0.1:9966"
		h.Handler().ServeHTTP(rec, req)
		return rec
	}
	rec := post(`{"flowId":` + strconv.FormatInt(f.ID, 10) + `,"side":"req","focus":"role = admin"}`)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Interseptor-Decoded-Body") != "req=form" {
		t.Fatalf("auto attach: %d %q %s", rec.Code, rec.Header().Get("X-Interseptor-Decoded-Body"), rec.Body.String())
	}
	rec = post(`{"flowId":` + strconv.FormatInt(f.ID, 10) + `,"side":"req","decode":"raw","focus":"role = admin"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("raw body has no 'role = admin' line, want 422 got %d %s", rec.Code, rec.Body.String())
	}
	rec = post(`{"flowId":` + strconv.FormatInt(f.ID, 10) + `,"decode":"nope"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid decode want 400 got %d", rec.Code)
	}
}
