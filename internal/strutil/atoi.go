package strutil

import "strconv"

// AtoiOr parses s as an int; returns def when s is empty or not a number.
func AtoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
