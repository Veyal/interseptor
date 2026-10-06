package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/rendermark"
)

// PutImageBytes stores screenshot/evidence bytes in the content-addressed bodies
// directory (same layout as flow bodies) and returns the sha256 hash. MIME is
// sanitized to the notes raster allowlist. Max size matches notes images (5 MiB).
func (s *Store) PutImageBytes(mime string, data []byte) (hash string, n int64, err error) {
	hash, n, _, err = s.putImageBytes(mime, data)
	return hash, n, err
}

// PutAndAttachImage atomically protects the upload-to-attachment window from
// body GC. Callers handling screenshots or rendered flow previews should use
// this operation when they already know the destination finding.
func (s *Store) PutAndAttachImage(findingID int64, mime string, data []byte, caption string, pos int, role, proof, source string, sourceFlowID int64, changes ...FindingChange) (string, int64, error) {
	return s.PutAndAttachImageRef(findingID, mime, data, caption, pos, role, proof, source, sourceFlowID, "", changes...)
}

// PutAndAttachImageRef is PutAndAttachImage plus a server-stamped sourceRef
// (for example "intruder:<runId>") naming the recorded data the image was
// rendered from. The ref is immutable once stored on a generated image.
func (s *Store) PutAndAttachImageRef(findingID int64, mime string, data []byte, caption string, pos int, role, proof, source string, sourceFlowID int64, sourceRef string, changes ...FindingChange) (string, int64, error) {
	if err := validateSourceRef(sourceRef); err != nil {
		return "", 0, err
	}
	// A generated Interseptor render carries a PNG marker; it can be attached
	// as a generated image but never relabelled as a capture or an operator
	// upload, which would let a drawn picture pass as browser proof.
	if _, marked := rendermark.Find(data); marked && !generatedFindingImage(source) {
		return "", 0, fmt.Errorf("%w: this image is a generated Interseptor render; attach it with source=evidence_render, not %q", ErrInvalidFinding, source)
	}
	s.bodyMu.Lock()
	defer s.bodyMu.Unlock()
	// If the upload is rejected by the destination finding (for example because
	// its aggregate narrative would exceed the cap), remove only a blob created
	// by this call. Existing content-addressed blobs may belong to another
	// record and must remain available.
	digest := sha256.Sum256(data)
	preexisting := s.BodyExists(hex.EncodeToString(digest[:]))
	hash, n, resolvedMIME, err := s.putImageBytes(mime, data)
	if err != nil {
		return "", 0, err
	}
	change := firstFindingChange(changes)
	change.ImageIngestion = "upload"
	if err := s.attachImage(findingID, hash, resolvedMIME, caption, pos, role, proof, source, sourceFlowID, sourceRef, change); err != nil {
		// A finalized upload is already present at this point. It is safe to
		// remove it only when this call created the file; the body lock prevents
		// concurrent image uploads in this store from racing this check.
		if !preexisting {
			_ = os.Remove(s.bodyPath(hash))
		}
		return "", 0, err
	}
	return hash, n, nil
}

func (s *Store) putImageBytes(mime string, data []byte) (string, int64, string, error) {
	if len(data) == 0 {
		return "", 0, "", fmt.Errorf("empty image")
	}
	if len(data) > maxNotesImageBytes {
		return "", 0, "", fmt.Errorf("image too large (max %d bytes)", maxNotesImageBytes)
	}
	mime = SanitizeNotesImageMIME(mime)
	detected, err := validateFindingImage(data)
	if err != nil {
		return "", 0, "", err
	}
	if mime == "application/octet-stream" {
		mime = detected
	} else if detected != mime {
		return "", 0, "", fmt.Errorf("image MIME mismatch: declared %s, detected %s", mime, detected)
	}
	w, err := s.NewBodyWriter()
	if err != nil {
		return "", 0, "", err
	}
	if _, err := w.Write(data); err != nil {
		w.Abort()
		return "", 0, "", err
	}
	hash, n, err := w.Finalize()
	return hash, n, mime, err
}

const maxFindingImagePixels = int64(100_000_000)

