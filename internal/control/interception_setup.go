package control

import (
	"errors"
	"net/http"

	"github.com/Veyal/interseptor/internal/android"
	"github.com/Veyal/interseptor/internal/store"
)

// Six short strings, up to MaxInterceptionEnablers enablers and host lists.
const maxInterceptionSetupRequestBytes int64 = 512 << 10

// detectedInterception is what Interseptor can observe itself, offered so the
// operator records the real proxy address and CA fingerprint instead of typing
// them. deviceProxy is only filled when ?serial= names an adb device.
type detectedInterception struct {
	ProxyAddress  string `json:"proxyAddress"`
	CAFingerprint string `json:"caFingerprint,omitempty"`
	DeviceProxy   string `json:"deviceProxy,omitempty"`
}

func (h *projectAPI) detectInterception(serial string) detectedInterception {
	d := detectedInterception{ProxyAddress: h.currentProxyAddr()}
	if h.ca != nil {
		d.CAFingerprint = h.ca.Fingerprint()
	}
	if serial != "" && android.Available() {
		if devs, err := android.Devices(); err == nil {
			if dev, err := android.ResolveDevice(serial, devs); err == nil {
				if pv, err := android.ProxyValue(dev); err == nil {
					d.DeviceProxy = pv
				}
			}
		}
	}
	return d
}

// getInterceptionSetup returns the project's interception setup record plus
// the proxy/CA values currently observable, for prefilling the record.
func (h *projectAPI) getInterceptionSetup(w http.ResponseWriter, r *http.Request) {
	setup, err := h.st.GetInterceptionSetup()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"setup":    setup,
		"detected": h.detectInterception(r.URL.Query().Get("serial")),
	})
}

// putInterceptionSetup replaces the record; the version bumps only on change.
func (h *projectAPI) putInterceptionSetup(w http.ResponseWriter, r *http.Request) {
	var in store.InterceptionSetup
	if !decodeLimitedJSON(w, r, maxInterceptionSetupRequestBytes, &in) {
		return
	}
	setup, err := h.st.SetInterceptionSetup(in)
	if err != nil {
		if errors.Is(err, store.ErrInvalidEngagement) {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		httpInternalErr(w, err)
		return
	}
	h.broadcast(map[string]any{"type": "interception.update"})
	writeJSON(w, http.StatusOK, map[string]any{"setup": setup})
}

// putFlowInterception marks a bodiless CONNECT status-0 flow pinning_blocked or
// not_intercepted ("" clears) so a capture gap is never read as a finding.
func (h *flowAPI) putFlowInterception(w http.ResponseWriter, r *http.Request) {
	f, ok := h.loadFlow(w, r)
	if !ok {
		return
	}
	var in struct {
		Annotation string `json:"annotation"`
	}
	if !decodeLimitedJSON(w, r, maxFlowMetadataRequestBytes, &in) {
		return
	}
	tags, err := h.st.SetFlowInterceptionAnnotation(f.ID, in.Annotation)
	if err != nil {
		if errors.Is(err, store.ErrInvalidEngagement) {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		httpInternalErr(w, err)
		return
	}
	if updated, err := h.st.GetFlow(f.ID); err == nil {
		h.FlowUpdated(updated)
	}
	writeJSON(w, http.StatusOK, map[string]any{"flowId": f.ID, "annotation": in.Annotation, "tags": tags})
}
