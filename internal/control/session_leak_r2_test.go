package control

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// The auth timeline and the session inspection promise fingerprints only. No
// spelling of a Cookie / Set-Cookie header, well-formed or not, may put a
// cookie value into either response.
func TestSessionViewsNeverReturnCookieValues(t *testing.T) {
	h, st, _ := newHub(t)
	var canaries []string
	c := func() string {
		v := fmt.Sprintf("CANARYval%02dxyz", len(canaries))
		canaries = append(canaries, v)
		return v
	}
	base := time.Now().Add(-time.Minute)
	var ids []string
	add := func(i int, path string, status int, req, res map[string][]string) {
		id, err := st.InsertFlow(&store.Flow{TS: base.Add(time.Duration(i) * time.Second), Method: "POST", Scheme: "https", Host: "app.example.com",
			Path: path, Status: status, ClientAddr: "127.0.0.1:5000", ReqHeaders: req, ResHeaders: res})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, fmt.Sprint(id))
	}
	sess := c()
	add(0, "/login", 302, map[string][]string{"Cookie": {"pre=" + c() + "; XSRF-TOKEN=" + c()}, "X-XSRF-TOKEN": {c()}},
		map[string][]string{
			"Set-Cookie": {
				"JSESSIONID=" + sess + "; Path=/; HttpOnly",
				"sessionid=" + c() + "; Path=/; Secure; SameSite=None",
				`quoted="` + c() + `"; Path=/`,
				"__Host-auth=" + c() + "; Domain=other.example.com",
				"__Secure-sid=" + c(),
				"badsession=" + c() + " with space; Path=/",
				"token=" + c() + "; Max-Age=notanumber; Expires=garbage",
				"auth_token=" + c() + "; Domain=.evil.example.net",
				"dup=" + c() + "; Path=/a", "dup=" + c() + "; Path=/b",
			},
			"set-cookie": {"lowersession=" + c() + "; path=/"},
			// Servlet URL rewriting repeats the session cookie's value in the URL.
			"Location": {"https://user:" + c() + "@app.example.com/home;jsessionid=" + sess + "?next=" + c() + "#" + c()},
		})
	add(1, "/home;jsessionid="+sess+"?token="+c(), 200,
		map[string][]string{"cookie": {"JSESSIONID=" + c() + ";sessionid=" + c() + "; lowersession=" + c() + "; =" + c() + "; dup=" + c()},
			"Authorization": {"Bearer " + c()}},
		map[string][]string{"Set-Cookie": {"JSESSIONID=" + c() + "; Path=/", "sessionid=; Max-Age=0", "XSRF-TOKEN=" + c()}, "Location": {"/a/b;JSESSIONID=" + sess + ";v=" + c() + "/c"}})
	add(2, "/mfa/otp", 401, map[string][]string{"Cookie": {"JSESSIONID=" + c()}},
		map[string][]string{"Set-Cookie": {"mfa_otp=" + c()}, "Www-Authenticate": {"Bearer"}, "Location": {"%zz;jsessionid=" + c()}})

	for _, path := range []string{
		"/api/flows/" + ids[0] + "/auth-timeline",
		"/api/flows/" + ids[0] + "/auth-timeline?max=100&windowSeconds=600",
		"/api/flows/session-inspect?ids=" + strings.Join(ids, ","),
		"/api/flows/session-inspect?ids=" + strings.Join(ids, ",") + "&roles=anonymous,user,admin",
	} {
		rec := getDecodePreview(t, h, path)
		if rec.Code != 200 {
			t.Fatalf("%s -> %d %s", path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, v := range canaries {
			if strings.Contains(body, v) {
				i := strings.Index(body, v)
				t.Errorf("%s returned the secret %s: …%s…", path, v, body[max(0, i-80):min(len(body), i+len(v)+20)])
			}
		}
		if !strings.Contains(body, "JSESSIONID") || !strings.Contains(body, shortFingerprint(sess)) {
			t.Errorf("%s: the session cookie is not reported by name and fingerprint", path)
		}
	}
}

// Path parameters keep their names; ordinary paths are returned unchanged.
func TestSafeFlowPathMasksPathParameters(t *testing.T) {
	for in, want := range map[string]string{
		"/home;jsessionid=ABC123":         "/home;jsessionid=…",
		"/a;x=1;y=2/b;z=3?q=4&r=5":        "/a;x=…;y=…/b;z=…?q=…&r=…",
		"/plain/path":                     "/plain/path",
		"/matrix;flag/seg":                "/matrix;flag/seg",
		"/a=b/c":                          "/a=b/c",
		"":                                "/",
		"/x;sid=%41%42?token=abc":         "/x;sid=…?token=…",
		"/semi;/colon;;k=v":               "/semi;/colon;;k=…",
		"/files/report;v=1.pdf":           "/files/report;v=…",
		"/home;jsessionid=A=B==":          "/home;jsessionid=…",
		"/home;jsessionid=ABC/next;k=v/z": "/home;jsessionid=…/next;k=…/z",
	} {
		if got := safeFlowPath(in); got != want {
			t.Errorf("safeFlowPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// windowSeconds and max are capped server-side; a value large enough to
// overflow the duration is capped too instead of collapsing the window.
func TestAuthTimelineWindowAndMaxAreClamped(t *testing.T) {
	h, st, _ := newHub(t)
	base := time.Now().Add(-time.Hour)
	var first int64
	for i := 0; i < 130; i++ {
		id, err := st.InsertFlow(&store.Flow{TS: base.Add(time.Duration(i) * 5 * time.Second), Method: "GET", Scheme: "https", Host: "app.example.com", Path: "/p", Status: 200, ClientAddr: "127.0.0.1:5000"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = id
		}
	}
	steps := func(query string) int {
		rec := getDecodePreview(t, h, fmt.Sprintf("/api/flows/%d/auth-timeline%s", first, query))
		var out struct {
			Steps []json.RawMessage `json:"steps"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 {
			t.Fatalf("%s -> %d %v", query, rec.Code, err)
		}
		return len(out.Steps)
	}
	// Flows are 5 s apart: 120 s holds 25, 600 s holds 121.
	for query, want := range map[string]int{
		"":                                         25,
		"?max=1000000":                             25,
		"?max=1000000&windowSeconds=600":           100,
		"?max=1000000&windowSeconds=999999":        100,
		"?max=1000000&windowSeconds=9223372037":    100,
		"?max=1000000&windowSeconds=18446744073":   100,
		"?max=3&windowSeconds=600":                 3,
		"?max=-5&windowSeconds=-5":                 25,
		"?max=abc&windowSeconds=abc":               25,
		"?windowSeconds=30":                        7,
		"?max=1000000&windowSeconds=9223372036854": 100,
	} {
		if got := steps(query); got != want {
			t.Errorf("auth-timeline%s returned %d steps, want %d", query, got, want)
		}
	}
}
