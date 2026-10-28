package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// bigJSONFlow stores a flow whose compact JSON response is ~110 KiB (over the
// 64 KiB image cap, under the 256 KiB pretty-print limit).
func bigJSONFlow(t *testing.T, st *store.Store) int64 {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"users":[`)
	for i := 0; i < 2500; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"user%d","isAdmin":%t}`, i, i, i == 2400)
	}
	b.WriteString(`]}`)
	body := []byte(b.String())
	hash := putTestBody(t, st, body)
	id, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: "https", Host: "api.example.com", Path: "/v1/users", Status: 200, HTTPVersion: "HTTP/1.1",
		ReqHeaders: map[string][]string{"Host": {"api.example.com"}},
		ResHeaders: map[string][]string{"Content-Type": {"application/json"}}, ResBodyHash: hash, ResLen: int64(len(body)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFlowPreviewReportsBodyTruncation(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	id := bigJSONFlow(t, st)

	resp, body := getPreview(t, ts, id, url.Values{"focus": {`"name":"user10"`}}.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Interseptor-Body-Truncated"); got != "res" {
		t.Fatalf("truncated header %q", got)
	}
	if got := resp.Header.Get("X-Interseptor-Focus-Matches"); got != "req=0,res=1" {
		t.Fatalf("matches %q", got)
	}
	// The admin flag lies beyond the cut: not found, still flagged truncated.
	resp, _ = getPreview(t, ts, id, url.Values{"focus": {`"isAdmin":true`}}.Encode())
	if resp.Header.Get("X-Interseptor-Focus-Mode") != "top" || resp.Header.Get("X-Interseptor-Body-Truncated") != "res" {
		t.Fatalf("headers %v", resp.Header)
	}
	// A short flow carries no truncation header.
	small := focusFlow(t, st)
	resp, _ = getPreview(t, ts, small, "")
	if v := resp.Header.Get("X-Interseptor-Body-Truncated"); v != "" {
		t.Fatalf("unexpected truncated header %q", v)
	}
}

func TestFlowPreviewMaskedFocusIsNotEchoed(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	id := focusFlow(t, st)
	resp, _ := getPreview(t, ts, id, "focus=demo-token&maskHeaders=1")
	if resp.Header.Get("X-Interseptor-Focus-Masked") != "1" {
		t.Fatalf("masked header missing: %v", resp.Header)
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			if strings.Contains(v, "demo-token") {
				t.Fatalf("header %s echoes the masked focus text: %q", k, v)
			}
		}
	}
}

func postFlowPreview(t *testing.T, ts *httptest.Server, fid int64, payload string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/api/findings/"+itoa(fid)+"/flow-preview", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func imageBlocks(t *testing.T, st *store.Store, fid int64) []store.FindingBlock {
	t.Helper()
	f, err := st.GetFinding(fid)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.FindingBlock
	for _, b := range f.Blocks {
		if b.Type == "image" {
			out = append(out, b)
		}
	}
	return out
}

func TestAttachFlowPreviewFocusNotFoundIs422WithoutAttaching(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	id := focusFlow(t, st)
	fid, err := st.CreateFinding(&store.Finding{Title: "Admin flag exposed on /v1/users"})
	if err != nil {
		t.Fatal(err)
	}
	resp, body := postFlowPreview(t, ts, fid, fmt.Sprintf(`{"flowId":%d,"focus":"absent-proof","lines":"res:9000-9010"}`, id))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Error    string   `json:"error"`
		NotFound []string `json:"notFound"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Error, "focus not found: ") || fmt.Sprint(out.NotFound) != "[absent-proof res:9000-9010]" {
		t.Fatalf("body %s", body)
	}
	if imgs := imageBlocks(t, st, fid); len(imgs) != 0 {
		t.Fatalf("a not-found focus attached an image: %+v", imgs)
	}

	// Explicit mode=top attaches the first lines without focus provenance.
	resp, body = postFlowPreview(t, ts, fid, fmt.Sprintf(`{"flowId":%d,"focus":"absent-proof","mode":"top"}`, id))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mode=top status %d: %s", resp.StatusCode, body)
	}
	imgs := imageBlocks(t, st, fid)
	if len(imgs) != 1 || imgs[0].SourceRef != "" || strings.Contains(imgs[0].Caption, "focus:") {
		t.Fatalf("mode=top image %+v", imgs)
	}
}

func TestAttachOptionsDrawsExplicitCaptionWithoutFocus(t *testing.T) {
	in := flowPreviewAttachInput{FlowID: 1, Caption: "Response to the replayed request"}
	opts, focused, err := in.attachOptions()
	if err != nil || focused {
		t.Fatalf("focused=%v err=%v", focused, err)
	}
	if opts.Caption != "Response to the replayed request" {
		t.Fatalf("an explicit caption must be drawn on an unfocused image too, got %q", opts.Caption)
	}
}
