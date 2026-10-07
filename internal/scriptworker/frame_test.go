package scriptworker

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func readAll(b []byte) (byte, []byte, error) {
	return ReadFrame(bufio.NewReader(bytes.NewReader(b)))
}

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, FrameOutput, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	typ, p, err := readAll(buf.Bytes())
	if err != nil || typ != FrameOutput || string(p) != `{"a":1}` {
		t.Fatalf("got %c %q %v", typ, p, err)
	}
}

func TestFrameRejectsOversizeBeforeAllocating(t *testing.T) {
	hdr := make([]byte, 5)
	binary.BigEndian.PutUint32(hdr, MaxFrame+1)
	hdr[4] = FrameRun
	if _, _, err := readAll(hdr); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if err := WriteFrame(io.Discard, FrameRun, make([]byte, MaxFrame+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("write err = %v", err)
	}
}

func TestFrameLyingLengthDoesNotAllocateUpFront(t *testing.T) {
	hdr := make([]byte, 5)
	binary.BigEndian.PutUint32(hdr, MaxFrame) // claims 32 MiB, sends 3 bytes
	hdr[4] = FrameRun
	_, _, err := readAll(append(hdr, 1, 2, 3))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v", err)
	}
}

func TestFrameBadTypeAndTruncated(t *testing.T) {
	if _, _, err := readAll([]byte{0, 0, 0, 0, 'Z'}); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("bad type err = %v", err)
	}
	if _, _, err := readAll([]byte{0, 0}); err == nil {
		t.Fatal("truncated header accepted")
	}
	if _, _, err := readAll(nil); !errors.Is(err, io.EOF) {
		t.Fatalf("empty err = %v", err)
	}
}

func FuzzReadFrame(f *testing.F) {
	var ok bytes.Buffer
	_ = WriteFrame(&ok, FrameRun, []byte(`{"v":1}`))
	f.Add(ok.Bytes())
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 'R'})
	f.Add([]byte{0, 0, 0, 3, 'S', 1})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		r := bufio.NewReader(bytes.NewReader(data))
		for i := 0; i < 8; i++ {
			typ, p, err := ReadFrame(r)
			if err != nil {
				return
			}
			if !validType(typ) || len(p) > MaxFrame {
				t.Fatalf("accepted invalid frame %c len %d", typ, len(p))
			}
		}
	})
}