func validateFindingImage(data []byte) (string, error) {
	if cfg, format, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		if !findingImageDimensionsOK(int64(cfg.Width), int64(cfg.Height)) {
			return "", fmt.Errorf("image dimensions exceed limit")
		}
		return "image/" + format, nil
	}
	if width, height, ok := parseWebPDimensions(data); ok {
		if !findingImageDimensionsOK(width, height) {
			return "", fmt.Errorf("image dimensions exceed limit")
		}
		return "image/webp", nil
	}
	if width, height, ok := parseBMPDimensions(data); ok {
		if !findingImageDimensionsOK(width, height) {
			return "", fmt.Errorf("image dimensions exceed limit")
		}
		return "image/bmp", nil
	}
	if width, height, ok := parseAVIFDimensions(data); ok {
		if !findingImageDimensionsOK(width, height) {
			return "", fmt.Errorf("image dimensions exceed limit")
		}
		return "image/avif", nil
	}
	return "", fmt.Errorf("invalid or unsupported image payload")
}

func findingImageDimensionsOK(width, height int64) bool {
	return width > 0 && height > 0 && width <= maxFindingImagePixels/height
}

// parseWebPDimensions validates the RIFF/chunk envelope and extracts the
// canvas dimensions from the first supported VP8, VP8L, or VP8X chunk. We do
// this locally because the standard library has no WebP decoder; accepting a
// signature without reading dimensions would allow a tiny dimension bomb.
func parseWebPDimensions(data []byte) (int64, int64, bool) {
	if len(data) < 20 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, false
	}
	riffSize := uint64(binary.LittleEndian.Uint32(data[4:8]))
	if riffSize < 12 || riffSize > uint64(len(data)-8) {
		return 0, 0, false
	}
	end := 8 + int(riffSize)
	var width, height int64
	found := false
	for pos := 12; pos < end; {
		if end-pos < 8 {
			return 0, 0, false
		}
		kind := string(data[pos : pos+4])
		size := uint64(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		payloadStart := pos + 8
		if size > uint64(end-payloadStart) {
			return 0, 0, false
		}
		payloadEnd := payloadStart + int(size)
		switch kind {
		case "VP8X":
			if size < 10 {
				return 0, 0, false
			}
			width = int64((uint32(data[payloadStart+4]) | uint32(data[payloadStart+5])<<8 | uint32(data[payloadStart+6])<<16) + 1)
			height = int64((uint32(data[payloadStart+7]) | uint32(data[payloadStart+8])<<8 | uint32(data[payloadStart+9])<<16) + 1)
			if !findingImageDimensionsOK(width, height) {
				return 0, 0, false
			}
			found = true
		case "VP8L":
			if size < 5 || data[payloadStart] != 0x2f {
				return 0, 0, false
			}
			w := uint32(data[payloadStart+1]) | uint32(data[payloadStart+2]&0x3f)<<8
			h := uint32(data[payloadStart+2]>>6) | uint32(data[payloadStart+3])<<2 | uint32(data[payloadStart+4]&0x0f)<<10
			width, height = int64(w+1), int64(h+1)
			if !findingImageDimensionsOK(width, height) {
				return 0, 0, false
			}
			found = true
		case "VP8 ":
			if size < 10 || data[payloadStart+3] != 0x9d || data[payloadStart+4] != 0x01 || data[payloadStart+5] != 0x2a {
				return 0, 0, false
			}
			width = int64(binary.LittleEndian.Uint16(data[payloadStart+6:payloadStart+8]) & 0x3fff)
			height = int64(binary.LittleEndian.Uint16(data[payloadStart+8:payloadStart+10]) & 0x3fff)
			if !findingImageDimensionsOK(width, height) {
				return 0, 0, false
			}
			found = true
		}
		// RIFF chunks are padded to an even boundary. A missing pad byte is a
		// malformed payload, not a reason to accept a partial image.
		pos = payloadEnd
		if size&1 != 0 {
			if pos >= end {
				return 0, 0, false
			}
			pos++
		}
	}
	return width, height, found
}

