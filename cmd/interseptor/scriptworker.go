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

func runScriptWorker() int {
	err := scriptworker.Serve(os.Stdin, os.Stdout, scriptworker.WorkerConfig{
		OnMemoryBreach: func() { os.Exit(scriptworker.ExitMemory) },
	})
	if err != nil {
		return scriptworker.ExitProto
	}
	return scriptworker.ExitOK
}
