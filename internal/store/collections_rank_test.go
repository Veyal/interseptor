package store

import "testing"

func TestEvenRanksAreOrderedShortAndLeaveRoom(t *testing.T) {
	for _, n := range []int{0, 1, 2, 35, 36, 37, 500, 3000, 20000} {
		rs := EvenRanks(n)
		if len(rs) != n {
			t.Fatalf("n=%d got %d", n, len(rs))
		}
		for i := range rs {
			if len(rs[i]) > 5 {
				t.Fatalf("n=%d rank %q too long", n, rs[i])
			}
			if i > 0 {
				if rs[i] <= rs[i-1] {
					t.Fatalf("n=%d not increasing at %d: %q %q", n, i, rs[i-1], rs[i])
				}
				if m := RankBetween(rs[i-1], rs[i]); !(rs[i-1] < m && m < rs[i]) {
					t.Fatalf("n=%d no room between %q and %q: %q", n, rs[i-1], rs[i], m)
				}
			}
		}
	}
}
