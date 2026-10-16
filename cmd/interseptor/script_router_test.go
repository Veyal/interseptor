package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/pmsandbox"
	"github.com/Veyal/interseptor/internal/scriptworker"
	"github.com/Veyal/interseptor/internal/store"
)

type countingExec struct {
	inner scriptworker.Executor
	n     *atomic.Int32
}

func (c countingExec) Run(ctx context.Context, in pmsandbox.Input) pmsandbox.Output {
	c.n.Add(1)
	return c.inner.Run(ctx, in)
}

// The headless runner sends approved scripts through the worker subprocess
// (a re-exec of this very binary) unless the operator opts into in-process.
func TestCLIScriptsRunInWorkerProcessByDefault(t *testing.T) {
	var n atomic.Int32
	orig := newScriptRouter
	newScriptRouter = func() scriptworker.Router {
		r := orig()
		r.Worker = countingExec{inner: r.Worker, n: &n}
		return r
	}
	t.Cleanup(func() { newScriptRouter = orig })

	p := newCLIProject(t)
	p.add("scripted", "/ok", func(it *store.Item) {
		it.Events = cliEvent("test", "pm.test('x', () => pm.expect(1).to.eql(1));")
	})
	code, out, errs := p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--allow-scripts", "--trust-hash", p.scriptHashes()[0])
	if code != 0 || !strings.Contains(out, "ok    x") {
		t.Fatalf("worker run exit %d\n%s\n%s", code, out, errs)
	}
	if n.Load() == 0 {
		t.Fatal("approved script did not go through the worker executor")
	}

	n.Store(0)
	t.Setenv(envScriptsInProcess, "1")
	code, out, _ = p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--allow-scripts", "--trust-hash", p.scriptHashes()[0])
	if code != 0 || !strings.Contains(out, "ok    x") {
		t.Fatalf("in-process run exit %d\n%s", code, out)
	}
	if n.Load() != 0 {
		t.Fatal("INTERSEPTOR_SCRIPTS_INPROCESS=1 must keep trusted scripts in-process")
	}
}
