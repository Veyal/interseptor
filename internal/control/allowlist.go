package control

import (
	"net"
	"net/http"
	"strconv"

	"github.com/Veyal/interseptor/internal/store"
)

const maxAllowlistRequestBytes = 16 << 10

func (h *metaAPI) listAllowlist(w http.ResponseWriter, r *http.Request) {
	entries, err := h.st.ListIPAllowlist()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":  entries,
		"clientIP": clientIP(r),
	})
}

func (h *metaAPI) createAllowlist(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CIDR  string `json:"cidr"`
		Label string `json:"label"`
	}
	if !decodeLimitedJSON(w, r, maxAllowlistRequestBytes, &in) {
		return
	}
	e, err := h.st.AddIPAllowlist(in.CIDR, in.Label)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "allowlist.update"})
	out := struct {
		store.IPAllowEntry
		Warning string `json:"warning,omitempty"`
	}{IPAllowEntry: e, Warning: allowlistWarning(e.CIDR)}
	writeJSON(w, http.StatusOK, out)
}

func (h *metaAPI) deleteAllowlist(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := h.st.DeleteIPAllowlist(id); err != nil {
		httpInternalErr(w, err)
		return
	}
	h.broadcast(map[string]any{"type": "allowlist.update"})
	w.WriteHeader(http.StatusNoContent)
}

// allowlistWarning flags entries that exempt far more than one remote machine.
// The proxy-auth exemption keys on the TCP peer address, so a loopback entry (or
// a tunnel whose egress connects from loopback) exempts every local process and
// everything relayed through that tunnel.
func allowlistWarning(cidr string) string {
	var ip net.IP
	if _, n, err := net.ParseCIDR(cidr); err == nil {
		ip = n.IP
	} else {
		ip = net.ParseIP(cidr)
	}
	if ip != nil && ip.IsLoopback() {
		return "This entry is loopback: every local process, and any tunnel that exits on this machine, skips proxy and API authentication."
	}
	return ""
}
