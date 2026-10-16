package store

const rankAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

func rankIdx(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	}
	return 0
}

// RankBetween returns a fractional-index string strictly between a and b
// (lexicographic order). Empty a means "before everything", empty b means
// "after everything". If a >= b (non-empty) it returns a string after a.
func RankBetween(a, b string) string {
	const base = len(rankAlphabet)
	if b != "" && a >= b {
		b = ""
	}
	var out []byte
	bInf := b == ""
	for i := 0; ; i++ {
		da, db := 0, base
		if i < len(a) {
			da = rankIdx(a[i])
		}
		if !bInf && i < len(b) {
			db = rankIdx(b[i])
		}
		if db-da > 1 {
			return string(append(out, rankAlphabet[(da+db)/2]))
		}
		out = append(out, rankAlphabet[da])
		if db != da {
			bInf = true
		}
	}
}

// EvenRanks returns n strictly increasing, fixed-width ranks spread evenly over
// the key space with headroom between neighbours. Bulk inserts (importers) use
// it instead of appending one RankBetween(prev, "") per item, which grows the
// key by about one character every few items (500 characters for 3000 items).
// Later RankBetween calls between two neighbours still have room to work.
func EvenRanks(n int) []string {
	if n <= 0 {
		return nil
	}
	const base = len(rankAlphabet)
	width, space := 1, base
	for space < (n+1)*4 {
		width++
		space *= base
	}
	stride := space / (n + 1)
	out := make([]string, n)
	buf := make([]byte, width)
	for i := range out {
		v := (i + 1) * stride
		if v%base == 0 { // a trailing minimum digit would make RankBetween ambiguous
			v++
		}
		for j := width - 1; j >= 0; j-- {
			buf[j] = rankAlphabet[v%base]
			v /= base
		}
		out[i] = string(buf)
	}
	return out
}
