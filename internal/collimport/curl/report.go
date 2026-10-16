package curl

import (
	"fmt"
	"sort"

	"github.com/Veyal/interseptor/internal/collimport/postman"
)

// Report types are shared with the Postman importer so the UI renders every
// import the same way.
type (
	Report = postman.Report
	Entry  = postman.Entry
	Level  = postman.Level
)

const (
	Converted      = postman.Converted
	Degraded       = postman.Degraded
	PreservedInert = postman.PreservedInert
	Unsupported    = postman.Unsupported
	Blocked        = postman.Blocked
	NeedsReview    = postman.NeedsReview
)

func levelRank(l Level) int {
	switch l {
	case Unsupported:
		return 0
	case Blocked:
		return 1
	case NeedsReview:
		return 2
	case Degraded:
		return 3
	case PreservedInert:
		return 4
	}
	return 5
}

// finishReport computes counts, orders entries worst-first and writes the
// headline.
func finishReport(r *Report) {
	r.Counts = map[Level]int{}
	for _, e := range r.Entries {
		r.Counts[e.Level]++
	}
	sort.SliceStable(r.Entries, func(i, j int) bool { return levelRank(r.Entries[i].Level) < levelRank(r.Entries[j].Level) })
	r.Headline = fmt.Sprintf("%d request(s) from curl: %d need review, %d degraded, %d unsupported",
		r.Stats.Requests, r.Counts[NeedsReview], r.Counts[Degraded], r.Counts[Unsupported])
}
