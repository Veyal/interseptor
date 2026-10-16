package collreport

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
)

// JUnit XML (Ant/Jenkins "junit-10" shape): one testsuite per executed
// request, one testcase per test or assertion. Mapping:
//
//	pass         plain testcase
//	fail         <failure type="AssertionError">
//	error        <error type="ScriptError">
//	unsupported  <error type="UnsupportedAPI"> (never a pass, never a failure)
//	skip         <skipped>
//
// A request that errored, was blocked or skipped gets one synthetic testcase,
// and a sent request with no tests gets one passing "request" testcase, so no
// suite is empty and every outcome is visible to CI.

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name      string      `xml:"name,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Errors    int         `xml:"errors,attr"`
	Skipped   int         `xml:"skipped,attr"`
	Time      string      `xml:"time,attr"`
	Timestamp string      `xml:"timestamp,attr"`
	Hostname  string      `xml:"hostname,attr"`
	ID        int         `xml:"id,attr"`
	Cases     []junitCase `xml:"testcase"`
	SystemOut *junitText  `xml:"system-out,omitempty"`
}

type junitCase struct {
	Name      string     `xml:"name,attr"`
	Classname string     `xml:"classname,attr"`
	Time      string     `xml:"time,attr"`
	Skipped   *junitMsg  `xml:"skipped,omitempty"`
	Errors    []junitMsg `xml:"error,omitempty"`
	Failures  []junitMsg `xml:"failure,omitempty"`
}

type junitMsg struct {
	Message string `xml:"message,attr,omitempty"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

type junitText struct {
	Text string `xml:",chardata"`
}

const maxSystemOut = 8 << 10

