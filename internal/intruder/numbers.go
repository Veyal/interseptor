package intruder

import (
	"fmt"
	"math/rand"
	"strconv"
)

// NumbersSpec describes a Numbers payload source (Burp-style start/end/step).
type NumbersSpec struct {
	Start  int64  // inclusive
	End    int64  // inclusive (when aligned with step)
	Step   int64  // sequence step; 0 defaults to 1 (or -1 when start > end)
	Mode   string // "sequence" | "random"
	Count  int    // random: how many values to draw
	Pad    int    // optional zero-pad width (0 = none)
	Unique bool   // random: sample without replacement
}

// ExpandNumbers materializes a Numbers payload list. Caps at maxRequests.
func ExpandNumbers(spec NumbersSpec) ([]string, error) {
	mode := spec.Mode
	if mode == "" {
		mode = "sequence"
	}
	switch mode {
	case "sequence":
		return expandSequence(spec)
	case "random":
		return expandRandom(spec, nil)
	default:
		return nil, fmt.Errorf("numbers mode must be sequence or random")
	}
}

// ExpandNumbersRand is like ExpandNumbers but uses the provided RNG (tests).
func ExpandNumbersRand(spec NumbersSpec, r *rand.Rand) ([]string, error) {
	mode := spec.Mode
	if mode == "" {
		mode = "sequence"
	}
	switch mode {
	case "sequence":
		return expandSequence(spec)
	case "random":
		return expandRandom(spec, r)
	default:
		return nil, fmt.Errorf("numbers mode must be sequence or random")
	}
}

func expandSequence(spec NumbersSpec) ([]string, error) {
	step := spec.Step
	if step == 0 {
		if spec.Start > spec.End {
			step = -1
		} else {
			step = 1
		}
	}
	if step == 0 {
		return nil, fmt.Errorf("numbers step cannot be zero")
	}
	if (step > 0 && spec.Start > spec.End) || (step < 0 && spec.Start < spec.End) {
		return nil, fmt.Errorf("numbers start/end inconsistent with step direction")
	}
	out := make([]string, 0, 64)
	for n := spec.Start; ; n += step {
		out = append(out, formatNumber(n, spec.Pad))
		if len(out) >= maxRequests {
			break
		}
		next := n + step
		if step > 0 {
			if next > spec.End {
				break
			}
		} else {
			if next < spec.End {
				break
			}
		}
	}
	return out, nil
}

func expandRandom(spec NumbersSpec, r *rand.Rand) ([]string, error) {
	if spec.Count <= 0 {
		return nil, fmt.Errorf("numbers random mode requires count > 0")
	}
	lo, hi := spec.Start, spec.End
	if lo > hi {
		lo, hi = hi, lo
	}
	step := spec.Step
	var lattice []int64
	if step != 0 {
		if step < 0 {
			step = -step
		}
		for n := lo; n <= hi; n += step {
			lattice = append(lattice, n)
			if len(lattice) >= maxRequests {
				break
			}
		}
	} else {
		// Full integer range — materialize only if unique needs a pool, else sample.
		span := hi - lo + 1
		if span <= 0 {
			return nil, fmt.Errorf("numbers range is empty")
		}
		if spec.Unique {
			if span > int64(maxRequests) {
				span = int64(maxRequests)
			}
			lattice = make([]int64, 0, span)
			for i := int64(0); i < span; i++ {
				lattice = append(lattice, lo+i)
			}
		}
	}
	n := spec.Count
	if n > maxRequests {
		n = maxRequests
	}
	if r == nil {
		r = rand.New(rand.NewSource(rand.Int63()))
	}
	out := make([]string, 0, n)
	if spec.Unique {
		if len(lattice) == 0 {
			// Non-stepped unique: build pool up to maxRequests from range.
			span := hi - lo + 1
			if span > int64(maxRequests) {
				span = int64(maxRequests)
			}
			lattice = make([]int64, span)
			for i := int64(0); i < span; i++ {
				lattice[i] = lo + i
			}
		}
		if n > len(lattice) {
			n = len(lattice)
		}
		perm := r.Perm(len(lattice))
		for i := 0; i < n; i++ {
			out = append(out, formatNumber(lattice[perm[i]], spec.Pad))
		}
		return out, nil
	}
	for i := 0; i < n; i++ {
		var v int64
		if len(lattice) > 0 {
			v = lattice[r.Intn(len(lattice))]
		} else {
			span := hi - lo + 1
			v = lo + r.Int63n(span)
		}
		out = append(out, formatNumber(v, spec.Pad))
	}
	return out, nil
}

func formatNumber(n int64, pad int) string {
	s := strconv.FormatInt(n, 10)
	if pad > 0 {
		neg := false
		if n < 0 {
			neg = true
			s = strconv.FormatInt(-n, 10)
		}
		for len(s) < pad {
			s = "0" + s
		}
		if neg {
			s = "-" + s
		}
	}
	return s
}
