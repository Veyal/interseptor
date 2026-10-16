package pmsandbox

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

const mask = "***"

// secretValues gathers the string forms of every secret-named variable value
// in the initial and final scopes, longest first so a value that contains
// another is masked whole.
func secretValues(in *Input, final *scriptctx.Vars) []string {
	if len(in.Vars.Secret) == 0 {
		return nil
	}
	names := map[string]bool{}
	for _, n := range in.Vars.Secret {
		names[n] = true
	}
	seen := map[string]bool{}
	var out []string
	collect := func(m map[string]any) {
		for k, v := range m {
			if !names[k] {
				continue
			}
			s := stringify(v)
			if s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	for _, v := range []*scriptctx.Vars{&in.Vars, final} {
		if v == nil {
			continue
		}
		collect(v.Environment)
		collect(v.Globals)
		collect(v.Collection)
		collect(v.Local)
		collect(v.IterationData)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	return fmt.Sprint(v)
}

func makeRedactor(in *Input, secrets []string) func(string) string {
	return func(s string) string {
		for _, v := range secrets {
			s = strings.ReplaceAll(s, v, mask)
		}
		if in.Scrub != nil {
			s = in.Scrub(s)
		}
		return s
	}
}

// capText truncates s to n bytes on a rune boundary.
func capText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "...[truncated]"
}

func finalize(h *host, in *Input, w *outWire, errs []ScriptError) Output {
	out := Output{Sends: h.sends}
	var final scriptctx.Vars
	if w != nil {
		final = scriptctx.Vars{
			Environment: w.Vars.Environment, Globals: w.Vars.Globals, Collection: w.Vars.Collection, Local: w.Vars.Local,
			IterationData: in.Vars.IterationData, EnvName: in.Vars.EnvName, Secret: in.Vars.Secret,
		}
		out.Vars = final
		out.Changes = w.Changes
		out.Flow = w.Flow
		out.CookieOps = w.CookieOps
		out.Unsupported = w.Unsupported
		if in.Phase == scriptctx.PhasePreRequest {
			out.Request = w.Request
		}
		errs = append(errs, w.Errors...)
	} else {
		out.Vars = in.Vars
	}
	redact := makeRedactor(in, secretValues(in, &final))
	out.redact = redact
	budget := in.Limits.MaxOutput
	take := func(s string) string {
		s = capText(redact(s), 4096)
		if budget -= len(s); budget < 0 {
			return ""
		}
		return s
	}

	out.Console = make([]scriptctx.ConsoleLine, 0, len(h.con))
	for _, c := range h.con {
		out.Console = append(out.Console, scriptctx.ConsoleLine{Level: c.Level, Text: take(c.Text)})
	}
	if h.conCut {
		out.Console = append(out.Console, scriptctx.ConsoleLine{Level: "warn", Text: "[console output truncated]"})
	}
	if w != nil {
		for _, t := range w.Tests {
			out.Tests = append(out.Tests, scriptctx.TestResult{
				Name: take(t.Name), Status: t.Status, Message: take(t.Message),
				Expected: take(t.Expected), Actual: take(t.Actual), Duration: time.Duration(t.DurationMs) * time.Millisecond,
			})
		}
	}
	for i := range errs {
		errs[i].Message = capText(redact(errs[i].Message), 2048)
		errs[i].Stack = capText(redact(errs[i].Stack), 4096)
		if errs[i].Kind == "unsupported" {
			out.Unsupported = appendUnique(out.Unsupported, strings.TrimPrefix(errs[i].Message, "unsupported: "))
		}
	}
	out.Errors = errs
	out.Status = overallStatus(&out)
	return out
}

func appendUnique(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}

// overallStatus: a real error beats a failed test beats unsupported beats
// pass, so an unsupported API is never reported as a pass or as a real fail.
func overallStatus(o *Output) string {
	hasFail, hasUnsup := false, len(o.Unsupported) > 0
	for _, e := range o.Errors {
		if e.Kind != "unsupported" {
			return StatusError
		}
		hasUnsup = true
	}
	for _, t := range o.Tests {
		switch t.Status {
		case scriptctx.StatusError:
			return StatusError
		case scriptctx.StatusFail:
			hasFail = true
		case scriptctx.StatusUnsupported:
			hasUnsup = true
		}
	}
	switch {
	case hasFail:
		return StatusFail
	case hasUnsup:
		return StatusUnsupported
	}
	return StatusPass
}
