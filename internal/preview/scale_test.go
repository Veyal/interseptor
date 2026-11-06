package preview

import (
	"bytes"
	"image/png"
	"testing"
)

// A report needs 1150-1450px-wide evidence; scale renders at that size with
// scaled text rather than upscaling a 1x bitmap.
func TestScaleRendersAtLinearMultiple(t *testing.T) {
	req := []byte("POST /login HTTP/1.1\r\nHost: app.example\r\nContent-Type: application/json\r\n\r\n{\"user\":\"alice\"}\r\n")
	res := []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"role\":\"user\"}\r\n")
	base, err := Render(req, res, Options{})
	if err != nil {
		t.Fatalf("render 1x: %v", err)
	}
	double, err := Render(req, res, Options{Scale: 2})
	if err != nil {
		t.Fatalf("render 2x: %v", err)
	}
	b, err := png.Decode(bytes.NewReader(base))
	if err != nil {
		t.Fatalf("decode 1x: %v", err)
	}
	d, err := png.Decode(bytes.NewReader(double))
	if err != nil {
		t.Fatalf("decode 2x: %v", err)
	}
	wRatio := float64(d.Bounds().Dx()) / float64(b.Bounds().Dx())
	hRatio := float64(d.Bounds().Dy()) / float64(b.Bounds().Dy())
	if wRatio < 1.9 || wRatio > 2.1 || hRatio < 1.9 || hRatio > 2.1 {
		t.Fatalf("2x render is %.2fx wide and %.2fx tall (%dx%d vs %dx%d), want ~2x both",
			wRatio, hRatio, d.Bounds().Dx(), d.Bounds().Dy(), b.Bounds().Dx(), b.Bounds().Dy())
	}
}

func TestScaleIsClamped(t *testing.T) {
	req := []byte("GET / HTTP/1.1\r\nHost: app.example\r\n\r\n")
	lo, err := Render(req, nil, Options{Scale: -3})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	one, err := Render(req, nil, Options{Scale: 1})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !bytes.Equal(lo, one) {
		t.Fatal("a scale below 1 must render exactly like 1x")
	}
	hi, err := Render(req, nil, Options{Scale: 9})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	three, err := Render(req, nil, Options{Scale: 3})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !bytes.Equal(hi, three) {
		t.Fatal("a scale above the maximum must clamp to it")
	}
}
