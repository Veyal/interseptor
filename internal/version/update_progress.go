package version

import (
	"fmt"
	"io"
	"os"
	"time"
)

// updateProgress prints human-readable step lines and optional download progress.
type updateProgress struct {
	out  io.Writer
	term bool
}

func newUpdateProgress(out io.Writer) *updateProgress {
	if out == nil {
		out = os.Stderr
	}
	return &updateProgress{out: out, term: isTerminal(out)}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func (p *updateProgress) step(format string, args ...any) {
	fmt.Fprintf(p.out, "→ ")
	fmt.Fprintf(p.out, format, args...)
	fmt.Fprintln(p.out)
}

func (p *updateProgress) done(format string, args ...any) {
	fmt.Fprintf(p.out, "✓ ")
	fmt.Fprintf(p.out, format, args...)
	fmt.Fprintln(p.out)
}

func (p *updateProgress) downloadProgress(done, total int64) {
	if p.term {
		if total > 0 {
			pct := float64(done) / float64(total) * 100
			fmt.Fprintf(p.out, "\r   downloading: %.0f%% (%s / %s)", pct, formatBytes(done), formatBytes(total))
		} else {
			fmt.Fprintf(p.out, "\r   downloading: %s", formatBytes(done))
		}
		return
	}
	// Non-TTY: print occasional milestones so logs/pipes still show movement.
	if total > 0 {
		pct := int(float64(done) / float64(total) * 100)
		if pct >= 100 || pct%25 == 0 {
			fmt.Fprintf(p.out, "   downloading: %d%% (%s / %s)\n", pct, formatBytes(done), formatBytes(total))
		}
	} else if done > 0 && done%(1<<20) < 64<<10 { // ~every MiB
		fmt.Fprintf(p.out, "   downloading: %s\n", formatBytes(done))
	}
}

func (p *updateProgress) downloadDone() {
	if p.term {
		fmt.Fprintln(p.out)
	}
}

type progressReader struct {
	r          io.Reader
	report     func(done, total int64)
	total      int64
	n          int64
	lastReport time.Time
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.n += int64(n)
		if time.Since(pr.lastReport) >= 200*time.Millisecond || err == io.EOF {
			pr.report(pr.n, pr.total)
			pr.lastReport = time.Now()
		}
	}
	return n, err
}

func formatBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 10<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	}
}
