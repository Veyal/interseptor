package control

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// The copy/view flow preview can mask credential headers so a screenshot pasted
// into a ticket does not leak a bearer token. Masking changes the rendered image
// only when a credential header is present, and never when none is.
func TestFlowPreviewMasksCredentialHeadersOnlyWhenAsked(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	withCred, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: "https", Host: "api.example.com", Path: "/v1/orders/1042", Status: 200,
		HTTPVersion: "HTTP/1.1",
		ReqHeaders:  map[string][]string{"Host": {"api.example.com"}, "Authorization": {"Bearer demo-token-abcdefghijklmnop"}},
		ResHeaders:  map[string][]string{"Content-Type": {"application/json"}},
	})
	if err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	noCred, err := st.InsertFlow(&store.Flow{
		Method: "GET", Scheme: "https", Host: "cdn.example.com", Path: "/app.css", Status: 200,
		HTTPVersion: "HTTP/1.1",
		ReqHeaders:  map[string][]string{"Host": {"cdn.example.com"}},
		ResHeaders:  map[string][]string{"Content-Type": {"text/css"}},
	})
	if err != nil {
		t.Fatalf("insert flow: %v", err)
	}

	get := func(id int64, mask string) []byte {
		u := ts.URL + "/api/flows/" + itoa(id) + "/preview.png?side=req"
		if mask != "" {
			u += "&maskHeaders=" + mask
		}
		resp, err := http.Get(u)
		if err != nil {
			t.Fatalf("GET preview: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("preview status = %d", resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		if len(b) == 0 {
			t.Fatal("empty preview")
		}
		return b
	}

	if bytes.Equal(get(withCred, "0"), get(withCred, "1")) {
		t.Error("maskHeaders=1 produced the same image as maskHeaders=0 for a flow carrying Authorization")
	}
	if !bytes.Equal(get(noCred, "0"), get(noCred, "1")) {
		t.Error("maskHeaders changed the image for a flow with no credential header")
	}
}
