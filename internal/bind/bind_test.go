package bind

import "testing"

func TestExternalBindAllowed(t *testing.T) {
	t.Setenv("INTERCEPTOR_ALLOW_EXTERNAL_BIND", "")
	if !ExternalBindAllowed() {
		t.Fatal("empty env should allow external bind by default")
	}
	for _, v := range []string{"1", "true", "yes"} {
		t.Setenv("INTERCEPTOR_ALLOW_EXTERNAL_BIND", v)
		if !ExternalBindAllowed() {
			t.Fatalf("%q should allow external bind", v)
		}
	}
	for _, v := range []string{"0", "false", "no", "off", "FALSE"} {
		t.Setenv("INTERCEPTOR_ALLOW_EXTERNAL_BIND", v)
		if ExternalBindAllowed() {
			t.Fatalf("%q should block external bind", v)
		}
	}
}
