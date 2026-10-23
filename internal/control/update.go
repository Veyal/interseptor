package control

import (
	"context"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/version"
)

// SelfUpdater is the self-update surface the control plane drives. cmd wires
// version.Updater (bound to the resolved executable path); tests inject fakes.
type SelfUpdater interface {
	Install() version.InstallInfo
	Latest(ctx context.Context) (string, error)
	Notes(ctx context.Context, ver string) (version.ReleaseNotes, error)
	Stage(ctx context.Context, ver string, progress func(version.StageProgress)) (version.StagedUpdate, error)
}

// Update phases beyond the version package's download/verify/install steps.
const (
	updatePhaseIdle  = "idle"
	updatePhaseReady = "ready"
	updatePhaseError = "error"
)

const (
	updateStageTimeout   = 10 * time.Minute
	updateLookupTimeout  = 8 * time.Second
	updateProgressPeriod = 100 * time.Millisecond // at most 10 progress events a second
	updateRestartDelay   = 300 * time.Millisecond // let the 202 flush before the re-exec
)

// updateJob is the single-flight state of a web-initiated self-update.
type updateJob struct {
	mu       sync.Mutex
	phase    string
	done     int64
	total    int64
	err      string
	staged   version.StagedUpdate
	cancel   context.CancelFunc
	notes    map[string]version.ReleaseNotes
	throttle progressThrottle
}

