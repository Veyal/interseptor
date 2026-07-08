package proxy

import (
	"bufio"
	"encoding/binary"
	"io"
	"log"
	"net"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// wsPreviewMax bounds how many payload bytes are captured per frame.
const wsPreviewMax = 512

// relayWSFrames copies RFC 6455 frames from src to dst verbatim while recording
// each frame's metadata + a bounded (unmasked) payload preview. Large frames are
// streamed in chunks, never buffered whole. It returns when the stream ends.
func (s *Server) relayWSFrames(flowID int64, dir string, src *bufio.Reader, dst net.Conn) error {
	for {
		b0, err := src.ReadByte()
		if err != nil {
			return err
		}
		b1, err := src.ReadByte()
		if err != nil {
			return err
		}
		opcode := b0 & 0x0f
		masked := b1&0x80 != 0
		ln := uint64(b1 & 0x7f)

		hdr := []byte{b0, b1}
		switch ln {
		case 126:
			var e [2]byte
			if _, err := io.ReadFull(src, e[:]); err != nil {
				return err
			}
			ln = uint64(binary.BigEndian.Uint16(e[:]))
			hdr = append(hdr, e[:]...)
		case 127:
			var e [8]byte
			if _, err := io.ReadFull(src, e[:]); err != nil {
				return err
			}
			ln = binary.BigEndian.Uint64(e[:])
			hdr = append(hdr, e[:]...)
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(src, mask[:]); err != nil {
				return err
			}
			hdr = append(hdr, mask[:]...)
		}
		if _, err := dst.Write(hdr); err != nil {
			return err
		}

		// Stream the payload to dst, capturing an (unmasked) preview prefix.
		preview := make([]byte, 0, min(ln, wsPreviewMax))
		buf := make([]byte, 32*1024)
		var idx uint64
		remaining := ln
		for remaining > 0 {
			n := uint64(len(buf))
			if n > remaining {
				n = remaining
			}
			r, rerr := src.Read(buf[:n])
			if r > 0 {
				for i := 0; i < r && uint64(len(preview)) < wsPreviewMax; i++ {
					c := buf[i]
					if masked {
						c ^= mask[(idx+uint64(i))%4]
					}
					preview = append(preview, c)
				}
				if _, werr := dst.Write(buf[:r]); werr != nil {
					return werr
				}
				idx += uint64(r)
				remaining -= uint64(r)
			}
			if rerr != nil {
				return rerr
			}
		}
		s.recordWSFrame(flowID, dir, opcode, ln, preview)
	}
}

func (s *Server) recordWSFrame(flowID int64, dir string, opcode byte, length uint64, preview []byte) {
	fr := &store.WSFrame{
		FlowID:  flowID,
		TS:      time.Now(),
		Dir:     dir,
		Opcode:  int(opcode),
		Length:  int64(length),
		Preview: string(preview),
	}
	if err := s.st.SaveWSFrame(fr); err != nil {
		log.Printf("proxy: persist ws frame: %v", err)
		return
	}
	if s.events != nil {
		if e, ok := s.events.(interface{ WSFramed(int64) }); ok {
			// The notifier is external code (SSE fan-out); a panic there must not
			// crash the proxy or abort the relay. Recover and log — capture is
			// best-effort and off the hot forwarding path.
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("proxy: ws frame notifier panic: %v", r)
					}
				}()
				e.WSFramed(flowID)
			}()
		}
	}
}
