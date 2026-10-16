package preview

import (
	"strings"
	"testing"
)

func TestRaceBannerIsNotAFindingByItself(t *testing.T) {
	in := RaceInput{Threads: 9}
	for i := 1; i <= 9; i++ {
		in.Rows = append(in.Rows, RaceRow{Seq: i, Status: 409, Length: 40, BodyHash: "same"})
	}
	b := analyzeRace(in).Banner
	for _, bad := range []string{"identical bodies", "same status and length"} {
		if strings.Contains(b, bad) {
			t.Fatalf("protected endpoint must not read as a finding: %q", b)
		}
	}
	if !strings.Contains(b, "no success pattern configured; counts are descriptive only") {
		t.Fatalf("banner: %q", b)
	}
}

func TestRaceRepeatedValueWording(t *testing.T) {
	b := analyzeRace(raceFixture()).Banner
	if !strings.Contains(b, "3 responses share extracted value") || !strings.Contains(b, "check whether it should be unique") {
		t.Fatalf("banner: %q", b)
	}
	if strings.Contains(b, "returned the same value") {
		t.Fatalf("old wording: %q", b)
	}
}

func TestRaceExpectedBaseline(t *testing.T) {
	in := raceFixture()
	in.ExpectedMax = 1
	b := analyzeRace(in).Banner
	if !strings.Contains(b, "3 of 10 returned the success pattern (expected at most 1)") {
		t.Fatalf("banner: %q", b)
	}
}

func TestRaceLaunchWindowOnlyCoversBarrierRows(t *testing.T) {
	in := RaceInput{Threads: 2, Barrier: true}
	starts := []int64{0, 100, 50000, 60000, 70000, 80000}
	for i, s := range starts {
		in.Rows = append(in.Rows, RaceRow{Seq: i + 1, StartUs: s + 1, EndUs: s + 20000, Status: 200, Length: 5})
	}
	st := analyzeRace(in)
	if st.LaunchN != 2 || st.SpreadUs != 100 {
		t.Fatalf("launchN=%d spread=%d", st.LaunchN, st.SpreadUs)
	}
	notes := strings.Join(raceNotes(in, st), "|")
	if !strings.Contains(notes, "first 2 launched within") || !strings.Contains(notes, "4 queued after completions") {
		t.Fatalf("notes: %s", notes)
	}
	in.Barrier = false
	notes = strings.Join(raceNotes(in, analyzeRace(in)), "|")
	if !strings.Contains(notes, "all 6 started within") {
		t.Fatalf("notes: %s", notes)
	}
	if !strings.Contains(raceLaunchTitle, "not server arrival") {
		t.Fatal(raceLaunchTitle)
	}
}

func TestRaceLanesSampleAcrossTheRun(t *testing.T) {
	var rows []RaceRow
	for i := 1; i <= 3000; i++ {
		rows = append(rows, RaceRow{Seq: i, StartUs: int64(i), EndUs: int64(i + 10)})
	}
	plot := raceLanePlan(rows, 320)
	if len(plot) > 320 || len(plot) < 150 {
		t.Fatalf("plotted %d", len(plot))
	}
	if last := plot[len(plot)-1].Seq; last < 2700 {
		t.Fatalf("lanes must span the run, last plotted seq=%d", last)
	}
	if n := raceSampleNote(len(plot), 3000, 3000); !strings.Contains(n, "of 3000") || !strings.Contains(n, "every") {
		t.Fatalf("note: %q", n)
	}
	if got := raceLanePlan(rows[:10], 320); len(got) != 10 {
		t.Fatalf("small run sampled: %d", len(got))
	}
}

func TestRaceLegendNoteOnlyWithDuplicates(t *testing.T) {
	if raceLegendNote(raceStats{}) != "" {
		t.Fatal("caption without duplicates")
	}
	if raceLegendNote(analyzeRace(raceFixture())) == "" {
		t.Fatal("missing caption with duplicates")
	}
	if raceHitsHeader != "Pattern matches" {
		t.Fatal(raceHitsHeader)
	}
}
