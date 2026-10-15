package control

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interseptor/internal/tlsca"
)

func TestAndroidMagiskModuleEndpointReturnsZip(t *testing.T) {
	hub, _, _ := newHub(t)
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub.ca = ca

	req := httptest.NewRequest(http.MethodGet, "/api/android/magisk-module.zip", nil)
	rec := httptest.NewRecorder()
	(&androidAPI{hub}).getAndroidMagiskModule(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type = %q, want application/zip", ct)
	}
	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("response is not a valid zip: %v", err)
	}
	found := false
	for _, f := range zr.File {
		if f.Name == "module.prop" {
			found = true
		}
	}
	if !found {
		t.Fatal("module zip missing module.prop")
	}
}

func TestAndroidMagiskModuleEndpointNoCA(t *testing.T) {
	hub, _, _ := newHub(t)
	hub.ca = nil
	req := httptest.NewRequest(http.MethodGet, "/api/android/magisk-module.zip", nil)
	rec := httptest.NewRecorder()
	(&androidAPI{hub}).getAndroidMagiskModule(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no CA", rec.Code)
	}
}
