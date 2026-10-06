package preview

import (
	"bytes"
	"crypto/sha256"
	"image/png"
	"strings"
	"testing"
)

// raceFixture: 10 threads, 3 responses share extracted value "coupon-A".
func raceFixture() RaceInput {
	in := RaceInput{RunID: "run-example", Threads: 10, Barrier: true, SuccessLabel: "redeemed"}
	for i := 1; i <= 10; i++ {
		r := RaceRow{Seq: i, Worker: i, StartUs: int64(1000 + i*40), EndUs: int64(21000 + i*900), Status: 200, Length: 120,
			BodyHash: "h1", Extracted: []string{"coupon-" + string(rune('A'+i))}}
		if i <= 3 {
			r.Extracted = []string{"coupon-A"}
			r.Matched = true
		}
		if i > 8 {
			r.Status, r.Length, r.BodyHash, r.Extracted = 409, 40, "h2", nil
		}
		in.Rows = append(in.Rows, r)
	}
	return in
}

func TestRaceDuplicatesAndBanner(t *testing.T) {
	st := analyzeRace(raceFixture())
	if len(st.Dups) == 0 || st.Dups[0].Count != 3 || st.Dups[0].Value != "coupon-A" {
		t.Fatalf("dups=%+v", st.Dups)
	}
	if !strings.Contains(st.Banner, "3 responses returned the same value") {
		t.Fatalf("banner=%q", st.Banner)
	}
	if !strings.Contains(st.Banner, "3 of 10 returned the success pattern") {
		t.Fatalf("banner=%q", st.Banner)
	}
}

func TestRaceLaunchSpreadExact(t *testing.T) {
	st := analyzeRace(raceFixture())
	if st.SpreadUs != 9*40 {
		t.Fatalf("spread=%d", st.SpreadUs)
	}
	if st.First.Seq != 1 || st.Last.Seq != 10 {
		t.Fatalf("first/last=%d/%d", st.First.Seq, st.Last.Seq)
	}
}

func TestRaceGroupsByHashElseLength(t *testing.T) {
	in := RaceInput{Rows: []RaceRow{
		{Seq: 1, Status: 200, Length: 5, BodyHash: "a"},
		{Seq: 2, Status: 200, Length: 5, BodyHash: "b"},
		{Seq: 3, Status: 200, Length: 7},
		{Seq: 4, Status: 200, Length: 7},
	}}
	st := analyzeRace(in)
	if len(st.Groups) != 3 || st.Groups[0].Count != 2 || st.Groups[0].Length != 7 {
		t.Fatalf("groups=%+v", st.Groups)
	}
}

func TestRaceWordingRestricted(t *testing.T) {
	for _, in := range []RaceInput{raceFixture(), {}, {Rows: raceFixture().Rows[:2]}} {
		r, err := RenderIntruderRace(in, Opts{})
		if err != nil {
			t.Fatal(err)
		}
		all := strings.ToLower(r.Summary + " " + r.Alt + " " + analyzeRace(in).Banner)
		for _, bad := range []string{"confirmed", "vulnerable"} {
			if strings.Contains(all, bad) {
				t.Fatalf("forbidden word %q in %q", bad, all)
			}
		}
		if !strings.Contains(r.Alt, "not single-packet synchronisation") && !strings.Contains(strings.ToLower(r.Alt), "not single-packet synchronisation") {
			t.Fatalf("alt lacks footer note: %q", r.Alt)
		}
	}
}

func TestRaceNoBarrierNote(t *testing.T) {
	in := raceFixture()
	in.Barrier = false
	st := analyzeRace(in)
	if n := raceNotes(in, st); n[0] != "no launch barrier" {
		t.Fatalf("notes=%v", n)
	}
	in.Barrier = true
	for _, n := range raceNotes(in, st) {
		if n == "no launch barrier" {
			t.Fatal("barrier note on barrier run")
		}
	}
}

func TestRaceRenderStableAndValid(t *testing.T) {
	a, err := RenderIntruderRace(raceFixture(), Opts{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := RenderIntruderRace(raceFixture(), Opts{})
	if sha256.Sum256(a.PNG) != sha256.Sum256(b.PNG) {
		t.Fatal("render not deterministic")
	}
	img, err := png.Decode(bytes.NewReader(a.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1100 || a.Width != 1100 || a.Height != img.Bounds().Dy() || a.Height > 1800 {
		t.Fatalf("dims %v %d %d", img.Bounds(), a.Width, a.Height)
	}
	if a.Kind != KindIntruderRace {
		t.Fatal(a.Kind)
	}
}

func TestRaceNoTimingStillRenders(t *testing.T) {
	in := raceFixture()
	for i := range in.Rows {
		in.Rows[i].StartUs, in.Rows[i].EndUs = 0, 0
	}
	st := analyzeRace(in)
	if st.HasTiming || st.First != nil || len(st.Groups) == 0 {
		t.Fatalf("%+v", st)
	}
	if n := raceNotes(in, st); !strings.Contains(strings.Join(n, "|"), "timing not recorded") {
		t.Fatalf("notes=%v", n)
	}
	r, err := RenderIntruderRace(in, Opts{})
	if err != nil || len(r.PNG) == 0 {
		t.Fatalf("err=%v", err)
	}
}

func TestRaceLargeInputBounded(t *testing.T) {
	in := RaceInput{Threads: 64}
	for i := 1; i <= 3000; i++ {
		in.Rows = append(in.Rows, RaceRow{Seq: i, Worker: i%64 + 1, StartUs: int64(i), EndUs: int64(5000 + i), Status: 200, Length: 10})
	}
	r, err := RenderIntruderRace(in, Opts{})
	if err != nil || r.Height > 1800 {
		t.Fatalf("err=%v h=%d", err, r.Height)
	}
}
