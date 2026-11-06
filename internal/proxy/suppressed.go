package proxy

import (
	"sort"
	"sync"
	"time"
)

// SuppressedRequest is one request the proxy forwarded but did not persist
// because a filter dropped it. The reason says which filter, so "the app sent
// nothing" can be told apart from "we filtered it" without reading /proc.
type SuppressedRequest struct {
	TS     int64  `json:"ts"`
	Host   string `json:"host"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Suppression reasons. Self traffic (our own loopback listeners) is expected and
// is not recorded: it would drown the signal.
const (
	SuppressedOutOfScope        = "out-of-scope"
	SuppressedBrowserTelemetry  = "browser-telemetry"
	SuppressedAndroidTelemetry  = "android-telemetry"
	suppressedRingSize          = 512
	suppressedSampleLimit       = 25
)

// suppressedLog is a bounded ring of recently dropped requests.
type suppressedLog struct {
	mu   sync.Mutex
	ring []SuppressedRequest
	next int
}

// Add records one dropped request, overwriting the oldest entry when full.
func (s *Server) recordSuppressed(req SuppressedRequest) {
	if s == nil || req.Host == "" {
		return
	}
	req.TS = time.Now().UnixMilli()
	// Take the address: suppressedLog carries a mutex, and copying it would
	// append to a throwaway ring.
	sl := &s.suppressed
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if len(sl.ring) < suppressedRingSize {
		sl.ring = append(sl.ring, req)
		return
	}
	sl.ring[sl.next] = req
	sl.next = (sl.next + 1) % suppressedRingSize
}

// SuppressedHostStat aggregates recent drops for one host and reason.
type SuppressedHostStat struct {
	Host     string `json:"host"`
	Reason   string `json:"reason"`
	Count    int64  `json:"count"`
	NewestTS int64  `json:"newestTs"`
}

// SuppressedReport is what the API, the readiness pill and the History banner read.
type SuppressedReport struct {
	WindowSec int                  `json:"windowSec"`
	Total     int64                `json:"total"`
	Hosts     []SuppressedHostStat `json:"hosts"`
	Samples   []SuppressedRequest  `json:"samples"`
}

// RecentSuppressed aggregates the drops inside the window, newest host first.
func (s *Server) RecentSuppressed(window time.Duration) SuppressedReport {
	if window <= 0 {
		window = 15 * time.Minute
	}
	cutoff := time.Now().Add(-window).UnixMilli()
	rep := SuppressedReport{WindowSec: int(window.Seconds()), Hosts: []SuppressedHostStat{}, Samples: []SuppressedRequest{}}
	sl := &s.suppressed
	sl.mu.Lock()
	defer sl.mu.Unlock()
	type key struct{ host, reason string }
	index := map[key]int{}
	for _, e := range sl.ring {
		if e.TS < cutoff {
			continue
		}
		k := key{e.Host, e.Reason}
		i, ok := index[k]
		if !ok {
			index[k] = len(rep.Hosts)
			rep.Hosts = append(rep.Hosts, SuppressedHostStat{Host: e.Host, Reason: e.Reason, Count: 1, NewestTS: e.TS})
		} else {
			rep.Hosts[i].Count++
			if e.TS > rep.Hosts[i].NewestTS {
				rep.Hosts[i].NewestTS = e.TS
			}
		}
		rep.Total++
	}
	// Newest first, then by count, so the banner and the list agree.
	sortSuppressed(rep.Hosts)
	// Samples: the newest few distinct requests, for "what exactly was dropped".
	for i := len(sl.ring) - 1; i >= 0 && len(rep.Samples) < suppressedSampleLimit; i-- {
		e := sl.ring[i]
		if e.TS >= cutoff {
			rep.Samples = append(rep.Samples, e)
		}
	}
	return rep
}

// sortSuppressed orders host stats newest-first, then by count.
func sortSuppressed(stats []SuppressedHostStat) {
	sort.SliceStable(stats, func(i, j int) bool {
		if stats[i].NewestTS != stats[j].NewestTS {
			return stats[i].NewestTS > stats[j].NewestTS
		}
		return stats[i].Count > stats[j].Count
	})
}
