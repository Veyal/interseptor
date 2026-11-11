package bruno

import (
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// In a Bruno environment the secret list marks a disabled entry with "~", the
// same as every other row. It must import as a disabled secret named without
// the marker, in file order, not as an enabled variable called "~name".
func TestDisabledSecretInEnvironmentList(t *testing.T) {
	files := []File{
		{Path: "environments/dev.bru", Data: []byte("vars {\n  base: https://example.com\n}\nvars:secret [\n  zeta,\n  ~other,\n  alpha\n]\n")},
		{Path: "get.bru", Data: []byte("meta {\n  name: G\n  type: http\n}\nget {\n  url: https://example.com/g\n}\n")},
	}
	for i := 0; i < 8; i++ { // map order used to make this nondeterministic
		r, err := ParseFiles(files, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Environments) != 1 {
			t.Fatalf("environments: %d", len(r.Environments))
		}
		var got []store.Variable
		for _, v := range r.Environments[0].Variables {
			if v.Type == store.VarTypeSecret {
				got = append(got, v)
			}
		}
		if len(got) != 3 || got[0].Key != "zeta" || got[1].Key != "other" || got[2].Key != "alpha" {
			t.Fatalf("secret variables (want zeta, other, alpha in file order): %+v", got)
		}
		if !got[0].Enabled || got[1].Enabled || !got[2].Enabled {
			t.Fatalf("enabled flags: %+v", got)
		}
	}
}
