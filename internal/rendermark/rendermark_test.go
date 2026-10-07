package rendermark

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func samplePNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestEmbedFindRoundTripAndStillDecodes(t *testing.T) {
	src := samplePNG(t)
	if _, ok := Find(src); ok {
		t.Fatal("unmarked PNG must not be found")
	}
	marked := Embed(src, "intruder_timeline;ref=intruder:abc123")
	text, ok := Find(marked)
	if !ok || text != "intruder_timeline;ref=intruder:abc123" {
		t.Fatalf("Find = %q %v", text, ok)
	}
	img, err := png.Decode(bytes.NewReader(marked))
	if err != nil || img.Bounds().Dx() != 4 {
		t.Fatalf("marked PNG must still decode: %v", err)
	}
	if !bytes.Equal(Embed(src, "x"), Embed(src, "x")) {
		t.Fatal("embedding must be deterministic")
	}
}

func TestEmbedSanitizesAndIgnoresNonPNG(t *testing.T) {
	marked := Embed(samplePNG(t), "管理\n"+string(rune(0)))
	if text, ok := Find(marked); !ok || text != "??"+"?"+"?" {
		t.Fatalf("text %q ok=%v", text, ok)
	}
	if got := Embed([]byte("not a png"), "x"); string(got) != "not a png" {
		t.Fatal("non-PNG must pass through")
	}
	if _, ok := Find([]byte("short")); ok {
		t.Fatal("garbage")
	}
}
