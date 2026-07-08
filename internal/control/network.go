package control

import (
	"net/http"

	"github.com/Veyal/interseptor/internal/netutil"
)

func (h *settingsAPI) getNetworkHosts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, netutil.ListListenHosts())
}
