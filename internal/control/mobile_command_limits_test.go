package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/tlsca"
)

func TestMobileCommandsRejectOversizedJSONBeforeDeviceAccess(t *testing.T) {
	hub, _, _ := newHub(t)
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate CA: %v", err)
	}
	hub.ca = ca
	androidAPI := &androidAPI{hub}
	iosAPI := &iosAPI{hub}
	tests := []struct {
		name string
		call http.HandlerFunc
	}{
		{"android proxy", androidAPI.postAndroidProxy},
		{"android unproxy", androidAPI.postAndroidUnproxy},
		{"android install CA", androidAPI.postAndroidInstallCA},
		{"android setup", androidAPI.postAndroidSetup},
		{"iOS setup", iosAPI.postIOSSetup},
		{"iOS install CA", iosAPI.postIOSInstallCA},
		{"iOS open profile", iosAPI.postIOSOpenProfile},
	}
	body := `{"padding":"` + strings.Repeat("x", int(maxMobileCommandRequestBytes))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/mobile", strings.NewReader(body))
			rec := httptest.NewRecorder()
			tt.call(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status=%d body=%q, want 413", rec.Code, rec.Body.String())
			}
		})
	}
}
