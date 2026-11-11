package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A masked flow image masks secret query values on the request line. The image
// title and the default caption of an attached preview carry the same path, so
// they must not show the token either.
func TestFlowPreviewMaskCoversTitleAndDefaultCaption(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	const canary = "CANARYquerytoken0123456789"
	flowID, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: "https", Host: "api.example.com", Path: "/cb?access_token=" + canary + "&page=2", Status: 200,
		HTTPVersion: "HTTP/1.1",
		ReqHeaders:  map[string][]string{"Host": {"api.example.com"}},
		ResHeaders:  map[string][]string{"Content-Type": {"text/plain"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	get := func(mask string) []byte {
		// side=res: the request line is not drawn, only the title names the path.
		resp, err := http.Get(ts.URL + "/api/flows/" + itoa(flowID) + "/preview.png?side=res&maskHeaders=" + mask)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || len(b) == 0 {
			t.Fatalf("preview status = %d", resp.StatusCode)
		}
		return b
	}
	if bytes.Equal(get("0"), get("1")) {
		t.Error("maskHeaders=1 drew the same title as maskHeaders=0 for a path carrying a query secret")
	}

	findingID, err := st.CreateFinding(&store.Finding{Title: "token in URL", Severity: "Low"})
	if err != nil {
		t.Fatal(err)
	}
	attach := func(body string) string {
		resp, err := http.Post(ts.URL+"/api/findings/"+itoa(findingID)+"/flow-preview", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("attach status = %d: %s", resp.StatusCode, b)
		}
		var out struct {
			Blocks []store.FindingBlock `json:"blocks"`
		}
		if err := json.Unmarshal(b, &out); err != nil || len(out.Blocks) == 0 {
			t.Fatalf("attach response: %v %s", err, b)
		}
		return out.Blocks[len(out.Blocks)-1].Caption
	}
	// Masking is the attach default: the stored caption becomes report text.
	if caption := attach(`{"flowId":` + itoa(flowID) + `}`); strings.Contains(caption, canary) || !strings.Contains(caption, "page=2") {
		t.Errorf("default caption of a masked preview = %q", caption)
	}
	// An explicit opt-out keeps the caption as captured.
	if caption := attach(`{"flowId":` + itoa(flowID) + `,"mask":false}`); !strings.Contains(caption, canary) {
		t.Errorf("unmasked preview caption = %q, want the captured path", caption)
	}
}
