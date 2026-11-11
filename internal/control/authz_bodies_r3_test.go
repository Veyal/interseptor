package control

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

const r3BigBody = 64 << 20

func r3AllocDuring(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func r3StoreBody(t *testing.T, st *store.Store, chunk []byte, times int) string {
	t.Helper()
	bw, err := st.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < times; i++ {
		if _, err := bw.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	hash, _, err := bw.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

// An authorization replay hashes the response body to compare identities. The
// hash must be computed from the stream: holding a 64 MiB response in memory
// per identity (several times over while it grows) is what this pins.
func TestAuthzReplayHashesTheResponseWithoutBufferingIt(t *testing.T) {
	chunk := bytes.Repeat([]byte("0123456789abcdef"), 1<<12) // 64 KiB
	want := sha256.New()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(r3BigBody))
		for i := 0; i < r3BigBody/len(chunk); i++ {
			_, _ = w.Write(chunk)
		}
	}))
	defer target.Close()
	for i := 0; i < r3BigBody/len(chunk); i++ {
		want.Write(chunk)
	}
	u, _ := url.Parse(target.URL)
	port, _ := strconv.Atoi(u.Port())
	h, _, _ := newHub(t)
	var rr authzResult
	alloc := r3AllocDuring(func() {
		rr = (&authzAPI{h}).authzReplay(&store.Flow{Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/big"}, identity{Name: "user"}, 0)
	})
	if rr.Error != "" || rr.Status != 200 || rr.Length != r3BigBody {
		t.Fatalf("replay: %+v", rr)
	}
	if got, exp := rr.BodyHash, hex.EncodeToString(want.Sum(nil)[:8]); got != exp {
		t.Fatalf("body hash = %q, want %q", got, exp)
	}
	t.Logf("allocated %.1f MiB replaying a %d MiB response", float64(alloc)/(1<<20), r3BigBody>>20)
	if alloc > 16<<20 {
		t.Errorf("replay allocated %.1f MiB for a %d MiB response", float64(alloc)/(1<<20), r3BigBody>>20)
	}
}

// The compared hash is of the decoded content, so a gzip replay and an
// identity replay of the same payload still match.
func TestAuthzReplayHashIsOfTheDecodedBody(t *testing.T) {
	payload := strings.Repeat(`{"id":1,"name":"example"}`, 2000)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Variant") == "gzip" {
			w.Header().Set("Content-Encoding", "gzip")
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			_, _ = zw.Write([]byte(payload))
			_ = zw.Close()
			_, _ = w.Write(buf.Bytes())
			return
		}
		_, _ = w.Write([]byte(payload))
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	port, _ := strconv.Atoi(u.Port())
	h, _, _ := newHub(t)
	f := &store.Flow{Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/p"}
	plain := (&authzAPI{h}).authzReplay(f, identity{Name: "a"}, 0)
	zipped := (&authzAPI{h}).authzReplay(f, identity{Name: "b", Headers: "X-Variant: gzip"}, 0)
	sum := sha256.Sum256([]byte(payload))
	if plain.Error != "" || zipped.Error != "" || plain.BodyHash != hex.EncodeToString(sum[:8]) || zipped.BodyHash != plain.BodyHash {
		t.Fatalf("plain %+v\nzipped %+v", plain, zipped)
	}
}

// The JWT source flow of a cross-host replay may hold bodies of any size. A JWT
// sits in a header or a small JSON document, so a 64 MiB body must not be read
// whole and unmarshalled to look for one.
func TestAuthzCrossHostReplayBoundsTheJWTSourceBodies(t *testing.T) {
	h, st, _ := newHub(t)
	chunk := bytes.Repeat([]byte("a"), 1<<16)
	bw, err := st.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = bw.Write([]byte(`{"pad":"`))
	for i := 0; i < r3BigBody/len(chunk); i++ {
		_, _ = bw.Write(chunk)
	}
	_, _ = bw.Write([]byte(`"}`))
	big, n, err := bw.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	flowID, err := st.InsertFlow(&store.Flow{Method: "POST", Scheme: "https", Host: "example.com", Port: 443, Path: "/login",
		ReqBodyHash: big, ReqLen: n, ResBodyHash: big, ResLen: n})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	alloc := r3AllocDuring(func() {
		req := httptest.NewRequest(http.MethodPost, "/api/authz/cross-host-replay", strings.NewReader(`{"flowId":`+itoa(flowID)+`}`))
		(&authzAPI{h}).authzCrossHostReplay(rec, req)
	})
	t.Logf("allocated %.1f MiB searching two %d MiB bodies for a JWT (status %d: %s)", float64(alloc)/(1<<20), r3BigBody>>20, rec.Code, strings.TrimSpace(rec.Body.String()))
	// two bounded reads of 1 MiB each, grown by io.ReadAll (about 4 MiB in
	// total, twice that under the race detector); before the bound: 699 MiB
	if alloc > 16<<20 {
		t.Errorf("allocated %.1f MiB to look for a JWT", float64(alloc)/(1<<20))
	}
}

func TestAuthzCrossHostReplaySaysWhenABodyWasTooLargeToSearch(t *testing.T) {
	h, st, _ := newHub(t)
	big := r3StoreBody(t, st, bytes.Repeat([]byte("a"), 1<<16), 40) // 2.5 MiB, over the search bound
	flowID, err := st.InsertFlow(&store.Flow{Method: "GET", Scheme: "https", Host: "example.com", Port: 443, Path: "/x", ResBodyHash: big, ResLen: 40 << 16})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/authz/cross-host-replay", strings.NewReader(`{"flowId":`+itoa(flowID)+`}`))
	(&authzAPI{h}).authzCrossHostReplay(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not searched") {
		t.Fatalf("status=%d body=%q, want 400 naming the body that was not searched", rec.Code, rec.Body.String())
	}
}

// A token in a small JSON body is still found, and a bearer header never needs
// the bodies at all (a missing blob does not get in its way).
func TestAuthzCrossHostReplayStillFindsTheJWT(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.c2lnbmF0dXJlLXZhbHVl"
	var got []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	port, _ := strconv.Atoi(u.Port())
	h, st, _ := newHub(t)
	body := r3StoreBody(t, st, []byte(`{"access_token":"`+jwt+`"}`), 1)
	ref, err := st.InsertFlow(&store.Flow{Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/me"})
	if err != nil {
		t.Fatal(err)
	}
	fromBody, _ := st.InsertFlow(&store.Flow{Method: "POST", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/login", ResBodyHash: body, ResLen: 80})
	fromHeader, _ := st.InsertFlow(&store.Flow{Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/a",
		ReqHeaders: map[string][]string{"Authorization": {"Bearer " + jwt}}, ResBodyHash: strings.Repeat("a", 64), ResLen: 9})
	for _, src := range []int64{fromBody, fromHeader} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/authz/cross-host-replay", strings.NewReader(`{"flowId":`+itoa(ref)+`,"jwtFlowId":`+itoa(src)+`}`))
		(&authzAPI{h}).authzCrossHostReplay(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("source %d: status=%d body=%q", src, rec.Code, rec.Body.String())
		}
	}
	if len(got) != 2 || got[0] != "Bearer "+jwt || got[1] != "Bearer "+jwt {
		t.Fatalf("replayed Authorization = %q", got)
	}
}

// A login response is usually compressed on the wire and stored that way. The
// token inside it must still be found.
func TestAuthzCrossHostReplayFindsTheJWTInACompressedResponse(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.c2lnbmF0dXJlLXZhbHVl"
	var got []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	port, _ := strconv.Atoi(u.Port())
	h, st, _ := newHub(t)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(`{"access_token":"` + jwt + `"}`))
	_ = zw.Close()
	body := r3StoreBody(t, st, buf.Bytes(), 1)
	src, err := st.InsertFlow(&store.Flow{Method: "POST", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/login",
		ResHeaders: map[string][]string{"Content-Encoding": {"gzip"}, "Content-Type": {"application/json"}}, ResBodyHash: body, ResLen: int64(buf.Len())})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/authz/cross-host-replay", strings.NewReader(`{"flowId":`+itoa(src)+`}`))
	(&authzAPI{h}).authzCrossHostReplay(rec, req)
	if rec.Code != http.StatusOK || len(got) != 1 || got[0] != "Bearer "+jwt {
		t.Fatalf("status=%d body=%q replayed=%q", rec.Code, rec.Body.String(), got)
	}
}
