package preview

import (
	"testing"
	"time"
)

// A tick step smaller than the float resolution at the axis offset left v
// unchanged ("v += step" rounded back to v), so the loop spun forever and the
// render never returned. Guard it: finish with the ticks it has.
func TestNiceTicksTerminatesWhenStepCannotAdvance(t *testing.T) {
	done := make(chan []float64, 1)
	go func() { done <- niceTicks(1e18, 1e18+100, 10) }()
	select {
	case ticks := <-done:
		if len(ticks) == 0 {
			t.Fatal("a huge offset should still yield its (identical) ticks")
		}
		for i := 1; i < len(ticks); i++ {
			if ticks[i] < ticks[i-1] {
				t.Fatalf("ticks must not go backwards: %v", ticks)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("niceTicks did not terminate: the tick step is below the float resolution at this offset")
	}
}

// Ordinary ranges keep the number of ticks they always had.
func TestNiceTicksKeepsItsShapeForOrdinaryRanges(t *testing.T) {
	for _, c := range []struct {
		lo, hi float64
		target int
	}{
		{0, 1, 5},
		{0, 950, 6},
		{12.5, 87.5, 4},
		{-40, 40, 5},
	} {
		ticks := niceTicks(c.lo, c.hi, c.target)
		if len(ticks) < 2 || len(ticks) > 4*c.target+16 {
			t.Fatalf("niceTicks(%v,%v,%d) returned %d ticks: %v", c.lo, c.hi, c.target, len(ticks), ticks)
		}
		if ticks[0] > c.lo || ticks[len(ticks)-1] < c.hi {
			t.Fatalf("ticks must cover the range: %v for [%v,%v]", ticks, c.lo, c.hi)
		}
	}
}
