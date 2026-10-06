package store

// WSFlowCount returns how many distinct flows have at least one persisted
// WebSocket frame. Frames still in the async write buffer are not counted.
func (s *Store) WSFlowCount() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(DISTINCT flow_id) FROM ws_frames`).Scan(&n)
	return n, err
}
