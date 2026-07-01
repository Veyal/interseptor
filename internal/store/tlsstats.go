package store

// CountFlowsWithFlag returns how many flows have any of the given flag bits set.
func (s *Store) CountFlowsWithFlag(flag int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM flows WHERE (flags & ?) != 0`, flag).Scan(&n)
	return n, err
}

// CountSuccessfulHTTPS returns flows where HTTPS MITM completed (non-zero status,
// not a recorded TLS handshake failure).
func (s *Store) CountSuccessfulHTTPS(hostSubstring string) (int64, error) {
	var n int64
	q := `SELECT COUNT(*) FROM flows WHERE scheme = 'https' AND status > 0 AND (flags & ?) = 0`
	args := []any{FlagTLSFailed}
	if hostSubstring != "" {
		q += ` AND instr(lower(host), lower(?)) > 0`
		args = append(args, hostSubstring)
	}
	err := s.db.QueryRow(q, args...).Scan(&n)
	return n, err
}
