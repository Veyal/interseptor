package scriptworker

import (
	"os"
	"testing"
	"time"
)

// TestMain lets the test binary double as the worker: Subprocess re-execs
// os.Args[0] with SW_TEST_MODE set, exactly like `interseptor __scriptworker`.
func TestMain(m *testing.M) {
	switch os.Getenv("SW_TEST_MODE") {
	case "worker":
		err := Serve(os.Stdin, os.Stdout, WorkerConfig{OnMemoryBreach: func() { os.Exit(ExitMemory) }})
		if err != nil {
			os.Exit(ExitProto)
		}
		os.Exit(0)
	case "hog": // a misbehaving worker with no self-limit: only the parent RSS watchdog can stop it
		buf := make([]byte, 400<<20)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = 1
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testWorker(mode string) Subprocess {
	return Subprocess{
		Path: os.Args[0], Args: []string{"-test.run=^$"},
		Env:    []string{"SW_TEST_MODE=" + mode},
		MaxRSS: 160 << 20, PollInterval: 20 * time.Millisecond, Grace: time.Second,
	}
}
