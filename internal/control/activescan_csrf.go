package control

import (
	"net/url"
	"strings"

	"github.com/Veyal/interseptor/internal/activescan/csrf"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

// csrfHeadersForHost returns Laravel/session CSRF material for active-scan probes.
func (h *Hub) csrfHeadersForHost(host string) csrf.Headers {
	flows, _ := h.st.QueryFlowsFilter(store.FlowFilter{
		Host:         host,
		Limit:        100,
		ExcludeFlags: store.FlagRepeater | store.FlagIntruder | store.FlagActiveScan,
	})
	for _, f := range flows {
		if h := csrf.FromFlowRequest(f.ReqHeaders); !h.Empty() {
			return h
		}
	}
	scheme := "https"
	port := 443
	for _, f := range flows {
		if f.Scheme != "" {
			scheme = f.Scheme
			port = f.Port
			break
		}
	}
	base := scheme + "://" + host
	if (scheme == "https" && port != 443) || (scheme == "http" && port != 80) {
		base += ":" + itoa64(int64(port))
	}
	for _, path := range []string{"/register", "/login", "/"} {
		flow, err := h.snd.Send(sender.Request{
			Method: "GET", URL: base + path, NoSession: true, Flags: store.FlagActiveScan,
		})
		if err != nil || flow == nil {
			continue
		}
		var sets []string
		for _, v := range flow.ResHeaders["Set-Cookie"] {
			sets = append(sets, v)
		}
		if h := csrf.FromSetCookies(sets); !h.Empty() {
			return h
		}
	}
	return csrf.Headers{}
}

func mutatingMethod(m string) bool {
	switch strings.ToUpper(m) {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}

func hostFromTargetURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
