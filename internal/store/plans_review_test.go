package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// reviewSeedFlows is the History size the review benchmarks and query-plan
// checks run against. Override with ISP_REVIEW_FLOWS for a larger capture.
func reviewSeedFlows() int {
	if v, err := strconv.Atoi(os.Getenv("ISP_REVIEW_FLOWS")); err == nil && v > 0 {
		return v
	}
	return 100000
}

func reviewHash(i int) string {
	sum := sha256.Sum256([]byte("body-" + strconv.Itoa(i)))
	return hex.EncodeToString(sum[:])
}

// seedReviewDB fills a project with n realistic flows: 60 hosts, a few thousand
// distinct endpoints, header JSON, body hashes (deduplicated across flows), and
// tags on about 2% of the rows. No body files are written.
func seedReviewDB(tb testing.TB, n int) *Store {
	tb.Helper()
	s, err := Open(tb.TempDir())
	if err != nil {
		tb.Fatalf("Open: %v", err)
	}
	tb.Cleanup(func() { _ = s.Close() })
	mimes := []string{"application/json", "text/html", "image/png", "text/javascript", "text/css"}
	methods := []string{"GET", "GET", "GET", "POST", "PUT", "DELETE"}
	statuses := []int{200, 200, 200, 201, 204, 301, 400, 401, 403, 404, 500}
	reqH := map[string][]string{
		"Accept":          {"application/json, text/plain, */*"},
		"Accept-Encoding": {"gzip, deflate, br"},
		"Cookie":          {"session=0123456789abcdef0123456789abcdef; theme=dark"},
		"User-Agent":      {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"},
	}
	resH := map[string][]string{
		"Cache-Control":             {"no-store"},
		"Content-Type":              {"application/json; charset=utf-8"},
		"Strict-Transport-Security": {"max-age=31536000"},
		"X-Request-Id":              {"8f14e45f-ceea-467f-a9c1-0f3b2a7d1e55"},
	}
	const chunk = 2000
	for start := 0; start < n; start += chunk {
		tx, err := s.db.Begin()
		if err != nil {
			tb.Fatalf("begin: %v", err)
		}
		ex := &txStmts{tx: tx}
		for i := start; i < min(start+chunk, n); i++ {
			f := &Flow{
				TS:          time.UnixMilli(int64(1_700_000_000_000 + i*37)),
				Method:      methods[i%len(methods)],
				Scheme:      "https",
				Host:        fmt.Sprintf("svc%02d.example.com", i%60),
				Port:        443,
				Path:        fmt.Sprintf("/v2/%s/%d/items?page=%d", []string{"orders", "users", "carts", "assets"}[i%4], i%977, i%50),
				HTTPVersion: "HTTP/1.1",
				Status:      statuses[i%len(statuses)],
				ReqHeaders:  reqH,
				ResHeaders:  resH,
				ReqLen:      int64(120 + i%900),
				ResLen:      int64(400 + (i*7)%90000),
				Mime:        mimes[i%len(mimes)],
				DurationMs:  int64(3 + i%400),
				ClientAddr:  "127.0.0.1:" + strconv.Itoa(40000+i%2000),
				ResBodyHash: reviewHash(i % 5000),
			}
			if i%6 >= 3 {
				f.ReqBodyHash = reviewHash(100000 + i%3000)
			}
			if i%11 == 0 {
				f.Note = "checked for idor on tenant parameter"
			}
			if i%97 == 0 {
				f.Flags = FlagRepeater
			}
			var tags []string
			if i%50 == 0 {
				tags = []string{"auth", "idor-candidate"}
			}
			if err := insertFlowRowExec(ex, f, tags, int64(i+1)); err != nil {
				tb.Fatalf("seed %d: %v", i, err)
			}
		}
		if err := tx.Commit(); err != nil {
			tb.Fatalf("commit: %v", err)
		}
	}
	s.noteFlowID(int64(n))
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		tb.Fatalf("checkpoint: %v", err)
	}
	return s
}

// explainPlan returns the EXPLAIN QUERY PLAN detail lines of one statement.
func explainPlan(tb testing.TB, s *Store, q string, args ...any) string {
	tb.Helper()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		tb.Fatalf("explain %s: %v", q, err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			tb.Fatalf("scan plan: %v", err)
		}
		b.WriteString(detail)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		tb.Fatalf("plan rows: %v", err)
	}
	return b.String()
}
