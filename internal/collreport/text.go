package collreport

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
)

// writeText renders the human CLI summary. Plain ASCII, no color: it is meant
// for terminals and CI logs alike.
func writeText(w io.Writer, rep *collrun.Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Collection: %s", rep.CollectionName)
	if rep.EnvName != "" {
		fmt.Fprintf(&b, "   Environment: %s", rep.EnvName)
	}
	fmt.Fprintf(&b, "\nRun: %s   Source: %s   Iterations: %d", rep.RunUID, rep.Source, rep.Iterations)
	if rep.Data != nil {
		fmt.Fprintf(&b, "   Data: %d rows (%s, sha256 %s)", rep.Data.Rows, rep.Data.Format, short(rep.Data.Hash))
	}
	b.WriteString("\n\n")
	lastIter := -1
	for _, it := range rep.Items {
		if rep.Iterations > 1 && it.Iteration != lastIter {
			fmt.Fprintf(&b, "Iteration %d/%d\n", it.Iteration+1, rep.Iterations)
			lastIter = it.Iteration
		}
		writeTextItem(&b, it)
	}
	if rep.Truncated {
		fmt.Fprintf(&b, "... output truncated at %d requests\n", len(rep.Items))
	}
	if len(rep.Quarantined) > 0 {
		b.WriteString("Scripts not approved (nothing was sent):\n")
		for _, q := range rep.Quarantined {
			fmt.Fprintf(&b, "  %s %q (%s)  hash %s\n", q.Owner, q.Name, q.Listen, q.Hash)
		}
		b.WriteString("  Trust them in the UI, pin with --trust-hash <hash> --allow-scripts, or run with --no-scripts.\n")
	}
	t := rep.Totals
	fmt.Fprintf(&b, "\nRequests: %d (sent %d, skipped %d, blocked %d, errors %d)\n", t.Requests, t.Sent, t.Skipped, t.Blocked, t.Errors)
	fmt.Fprintf(&b, "Tests: %d passed, %d failed, %d errored, %d unsupported, %d skipped\n", t.Pass, t.Fail, t.TestError, t.Unsupported, t.TestSkip)
	fmt.Fprintf(&b, "Status: %s", rep.Status)
	if rep.StopReason != "" {
		fmt.Fprintf(&b, " (%s)", rep.StopReason)
	}
	b.WriteString("\n")
	if rep.FinishedMs > rep.StartedMs {
		fmt.Fprintf(&b, "Duration: %s\n", (time.Duration(rep.FinishedMs-rep.StartedMs) * time.Millisecond).Round(time.Millisecond))
	}
	p := rep.Persist
	switch {
	case len(p.Pending) == 0:
	case p.Mode == collrun.PersistKeep || p.Decision == collrun.PersistKeep:
		fmt.Fprintf(&b, "Variables: %d written, %d stored\n", len(p.Pending), p.Committed)
	default:
		fmt.Fprintf(&b, "Variables: %d script writes discarded (persist=%s)\n", len(p.Pending), p.Mode)
	}
	fmt.Fprintf(&b, "Exit code: %d\n", rep.ExitCode())
	_, err := io.WriteString(w, b.String())
	return err
}

func writeTextItem(b *strings.Builder, it collrun.ItemResult) {
	tag := "PASS"
	switch {
	case it.Outcome == collexec.OutcomeBlocked:
		tag = "BLOCK"
	case it.Outcome == collexec.OutcomeError:
		tag = "ERROR"
	case it.Outcome == collexec.OutcomeSkipped:
		tag = "SKIP"
	case it.Problem():
		tag = "FAIL"
	}
	name := it.Name
	if it.Path != "" {
		name = it.Path + " / " + it.Name
	}
	fmt.Fprintf(b, "[%s] %s %s", tag, it.Method, name)
	if it.HTTPStatus > 0 {
		fmt.Fprintf(b, " -> %d", it.HTTPStatus)
		if it.DurationMs > 0 {
			fmt.Fprintf(b, " (%d ms)", it.DurationMs)
		}
	}
	if it.FlowID > 0 {
		fmt.Fprintf(b, "  flow #%d", it.FlowID)
	}
	b.WriteString("\n")
	if it.Error != "" {
		fmt.Fprintf(b, "       %s\n", oneLine(it.Error))
	}
	for _, t := range it.Tests {
		fmt.Fprintf(b, "       %-5s %s", testTag(t.Status), t.Name)
		if t.Status != collexec.TestPass && t.Message != "" {
			fmt.Fprintf(b, ": %s", oneLine(t.Message))
		}
		b.WriteString("\n")
	}
	for _, c := range it.Console {
		fmt.Fprintf(b, "       console.%s: %s\n", c.Level, oneLine(c.Text))
	}
}

func testTag(s collexec.TestStatus) string {
	switch s {
	case collexec.TestPass:
		return "ok"
	case collexec.TestFail:
		return "FAIL"
	case collexec.TestSkip:
		return "skip"
	case collexec.TestUnsupported:
		return "UNSUP"
	}
	return "ERR"
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
