package store

import (
	"errors"
	"log"
	"time"
)

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
// The retention DELETE runs once this many frames have been inserted since the
// previous trim, not on every frame.
var wsFramesPerFlow int64 = 5000

const (
	wsFrameBatchSize  = 64
	wsFrameBufCap     = 4096
	wsFrameFlushDelay = 500 * time.Millisecond
)

var errWSFramesClosed = errors.New("ws frame capture closed")

type wsFlushOp struct {
	close bool
	errc  chan error
}

// SaveWSFrame enqueues a captured frame for batched persistence. It never
// blocks on SQLite: a full buffer drops the oldest pending frame and counts
// it. Persistence errors surface from FlushWSFrames / Close, not here.
func (s *Store) SaveWSFrame(f *WSFrame) error {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	if s.wsClosed || s.wsWake == nil {
		return errWSFramesClosed
	}
	if len(s.wsPending) >= wsFrameBufCap {
		s.wsPending[0] = nil
		s.wsPending = s.wsPending[1:]
		n := s.wsDropped.Add(1)
		if n == 1 || n%1024 == 0 {
			log.Printf("store: ws frame buffer full, dropped %d oldest frame(s)", n)
		}
	}
	s.wsPending = append(s.wsPending, f)
	select {
	case s.wsWake <- struct{}{}:
	default:
	}
	return nil
}

// FlushWSFrames writes every pending frame before returning. Tests use it to
// observe a batch without waiting for the size or timer trigger.
func (s *Store) FlushWSFrames() error {
	s.wsCmdMu.Lock()
	defer s.wsCmdMu.Unlock()
	s.wsMu.Lock()
	closed := s.wsClosed
	s.wsMu.Unlock()
	if closed || s.wsOp == nil {
		return errWSFramesClosed
	}
	errc := make(chan error, 1)
	s.wsOp <- wsFlushOp{errc: errc}
	return <-errc
}

// SetWSFlushNotify registers a callback invoked after a batch commits, with
// the distinct flow ids in that batch. The callback runs outside the store
// lock; a panic is recovered so capture keeps running.
func (s *Store) SetWSFlushNotify(fn func([]int64)) {
	s.wsMu.Lock()
	s.wsNotify = fn
	s.wsMu.Unlock()
}

func (s *Store) startWSFlusher() {
	s.wsWake = make(chan struct{}, 1)
	s.wsOp = make(chan wsFlushOp)
	s.wsDone = make(chan struct{})
	s.wsDirty = map[int64]struct{}{}
	go s.wsFlusher()
}

func (s *Store) shutdownWS() error {
	var err error
	s.wsStopOnce.Do(func() {
		s.wsCmdMu.Lock()
		defer s.wsCmdMu.Unlock()
		if s.wsDone == nil {
			s.wsMu.Lock()
			s.wsClosed = true
			s.wsMu.Unlock()
			return
		}
		s.wsMu.Lock()
		s.wsClosed = true
		s.wsMu.Unlock()
		errc := make(chan error, 1)
		s.wsOp <- wsFlushOp{close: true, errc: errc}
		err = <-errc
		<-s.wsDone
	})
	return err
}

func (s *Store) wsFlusher() {
	defer close(s.wsDone)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	armed := false
	disarm := func() {
		if !armed {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		armed = false
	}
	flush := func() error {
		err := s.flushWSPending()
		if err != nil {
			log.Printf("store: persist ws frames: %v", err)
		}
		return err
	}
	for {
		s.wsMu.Lock()
		n := len(s.wsPending)
		paused := s.wsPaused
		s.wsMu.Unlock()
		if !paused && n >= wsFrameBatchSize {
			disarm()
			flush()
			continue
		}
		select {
		case <-s.wsWake:
			if paused {
				continue
			}
			s.wsMu.Lock()
			n = len(s.wsPending)
			s.wsMu.Unlock()
			if n >= wsFrameBatchSize {
				disarm()
				flush()
				continue
			}
			if n > 0 && !armed {
				timer.Reset(wsFrameFlushDelay)
				armed = true
			}
		case <-timer.C:
			armed = false
			if paused {
				continue
			}
			flush()
		case op := <-s.wsOp:
			disarm()
			err := flush()
			if op.close {
				op.errc <- err
				return
			}
			op.errc <- err
		}
	}
}

func (s *Store) flushWSPending() error {
	s.wsMu.Lock()
	batch := s.wsPending
	s.wsPending = nil
	fn := s.wsNotify
	s.wsMu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	if err := s.writeWSBatch(batch); err != nil {
		return err
	}
	if fn == nil {
		return nil
	}
	seen := make(map[int64]struct{}, 4)
	ids := make([]int64, 0, 4)
	for _, f := range batch {
		if _, ok := seen[f.FlowID]; ok {
			continue
		}
		seen[f.FlowID] = struct{}{}
		ids = append(ids, f.FlowID)
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("store: ws flush notify panic: %v", r)
			}
		}()
		fn(ids)
	}()
	return nil
}

func (s *Store) writeWSBatch(batch []*WSFrame) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids := make([]int64, len(batch))
	for i, f := range batch {
		res, err := tx.Exec(
			`INSERT INTO ws_frames (flow_id, ts, dir, opcode, length, preview) VALUES (?,?,?,?,?,?)`,
			f.FlowID, f.TS.UnixMilli(), f.Dir, f.Opcode, f.Length, f.Preview)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		ids[i] = id
	}
	since := s.wsInserted + int64(len(batch))
	doTrim := since >= wsFramesPerFlow
	if doTrim {
		seen := make(map[int64]struct{}, len(s.wsDirty)+len(batch))
		for id := range s.wsDirty {
			seen[id] = struct{}{}
		}
		for _, f := range batch {
			seen[f.FlowID] = struct{}{}
		}
		for flowID := range seen {
			if _, err := tx.Exec(
				`DELETE FROM ws_frames WHERE flow_id=? AND id NOT IN (
				   SELECT id FROM ws_frames WHERE flow_id=? ORDER BY id DESC LIMIT ?)`,
				flowID, flowID, wsFramesPerFlow); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for i, f := range batch {
		f.ID = ids[i]
	}
	if doTrim {
		s.wsInserted = 0
		s.wsDirty = map[int64]struct{}{}
	} else {
		s.wsInserted = since
		if s.wsDirty == nil {
			s.wsDirty = map[int64]struct{}{}
		}
		for _, f := range batch {
			s.wsDirty[f.FlowID] = struct{}{}
		}
	}
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

// WSFramesDropped reports how many frames were discarded because the pending
// buffer was full. Capture stays non-blocking; the count is the only signal.
func (s *Store) WSFramesDropped() uint64 {
	return s.wsDropped.Load()
}
