package intruder

import (
	"math/rand"
	"strconv"
	"testing"
)

func TestExpandNumbersSequence(t *testing.T) {
	got, err := ExpandNumbers(NumbersSpec{Start: 1, End: 5, Step: 1, Mode: "sequence"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1", "2", "3", "4", "5"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestExpandNumbersSequenceStepAndPad(t *testing.T) {
	got, err := ExpandNumbers(NumbersSpec{Start: 0, End: 100, Step: 50, Mode: "sequence", Pad: 3})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"000", "050", "100"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestExpandNumbersCountdown(t *testing.T) {
	got, err := ExpandNumbers(NumbersSpec{Start: 10, End: 1, Step: -1, Mode: "sequence"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 || got[0] != "10" || got[9] != "1" {
		t.Fatalf("countdown got %v", got)
	}
}

func TestExpandNumbersRandomInRange(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	got, err := ExpandNumbersRand(NumbersSpec{Start: 1000, End: 9999, Mode: "random", Count: 50}, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 {
		t.Fatalf("got %d want 50", len(got))
	}
	for _, s := range got {
		n := 0
		for _, c := range s {
			n = n*10 + int(c-'0')
		}
		if n < 1000 || n > 9999 {
			t.Fatalf("out of range: %s", s)
		}
	}
}

// An oversized Numbers range is refused with an explicit error, never silently
// truncated to a cap: the payload count the user sees must be what is sent.
func TestExpandNumbersRefusesOversizedRange(t *testing.T) {
	if _, err := ExpandNumbers(NumbersSpec{Start: 1, End: maxNumbersValues + 1, Step: 1, Mode: "sequence"}); err == nil {
		t.Fatal("expected an error for a range larger than maxNumbersValues")
	}
	got, err := ExpandNumbers(NumbersSpec{Start: 1, End: maxNumbersValues, Step: 1, Mode: "sequence"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxNumbersValues {
		t.Fatalf("got %d want %d", len(got), maxNumbersValues)
	}
	// A range exactly at the cap in random mode is allowed too.
	if _, err := ExpandNumbers(NumbersSpec{Start: 0, End: 9, Mode: "random", Count: 10}); err != nil {
		t.Fatal(err)
	}
}

// A wide stepped range must sample across the whole range, not only from its
// first N values (the old lattice materialisation biased it).
func TestExpandNumbersRandomSamplesWholeSteppedRange(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	got, err := ExpandNumbersRand(NumbersSpec{Start: 0, End: 1_000_000, Step: 2, Mode: "random", Count: 200}, r)
	if err != nil {
		t.Fatal(err)
	}
	var maxSeen int64
	for _, s := range got {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			t.Fatalf("bad number %q: %v", s, err)
		}
		if n%2 != 0 {
			t.Fatalf("stepped range produced an off-step value %d", n)
		}
		if n > maxSeen {
			maxSeen = n
		}
	}
	if maxSeen < 500_000 {
		t.Fatalf("max sampled value = %d; a 1,000,000 range must not only sample its first values", maxSeen)
	}
}

func TestExpandNumbersRandomUniqueSpansFullRange(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	got, err := ExpandNumbersRand(NumbersSpec{Start: 0, End: 1_000_000, Mode: "random", Count: 100, Unique: true}, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 100 {
		t.Fatalf("got %d want 100", len(got))
	}
	seen := make(map[string]bool)
	var sawLarge bool
	for _, s := range got {
		if seen[s] {
			t.Fatalf("duplicate random number %s", s)
		}
		seen[s] = true
		n := 0
		for _, c := range s {
			n = n*10 + int(c-'0')
		}
		if n > 10_000 {
			sawLarge = true
		}
	}
	if !sawLarge {
		t.Fatal("expected numbers to span beyond the first 10,000 items in a 1,000,000 range")
	}
}
