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
// streamed in chunks, never buffered whole. It returns when the stream ends:
// io.EOF when it ended between two frames, another error otherwise.
//
// One copy buffer and one preview buffer serve the whole relay (chatty sockets
// used to allocate 32 KiB per frame), and a frame that fits the buffer is sent
// with a single write — header and payload together — instead of two (two TLS
// records and two syscalls for a 20-byte message).
func (s *Server) relayWSFrames(flowID int64, dir string, src *bufio.Reader, dst net.Conn) error {
	buf := make([]byte, 32*1024)
	preview := make([]byte, 0, wsPreviewMax)
	for {
		b0, err := src.ReadByte()
		if err != nil {
			return err
		}
		b1, err := src.ReadByte()
		if err != nil {
			return midFrame(err)
		}
		opcode := b0 & 0x0f
		masked := b1&0x80 != 0
		ln := uint64(b1 & 0x7f)

		buf[0], buf[1] = b0, b1
		hl := 2
		switch ln {
		case 126:
			if _, err := io.ReadFull(src, buf[hl:hl+2]); err != nil {
				return midFrame(err)
			}
			ln = uint64(binary.BigEndian.Uint16(buf[hl : hl+2]))
			hl += 2
		case 127:
			if _, err := io.ReadFull(src, buf[hl:hl+8]); err != nil {
				return midFrame(err)
			}
			ln = binary.BigEndian.Uint64(buf[hl : hl+8])
			hl += 8
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(src, buf[hl:hl+4]); err != nil {
				return midFrame(err)
			}
			copy(mask[:], buf[hl:hl+4])
			hl += 4
		}

		preview = preview[:0]
		if ln <= uint64(len(buf)-hl) {
			// Small frame: forward header and payload in one write.
			end := hl + int(ln)
			r, rerr := io.ReadFull(src, buf[hl:end])
			preview = appendWSPreview(preview, buf[hl:hl+r], masked, mask, 0)
			if _, werr := dst.Write(buf[:hl+r]); werr != nil {
				return werr
			}
			if rerr != nil {
				return midFrame(rerr)
			}
		} else {
			if _, err := dst.Write(buf[:hl]); err != nil {
				return err
			}
			// Stream the payload to dst, capturing an (unmasked) preview prefix.
			var idx uint64
			remaining := ln
			for remaining > 0 {
				n := uint64(len(buf))
				if n > remaining {
					n = remaining
				}
				r, rerr := src.Read(buf[:n])
				if r > 0 {
					preview = appendWSPreview(preview, buf[:r], masked, mask, idx)
					if _, werr := dst.Write(buf[:r]); werr != nil {
						return werr
					}
					idx += uint64(r)
					remaining -= uint64(r)
				}
				if rerr != nil {
					if remaining > 0 {
						rerr = midFrame(rerr)
					}
					return rerr
				}
			}
		}
		s.recordWSFrame(flowID, dir, opcode, ln, preview)
	}
}

// midFrame turns an EOF that arrives inside a frame into io.ErrUnexpectedEOF,
// so io.EOF from relayWSFrames always means the stream ended between frames.
func midFrame(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// appendWSPreview appends the (unmasked) bytes of chunk, which starts at
// payload offset idx, until preview holds wsPreviewMax bytes.
func appendWSPreview(preview, chunk []byte, masked bool, mask [4]byte, idx uint64) []byte {
	for i := 0; i < len(chunk) && len(preview) < wsPreviewMax; i++ {
		c := chunk[i]
		if masked {
			c ^= mask[(idx+uint64(i))%4]
		}
		preview = append(preview, c)
	}
	return preview
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
	// Nudge immediately so a quiet socket still refreshes, and again after the
	// store commits the batch (SetWSFlushNotify) so the UI doesn't fetch early.
	s.notifyWSFrames([]int64{flowID})
}

func (s *Server) notifyWSFrames(flowIDs []int64) {
	if s.events == nil {
		return
	}
	e, ok := s.events.(interface{ WSFramed(int64) })
	if !ok {
		return
	}
	for _, flowID := range flowIDs {
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
