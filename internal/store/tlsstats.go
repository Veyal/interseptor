package store

// CountFlowsWithFlag returns how many flows have any of the given flag bits set.
// Queued write-behind capture is drained first, like every other read path, so a
// diagnostic count taken during live traffic includes the flows the store has
// already accepted.
func (s *Store) CountFlowsWithFlag(flag int64) (int64, error) {
	s.syncFlows()
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM flows WHERE ` + flagsAnyPredicate(flag)).Scan(&n)
	return n, err
}

// The HTTPS-success reads name idx_flows_https_ok: left to itself the planner
// takes idx_flows_status for "status > 0", which is true for nearly every flow
// and so walks the whole table one row lookup at a time.
const httpsOKFrom = ` FROM flows INDEXED BY idx_flows_https_ok WHERE `

var (
	newestHTTPSOKSQL      = `SELECT COALESCE(MAX(ts),0)` + httpsOKFrom + httpsOKPredicate
	exactHTTPSOKCountSQL  = `SELECT COUNT(*)` + httpsOKFrom + httpsOKPredicate
	cappedHTTPSOKCountSQL = `SELECT COUNT(*) FROM (SELECT 1` + httpsOKFrom + httpsOKPredicate + ` LIMIT ?)`
)

const httpsOKHostClause = ` AND instr(lower(host), lower(?)) > 0`

// CountSuccessfulHTTPS returns flows where HTTPS MITM completed (non-zero status,
// not a recorded TLS handshake failure). Queued write-behind capture is drained
// first so the TLS diagnostic does not report zero interception while the newest
// flows are still queued. The count reads every entry of idx_flows_https_ok, so
// it grows with History; a caller that only needs "any" or "at least N" uses
// CountSuccessfulHTTPSUpTo.
func (s *Store) CountSuccessfulHTTPS(hostSubstring string) (int64, error) {
	s.syncFlows()
	var n int64
	q := exactHTTPSOKCountSQL
	var args []any
	if hostSubstring != "" {
		q += httpsOKHostClause
		args = append(args, hostSubstring)
	}
	err := s.db.QueryRow(q, args...).Scan(&n)
	return n, err
}

// CountSuccessfulHTTPSUpTo counts successfully intercepted HTTPS flows but
// stops at limit, so the cost is bounded by limit instead of by the size of
// History. A result equal to limit means "limit or more".
func (s *Store) CountSuccessfulHTTPSUpTo(hostSubstring string, limit int64) (int64, error) {
	s.syncFlows()
	if limit <= 0 {
		return 0, nil
	}
	var n int64
	q := cappedHTTPSOKCountSQL
	args := []any{limit}
	if hostSubstring != "" {
		q = `SELECT COUNT(*) FROM (SELECT 1` + httpsOKFrom + httpsOKPredicate + httpsOKHostClause + ` LIMIT ?)`
		args = []any{hostSubstring, limit}
	}
	err := s.db.QueryRow(q, args...).Scan(&n)
	return n, err
}

// TLSFailureStats summarises the TLS-interception picture for the readiness
// check: the newest rejection, how many are recent enough to matter, and the
// newest successfully intercepted HTTPS flow. Timestamps are unix milliseconds
// (0 when there is none). A wall of old rejections must not outvote fresh
// successful traffic, and a fresh one must not hide behind a stale count.
func (s *Store) TLSFailureStats(hostSubstring string, sinceMS int64) (newestFailureMS, recentFailures, newestSuccessMS int64, err error) {
	s.syncFlows()
	hostClause := ""
	if hostSubstring != "" {
		hostClause = ` AND instr(lower(host), lower(?)) > 0`
	}
	q := `SELECT COALESCE(MAX(ts),0), COALESCE(SUM(CASE WHEN ts >= ? THEN 1 ELSE 0 END),0)
	      FROM flows WHERE ` + flagsAnyPredicate(FlagTLSFailed) + hostClause
	qArgs := []any{sinceMS}
	if hostSubstring != "" {
		qArgs = append(qArgs, hostSubstring)
	}
	if err = s.db.QueryRow(q, qArgs...).Scan(&newestFailureMS, &recentFailures); err != nil {
		return 0, 0, 0, err
	}
	q2 := newestHTTPSOKSQL + hostClause
	var args2 []any
	if hostSubstring != "" {
		args2 = append(args2, hostSubstring)
	}
	if err = s.db.QueryRow(q2, args2...).Scan(&newestSuccessMS); err != nil {
		return 0, 0, 0, err
	}
	return newestFailureMS, recentFailures, newestSuccessMS, nil
}
