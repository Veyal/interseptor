package curl

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

func counter() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id%03d", n) }
}

func parse1(t *testing.T, cmd string) (*store.Item, *Report) {
	t.Helper()
	res, err := Parse([]byte(cmd), Options{NewID: counter()})
	if err != nil {
		t.Fatalf("Parse(%q): %v", cmd, err)
	}
	return &res.Items[0], &res.Report
}

type hdr struct{ Key, Value string }

func headersOf(t *testing.T, it *store.Item) []hdr {
	t.Helper()
	var hs []hdr
	if err := json.Unmarshal(it.Headers, &hs); err != nil {
		t.Fatal(err)
	}
	return hs
}

func hget(hs []hdr, k string) string {
	for _, h := range hs {
		if strings.EqualFold(h.Key, k) {
			return h.Value
		}
	}
	return ""
}

func TestGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/commands.sh")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(data, Options{NewID: counter()})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.MarshalIndent(res, "", "  ")
	got = append(got, '\n')
	const golden = "testdata/commands.golden.json"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("missing golden (run with -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch; rerun with -update and review the diff")
	}
}

func TestPostJSON(t *testing.T) {
	it, rep := parse1(t, `curl -X POST 'https://example.com/a?x=1' -H 'Content-Type: application/json' -d '{"a":1}' --compressed`)
	if it.Method != "POST" || it.Name != "POST example.com/a" {
		t.Fatalf("method/name = %s / %s", it.Method, it.Name)
	}
	hs := headersOf(t, it)
	if hget(hs, "Accept-Encoding") == "" || hget(hs, "Content-Type") != "application/json" {
		t.Fatalf("headers: %v", hs)
	}
	var body struct {
		Mode, Raw string
		Options   struct{ Raw struct{ Language string } }
	}
	_ = json.Unmarshal(it.Body, &body)
	if body.Mode != "raw" || body.Raw != `{"a":1}` || body.Options.Raw.Language != "json" {
		t.Fatalf("body = %s", it.Body)
	}
	if rep.Stats.Requests != 1 {
		t.Fatalf("stats = %+v", rep.Stats)
	}
}

func TestImplicitFormContentTypeAndMethod(t *testing.T) {
	it, _ := parse1(t, `curl https://example.com -d a=1 -d b=2`)
	if it.Method != "POST" || hget(headersOf(t, it), "Content-Type") != "application/x-www-form-urlencoded" {
		t.Fatalf("%s %v", it.Method, headersOf(t, it))
	}
	if !strings.Contains(string(it.Body), `"a=1&b=2"`) && !strings.Contains(string(it.Body), `a=1&b=2`) {
		t.Fatalf("body = %s", it.Body)
	}
}

func TestGetModeMovesDataToQuery(t *testing.T) {
	it, _ := parse1(t, `curl -G https://example.com/s --data-urlencode 'q=a b' -d page=2`)
	if it.Method != "GET" || len(it.Body) != 0 {
		t.Fatalf("%s body=%s", it.Method, it.Body)
	}
	if !strings.Contains(string(it.URL), "https://example.com/s?q=a+b\\u0026page=2") && !strings.Contains(string(it.URL), "q=a+b&page=2") {
		t.Fatalf("url = %s", it.URL)
	}
}

func TestShortClusterAndAttachedValues(t *testing.T) {
	it, _ := parse1(t, `curl -sSLXPATCH -H'X-A: 1' -uu:p https://example.com`)
	if it.Method != "PATCH" || hget(headersOf(t, it), "X-A") != "1" || !strings.Contains(string(it.Auth), `"basic"`) {
		t.Fatalf("%s %s %s", it.Method, it.Headers, it.Auth)
	}
	if !strings.Contains(string(it.Settings), `"followRedirects":true`) {
		t.Fatalf("settings = %s", it.Settings)
	}
}

func TestLongEqualsForm(t *testing.T) {
	it, _ := parse1(t, `curl --request=DELETE --header='X-B: 2' https://example.com/x`)
	if it.Method != "DELETE" || hget(headersOf(t, it), "X-B") != "2" {
		t.Fatalf("%s %s", it.Method, it.Headers)
	}
}

func TestFormFilesNeverRead(t *testing.T) {
	it, rep := parse1(t, `curl https://example.com/u -F 'a=b' -F 'f=@/etc/passwd;type=text/plain'`)
	s := string(it.Body)
	if strings.Contains(s, "/etc/passwd") || !strings.Contains(s, `"type":"file"`) || !strings.Contains(s, `"contentType":"text/plain"`) {
		t.Fatalf("body = %s", s)
	}
	if rep.Stats.NeedsAsset != 1 {
		t.Fatalf("needs-asset = %d", rep.Stats.NeedsAsset)
	}
}

func TestDataFileIsNeedsAsset(t *testing.T) {
	it, rep := parse1(t, `curl -X PUT https://example.com -d @/etc/passwd`)
	if strings.Contains(string(it.Body), "passwd") || rep.Stats.NeedsAsset != 1 {
		t.Fatalf("body=%s stats=%+v", it.Body, rep.Stats)
	}
}

func TestEmbeddedCredentialsFlaggedWithoutLeak(t *testing.T) {
	_, rep := parse1(t, `curl -u alice:CANARY-PW https://example.com -H 'Authorization: Bearer CANARY-TOK'`)
	if rep.Stats.EmbeddedCredentials < 2 {
		t.Fatalf("creds = %d", rep.Stats.EmbeddedCredentials)
	}
	b, _ := json.Marshal(rep)
	if strings.Contains(string(b), "CANARY") {
		t.Fatalf("report leaks a secret: %s", b)
	}
}

