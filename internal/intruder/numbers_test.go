package intruder

import (
	"math/rand"
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

func TestExpandNumbersCapsAtMaxRequests(t *testing.T) {
	got, err := ExpandNumbers(NumbersSpec{Start: 1, End: 100000, Step: 1, Mode: "sequence"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxRequests {
		t.Fatalf("got %d want cap %d", len(got), maxRequests)
	}
}
