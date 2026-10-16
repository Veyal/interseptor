package scriptworker

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// processRSS returns the resident set size of pid in bytes, or 0 when it
// cannot be read (process gone, unsupported platform).
func processRSS(pid int) uint64 {
	if runtime.GOOS == "linux" {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/statm")
		if err != nil {
			return 0
		}
		f := strings.Fields(string(b))
		if len(f) < 2 {
			return 0
		}
		pages, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			return 0
		}
		return pages * uint64(os.Getpagesize())
	}
	if runtime.GOOS == "windows" {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2e9)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	kb, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return kb << 10
}
