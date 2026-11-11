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

// CountSuccessfulHTTPS returns flows where HTTPS MITM completed (non-zero status,
// not a recorded TLS handshake failure). Queued write-behind capture is drained
// first so the TLS diagnostic does not report zero interception while the newest
// flows are still queued.
func (s *Store) CountSuccessfulHTTPS(hostSubstring string) (int64, error) {
	s.syncFlows()
	var n int64
	// The unary plus keeps the planner off idx_flows_status: "status > 0" is
	// true for nearly every flow, so that index walks the whole table one row
	// lookup at a time (4x slower than a plain scan on 100,000 flows).
	q := `SELECT COUNT(*) FROM flows WHERE scheme = 'https' AND +status > 0 AND (flags & ?) = 0`
	args := []any{FlagTLSFailed}
	if hostSubstring != "" {
		q += ` AND instr(lower(host), lower(?)) > 0`
		args = append(args, hostSubstring)
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
	q2 := `SELECT COALESCE(MAX(ts),0) FROM flows WHERE scheme = 'https' AND status > 0 AND (flags & ?) = 0` + hostClause
	args2 := []any{FlagTLSFailed}
	if hostSubstring != "" {
		args2 = append(args2, hostSubstring)
	}
	if err = s.db.QueryRow(q2, args2...).Scan(&newestSuccessMS); err != nil {
		return 0, 0, 0, err
	}
	return newestFailureMS, recentFailures, newestSuccessMS, nil
}
