package store

import (
	"math/rand"
	"sort"
	"testing"
	"time"
)

// unionBodyHashes is the reference set the previous four-way UNION produced.
func unionBodyHashes(tb testing.TB, s *Store) []string {
	tb.Helper()
	rows, err := s.db.Query(
		`SELECT h FROM (
			SELECT req_body_hash AS h FROM flows WHERE req_body_hash != ''
			UNION
			SELECT res_body_hash AS h FROM flows WHERE res_body_hash != ''
			UNION
			SELECT original_req_body_hash AS h FROM flows WHERE original_req_body_hash != ''
			UNION
			SELECT original_res_body_hash AS h FROM flows WHERE original_res_body_hash != ''
		)`)
	if err != nil {
		tb.Fatalf("union query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			tb.Fatalf("scan: %v", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		tb.Fatalf("rows: %v", err)
	}
	sort.Strings(out)
	return out
}

// Body GC deletes every body file that is not in the reference set, so the
// single-scan query must yield exactly the hashes the four-way UNION did:
// every non-empty hash in any of the four columns, NULLs and ” excluded.
func TestR2ReferencedBodyHashesMatchTheUnion(t *testing.T) {
	s := reviewOpen(t)
	rng := rand.New(rand.NewSource(11))
	pick := func() string {
		switch rng.Intn(6) {
		case 0, 1:
			return ""
		default:
			return reviewHash(rng.Intn(150)) // collisions across columns and rows
		}
	}
	var ids []int64
	for i := 0; i < 800; i++ {
		ids = append(ids, reviewFlow(t, s, Flow{
			TS: time.UnixMilli(1_700_000_000_000 + int64(i)), Scheme: "https", Host: "api.example.com", Status: 200,
			ReqBodyHash: pick(), ResBodyHash: pick(), OriginalReqBodyHash: pick(), OriginalResBodyHash: pick(),
		}))
	}
	// Rows from an importer or an older schema can hold NULL in a hash column.
	for _, col := range []string{"req_body_hash", "res_body_hash", "original_req_body_hash", "original_res_body_hash"} {
		for i := 0; i < 40; i++ {
			if _, err := s.db.Exec(`UPDATE flows SET `+col+` = NULL WHERE id = ?`, ids[rng.Intn(len(ids))]); err != nil {
				t.Fatalf("null %s: %v", col, err)
			}
		}
	}
	want := unionBodyHashes(t, s)
	if len(want) < 100 {
		t.Fatalf("data set too thin: %d distinct hashes", len(want))
	}
	set, err := s.referencedBodyHashes()
	if err != nil {
		t.Fatalf("referencedBodyHashes: %v", err)
	}
	got := make([]string, 0, len(set))
	for h := range set {
		got = append(got, h)
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("referencedBodyHashes returned %d hashes, the UNION %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hash %d differs: got %q, want %q", i, got[i], want[i])
		}
	}
	if _, bad := set[""]; bad {
		t.Fatal("the empty hash must not be in the reference set")
	}
}

// BenchmarkR2ReferencedBodyHashes times the database half of a body GC pass
// against the UNION it replaced (ISP_REVIEW_FLOWS flows, default 100,000).
func BenchmarkR2ReferencedBodyHashes(b *testing.B) {
	s := seedReviewDB(b, reviewSeedFlows())
	b.Run("union", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if len(unionBodyHashes(b, s)) == 0 {
				b.Fatal("no hashes")
			}
		}
	})
	b.Run("single-scan", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if set, err := s.referencedBodyHashes(); err != nil || len(set) == 0 {
				b.Fatalf("referencedBodyHashes: %d, %v", len(set), err)
			}
		}
	})
}
