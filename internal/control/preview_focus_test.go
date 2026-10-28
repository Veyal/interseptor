package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// focusFlow stores a flow whose JSON response is ~360 pretty lines with one
// admin flag around line 240, past the 80-line top-mode cut.
func focusFlow(t *testing.T, st *store.Store) int64 {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"users":[`)
	for i := 0; i < 70; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"user%d","isAdmin":%t}`, i, i, i == 47)
	}
	b.WriteString(`]}`)
	hash := putTestBody(t, st, []byte(b.String()))
	id, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: "https", Host: "api.example.com", Path: "/v1/users", Status: 200, HTTPVersion: "HTTP/1.1",
		ReqHeaders: map[string][]string{"Host": {"api.example.com"}, "Authorization": {"Bearer demo-token-abcdefghijklmnop"}},
		ResHeaders: map[string][]string{"Content-Type": {"application/json"}}, ResBodyHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func getPreview(t *testing.T, ts *httptest.Server, id int64, query string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/flows/" + itoa(id) + "/preview.png?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func TestFlowPreviewFocusReportsMatchesInHeaders(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	id := focusFlow(t, st)

	q := url.Values{"focus": {`"isAdmin":true`}, "caption": {"Highlighted: admin flag"}, "context": {"4"}}
	resp, body := getPreview(t, ts, id, q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if _, err := png.Decode(bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("X-Interseptor-Focus-Matches"); got != "req=0,res=1" {
		t.Fatalf("matches header %q", got)
	}
	if got := resp.Header.Get("X-Interseptor-Focus-Mode"); got != "focus" {
		t.Fatalf("mode header %q", got)
	}
	if resp.Header.Get("X-Interseptor-Focus-Notfound") != "" {
		t.Fatal("unexpected not-found header")
	}

	// focusSide narrows matching; repeated focus values are all reported.
	q = url.Values{"focus": {"isAdmin", "nowhere, really"}, "focusSide": {"res"}, "side": {"res"}}
	resp, body = getPreview(t, ts, id, q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Interseptor-Focus-Matches"); got != "res=70" {
		t.Fatalf("matches header %q", got)
	}
	if got := resp.Header.Get("X-Interseptor-Focus-Notfound"); got != "nowhere%2C%20really" {
		t.Fatalf("not-found header %q", got)
	}
}

func TestFlowPreviewFocusNotFoundFallsBackToTop(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	id := focusFlow(t, st)
	resp, body := getPreview(t, ts, id, "focus=absent-text&lines=res:5000-5010")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Interseptor-Focus-Mode"); got != "top" {
		t.Fatalf("mode %q", got)
	}
	if got := resp.Header.Get("X-Interseptor-Focus-Notfound"); got != "absent-text,res:5000-5010" {
		t.Fatalf("not-found %q", got)
	}
}

func TestFlowPreviewFocusMaskedValueIsReported(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	id := focusFlow(t, st)
	resp, body := getPreview(t, ts, id, "focus=demo-token&maskHeaders=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	// Reported as masked, never echoed back in the not-found list.
	if resp.Header.Get("X-Interseptor-Focus-Masked") != "1" || resp.Header.Get("X-Interseptor-Focus-Notfound") != "" {
		t.Fatalf("masked headers %v", resp.Header)
	}
	resp, _ = getPreview(t, ts, id, "focus=demo-token")
	if got := resp.Header.Get("X-Interseptor-Focus-Matches"); got != "req=1,res=0" {
		t.Fatalf("unmasked preview should match the header value, got %q", got)
	}
}

func TestFlowPreviewFocusRejectsBadParams(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	id := focusFlow(t, st)
	for _, q := range []string{
		"focus=(&regex=1",
		"lines=res:9-3",
		"lines=body:1-2",
		"context=99",
		"context=abc",
		"mode=focus",
		"mode=middle&focus=x",
		"focus=" + strings.Repeat("x", 201),
	} {
		resp, body := getPreview(t, ts, id, q)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d (%s)", q, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "invalid focus") {
			t.Fatalf("%s: unclear error %q", q, body)
		}
	}
}

func TestAttachFlowPreviewStoresFocusedImage(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	id := focusFlow(t, st)
	fid, err := st.CreateFinding(&store.Finding{Title: "Admin flag exposed on /v1/users"})
	if err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"flowId":%d,"focus":["\"isAdmin\":true"],"focusSide":"res","context":3,"role":"result","proof":"the response lists an admin"}`, id)
	resp, err := http.Post(ts.URL+"/api/findings/"+itoa(fid)+"/flow-preview", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Interseptor-Focus-Matches"); got != "req=0,res=1" {
		t.Fatalf("matches header %q", got)
	}
	var out store.Finding
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	var img *store.FindingBlock
	for i := range out.Blocks {
		if out.Blocks[i].Type == "image" {
			img = &out.Blocks[i]
		}
	}
	if img == nil {
		t.Fatalf("no image block: %+v", out.Blocks)
	}
	if img.Source != "flow_preview" || img.SourceFlowID != id || img.SourceRef != fmt.Sprintf("flow:%d:focus:text", id) {
		t.Fatalf("provenance %+v", img)
	}
	if !strings.Contains(img.Caption, `focus: “"isAdmin":true”`) {
		t.Fatalf("caption %q should record the focus", img.Caption)
	}
}

func TestAttachFlowPreviewRejectsBadFocusBeforeStoring(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	id := focusFlow(t, st)
	fid, err := st.CreateFinding(&store.Finding{Title: "Admin flag exposed on /v1/users"})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		fmt.Sprintf(`{"flowId":%d,"focus":"(","regex":true}`, id),
		fmt.Sprintf(`{"flowId":%d,"lines":"res:0-2"}`, id),
		fmt.Sprintf(`{"flowId":%d,"focus":7}`, id),
	} {
		resp, err := http.Post(ts.URL+"/api/findings/"+itoa(fid)+"/flow-preview", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d", body, resp.StatusCode)
		}
	}
	f, err := st.GetFinding(fid)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range f.Blocks {
		if b.Type == "image" {
			t.Fatalf("a rejected request attached an image: %+v", b)
		}
	}
}