func writeJUnit(w io.Writer, rep *collrun.Report, o Options) error {
	host := o.Hostname
	if host == "" {
		host = "interseptor"
	}
	root := junitSuites{Name: xmlSafe(rep.CollectionName), Time: seconds(rep.FinishedMs - rep.StartedMs)}
	started := time.UnixMilli(rep.StartedMs).UTC().Format("2006-01-02T15:04:05")
	var totalMs int64
	for i, it := range rep.Items {
		s := suiteFor(rep, it, i, started, host)
		totalMs += it.DurationMs
		root.Tests += s.Tests
		root.Failures += s.Failures
		root.Errors += s.Errors
		root.Skipped += s.Skipped
		root.Suites = append(root.Suites, s)
	}
	if rep.Status == collrun.StatusNotApproved || rep.Status == collrun.StatusLoopGuard || rep.Status == collrun.StatusBadTarget || rep.Status == collrun.StatusAborted {
		// The run itself did not complete: surface that as an error suite.
		s := junitSuite{Name: "run", Tests: 1, Errors: 1, Time: "0.000", Timestamp: started, Hostname: host, ID: len(root.Suites)}
		s.Cases = []junitCase{{Name: "run " + rep.Status, Classname: xmlSafe(rep.CollectionName), Time: "0.000",
			Errors: []junitMsg{{Message: xmlSafe(rep.StopReason), Type: "RunError", Body: xmlSafe(rep.StopReason)}}}}
		root.Tests++
		root.Errors++
		root.Suites = append(root.Suites, s)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(root); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func suiteFor(rep *collrun.Report, it collrun.ItemResult, idx int, started, host string) junitSuite {
	class := it.Name
	if it.Path != "" {
		class = it.Path + " / " + it.Name
	}
	name := class
	if rep.Iterations > 1 {
		name = fmt.Sprintf("%s [iteration %d]", class, it.Iteration+1)
	}
	s := junitSuite{Name: xmlSafe(name), Timestamp: started, Hostname: host, ID: idx, Time: seconds(it.DurationMs)}
	classname := xmlSafe(strings.ReplaceAll(class, " / ", "."))
	add := func(c junitCase) {
		s.Cases = append(s.Cases, c)
		s.Tests++
		s.Failures += len(c.Failures)
		s.Errors += len(c.Errors)
		if c.Skipped != nil {
			s.Skipped++
		}
	}
	reqCase := func() junitCase {
		return junitCase{Name: xmlSafe(requestLabel(it)), Classname: classname, Time: seconds(it.DurationMs)}
	}
	switch it.Outcome {
	case collexec.OutcomeError:
		c := reqCase()
		c.Errors = []junitMsg{{Message: xmlSafe(oneLine(it.Error)), Type: "RequestError", Body: xmlSafe(it.Error)}}
		add(c)
	case collexec.OutcomeBlocked:
		c := reqCase()
		msg := it.Error
		if msg == "" {
			msg = string(it.BlockReason)
		}
		c.Errors = []junitMsg{{Message: xmlSafe(oneLine(msg)), Type: "Blocked:" + string(it.BlockReason), Body: xmlSafe(msg)}}
		add(c)
	case collexec.OutcomeSkipped:
		c := reqCase()
		c.Skipped = &junitMsg{Message: "skipped by pm.execution.skipRequest"}
		add(c)
	}
	for _, t := range it.Tests {
		c := junitCase{Name: xmlSafe(t.Name), Classname: classname, Time: seconds(t.DurationMs)}
		msg := t.Message
		body := detail(t)
		switch t.Status {
		case collexec.TestFail:
			c.Failures = []junitMsg{{Message: xmlSafe(oneLine(msg)), Type: "AssertionError", Body: xmlSafe(body)}}
		case collexec.TestError:
			c.Errors = []junitMsg{{Message: xmlSafe(oneLine(msg)), Type: "ScriptError", Body: xmlSafe(body)}}
		case collexec.TestUnsupported:
			c.Errors = []junitMsg{{Message: xmlSafe(oneLine(msg)), Type: "UnsupportedAPI", Body: xmlSafe(body)}}
		case collexec.TestSkip:
			c.Skipped = &junitMsg{Message: xmlSafe(oneLine(msg))}
		}
		add(c)
	}
	if len(s.Cases) == 0 {
		add(reqCase())
	}
	if out := systemOut(it); out != "" {
		s.SystemOut = &junitText{Text: xmlSafe(out)}
	}
	return s
}

func requestLabel(it collrun.ItemResult) string {
	label := strings.TrimSpace(it.Method + " " + it.Name)
	if it.HTTPStatus > 0 {
		label += " -> " + strconv.Itoa(it.HTTPStatus)
	}
	return label
}

func detail(t collexec.TestResult) string {
	var parts []string
	if t.Message != "" {
		parts = append(parts, t.Message)
	}
	if t.Expected != "" || t.Actual != "" {
		parts = append(parts, fmt.Sprintf("expected: %s\nactual: %s", t.Expected, t.Actual))
	}
	if t.Source != "" {
		parts = append(parts, "at "+t.Source)
	}
	return strings.Join(parts, "\n")
}

func systemOut(it collrun.ItemResult) string {
	var b strings.Builder
	if it.URL != "" {
		fmt.Fprintf(&b, "%s %s\n", it.Method, it.URL)
	}
	for _, c := range it.Console {
		fmt.Fprintf(&b, "[%s] %s\n", c.Level, c.Text)
	}
	s := b.String()
	if len(s) > maxSystemOut {
		s = s[:maxSystemOut] + "\n[truncated]\n"
	}
	return s
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func seconds(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64)
}

// xmlSafe drops characters XML 1.0 cannot carry (control characters, lone
// surrogates, U+FFFE/FFFF) and repairs invalid UTF-8.
func xmlSafe(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && n == 1 {
			s = s[1:]
			continue
		}
		s = s[n:]
		switch {
		case r == 0x9, r == 0xA, r == 0xD,
			r >= 0x20 && r <= 0xD7FF,
			r >= 0xE000 && r <= 0xFFFD,
			r >= 0x10000 && r <= 0x10FFFF:
			b.WriteRune(r)
		}
	}
	return b.String()
}