// parseBMPDimensions validates the DIB header before reading its dimensions.
// Windows BMP permits a negative height for top-down images; its absolute
// value is still the raster height and is checked against the pixel budget.
func parseBMPDimensions(data []byte) (int64, int64, bool) {
	if len(data) < 26 || data[0] != 'B' || data[1] != 'M' {
		return 0, 0, false
	}
	fileSize := uint64(binary.LittleEndian.Uint32(data[2:6]))
	if fileSize != 0 && (fileSize < 26 || fileSize > uint64(len(data))) {
		return 0, 0, false
	}
	pixelOffset := binary.LittleEndian.Uint32(data[10:14])
	if pixelOffset != 0 && uint64(pixelOffset) > uint64(len(data)) {
		return 0, 0, false
	}
	dibSize := binary.LittleEndian.Uint32(data[14:18])
	if dibSize == 12 {
		if len(data) < 26 {
			return 0, 0, false
		}
		if pixelOffset != 0 && pixelOffset < 26 {
			return 0, 0, false
		}
		return int64(binary.LittleEndian.Uint16(data[18:20])), int64(binary.LittleEndian.Uint16(data[20:22])), true
	}
	if dibSize < 40 || uint64(dibSize) > uint64(len(data)-14) || len(data) < 26 {
		return 0, 0, false
	}
	if pixelOffset != 0 && uint64(pixelOffset) < 14+uint64(dibSize) {
		return 0, 0, false
	}
	width := int64(int32(binary.LittleEndian.Uint32(data[18:22])))
	rawHeight := int32(binary.LittleEndian.Uint32(data[22:26]))
	if rawHeight == -1<<31 {
		return 0, 0, false
	}
	height := int64(rawHeight)
	if height < 0 {
		height = -height
	}
	return width, height, true
}

// parseAVIFDimensions walks the ISO-BMFF boxes needed by AVIF and requires an
// AVIF file type plus at least one well-formed ispe property. AVIF dimensions
// are carried by ispe, commonly nested under meta/iprp/ipco; a brand-only
// signature is intentionally rejected as dimensionless.
func parseAVIFDimensions(data []byte) (int64, int64, bool) {
	if len(data) < 16 {
		return 0, 0, false
	}
	state := avifParseState{}
	if !walkAVIFBoxes(data, 0, len(data), 0, &state) || !state.avifBrand || !state.hasDimensions {
		return 0, 0, false
	}
	return state.width, state.height, true
}

type avifParseState struct {
	avifBrand     bool
	hasDimensions bool
	width, height int64
}

func walkAVIFBoxes(data []byte, start, end, depth int, state *avifParseState) bool {
	if depth > 8 || start < 0 || start > end || end > len(data) {
		return false
	}
	for pos := start; pos < end; {
		if end-pos < 8 {
			return false
		}
		size32 := binary.BigEndian.Uint32(data[pos : pos+4])
		kind := string(data[pos+4 : pos+8])
		header := 8
		var boxSize uint64
		switch size32 {
		case 0:
			boxSize = uint64(end - pos)
		case 1:
			if end-pos < 16 {
				return false
			}
			boxSize = binary.BigEndian.Uint64(data[pos+8 : pos+16])
			header = 16
		default:
			boxSize = uint64(size32)
		}
		if boxSize < uint64(header) || boxSize > uint64(end-pos) {
			return false
		}
		boxEnd := pos + int(boxSize)
		payload := pos + header
		switch kind {
		case "ftyp":
			if boxSize < uint64(header+8) {
				return false
			}
			if (boxEnd-payload)%4 != 0 {
				return false
			}
			for brandPos := payload; brandPos+4 <= boxEnd; brandPos += 4 {
				brand := string(data[brandPos : brandPos+4])
				if brand == "avif" || brand == "avis" {
					state.avifBrand = true
				}
			}
		case "ispe":
			if boxSize < uint64(header+12) {
				return false
			}
			width := uint64(binary.BigEndian.Uint32(data[payload+4 : payload+8]))
			height := uint64(binary.BigEndian.Uint32(data[payload+8 : payload+12]))
			if width == 0 || height == 0 || width > uint64(maxFindingImagePixels) || height > uint64(maxFindingImagePixels) || width > uint64(maxFindingImagePixels)/height {
				return false
			}
			state.width, state.height = int64(width), int64(height)
			state.hasDimensions = true
		case "meta", "iprp", "ipco":
			childStart := payload
			if kind == "meta" {
				if boxSize < uint64(header+4) {
					return false
				}
				childStart += 4 // FullBox version and flags.
			}
			if !walkAVIFBoxes(data, childStart, boxEnd, depth+1, state) {
				return false
			}
		}
		pos = boxEnd
	}
	return true
}

