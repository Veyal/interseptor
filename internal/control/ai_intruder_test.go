package control

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func putTestBody(t *testing.T, st *store.Store, b []byte) string {
	t.Helper()
	w, err := st.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	sum, _, err := w.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func TestBuildIntruderPayloadsPromptSections(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	reqBody := []byte(`{"email":"a@example.com"}`)
	resBody := []byte(`{"id":42,"role":"user"}`)
	f := &store.Flow{
		Method: "GET", Scheme: "https", Host: "example.com", Port: 443,
		Path: "/api/users?id=42", Status: 200,
		ReqHeaders: map[string][]string{
			"Authorization": {"Bearer test-token"},
			"Content-Type":  {"application/json"},
		},
		ResHeaders: map[string][]string{"Content-Type": {"application/json"}},
		ReqBodyHash: putTestBody(t, st, reqBody),
		ResBodyHash: putTestBody(t, st, resBody),
		ReqLen:      int64(len(reqBody)),
		ResLen:      int64(len(resBody)),
	}
	id, err := st.InsertFlow(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetFlow(id)
	if err != nil {
		t.Fatal(err)
	}

	ai := &aiAPI{Hub: &Hub{st: st}}
	prompt := ai.buildIntruderPayloadsPrompt(got, "SQLi on id")

	for _, want := range []string{
		"=== Summary ===",
		"=== Request ===",
		"=== Response ===",
		"Method: GET",
		"https://example.com/api/users?id=42",
		"Query params: id",
		"Body fields: email",
		"Authorization:",
		"User hint: SQLi on id",
		"positions",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestExtractJSONObject(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a":1}`, `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{`Here:\n{"positions":[]}\nok`, `{"positions":[]}`},
		{`no object`, ``},
	}
	for _, c := range cases {
		if got := extractJSONObject(c.in); got != c.want {
			t.Fatalf("extractJSONObject(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestBodyFieldNames(t *testing.T) {
	jsonNames := bodyFieldNames(`{"email":"a","id":1}`, "application/json")
	if len(jsonNames) < 2 || jsonNames[0] != "email" {
		t.Fatalf("json names: %v", jsonNames)
	}
	form := bodyFieldNames("user=admin&pass=x", "application/x-www-form-urlencoded")
	if len(form) != 2 {
		t.Fatalf("form names: %v", form)
	}
}
