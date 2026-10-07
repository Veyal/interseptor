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