// BodyExists reports whether a content-addressed body file is present on disk.
func (s *Store) BodyExists(hash string) bool {
	if !isContentHash(hash) {
		return false
	}
	_, err := os.Stat(s.bodyPath(hash))
	return err == nil
}

// AttachImage inserts (or updates) an image block in the finding's narrative body.
// hash must already be stored via PutImageBytes. pos is the 0-based block index;
// pass -1 to append. Idempotent on the same hash — updates mime/caption in place.
func (s *Store) AttachImage(findingID int64, hash, mime, caption string, pos int) error {
	return s.AttachImageWithMetadata(findingID, hash, mime, caption, pos, "", "", "", 0)
}

// AttachImageWithMetadata preserves semantic evidence role and provenance.
func (s *Store) AttachImageWithMetadata(findingID int64, hash, mime, caption string, pos int, role, proof, source string, sourceFlowID int64, changes ...FindingChange) error {
	return s.attachImage(findingID, hash, mime, caption, pos, role, proof, source, sourceFlowID, "", firstFindingChange(changes))
}

func (s *Store) attachImage(findingID int64, hash, mime, caption string, pos int, role, proof, source string, sourceFlowID int64, sourceRef string, change FindingChange) error {
	changes := []FindingChange{change}
	if err := validateSourceRef(sourceRef); err != nil {
		return err
	}
	if err := validateFindingEvidenceMetadata(role, source, sourceFlowID); err != nil {
		return err
	}
	if !isContentHash(hash) {
		return fmt.Errorf("invalid image hash")
	}
	if !s.BodyExists(hash) {
		return fmt.Errorf("image blob not found")
	}
	mime = SanitizeNotesImageMIME(mime)
	caption = strings.TrimSpace(caption)

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := revisionBefore(tx, findingID); err != nil {
		return err
	}

	narrative, err := scanFindingNarrative(findingNarrativeRow(tx, findingID))
	if err != nil {
		return err
	}
	newBody := insertImageIntoBodyRef(narrative.Body, hash, mime, caption, pos, role, proof, source, sourceFlowID, sourceRef)
	newBody, err = stampFindingImageProvenance(tx, narrative.Body, newBody, firstFindingChange(changes))
	if err != nil {
		return err
	}
	// Validate the complete canonical body before committing the attachment.
	// This keeps screenshot evidence subject to the same aggregate limit as
	// create/update paths and ensures a rejected attach cannot mutate the row.
	narrative.Body = newBody
	if detailSync := firstTextMD(newBody); detailSync != "" {
		narrative.Detail = detailSync
	}
	if err := validateFindingNarrativeSize(narrative); err != nil {
		return err
	}
	detailSync := firstTextMD(newBody)
	if _, err := tx.Exec(
		`UPDATE findings SET body=?, detail=CASE WHEN ?<>'' THEN ? ELSE detail END, updated_ts=? WHERE id=?`,
		newBody, detailSync, detailSync, time.Now().UnixMilli(), findingID); err != nil {
		return err
	}
	if err := appendFindingRevision(tx, findingID, "update", firstFindingChange(changes)); err != nil {
		return err
	}
	return tx.Commit()
}

// insertImageIntoBody inserts an image block at position pos. If the hash is
// already present, mime/caption are updated in place (position unchanged).
func insertImageIntoBody(bodyJSON, hash, mime, caption string, pos int) string {
	return insertImageIntoBodyWithMetadata(bodyJSON, hash, mime, caption, pos, "", "", "", 0)
}

func insertImageIntoBodyWithMetadata(bodyJSON, hash, mime, caption string, pos int, role, proof, source string, sourceFlowID int64) string {
	return insertImageIntoBodyRef(bodyJSON, hash, mime, caption, pos, role, proof, source, sourceFlowID, "")
}

