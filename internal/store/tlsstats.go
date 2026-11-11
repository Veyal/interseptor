package store

import "strings"

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

// distinctHostsContainingSQL lists the stored host values that contain a
// substring, case-insensitively, without reading the flows: a recursive seek
// to the next distinct key of idx_flows_host, so it costs one index seek per
// distinct host (0.13 ms for 61 hosts over 500,000 flows).
const distinctHostsContainingSQL = `WITH RECURSIVE h(x) AS (
	SELECT MIN(host) FROM flows
	UNION ALL
	SELECT (SELECT MIN(host) FROM flows WHERE host > h.x) FROM h WHERE h.x IS NOT NULL)
SELECT x FROM h WHERE x IS NOT NULL AND instr(lower(x), lower(?)) > 0 LIMIT ?`

// httpsOKRecentProbe is how many of the newest intercepted HTTPS flows are
// checked for the host before the per-host lookup (about 0.8 µs each).
const httpsOKRecentProbe = 2000

// recentHTTPSOKForHostSQL is the newest success of a host among the newest
// httpsOKRecentProbe successes of any host. A hit is the host's newest
// success overall, since every flow outside the probe is older.
var recentHTTPSOKForHostSQL = `SELECT COALESCE(MAX(ts),0) FROM (SELECT ts, host` + httpsOKFrom + httpsOKPredicate +
	` ORDER BY ts DESC LIMIT ?) WHERE instr(lower(host), lower(?)) > 0`

// httpsOKHostListMax is the most matching hosts named in one IN list.
const httpsOKHostListMax = 400

// httpsOKHostFlowsMax is the most flows the matching hosts may hold for the
// host-index plan: it visits each of those flows (about 1.1 µs each to count
// them, as much again to read them), so this bounds it near 50 ms. A var so
// tests can lower it.
var httpsOKHostFlowsMax int64 = 20000

// httpsOKHostSource returns the FROM/WHERE text and arguments that select the
// successfully intercepted HTTPS flows of the hosts containing hostSubstring.
// none is true when no stored host matches, so the answer is zero without
// reading a flow.
//
// The plain form, "walk idx_flows_https_ok and test each row's host", reads
// every intercepted flow when the filter matches few or none of them: 257 ms
// to count and 408 ms to find the newest on 500,000 flows for a host that is
// not in History, and the same for a pinned host that has only failures,
// which is the host the diagnosis is usually asked about. So the matching
// hosts are resolved first; when they hold few flows, those flows are read
// through idx_flows_host instead (63 µs and 47 µs for the pinned host). A
// filter matching many flows keeps the plain form, where a match is found
// within the first rows of the walk. Both forms select the same rows: a flow
// matches the substring exactly when its host is one of the matching hosts.
func (s *Store) httpsOKHostSource(hostSubstring string) (from string, args []any, none bool, err error) {
	plain := httpsOKFrom + httpsOKPredicate + httpsOKHostClause
	rows, err := s.db.Query(distinctHostsContainingSQL, hostSubstring, httpsOKHostListMax+1)
	if err != nil {
		return "", nil, false, err
	}
	defer rows.Close()
	var hosts []any
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return "", nil, false, err
		}
		hosts = append(hosts, h)
	}
	if err := rows.Err(); err != nil {
		return "", nil, false, err
	}
	if len(hosts) == 0 {
		return "", nil, true, nil
	}
	if len(hosts) > httpsOKHostListMax {
		return plain, []any{hostSubstring}, false, nil
	}
	in := ` FROM flows INDEXED BY idx_flows_host WHERE host IN (` + strings.TrimRight(strings.Repeat("?,", len(hosts)), ",") + `)`
	var held int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM (SELECT 1`+in+` LIMIT ?)`,
		append(append([]any{}, hosts...), httpsOKHostFlowsMax+1)...).Scan(&held); err != nil {
		return "", nil, false, err
	}
	if held > httpsOKHostFlowsMax {
		return plain, []any{hostSubstring}, false, nil
	}
	return in + ` AND ` + httpsOKPredicate, hosts, false, nil
}

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
		from, hostArgs, none, err := s.httpsOKHostSource(hostSubstring)
		if err != nil || none {
			return 0, err
		}
		q, args = `SELECT COUNT(*)`+from, hostArgs
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
		from, hostArgs, none, err := s.httpsOKHostSource(hostSubstring)
		if err != nil || none {
			return 0, err
		}
		q = `SELECT COUNT(*) FROM (SELECT 1` + from + ` LIMIT ?)`
		args = append(hostArgs, limit)
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
	q2 := newestHTTPSOKSQL
	var args2 []any
	if hostSubstring != "" {
		// A host with recent intercepted traffic is answered by the newest
		// entries of the success index; only a host without any there needs
		// the per-host lookup.
		if err = s.db.QueryRow(recentHTTPSOKForHostSQL, httpsOKRecentProbe, hostSubstring).Scan(&newestSuccessMS); err != nil {
			return 0, 0, 0, err
		}
		if newestSuccessMS > 0 {
			return newestFailureMS, recentFailures, newestSuccessMS, nil
		}
		from, hostArgs, none, herr := s.httpsOKHostSource(hostSubstring)
		if herr != nil {
			return 0, 0, 0, herr
		}
		if none {
			return newestFailureMS, recentFailures, 0, nil
		}
		q2, args2 = `SELECT COALESCE(MAX(ts),0)`+from, hostArgs
	}
	if err = s.db.QueryRow(q2, args2...).Scan(&newestSuccessMS); err != nil {
		return 0, 0, 0, err
	}
	return newestFailureMS, recentFailures, newestSuccessMS, nil
}
