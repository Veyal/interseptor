package verify

import "testing"

// ex is a small builder for a completed exchange (status 200) with the given body.
func ex(body string) Exchange {
	return Exchange{Status: 200, Body: []byte(body)}
}

func TestReflectedMarkerHeld(t *testing.T) {
	tests := []struct {
		name             string
		baseline, payloa string
		marker           string
		want             bool
	}{
		{"present in payload absent in baseline", "hello world", "hello xyzzy123 world", "xyzzy123", true},
		{"present in both rejected", "echo xyzzy123", "echo xyzzy123 again", "xyzzy123", false},
		{"absent from payload rejected", "hello", "hello world", "xyzzy123", false},
		{"empty marker never reflects", "a", "b", "", false},
		{"marker is substring at edge", "", "prefix-MARK", "MARK", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ReflectedMarkerHeld(ex(tc.baseline), ex(tc.payloa), tc.marker)
			if got != tc.want {
				t.Fatalf("ReflectedMarkerHeld = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestErrorSignatureHeld(t *testing.T) {
	tests := []struct {
		name             string
		baseline, payloa string
		payloadOK        bool
		want             bool
	}{
		{"sql syntax error in payload", "ok", "You have an error in your SQL syntax near", true, true},
		{"ORA error in payload", "ok", "ORA-01756: quoted string not properly terminated", true, true},
		{"error in both rejected", "SQLSTATE[42000] always", "SQLSTATE[42000] again", true, false},
		{"no error at all rejected", "all good", "still good", true, false},
		{"payload not completed rejected", "ok", "SQL syntax", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pl := ex(tc.payloa)
			if !tc.payloadOK {
				pl = Exchange{Status: 0, Err: errFake, Body: []byte(tc.payloa)}
			}
			got := ErrorSignatureHeld(ex(tc.baseline), pl)
			if got != tc.want {
				t.Fatalf("ErrorSignatureHeld = %v, want %v", got, tc.want)
			}
		})
	}
}

// body returns a body of the given length made of repeated 'x'.
func body(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

func TestBooleanLengthHeld(t *testing.T) {
	tests := []struct {
		name       string
		lb, lt, lf int
		want       bool
	}{
		// baseline 1000: true≈baseline (1000), false diverges to 700
		{"true matches baseline false diverges", 1000, 1000, 700, true},
		{"true near baseline within tol false far", 1000, 1010, 500, true},
		{"false close to true rejected", 1000, 1000, 1005, false},
		{"true far from baseline rejected", 1000, 700, 700, false},
		{"tiny baseline rejected (floor)", 40, 40, 10, false},
		{"all equal rejected", 1000, 1000, 1000, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BooleanLengthHeld(ex(body(tc.lb)), ex(body(tc.lt)), ex(body(tc.lf)))
			if got != tc.want {
				t.Fatalf("BooleanLengthHeld(%d,%d,%d) = %v, want %v", tc.lb, tc.lt, tc.lf, got, tc.want)
			}
		})
	}
}

func TestBooleanLengthHeldIncompleteRejected(t *testing.T) {
	good := ex(body(1000))
	bad := Exchange{Status: 0, Err: errFake}
	if BooleanLengthHeld(bad, good, ex(body(500))) {
		t.Fatal("incomplete baseline must reject")
	}
	if BooleanLengthHeld(good, bad, ex(body(500))) {
		t.Fatal("incomplete true must reject")
	}
	if BooleanLengthHeld(good, good, bad) {
		t.Fatal("incomplete false must reject")
	}
}

func dur(ms int64) Exchange { return Exchange{Status: 200, DurMs: ms} }

func TestTimingHeld(t *testing.T) {
	tests := []struct {
		name                      string
		baseMs, payloadMs, ctrlMs int64
		want                      bool
	}{
		{"slow payload fast control and baseline", 100, 6000, 120, true},
		{"payload exactly at threshold", 50, 5000, 50, true},
		{"payload too fast rejected", 100, 4000, 100, false},
		{"slow baseline rejected", 4000, 6000, 100, false},
		{"slow control rejected", 100, 6000, 3500, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TimingHeld(dur(tc.baseMs), dur(tc.payloadMs), dur(tc.ctrlMs))
			if got != tc.want {
				t.Fatalf("TimingHeld = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTimingHeldBlockedControlRejected(t *testing.T) {
	// A blocked/errored control returns DurMs 0 which would naively pass "<3s";
	// ok() must reject it so a slow endpoint isn't falsely confirmed.
	base := dur(100)
	payload := dur(6000)
	blockedCtrl := Exchange{Status: 0, Err: errFake, DurMs: 0}
	if TimingHeld(base, payload, blockedCtrl) {
		t.Fatal("blocked control (DurMs 0, errored) must reject")
	}
}