func TestVariablesAreNotFlaggedAsCredentials(t *testing.T) {
	_, rep := parse1(t, `curl -u '{{user}}:{{pass}}' https://example.com -H 'Authorization: Bearer {{token}}'`)
	if rep.Stats.EmbeddedCredentials != 0 {
		t.Fatalf("creds = %d", rep.Stats.EmbeddedCredentials)
	}
}

func TestReportsUnknownAndUnsupportedFlags(t *testing.T) {
	_, rep := parse1(t, `curl --nonsense --proxy http://127.0.0.1:1 --connect-timeout 3 https://example.com`)
	for _, f := range []string{"unknown-flag", "flag:proxy", "flag:connect-timeout"} {
		found := false
		for _, e := range rep.Entries {
			found = found || e.Feature == f
		}
		if !found {
			t.Errorf("missing report entry %s: %+v", f, rep.Entries)
		}
	}
}

func TestShellExpansionNeedsReview(t *testing.T) {
	_, rep := parse1(t, `curl "https://example.com/$P"`)
	if rep.Counts[NeedsReview] == 0 {
		t.Fatalf("report = %+v", rep.Entries)
	}
}

func TestPromptEnvPrefixAndPipes(t *testing.T) {
	res, err := Parse([]byte("$ TOKEN=x curl https://example.com/a | jq .\nls -l\ncurl.exe https://example.com/b"), Options{NewID: counter()})
	if err != nil || len(res.Items) != 2 {
		t.Fatalf("err=%v items=%d", err, len(res.Items))
	}
	if res.Report.Counts[Degraded] == 0 {
		t.Fatal("non-curl command not reported")
	}
}

func TestNoCurl(t *testing.T) {
	if _, err := Parse([]byte("echo hi"), Options{}); err != ErrNoCurl {
		t.Fatalf("err = %v", err)
	}
}

func TestChromeCmdAndPowerShellForms(t *testing.T) {
	cmd := "curl ^\"https://example.com/x^\" ^\r\n  -H ^\"accept: */*^\" ^\r\n  --data-raw ^\"^{^\\^\"a^\\^\":1^}^\" ^\r\n  --compressed"
	it, _ := parse1(t, cmd)
	if it.Method != "POST" || !strings.Contains(string(it.Body), `{\"a\":1}`) {
		t.Fatalf("cmd form: %s %s", it.Method, it.Body)
	}
	ps := "curl.exe 'https://example.com/y' `\n  -H 'x-a: 1' `\n  -X POST"
	it, _ = parse1(t, ps)
	if it.Method != "POST" || hget(headersOf(t, it), "x-a") != "1" {
		t.Fatalf("ps form: %s %s", it.Method, it.Headers)
	}
}

func TestHostileInputBounds(t *testing.T) {
	if _, err := Parse(bytes.Repeat([]byte("a"), MaxInputBytes+1), Options{}); err != ErrTooLarge {
		t.Fatalf("large: %v", err)
	}
	many := strings.Repeat("curl https://example.com\n", MaxCommands+50)
	res, err := Parse([]byte(many), Options{})
	if err != nil || len(res.Items) != MaxCommands {
		t.Fatalf("many: err=%v items=%d", err, len(res.Items))
	}
	long := "curl https://example.com/" + strings.Repeat("a", MaxURLLen+100)
	it, _ := parse1(t, long)
	if len(it.Name) > 130 {
		t.Fatalf("name length %d", len(it.Name))
	}
	// pathological option soup must not loop or panic
	_, _ = Parse([]byte("curl "+strings.Repeat("-H ", 50000)), Options{})
	_, _ = Parse([]byte("curl "+strings.Repeat("-sSL", 50000)+" https://example.com"), Options{})
}

func TestParseCommandSingle(t *testing.T) {
	it, rep, err := ParseCommand("curl https://example.com/z", Options{})
	if err != nil || it == nil || rep == nil || it.Method != "GET" {
		t.Fatalf("%v %v %v", it, rep, err)
	}
}

func FuzzParse(f *testing.F) {
	d, _ := os.ReadFile("testdata/commands.sh")
	f.Add(string(d))
	f.Add("curl -d@- -F a=@b;type=x -u: -H @f https://x")
	f.Add("curl ^\"a^\" ^\n -X")
	f.Fuzz(func(t *testing.T, s string) {
		res, err := Parse([]byte(s), Options{NewID: counter()})
		if err != nil {
			return
		}
		if _, err := json.Marshal(res); err != nil {
			t.Fatalf("result not marshalable: %v", err)
		}
		for _, it := range res.Items {
			for _, raw := range []json.RawMessage{it.URL, it.Headers, it.Body, it.Auth, it.Settings} {
				if len(raw) > 0 && !json.Valid(raw) {
					t.Fatalf("invalid JSON column %q", raw)
				}
			}
		}
	})
}

func TestURLQueryAndBodyCredentialsFlaggedWithoutLeak(t *testing.T) {
	_, rep := parse1(t, `curl -X POST 'https://example.com/login?api_key=CANARY-Q' -d 'username=bob&password=CANARY-P'`)
	if rep.Stats.EmbeddedCredentials != 2 {
		t.Fatalf("creds = %d, want 2 (url api_key + body password)", rep.Stats.EmbeddedCredentials)
	}
	b, _ := json.Marshal(rep)
	if strings.Contains(string(b), "CANARY") {
		t.Fatalf("report leaks a secret: %s", b)
	}
}
