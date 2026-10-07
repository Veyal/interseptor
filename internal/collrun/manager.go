package collrun

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// Manager owns asynchronous runs for a long-lived process (the control
// server). The control layer wraps it in REST/SSE handlers; the CLI uses
// Runner directly. Runs are not tied to the HTTP request that started them.
type Manager struct {
	// MaxActive caps concurrently running runs (default 4).
	MaxActive int
	// Keep is how many finished runs stay queryable in memory (default 20).
	Keep int

	mu     sync.Mutex
	runs   map[string]*LiveRun
	order  []string
	ctx    context.Context
	cancel context.CancelFunc
}

// NewManager returns a Manager. Close aborts every active run.
func NewManager() *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{MaxActive: 4, Keep: 20, runs: map[string]*LiveRun{}, ctx: ctx, cancel: cancel}
}

// Live statuses beyond the run statuses.
const (
	LivePaused          = "paused"
	LiveAwaitingPersist = "awaiting_persist"
)

// Progress is a point-in-time view of a live run. Items holds results from
// offset Since onward, so a poller never re-downloads what it has.
type Progress struct {
	RunUID   string          `json:"runUid"`
	Status   string          `json:"status"`
	Totals   Totals          `json:"totals"`
	Planned  int             `json:"plannedSteps"`
	Since    int             `json:"since"`
	Count    int             `json:"count"` // total items so far
	Items    []ItemResult    `json:"items"`
	Pending  []VarChangeView `json:"pendingWrites,omitempty"` // persist=ask: waiting for a decision
	Finished bool            `json:"finished"`
	Report   *Report         `json:"report,omitempty"` // set once finished
	Error    string          `json:"error,omitempty"`
}

// LiveRun is one run started through a Manager.
type LiveRun struct {
	UID    string
	runner *Runner

	mu          sync.Mutex
	status      string
	totals      Totals
	planned     int
	items       []ItemResult
	pending     []VarChangeView
	report      *Report
	err         error
	subs        map[int]chan Event
	nextSub     int
	decide      chan bool
	started     chan struct{}
	done        chan struct{}
	startedOnce sync.Once
	finished    time.Time
}

// ErrTooManyRuns is returned when MaxActive runs are already in flight.
var ErrTooManyRuns = errors.New("collrun: too many active runs")

// Start launches a run and returns once it has an id (or has already ended,
// for example when scripts are not approved).
func (m *Manager) Start(b Backend, rs RunStore, o Options) (*LiveRun, error) {
	m.mu.Lock()
	active := 0
	for _, r := range m.runs {
		if !r.isFinished() {
			active++
		}
	}
	max := m.MaxActive
	if max <= 0 {
		max = 4
	}
	if active >= max {
		m.mu.Unlock()
		return nil, ErrTooManyRuns
	}
	m.mu.Unlock()

	lr := &LiveRun{runner: New(b, rs), status: "starting", subs: map[int]chan Event{},
		decide: make(chan bool, 1), started: make(chan struct{}), done: make(chan struct{})}
	user := o.OnEvent
	o.OnEvent = func(e Event) {
		lr.onEvent(e)
		if user != nil {
			user(e)
		}
	}
	if o.Persist == PersistAsk && o.Decide == nil {
		o.Decide = lr.waitDecision
	}
	go func() {
		rep, err := lr.runner.Run(m.ctx, o)
		lr.finish(rep, err)
		m.remember(lr)
	}()
	select {
	case <-lr.started:
	case <-lr.done:
	}
	lr.mu.Lock()
	startErr, uid := lr.err, lr.UID
	lr.mu.Unlock()
	if startErr != nil {
		return nil, startErr
	}
	m.mu.Lock()
	if uid != "" {
		m.runs[uid] = lr
	}
	m.mu.Unlock()
	return lr, nil
}

// remember registers a finished run (covers runs that ended before Start
// returned) and trims old ones.
func (m *Manager) remember(lr *LiveRun) {
	lr.mu.Lock()
	uid := lr.UID
	lr.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if uid == "" {
		return
	}
	if _, ok := m.runs[uid]; !ok {
		m.runs[uid] = lr
	}
	m.order = append(m.order, uid)
	keep := m.Keep
	if keep <= 0 {
		keep = 20
	}
	var fin []string
	for _, id := range m.order {
		if r, ok := m.runs[id]; ok && r.isFinished() {
			fin = append(fin, id)
		}
	}
	for len(fin) > keep {
		delete(m.runs, fin[0])
		fin = fin[1:]
	}
	var ord []string
	for _, id := range m.order {
		if _, ok := m.runs[id]; ok {
			ord = append(ord, id)
		}
	}
	m.order = ord
}

