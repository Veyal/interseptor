package store

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"
)

// Minimal 1x1 PNG (68 bytes).
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
	0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
	0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x05, 0xfe, 0xd4, 0xef, 0x00, 0x00,
	0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestPutImageBytesAndAttachImage(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	hash, n, err := s.PutImageBytes("image/png", tinyPNG)
	if err != nil {
		t.Fatalf("PutImageBytes: %v", err)
	}
	if n != int64(len(tinyPNG)) || !isContentHash(hash) {
		t.Fatalf("unexpected hash/n: hash=%q n=%d", hash, n)
	}

	id, err := s.CreateFinding(&Finding{Severity: "High", Title: "xss", Detail: "intro", Target: "t.com"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if err := s.AttachImage(id, hash, "image/png", "alert fired", 1); err != nil {
		t.Fatalf("AttachImage: %v", err)
	}

	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	if len(got.Blocks) < 2 {
		t.Fatalf("expected text+image, got %+v", got.Blocks)
	}
	// position 1 inserts between/after first text depending on length; with one
	// text block, pos=1 appends.
	var img *FindingBlock
	for i := range got.Blocks {
		if got.Blocks[i].Type == "image" {
			img = &got.Blocks[i]
			break
		}
	}
	if img == nil {
		t.Fatalf("no image block: %+v", got.Blocks)
	}
	if img.Hash != hash || img.Caption != "alert fired" || img.Missing {
		t.Fatalf("image block wrong: %+v", img)
	}
	if img.URL != "/api/findings/images/"+hash {
		t.Fatalf("url = %q", img.URL)
	}
	if img.Mime != "image/png" {
		t.Fatalf("mime = %q", img.Mime)
	}
}

func TestAttachImageMissingBlob(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	id, _ := s.CreateFinding(&Finding{Severity: "Low", Title: "t"})
	fake := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := s.AttachImage(id, fake, "image/png", "", -1); err == nil {
		t.Fatal("AttachImage should fail for missing blob")
	}
}

func TestAttachImageRejectsAggregateBodyCapAtomically(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	blocks := []FindingBlock{
		{Type: "text", MD: strings.Repeat("a", 150<<10)},
		{Type: "text", MD: strings.Repeat("b", 200<<10)},
		{Type: "text", MD: strings.Repeat("c", 200<<10)},
		{Type: "text", MD: strings.Repeat("d", 200<<10)},
		{Type: "text", MD: strings.Repeat("e", 200<<10)},
	}
	// Fill the first block until the serialized body is just below the cap.
	var body string
	for {
		body, err = MarshalFindingBlocks(blocks)
		if err != nil {
			t.Fatal(err)
		}
		remaining := maxFindingBodyBytes - 80 - len(body)
		if remaining <= 0 {
			break
		}
		blocks[0].MD += strings.Repeat("a", remaining)
	}
	if len(body) >= maxFindingBodyBytes {
		t.Fatalf("fixture body is not below cap: %d", len(body))
	}
	id, err := s.CreateFinding(&Finding{Title: "image aggregate cap", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(tinyPNG)
	hash := hex.EncodeToString(digest[:])
	_, _, attachErr := s.PutAndAttachImage(id, "image/png", tinyPNG, "proof", -1, "result", "shows the result", "operator_upload", 0)
	if attachErr == nil || !strings.Contains(attachErr.Error(), "body too large") {
		t.Fatalf("AttachImageWithMetadata error = %v, want aggregate body cap", attachErr)
	}
	if s.BodyExists(hash) {
		t.Fatal("rejected PutAndAttachImage left an unreferenced image blob")
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocks) != len(blocks) {
		t.Fatalf("rejected image attach changed blocks: got %d want %d", len(got.Blocks), len(blocks))
	}
}

func TestBuildBlocksImageMissingWhenBlobGone(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	hash, _, err := s.PutImageBytes("image/png", tinyPNG)
	if err != nil {
		t.Fatalf("PutImageBytes: %v", err)
	}
	id, _ := s.CreateFinding(&Finding{Severity: "High", Title: "t"})
	if err := s.AttachImage(id, hash, "image/png", "cap", -1); err != nil {
		t.Fatalf("AttachImage: %v", err)
	}
	if err := os.Remove(s.bodyPath(hash)); err != nil {
		t.Fatalf("remove blob: %v", err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	var saw bool
	for _, b := range got.Blocks {
		if b.Type == "image" && b.Hash == hash {
			saw = true
			if !b.Missing {
				t.Fatalf("expected Missing=true: %+v", b)
			}
		}
	}
	if !saw {
		t.Fatalf("image block gone: %+v", got.Blocks)
	}
}

func TestGCBodiesKeepsFindingImageHash(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	hash, _, err := s.PutImageBytes("image/png", tinyPNG)
	if err != nil {
		t.Fatalf("PutImageBytes: %v", err)
	}
	id, _ := s.CreateFinding(&Finding{Severity: "High", Title: "t", TS: time.Now().UnixMilli()})
	if err := s.AttachImage(id, hash, "image/png", "keep", -1); err != nil {
		t.Fatalf("AttachImage: %v", err)
	}

	removed, _, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 0 {
		t.Fatalf("GC should keep finding image, removed=%d", removed)
	}
	if !s.BodyExists(hash) {
		t.Fatal("finding image blob was deleted by GC")
	}
}

func TestPutImageBytesRejectsEmptyAndHuge(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if _, _, err := s.PutImageBytes("image/png", nil); err == nil {
		t.Fatal("empty should fail")
	}
	huge := make([]byte, maxNotesImageBytes+1)
	if _, _, err := s.PutImageBytes("image/png", huge); err == nil {
		t.Fatal("oversized should fail")
	}
}

func TestValidateFindingImageWebPDimensions(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
		ok   bool
	}{
		{name: "vp8x", data: testWebPVP8X(640, 480), want: "image/webp", ok: true},
		{name: "vp8l", data: testWebPVP8L(640, 480), want: "image/webp", ok: true},
		{name: "vp8", data: testWebPVP8(640, 480), want: "image/webp", ok: true},
		{name: "zero canvas", data: testWebPVP8X(0, 480), ok: false},
		{name: "oversized canvas", data: testWebPVP8X(10001, 10000), ok: false},
		{name: "truncated riff", data: append(testWebPVP8X(1, 1), 0x00)[:12], ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateFindingImage(tt.data)
			if tt.ok {
				if err != nil || got != tt.want {
					t.Fatalf("validateFindingImage() = %q, %v; want %q", got, err, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateFindingImage() accepted malformed/oversized %s", tt.name)
			}
		})
	}
}

func TestParseWebPVP8XDimensionsPreservesLowByteCarry(t *testing.T) {
	width, height, ok := parseWebPDimensions(testWebPVP8X(512, 768))
	if !ok || width != 512 || height != 768 {
		t.Fatalf("VP8X dimensions = %dx%d ok=%t; want 512x768", width, height, ok)
	}
}

func TestValidateFindingImageBMPDimensions(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		ok   bool
	}{
		{name: "info header", data: testBMP(640, 480), ok: true},
		{name: "top down", data: testBMP(640, -480), ok: true},
		{name: "zero width", data: testBMP(0, 480), ok: false},
		{name: "oversized", data: testBMP(10001, 10000), ok: false},
		{name: "pixel data before dib", data: func() []byte { data := testBMP(640, 480); binary.LittleEndian.PutUint32(data[10:14], 20); return data }(), ok: false},
		{name: "short dib", data: append([]byte{'B', 'M'}, make([]byte, 16)...), ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateFindingImage(tt.data)
			if tt.ok {
				if err != nil || got != "image/bmp" {
					t.Fatalf("validateFindingImage() = %q, %v; want image/bmp", got, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateFindingImage() accepted malformed/oversized %s", tt.name)
			}
		})
	}
}

func TestValidateFindingImageAVIFDimensions(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		ok   bool
	}{
		{name: "ispe", data: testAVIF(640, 480), ok: true},
		{name: "zero width", data: testAVIF(0, 480), ok: false},
		{name: "oversized", data: testAVIF(10001, 10000), ok: false},
		{name: "dimensionless", data: testAVIFFtypOnly(), ok: false},
		{name: "short ispe", data: testAVIFShortISPE(), ok: false},
		{name: "truncated box", data: append(testAVIFFtypOnly(), 0x00, 0x00), ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateFindingImage(tt.data)
			if tt.ok {
				if err != nil || got != "image/avif" {
					t.Fatalf("validateFindingImage() = %q, %v; want image/avif", got, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateFindingImage() accepted malformed/oversized %s", tt.name)
			}
		})
	}
}

func TestPutImageBytesRejectsRasterDimensionBombs(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		mime string
		data []byte
	}{
		{mime: "image/webp", data: testWebPVP8X(10001, 10000)},
		{mime: "image/bmp", data: testBMP(10001, 10000)},
		{mime: "image/avif", data: testAVIF(10001, 10000)},
	} {
		t.Run(tc.mime, func(t *testing.T) {
			if _, _, err := s.PutImageBytes(tc.mime, tc.data); err == nil {
				t.Fatalf("PutImageBytes accepted oversized %s", tc.mime)
			}
		})
	}
}

func testWebPVP8X(width, height int) []byte {
	payload := make([]byte, 10)
	put24LE(payload[4:7], uint32(width-1))
	put24LE(payload[7:10], uint32(height-1))
	return testRIFFChunk("VP8X", payload)
}

func testWebPVP8L(width, height int) []byte {
	payload := make([]byte, 5)
	payload[0] = 0x2f
	w := uint32(width - 1)
	h := uint32(height - 1)
	payload[1] = byte(w)
	payload[2] = byte(w>>8) | byte(h<<6)
	payload[3] = byte(h >> 2)
	payload[4] = byte(h >> 10)
	return testRIFFChunk("VP8L", payload)
}

func testWebPVP8(width, height int) []byte {
	payload := make([]byte, 10)
	payload[3], payload[4], payload[5] = 0x9d, 0x01, 0x2a
	binary.LittleEndian.PutUint16(payload[6:8], uint16(width))
	binary.LittleEndian.PutUint16(payload[8:10], uint16(height))
	return testRIFFChunk("VP8 ", payload)
}

func testRIFFChunk(kind string, payload []byte) []byte {
	data := make([]byte, 12+8+len(payload)+(len(payload)&1))
	copy(data[:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WEBP")
	copy(data[12:16], kind)
	binary.LittleEndian.PutUint32(data[16:20], uint32(len(payload)))
	copy(data[20:], payload)
	return data
}

func testBMP(width, height int32) []byte {
	data := make([]byte, 54)
	copy(data[:2], "BM")
	binary.LittleEndian.PutUint32(data[10:14], 54)
	binary.LittleEndian.PutUint32(data[14:18], 40)
	binary.LittleEndian.PutUint32(data[18:22], uint32(width))
	binary.LittleEndian.PutUint32(data[22:26], uint32(height))
	binary.LittleEndian.PutUint16(data[26:28], 1)
	binary.LittleEndian.PutUint16(data[28:30], 24)
	return data
}

func testBMFFBox(kind string, payload []byte) []byte {
	data := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(data[:4], uint32(len(data)))
	copy(data[4:8], kind)
	copy(data[8:], payload)
	return data
}

func testAVIFFtypOnly() []byte {
	payload := append([]byte("avif"), []byte{0, 0, 0, 0}...)
	payload = append(payload, []byte("avif")...)
	return testBMFFBox("ftyp", payload)
}

func testAVIF(width, height uint32) []byte {
	ispePayload := make([]byte, 12)
	binary.BigEndian.PutUint32(ispePayload[4:8], width)
	binary.BigEndian.PutUint32(ispePayload[8:12], height)
	ispe := testBMFFBox("ispe", ispePayload)
	ipco := testBMFFBox("ipco", ispe)
	iprp := testBMFFBox("iprp", ipco)
	meta := testBMFFBox("meta", append([]byte{0, 0, 0, 0}, iprp...))
	return append(testAVIFFtypOnly(), meta...)
}

func testAVIFShortISPE() []byte {
	ispe := testBMFFBox("ispe", make([]byte, 4))
	ipco := testBMFFBox("ipco", ispe)
	iprp := testBMFFBox("iprp", ipco)
	meta := testBMFFBox("meta", append([]byte{0, 0, 0, 0}, iprp...))
	return append(testAVIFFtypOnly(), meta...)
}

func put24LE(dst []byte, value uint32) {
	dst[0] = byte(value)
	dst[1] = byte(value >> 8)
	dst[2] = byte(value >> 16)
}

func TestPutAndAttachImageSurvivesConcurrentGC(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.CreateFinding(&Finding{Title: "evidence"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, e := s.PutAndAttachImage(id, "image/png", tinyPNG, "proof", -1, "result", "screen shows exposed invoice", "browser_screenshot", 0)
		done <- e
	}()
	for i := 0; i < 20; i++ {
		if _, _, e := s.GCBodies(); e != nil {
			t.Fatal(e)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Blocks) != 1 || f.Blocks[0].Missing {
		t.Fatalf("attached image missing: %+v", f.Blocks)
	}
}

func TestPutAndAttachImageInfersMissingMIME(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.CreateFinding(&Finding{Title: "evidence"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PutAndAttachImage(id, "", tinyPNG, "proof", -1, "result", "browser shows the result", "browser_screenshot", 0); err != nil {
		t.Fatal(err)
	}
	f, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Blocks) != 1 || f.Blocks[0].Mime != "image/png" {
		t.Fatalf("mime=%q blocks=%+v", f.Blocks[0].Mime, f.Blocks)
	}
}

func TestMarshalBodyStripsImageURL(t *testing.T) {
	body := marshalBody([]FindingBlock{
		{Type: "image", Hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Mime: "image/png", Caption: "c", URL: "/api/findings/images/aa", Missing: true},
	})
	if body == "" {
		t.Fatal("empty body")
	}
	if strings.Contains(body, `"url"`) || strings.Contains(body, `"missing"`) {
		t.Fatalf("enriched fields leaked into stored body: %s", body)
	}
	if !strings.Contains(body, `"hash"`) || !strings.Contains(body, `"caption"`) {
		t.Fatalf("expected hash/caption persisted: %s", body)
	}
}
