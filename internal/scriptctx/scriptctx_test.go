package scriptctx

import (
	"encoding/json"
	"testing"
)

func TestZeroCapsGrantNothing(t *testing.T) {
	var c Caps
	if c.VarsRead || c.VarsWrite || c.CookiesRead || c.CookiesWrite || c.NetSend || c.SecretsRead {
		t.Fatalf("zero Caps must be default-deny: %+v", c)
	}
}

func TestDefaultCapsAreTrustedScriptSet(t *testing.T) {
	c := DefaultCaps()
	if !(c.VarsRead && c.VarsWrite && c.CookiesRead && c.CookiesWrite && c.NetSend && c.SecretsRead) {
		t.Fatalf("%+v", c)
	}
}

func TestRequestJSONRoundTrip(t *testing.T) {
	in := Request{Name: "n", Method: "POST", URL: "https://api.example.com/x", Headers: []Header{{Key: "A", Value: "b"}}, Body: Body{Mode: "raw", Raw: "x"}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Request
	if err := json.Unmarshal(b, &out); err != nil || out.URL != in.URL || out.Headers[0].Key != "A" || out.Body.Raw != "x" {
		t.Fatalf("%v %+v", err, out)
	}
}

func TestStatusUnsupportedIsDistinct(t *testing.T) {
	for _, s := range []string{StatusPass, StatusFail, StatusSkip, StatusError} {
		if s == StatusUnsupported {
			t.Fatal("unsupported must not alias another status")
		}
	}
}
