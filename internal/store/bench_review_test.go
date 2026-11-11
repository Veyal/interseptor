package store

import "testing"

// BenchmarkReviewReads times the whole-table read paths on a seeded capture
// (reviewSeedFlows flows, 20 of them TLS failures): the four queries behind
// GET /api/readiness and /api/tls-diagnosis, the retention pass that keeps the
// newest N flows when nothing is over the limit.
func BenchmarkReviewReads(b *testing.B) {
	n := reviewSeedFlows()
	s := seedReviewDB(b, n)
	if _, err := s.db.Exec(`UPDATE flows SET flags = flags | ?, status = 0 WHERE id % 5000 = 0`, FlagTLSFailed); err != nil {
		b.Fatalf("mark tls failures: %v", err)
	}
	b.Run("tls-diagnosis", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := s.QueryFlowsFilter(FlowFilter{RequireFlags: FlagTLSFailed, Limit: 50, SortKey: "id", SortDir: -1}); err != nil {
				b.Fatal(err)
			}
			if _, err := s.CountFlowsWithFlag(FlagTLSFailed); err != nil {
				b.Fatal(err)
			}
			if _, err := s.CountSuccessfulHTTPS(""); err != nil {
				b.Fatal(err)
			}
			if _, _, _, err := s.TLSFailureStats("", 1); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("keep-newest-noop", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if d, err := s.DeleteFlowsKeepNewest(int64(n)); err != nil || d != 0 {
				b.Fatalf("deleted=%d err=%v", d, err)
			}
		}
	})
}
