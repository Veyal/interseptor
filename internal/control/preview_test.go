package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/preview"
	"github.com/Veyal/interseptor/internal/store"
)

func TestFlowPreviewPNG(t *testing.T) {
	h, s, _ := newHub(t)
	flowID, err := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/api/user", Status: 200,
		ReqHeaders: map[string][]string{"Host": {"example.com"}},
		ResHeaders: map[string][]string{"Content-Type": {"application/json"}},
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/flows/" + strconv.FormatInt(flowID, 10) + "/preview.png?side=both")
	if err != nil {
		t.Fatalf("GET preview: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q", ct)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if len(raw) < 200 {
		t.Fatalf("PNG too small: %d", len(raw))
	}
}

func TestFlowPreviewPNGOptions(t *testing.T) {
	h, s, _ := newHub(t)
	flowID, err := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/api/user", Status: 200,
		ReqHeaders: map[string][]string{"Host": {"example.com"}},
		ResHeaders: map[string][]string{"Content-Type": {"application/json"}},
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	base := ts.URL + "/api/flows/" + strconv.FormatInt(flowID, 10) + "/preview.png"
	cases := []string{
		"?side=both&pretty=1&layout=horizontal&theme=light",
		"?side=both&pretty=0&layout=vertical&theme=dark",
	}
	var widths []int
	for _, q := range cases {
		resp, err := http.Get(base + q)
		if err != nil {
			t.Fatalf("GET %s: %v", q, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status %d: %s", q, resp.StatusCode, raw)
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s decode: %v", q, err)
		}
		widths = append(widths, img.Bounds().Dx())
	}
	if widths[0] <= widths[1] {
		t.Fatalf("horizontal+pretty width %d should exceed vertical %d", widths[0], widths[1])
	}
}

func TestAttachFindingFlowPreviewWithOptions(t *testing.T) {
	h, s, _ := newHub(t)
	flowID, _ := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Host: "example.com", Path: "/login", Status: 302,
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/api/findings", "application/json",
		strings.NewReader(`{"title":"auth bypass","detail":"intro"}`))
	var created store.Finding
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	payload := fmt.Sprintf(`{"flowId":%d,"side":"both","pretty":true,"layout":"horizontal","theme":"light","caption":"login PoC","position":1}`, flowID)
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/api/findings/"+strconv.FormatInt(created.ID, 10)+"/flow-preview",
		strings.NewReader(payload))
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("flow-preview: %v", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(r.Body)
		t.Fatalf("status %d: %s", r.StatusCode, body)
	}
	var out store.Finding
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range out.Blocks {
		if b.Type == "image" && b.Caption == "login PoC" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no image block: %+v", out.Blocks)
	}
}

func TestAttachFindingFlowPreviewRejectsTrailingJSONBeforeAttachment(t *testing.T) {
	h, st, _ := newHub(t)
	flowID, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/account", Status: 200,
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	findingID, err := st.CreateFinding(&store.Finding{Title: "finding"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	payload := fmt.Sprintf(`{"flowId":%d,"caption":"preview"}{}`, flowID)
	resp, err := http.Post(ts.URL+"/api/findings/"+strconv.FormatInt(findingID, 10)+"/flow-preview",
		"application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST flow preview: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	got, err := st.GetFinding(findingID)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	for _, block := range got.Blocks {
		if block.Type == "image" {
			t.Fatalf("preview attached after rejected command: %+v", block)
		}
	}
}

func TestAttachFindingFlowPreview(t *testing.T) {
	h, s, _ := newHub(t)
	flowID, _ := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Host: "example.com", Path: "/login", Status: 302,
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/api/findings", "application/json",
		strings.NewReader(`{"title":"auth bypass","detail":"intro"}`))
	var created store.Finding
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	payload := fmt.Sprintf(`{"flowId":%d,"side":"both","caption":"login PoC","position":1}`, flowID)
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/api/findings/"+strconv.FormatInt(created.ID, 10)+"/flow-preview",
		strings.NewReader(payload))
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("flow-preview: %v", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(r.Body)
		t.Fatalf("status %d: %s", r.StatusCode, body)
	}
	var out store.Finding
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, b := range out.Blocks {
		if b.Type == "image" && b.Caption == "login PoC" && b.Hash != "" && !b.Missing {
			found = true
			if b.Mime != "image/png" {
				t.Fatalf("mime = %q", b.Mime)
			}
			// Serve the attached image.
			imgResp, err := http.Get(ts.URL + b.URL)
			if err != nil {
				t.Fatal(err)
			}
			imgBody, _ := io.ReadAll(imgResp.Body)
			imgResp.Body.Close()
			if imgResp.StatusCode != http.StatusOK {
				t.Fatalf("GET image %d", imgResp.StatusCode)
			}
			if _, err := png.Decode(bytes.NewReader(imgBody)); err != nil {
				t.Fatalf("attached png: %v", err)
			}
			if len(imgBody) > preview.MaxPNGBytes {
				t.Fatalf("oversized PNG")
			}
		}
	}
	if !found {
		t.Fatalf("no image block: %+v", out.Blocks)
	}
}

func TestHTMLReportEmbedsFindingImagesAsDataURI(t *testing.T) {
	h, s, _ := newHub(t)
	flowID, _ := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/x", Status: 200,
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/api/findings", "application/json",
		strings.NewReader(`{"title":"embed test","detail":"intro"}`))
	var created store.Finding
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	payload := fmt.Sprintf(`{"flowId":%d,"side":"req","caption":"req shot"}`, flowID)
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/api/findings/"+strconv.FormatInt(created.ID, 10)+"/flow-preview",
		strings.NewReader(payload))
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("attach status %d", r.StatusCode)
	}

	htmlResp, err := http.Get(ts.URL + "/api/findings/report?format=html")
	if err != nil {
		t.Fatal(err)
	}
	defer htmlResp.Body.Close()
	body, _ := io.ReadAll(htmlResp.Body)
	html := string(body)
	if !strings.Contains(html, `src="data:image/png;base64,`) {
		t.Fatalf("expected data URI image in HTML report, got:\n%.500s…", html)
	}
	if strings.Contains(html, `/api/findings/images/`) {
		t.Fatalf("HTML report should not keep API image URLs when blob is embeddable")
	}
}
