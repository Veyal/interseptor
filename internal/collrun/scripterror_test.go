package collrun

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/pmsandbox"
)

// The step wraps a script failure as "error: <message>", so the message must
// not already begin with the generic "error" kind or the reader sees
// "error: error: ...".
func TestScriptErrorDoesNotRepeatGenericKind(t *testing.T) {
	out := pmsandbox.Output{Status: pmsandbox.StatusError, Errors: []pmsandbox.ScriptError{{Kind: "error", Message: "capability denied: pm.sendRequest requires net.send"}}}
	err := scriptError(out)
	if err == nil || strings.HasPrefix(err.Error(), "error:") {
		t.Fatalf("generic kind repeated in %v", err)
	}
	out.Errors[0].Kind = "timeout"
	if err := scriptError(out); err == nil || !strings.HasPrefix(err.Error(), "timeout: ") {
		t.Fatalf("a specific kind must stay: %v", err)
	}
}
