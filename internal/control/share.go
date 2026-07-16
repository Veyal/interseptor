package control

import (
	"context"
	"net/http"
)

// Share = remote access via a Cloudflare quick tunnel. The panel starts/stops the
// tunnel and shows the public URL a collaborator (or a VPS-hosted AI agent) uses
// with an access key. Starting a tunnel is REFUSED unless at least one API key
// exists, so the surface is never exposed unauthenticated.

// shareStatus reports the tunnel state plus whether any key exists (the UI needs
// this to require a key before offering to share).
func (h *Hub) shareStatus(w http.ResponseWriter, r *http.Request) {
	st := h.tun.Status()
	hasKeys, _ := h.st.HasAPIKeys()
	writeJSON(w, http.StatusOK, map[string]any{
		"installed": st.Installed,
		"running":   st.Running,
		"url":       st.URL,
		"err":       st.Err,
		"startedAt": st.StartedAt,
		"hasKeys":   hasKeys,
	})
}

// shareStart launches the tunnel. It refuses when no API key exists (would expose
// captured pentest traffic unauthenticated) or when cloudflared is not installed.
func (h *Hub) shareStart(w http.ResponseWriter, r *http.Request) {
	hasKeys, err := h.st.HasAPIKeys()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if !hasKeys {
		httpErr(w, http.StatusBadRequest, "create an access key before sharing — a tunnel must never expose Interseptor unauthenticated")
		return
	}
	if !h.tun.Installed() {
		httpErr(w, http.StatusBadRequest, "cloudflared is not installed. Install it (https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/) and retry")
		return
	}
	// A background context so the tunnel outlives the request; Stop() cancels it.
	st, err := h.tun.Start(context.Background())
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "failed to start tunnel: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"running": st.Running, "url": st.URL, "installed": st.Installed,
	})
}

// shareStop tears down the tunnel.
func (h *Hub) shareStop(w http.ResponseWriter, r *http.Request) {
	_ = h.tun.Stop()
	writeJSON(w, http.StatusOK, map[string]any{"running": false})
}

// StopTunnel shuts the tunnel down (called on control-plane shutdown).
func (h *Hub) StopTunnel() {
	if h.tun != nil {
		_ = h.tun.Stop()
	}
}
