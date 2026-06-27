package store

import "time"

// WSFrame is one captured WebSocket frame. Dir is "send" (client→server) or
// "recv" (server→client). Preview holds a bounded prefix of the (unmasked)
// payload; Length is the full frame payload length.
type WSFrame struct {
	ID      int64     `json:"id"`
	FlowID  int64     `json:"flowId"`
	TS      time.Time `json:"-"`
	Dir     string    `json:"dir"`
	Opcode  int       `json:"opcode"`
	Length  int64     `json:"length"`
	Preview string    `json:"preview"`
}

// wsFramesPerFlow bounds how many frames are retained per flow so a long-lived
// WebSocket can't grow ws_frames without bound. A var so tests can lower it.
var wsFramesPerFlow int64 = 5000

// SaveWSFrame records a captured frame, trimming the flow to the most recent
// wsFramesPerFlow frames.
func (s *Store) SaveWSFrame(f *WSFrame) error {
	res, err := s.db.Exec(
		`INSERT INTO ws_frames (flow_id, ts, dir, opcode, length, preview) VALUES (?,?,?,?,?,?)`,
		f.FlowID, f.TS.UnixMilli(), f.Dir, f.Opcode, f.Length, f.Preview)
	if err != nil {
		return err
	}
	f.ID, _ = res.LastInsertId()
	_, _ = s.db.Exec(
		`DELETE FROM ws_frames WHERE flow_id=? AND id NOT IN (
		   SELECT id FROM ws_frames WHERE flow_id=? ORDER BY id DESC LIMIT ?)`,
		f.FlowID, f.FlowID, wsFramesPerFlow)
	return nil
}

// QueryWSFrames returns up to limit frames for a flow, oldest first.
func (s *Store) QueryWSFrames(flowID int64, limit int) ([]*WSFrame, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.Query(
		`SELECT id, flow_id, ts, dir, opcode, length, preview
		 FROM ws_frames WHERE flow_id = ? ORDER BY id LIMIT ?`, flowID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WSFrame
	for rows.Next() {
		var f WSFrame
		var ms int64
		if err := rows.Scan(&f.ID, &f.FlowID, &ms, &f.Dir, &f.Opcode, &f.Length, &f.Preview); err != nil {
			return nil, err
		}
		f.TS = time.UnixMilli(ms).UTC()
		out = append(out, &f)
	}
	return out, rows.Err()
}
