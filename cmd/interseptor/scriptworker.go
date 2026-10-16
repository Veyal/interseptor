package main

import (
	"os"

	"github.com/Veyal/interseptor/internal/scriptworker"
)

// The script worker is a re-exec of this binary: `interseptor __scriptworker`.
// It is handled in init so it never reaches flag parsing, project setup or any
// listener: the process only speaks the framed protocol on stdin/stdout.
func init() {
	if len(os.Args) > 1 && os.Args[1] == "__scriptworker" {
		os.Exit(runScriptWorker())
	}
}

// envScriptsInProcess opts trusted collection scripts out of worker isolation
// (faster, but a runaway script can use unbounded memory in this process).
const envScriptsInProcess = "INTERSEPTOR_SCRIPTS_INPROCESS"

// newScriptRouter is the script-engine policy of every host process (server
// and CLI): scripts run in the re-exec'd worker, which is killed on timeout or
// memory breach and never inherits this environment. Only a script the owner
// trusted ever reaches an engine, and only INTERSEPTOR_SCRIPTS_INPROCESS=1 lets
// those run in-process. Overridable for tests.
var newScriptRouter = func() scriptworker.Router {
	return scriptworker.Router{
		Worker:           scriptworker.Subprocess{},
		InProcessTrusted: os.Getenv(envScriptsInProcess) == "1",
	}
}

func runScriptWorker() int {
	err := scriptworker.Serve(os.Stdin, os.Stdout, scriptworker.WorkerConfig{
		OnMemoryBreach: func() { os.Exit(scriptworker.ExitMemory) },
	})
	if err != nil {
		return scriptworker.ExitProto
	}
	return scriptworker.ExitOK
}
