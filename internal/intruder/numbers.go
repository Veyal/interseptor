package intruder

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
)

// maxNumbersValues bounds one Numbers payload list. It is a materialisation
// bound on the generated list, not a bound on the attack: the run itself has no
// request cap. A range that would exceed it is refused with an explicit error
// rather than silently truncated, so the payload count the user sees is the
// payload count that is sent.
const maxNumbersValues = 1_000_000

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

// ExpandNumbers materializes a Numbers payload list, refusing (never silently
// truncating) a range larger than maxNumbersValues.
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
	// Count the range up front (saturating, so extreme inputs cannot overflow) and
	// refuse an oversized one instead of quietly stopping at a cap.
	count := stepsBetween(spec.Start, spec.End, step)
	if count <= 0 {
		return nil, fmt.Errorf("numbers range is empty")
	}
	if count > maxNumbersValues {
		return nil, fmt.Errorf("numbers range produces %d values; the maximum is %d — widen the step or narrow the range", count, maxNumbersValues)
	}
	out := make([]string, 0, count)
	for n := spec.Start; ; n += step {
		out = append(out, formatNumber(n, spec.Pad))
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

// stepsBetween is the number of values from start to end inclusive at step,
// computed without overflowing for extreme int64 inputs.
func stepsBetween(start, end, step int64) int {
	if step == math.MinInt64 {
		return math.MaxInt // -step is not representable; treat as unbounded
	}
	var dist, stride uint64
	if step > 0 {
		dist = uint64(end) - uint64(start) // correct unsigned distance when end >= start
		stride = uint64(step)
	} else {
		dist = uint64(start) - uint64(end)
		stride = uint64(-step)
	}
	if stride == 0 {
		return 0
	}
	steps := dist/stride + 1
	if steps > uint64(math.MaxInt) {
		return math.MaxInt
	}
	return int(steps)
}

func expandRandom(spec NumbersSpec, r *rand.Rand) ([]string, error) {
	if spec.Count <= 0 {
		return nil, fmt.Errorf("numbers random mode requires count > 0")
	}
	if spec.Count > maxNumbersValues {
		return nil, fmt.Errorf("numbers random mode asks for %d values; the maximum is %d", spec.Count, maxNumbersValues)
	}
	lo, hi := spec.Start, spec.End
	if lo > hi {
		lo, hi = hi, lo
	}
	step := spec.Step
	if step < 0 {
		step = -step
	}
	span := hi - lo + 1
	if span <= 0 {
		return nil, fmt.Errorf("numbers range is empty")
	}
	// latticeCount is how many distinct values the range offers (step 0 means every
	// integer). It is computed, never materialised, so a wide range is sampled
	// uniformly across the whole range instead of only from its first N values.
	latticeCount := span
	if step > 0 {
		latticeCount = (hi-lo)/step + 1
	}
	if r == nil {
		r = rand.New(rand.NewSource(rand.Int63()))
	}
	n := spec.Count
	if int64(n) > latticeCount {
		n = int(latticeCount)
	}
	// at maps a lattice index back to its value.
	at := func(k int64) int64 {
		if step > 0 {
			return lo + k*step
		}
		return lo + k
	}
	out := make([]string, 0, n)
	if spec.Unique {
		seen := make(map[int64]struct{}, n)
		for len(out) < n {
			k := r.Int63n(latticeCount)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, formatNumber(at(k), spec.Pad))
		}
		return out, nil
	}
	for i := 0; i < n; i++ {
		out = append(out, formatNumber(at(r.Int63n(latticeCount)), spec.Pad))
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
