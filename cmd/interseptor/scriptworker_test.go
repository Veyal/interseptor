package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/pmsandbox"
	"github.com/Veyal/interseptor/internal/scriptctx"
	"github.com/Veyal/interseptor/internal/scriptworker"
)

// The test binary re-execs itself with "__scriptworker", exercising the same
// init hook the real binary uses.
func TestScriptWorkerReexec(t *testing.T) {
	if len(os.Args) > 1 && os.Args[1] == "__scriptworker" {
		t.Skip("worker child")
	}
	ex := scriptworker.Subprocess{MaxRSS: 192 << 20}
	out := ex.Run(context.Background(), pmsandbox.Input{
		Phase: scriptctx.PhaseTest, Script: `pm.test("ok", function(){})`, Caps: scriptctx.DefaultCaps(),
	})
	if out.Status != pmsandbox.StatusPass {
		t.Fatalf("status %s errors %+v", out.Status, out.Errors)
	}
	bomb := pmsandbox.Input{Phase: scriptctx.PhaseTest, Script: `var a=[];while(true)a.push(new Array(1e6).fill(1))`, Caps: scriptctx.DefaultCaps()}
	bomb.Limits.Timeout = 20 * time.Second
	if out := ex.Run(context.Background(), bomb); out.Status != pmsandbox.StatusError {
		t.Fatalf("bomb status %s", out.Status)
	}
}