type updateState struct {
	Phase string `json:"phase"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Error string `json:"error,omitempty"`
}

func (j *updateJob) snapshot() updateState {
	j.mu.Lock()
	defer j.mu.Unlock()
	phase := j.phase
	if phase == "" {
		phase = updatePhaseIdle
	}
	return updateState{Phase: phase, Done: j.done, Total: j.total, Error: j.err}
}

func (j *updateJob) running() bool {
	switch j.phase {
	case version.PhaseDownloading, version.PhaseVerifying, version.PhaseInstalling:
		return true
	}
	return false
}

// stop cancels a running stage (hub shutdown).
func (j *updateJob) stop() {
	j.mu.Lock()
	cancel := j.cancel
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// progressThrottle passes every phase change and at most one same-phase event
// per updateProgressPeriod.
type progressThrottle struct {
	phase string
	last  time.Time
}

func (t *progressThrottle) allow(phase string, now time.Time) bool {
	if phase != t.phase || now.Sub(t.last) >= updateProgressPeriod {
		t.phase, t.last = phase, now
		return true
	}
	return false
}

// updateView is the GET /api/update body.
type updateView struct {
	Current   string      `json:"current"`
	Latest    string      `json:"latest"`
	Available bool        `json:"available"`
	Notes     string      `json:"notes"`
	URL       string      `json:"url"`
	Method    string      `json:"method"`
	Command   string      `json:"command,omitempty"`
	Reason    string      `json:"reason,omitempty"`
	State     updateState `json:"state"`
}

// latestRelease returns the newest release known to the hub, asking the
// updater when the startup check has not produced one yet.
func (h *Hub) latestRelease(ctx context.Context) string {
	h.updMu.Lock()
	latest := h.updLatest
	h.updMu.Unlock()
	if latest != "" || h.Updater == nil {
		return latest
	}
	got, err := h.Updater.Latest(ctx)
	if err != nil || got == "" {
		return ""
	}
	h.SetUpdate(got, version.IsNewer(version.String(), got))
	return got
}

// releaseNotes returns (and caches per version) the notes for ver.
func (h *Hub) releaseNotes(ctx context.Context, ver string) version.ReleaseNotes {
	h.upd.mu.Lock()
	n, ok := h.upd.notes[ver]
	h.upd.mu.Unlock()
	if ok {
		return n
	}
	n, err := h.Updater.Notes(ctx, ver)
	if n.URL == "" {
		n.URL = version.ReleasePageURL(ver)
	}
	if err != nil {
		return n // not cached: the next dialog open retries
	}
	h.upd.mu.Lock()
	if h.upd.notes == nil {
		h.upd.notes = map[string]version.ReleaseNotes{}
	}
	h.upd.notes[ver] = n
	h.upd.mu.Unlock()
	return n
}

// apiUpdateStatus serves GET /api/update.
func (h *metaAPI) apiUpdateStatus(w http.ResponseWriter, r *http.Request) {
	v := updateView{Current: version.String(), Method: "unavailable", State: h.upd.snapshot()}
	if h.Updater == nil {
		v.Reason = "Self-update is not available in this process."
		writeJSON(w, http.StatusOK, v)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), updateLookupTimeout)
	defer cancel()
	info := h.Updater.Install()
	v.Method, v.Command, v.Reason = string(info.Method), info.Command, info.Reason
	v.Latest = h.latestRelease(ctx)
	v.Available = v.Latest != "" && version.IsNewer(v.Current, v.Latest)
	if v.Latest != "" {
		n := h.releaseNotes(ctx, v.Latest)
		v.Notes, v.URL = n.Body, n.URL
	}
	writeJSON(w, http.StatusOK, v)
}

// localOperator reports the unauthenticated local path: a loopback connection
// addressed to a loopback Host. A collaborator signed in through a tunnel also
// arrives on loopback, but with the tunnel's Host, and is not the operator.
func localOperator(r *http.Request) bool {
	la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	return ok && la != nil && isLoopbackAddr(la) && isLoopbackHost(r.Host)
}

// refuseAgentUpdate rejects self-update requests from AI/MCP callers, API keys
// and remote sessions: replacing and restarting the tool is a decision for the
// operator at this machine.
func refuseAgentUpdate(w http.ResponseWriter, r *http.Request) bool {
	if isAISource(r) || bearerToken(r) != "" || !localOperator(r) {
		httpErr(w, http.StatusForbidden, "self-update must be started by the operator from the Interseptor UI")
		return true
	}
	return false
}

// apiUpdateStart serves POST /api/update: download, verify and install the
// latest release in the background, reporting update.progress over SSE.
func (h *metaAPI) apiUpdateStart(w http.ResponseWriter, r *http.Request) {
	if refuseAgentUpdate(w, r) {
		return
	}
	if h.Updater == nil {
		httpErr(w, http.StatusNotImplemented, "self-update is not available in this process")
		return
	}
	if info := h.Updater.Install(); !info.SelfUpdatable() {
		msg := info.Reason
		if info.Command != "" {
			msg += " Run: " + info.Command
		}
		httpErr(w, http.StatusConflict, msg)
		return
	}
	lookup, cancel := context.WithTimeout(r.Context(), updateLookupTimeout)
	latest := h.latestRelease(lookup)
	cancel()
	if latest == "" || !version.IsNewer(version.String(), latest) {
		httpErr(w, http.StatusConflict, "Interseptor v"+version.String()+" is already up to date")
		return
	}
	if msg := h.Hub.beginUpdate(latest); msg != "" {
		httpErr(w, http.StatusConflict, msg)
		return
	}
	writeJSON(w, http.StatusAccepted, h.upd.snapshot())
}

// beginUpdate claims the single update slot and starts staging target. It
// returns a refusal message when an update is running or already staged.
func (h *Hub) beginUpdate(target string) string {
	ctx, cancel := context.WithTimeout(context.Background(), updateStageTimeout)
	j := &h.upd
	j.mu.Lock()
	switch {
	case j.running():
		j.mu.Unlock()
		cancel()
		return "an update is already in progress"
	case j.phase == updatePhaseReady:
		j.mu.Unlock()
		cancel()
		return "update already installed — restart Interseptor to finish"
	}
	j.phase, j.done, j.total, j.err = version.PhaseDownloading, 0, 0, ""
	j.cancel = cancel
	j.throttle = progressThrottle{}
	j.mu.Unlock()
	go h.runUpdate(ctx, cancel, target)
	return ""
}

func (h *Hub) runUpdate(ctx context.Context, cancel context.CancelFunc, target string) {
	defer cancel()
	staged, err := h.Updater.Stage(ctx, target, h.noteUpdateProgress)
	j := &h.upd
	j.mu.Lock()
	j.cancel = nil
	if err != nil {
		j.phase, j.err = updatePhaseError, err.Error()
	} else {
		j.phase, j.staged = updatePhaseReady, staged
	}
	ev := j.eventLocked()
	j.mu.Unlock()
	if err != nil {
		log.Printf("control: self-update to v%s failed: %v", target, err)
	}
	h.broadcast(ev)
}

// noteUpdateProgress records a stage report and broadcasts it, throttled.
func (h *Hub) noteUpdateProgress(p version.StageProgress) {
	j := &h.upd
	j.mu.Lock()
	if !j.running() { // a late report after the job settled
		j.mu.Unlock()
		return
	}
	j.phase, j.done, j.total = p.Phase, p.Done, p.Total
	send := j.throttle.allow(p.Phase, time.Now())
	ev := j.eventLocked()
	j.mu.Unlock()
	if send {
		h.broadcast(ev)
	}
}

func (j *updateJob) eventLocked() map[string]any {
	ev := map[string]any{"type": "update.progress", "phase": j.phase, "done": j.done, "total": j.total}
	if j.err != "" {
		ev["error"] = j.err
	}
	return ev
}

// apiUpdateRestart serves POST /api/update/restart: once an update is staged,
// answer 202 and relaunch the new binary on the same project and ports.
func (h *metaAPI) apiUpdateRestart(w http.ResponseWriter, r *http.Request) {
	if refuseAgentUpdate(w, r) {
		return
	}
	h.upd.mu.Lock()
	phase, staged := h.upd.phase, h.upd.staged
	h.upd.mu.Unlock()
	if phase != updatePhaseReady {
		httpErr(w, http.StatusConflict, "no update is ready to install")
		return
	}
	restart := h.RestartForUpdate
	if restart == nil {
		httpErr(w, http.StatusNotImplemented, "restart is not available in this process; quit and start Interseptor again")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"restarting": true, "version": staged.Version})
	h.Hub.scheduleRelaunch(updateRestartDelay, "update restart", func() error { return restart(staged) })
}
