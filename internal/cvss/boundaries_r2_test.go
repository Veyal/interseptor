package cvss

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func bandFor(score float64) string {
	switch {
	case score == 0:
		return "Info"
	case score < 4:
		return "Low"
	case score < 7:
		return "Medium"
	case score < 9:
		return "High"
	}
	return "Critical"
}

func product(metrics [][]string, visit func(string)) {
	var walk func(i int, prefix string)
	walk = func(i int, prefix string) {
		if i == len(metrics) {
			visit(prefix)
			return
		}
		for _, v := range metrics[i][1:] {
			walk(i+1, prefix+"/"+metrics[i][0]+":"+v)
		}
	}
	walk(0, "")
}

// Every base vector of both versions is scored and its severity must be the
// band its own score falls in. The band edges 0.0, 3.9, 4.0, 6.9, 7.0, 8.9,
// 9.0 and 10.0 are reached (v4.0 has no base vector at 3.9), so an off-by-one
// at an edge cannot hide.
func TestSeverityMatchesScoreBandForEveryBaseVector(t *testing.T) {
	versions := map[string][][]string{
		"CVSS:3.1": {{"AV", "N", "A", "L", "P"}, {"AC", "L", "H"}, {"PR", "N", "L", "H"}, {"UI", "N", "R"}, {"S", "U", "C"},
			{"C", "H", "L", "N"}, {"I", "H", "L", "N"}, {"A", "H", "L", "N"}},
		"CVSS:4.0": {{"AV", "N", "A", "L", "P"}, {"AC", "L", "H"}, {"AT", "N", "P"}, {"PR", "N", "L", "H"}, {"UI", "N", "P", "A"},
			{"VC", "H", "L", "N"}, {"VI", "H", "L", "N"}, {"VA", "H", "L", "N"}, {"SC", "H", "L", "N"}, {"SI", "H", "L", "N"}, {"SA", "H", "L", "N"}},
	}
	for version, metrics := range versions {
		seen := map[string]string{}
		n := 0
		product(metrics, func(suffix string) {
			vector := version + suffix
			ev, err := Evaluate(vector)
			if err != nil {
				t.Fatalf("%s: %v", vector, err)
			}
			n++
			if ev.Score < 0 || ev.Score > 10 || math.Round(ev.Score*10) != ev.Score*10 && math.Abs(math.Round(ev.Score*10)-ev.Score*10) > 1e-9 {
				t.Fatalf("%s: score %v is not a one-decimal value in 0..10", vector, ev.Score)
			}
			if want := bandFor(math.Round(ev.Score*10) / 10); ev.Severity != want {
				t.Fatalf("%s: score %.1f has severity %q, want %q (rating %q)", vector, ev.Score, ev.Severity, want, ev.RawRating)
			}
			if (ev.RawRating == "NONE") != (ev.Rating == "INFO") || ProductSeverity(ev.Rating) != ev.Severity {
				t.Fatalf("%s: rating %q / raw %q / severity %q disagree", vector, ev.Rating, ev.RawRating, ev.Severity)
			}
			seen[fmt.Sprintf("%.1f", ev.Score)] = ev.Severity
		})
		for edge, want := range map[string]string{"0.0": "Info", "3.9": "Low", "4.0": "Medium", "6.9": "Medium", "7.0": "High", "8.9": "High", "9.0": "Critical", "10.0": "Critical"} {
			got, ok := seen[edge]
			if !ok && version == "CVSS:4.0" && edge == "3.9" {
				continue // no v4.0 base vector scores 3.9 (the table goes 3.8 -> 4.0)
			}
			if !ok || got != want {
				t.Errorf("%s: score %s -> %q (reached=%v), want %q", version, edge, got, ok, want)
			}
		}
		t.Logf("%s: %d base vectors, %d distinct scores", version, n, len(seen))
	}
}

// Malformed vectors are errors, never a score, a panic or a long echo.
func TestEvaluateRejectsMalformedVectors(t *testing.T) {
	long := "CVSS:4.0/" + strings.Repeat("AV:N/", 200000)
	for _, vector := range []string{
		"", " ", "CVSS:4.0", "CVSS:4.0/", "CVSS:3.1", "CVSS:3.1/", "CVSS:3.0/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", "CVSS:2.0/AV:N",
		"AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H",
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:X",
		"CVSS:3.1/AV:N/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N",
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:Z",
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N/",
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N/SA:N",
		"CVSS:4.0/AC:L/AV:N/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N",
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N\nX",
		"CVSS:4.0/\x00/::////", "CVSS:3.1/\xff\xfe", "CVSS:4.0/AV::N", "CVSS:4.0/:/:/:", long,
	} {
		ev, err := Evaluate(vector)
		if err == nil {
			t.Errorf("Evaluate(%.60q) = %+v, want an error", vector, ev)
			continue
		}
		if len(err.Error()) > 400 {
			t.Errorf("Evaluate(%.40q…): error is %d bytes long", vector, len(err.Error()))
		}
		if vector != "" && strings.TrimSpace(vector) != "" {
			if werr := ValidateForWrite(vector); werr == nil {
				t.Errorf("ValidateForWrite(%.60q) accepted a vector Evaluate rejects", vector)
			} else if len(werr.Error()) > 600 {
				t.Errorf("ValidateForWrite(%.40q…): error is %d bytes long", vector, len(werr.Error()))
			}
		}
	}
}