// Get returns a run by uid.
func (m *Manager) Get(uid string) (*LiveRun, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[uid]
	return r, ok
}

// Active lists the uids of unfinished runs.
func (m *Manager) Active() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for id, r := range m.runs {
		if !r.isFinished() {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Close aborts every active run.
func (m *Manager) Close() { m.cancel() }

// ---- LiveRun ---------------------------------------------------------------------

func (l *LiveRun) isFinished() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

func (l *LiveRun) onEvent(e Event) {
	l.mu.Lock()
	switch e.Type {
	case "start":
		l.UID, l.status, l.planned = e.RunUID, StatusRunning, e.Total
	case "item":
		if e.Item != nil {
			l.items = append(l.items, *e.Item)
		}
		l.totals = e.Totals
	case "done":
		l.totals = e.Totals
	}
	subs := make([]chan Event, 0, len(l.subs))
	for _, ch := range l.subs {
		subs = append(subs, ch)
	}
	l.mu.Unlock()
	if e.Type == "start" {
		l.startedOnce.Do(func() { close(l.started) })
	}
	// Subscribers never block the runner: a full channel drops the event (the
	// consumer re-syncs from Snapshot).
	for _, ch := range subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (l *LiveRun) finish(rep *Report, err error) {
	l.mu.Lock()
	l.report, l.err, l.finished = rep, err, time.Now()
	if rep != nil {
		l.status = rep.Status
		l.totals = rep.Totals
		if l.UID == "" {
			l.UID = rep.RunUID
		}
	} else {
		l.status = StatusAborted
	}
	l.pending = nil
	subs := l.subs
	l.subs = map[int]chan Event{}
	l.mu.Unlock()
	close(l.done)
	for _, ch := range subs {
		close(ch)
	}
}

// Subscribe streams events until the run ends (the channel is then closed).
// cancel releases the subscription early.
func (l *LiveRun) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	l.mu.Lock()
	if l.isFinished() {
		l.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	l.nextSub++
	id := l.nextSub
	l.subs[id] = ch
	l.mu.Unlock()
	return ch, func() {
		l.mu.Lock()
		if c, ok := l.subs[id]; ok {
			delete(l.subs, id)
			close(c)
		}
		l.mu.Unlock()
	}
}

// Snapshot returns the run's progress; items from offset since.
func (l *LiveRun) Snapshot(since int) Progress {
	l.mu.Lock()
	defer l.mu.Unlock()
	if since < 0 || since > len(l.items) {
		since = 0
	}
	p := Progress{RunUID: l.UID, Status: l.status, Totals: l.totals, Planned: l.planned, Since: since, Count: len(l.items),
		Items: append([]ItemResult(nil), l.items[since:]...), Pending: append([]VarChangeView(nil), l.pending...),
		Finished: l.isFinished(), Report: l.report}
	if l.err != nil {
		p.Error = l.err.Error()
	}
	return p
}

// Pause holds the run before its next request.
func (l *LiveRun) Pause() {
	l.runner.Pause()
	l.setStatusIf(StatusRunning, LivePaused)
}

// Resume continues a paused run.
func (l *LiveRun) Resume() {
	l.runner.Resume()
	l.setStatusIf(LivePaused, StatusRunning)
}

// Abort cancels the run, including an in-flight request.
func (l *LiveRun) Abort() { l.runner.Abort() }

func (l *LiveRun) setStatusIf(from, to string) {
	l.mu.Lock()
	if l.status == from {
		l.status = to
	}
	l.mu.Unlock()
}

// Decide answers a persist=ask prompt: keep the script variable writes or
// discard them. It reports false when the run is not waiting.
func (l *LiveRun) Decide(keep bool) bool {
	l.mu.Lock()
	waiting := l.status == LiveAwaitingPersist
	l.mu.Unlock()
	if !waiting {
		return false
	}
	select {
	case l.decide <- keep:
		return true
	default:
		return false
	}
}

// waitDecision blocks the run's final step until Decide is called (or the run
// is aborted: no answer means discard).
func (l *LiveRun) waitDecision(pending []VarChangeView) bool {
	l.mu.Lock()
	l.status, l.pending = LiveAwaitingPersist, append([]VarChangeView(nil), pending...)
	l.mu.Unlock()
	select {
	case keep := <-l.decide:
		return keep
	case <-l.runner.abortCh():
		return false
	}
}

// Wait blocks until the run ends or ctx is done.
func (l *LiveRun) Wait(ctx context.Context) (*Report, error) {
	select {
	case <-l.done:
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.report, l.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