func insertImageIntoBodyRef(bodyJSON, hash, mime, caption string, pos int, role, proof, source string, sourceFlowID int64, sourceRef string) string {
	var recs []blockRecord
	if bodyJSON != "" {
		_ = json.Unmarshal([]byte(bodyJSON), &recs)
	}
	for i, r := range recs {
		if r.Type == "image" && r.Hash == hash {
			recs[i].Mime = mime
			recs[i].Caption = caption
			if strings.TrimSpace(role) != "" {
				recs[i].Role = normalizeFindingBlockRole(role)
			}
			if strings.TrimSpace(proof) != "" {
				recs[i].Proof = strings.TrimSpace(proof)
			}
			if strings.TrimSpace(source) != "" && !generatedFindingImage(r.Source) {
				recs[i].Source = normalizeFindingBlockSource(source)
			}
			if sourceFlowID != 0 && !generatedFindingImage(r.Source) {
				recs[i].SourceFlowID = sourceFlowID
			}
			if sourceRef != "" && !generatedFindingImage(r.Source) {
				recs[i].SourceRef = sourceRef
			}
			j, _ := json.Marshal(recs)
			return string(j)
		}
	}
	newBlock := blockRecord{Type: "image", Hash: hash, Mime: mime, Caption: caption, Role: normalizeFindingBlockRole(role), Proof: strings.TrimSpace(proof), Source: normalizeFindingBlockSource(source), SourceFlowID: sourceFlowID, SourceRef: sourceRef}
	if pos < 0 || pos >= len(recs) {
		recs = append(recs, newBlock)
	} else {
		recs = append(recs, blockRecord{})
		copy(recs[pos+1:], recs[pos:])
		recs[pos] = newBlock
	}
	j, _ := json.Marshal(recs)
	return string(j)
}

// enrichImageBlocks sets URL + Missing on image blocks based on blob presence.
func (s *Store) enrichImageBlocks(blocks []FindingBlock) {
	for i := range blocks {
		if blocks[i].Type != "image" {
			continue
		}
		h := blocks[i].Hash
		if h == "" || !isContentHash(h) {
			blocks[i].Missing = true
			continue
		}
		blocks[i].URL = "/api/findings/images/" + h
		if blocks[i].Mime == "" {
			blocks[i].Mime = "application/octet-stream"
		} else {
			blocks[i].Mime = SanitizeNotesImageMIME(blocks[i].Mime)
		}
		if !s.BodyExists(h) {
			blocks[i].Missing = true
		}
	}
}

// FindingImageHashes returns every content hash referenced by image blocks in
// all findings' body JSON. Used by GCBodies so screenshot evidence is not
// deleted while still attached to a finding.
func (s *Store) FindingImageHashes() (map[string]struct{}, error) {
	out := make(map[string]struct{})
	rows, err := s.db.Query(`SELECT body FROM findings WHERE body != '' UNION ALL SELECT COALESCE(json_extract(snapshot,'$.body'),'') FROM finding_revisions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var recs []blockRecord
		if err := json.Unmarshal([]byte(body), &recs); err != nil {
			continue
		}
		for _, r := range recs {
			if r.Type == "image" && isContentHash(r.Hash) {
				out[r.Hash] = struct{}{}
			}
		}
	}
	return out, rows.Err()
}

// FindingImageMIME returns the MIME stored on the first finding image block
// that references hash, or "" if none.
func (s *Store) FindingImageMIME(hash string) string {
	if !isContentHash(hash) {
		return ""
	}
	rows, err := s.db.Query(`SELECT body FROM findings WHERE body LIKE ?`, "%"+hash+"%")
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return ""
		}
		var recs []blockRecord
		if err := json.Unmarshal([]byte(body), &recs); err != nil {
			continue
		}
		for _, r := range recs {
			if r.Type == "image" && r.Hash == hash {
				return SanitizeNotesImageMIME(r.Mime)
			}
		}
	}
	return ""
}

// IsFindingImageReferenced verifies that hash is an image evidence block in a
// finding, preventing handlers from serving arbitrary body hashes.
func (s *Store) IsFindingImageReferenced(findingID int64, hash string) bool {
	if !isContentHash(hash) {
		return false
	}
	var body string
	if err := s.db.QueryRow(`SELECT body FROM findings WHERE id=?`, findingID).Scan(&body); err != nil {
		return false
	}
	var recs []blockRecord
	if json.Unmarshal([]byte(body), &recs) != nil {
		return false
	}
	for _, r := range recs {
		if r.Type == "image" && r.Hash == hash {
			return true
		}
	}
	return false
}
