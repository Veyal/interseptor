package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Provenance is server-stamped. Classification is a reviewer declaration, not
// independent verification that an image came from a browser or device.
type FindingImageProvenance struct {
	Ingestion      string `json:"ingestion"`
	OriginalSource string `json:"originalSource"`
	IngestedTS     int64  `json:"ingestedTs,omitempty"`
	ClassifiedBy   string `json:"classifiedBy,omitempty"`
	ClassifiedTS   int64  `json:"classifiedTs,omitempty"`
}

func generatedFindingImage(source string) bool {
	return source == "flow_preview" || source == "generated_image"
}
func capturedFindingImage(source string) bool {
	return source == "browser_screenshot" || source == "device_screenshot"
}

func stampFindingImageProvenance(tx *sql.Tx, oldBody, newBody string, change FindingChange) (string, error) {
	if newBody == "" {
		return newBody, nil
	}
	var old, blocks []blockRecord
	_ = json.Unmarshal([]byte(oldBody), &old)
	if err := json.Unmarshal([]byte(newBody), &blocks); err != nil {
		return "", err
	}
	previous := map[string]blockRecord{}
	for _, b := range old {
		if b.Type == "image" {
			previous[b.Hash] = b
		}
	}
	for i := range blocks {
		b := &blocks[i]
		if b.Type != "image" {
			continue
		}
		prev, existed := previous[b.Hash]
		var p FindingImageProvenance
		err := tx.QueryRow(`SELECT ingestion,original_source,ingested_ts FROM finding_image_provenance WHERE hash=?`, b.Hash).Scan(&p.Ingestion, &p.OriginalSource, &p.IngestedTS)
		if errors.Is(err, sql.ErrNoRows) {
			p.Ingestion = change.ImageIngestion
			if p.Ingestion == "" {
				p.Ingestion = "reference"
			}
			p.OriginalSource = b.Source
			p.IngestedTS = time.Now().UnixMilli()
			if existed {
				p.Ingestion = "legacy_unknown"
				p.IngestedTS = 0
				p.OriginalSource = prev.Source
			}
			if p.OriginalSource == "" {
				p.OriginalSource = "operator_upload"
			}
			if generatedFindingImage(p.OriginalSource) {
				p.Ingestion = "generated"
			}
			if _, err = tx.Exec(`INSERT INTO finding_image_provenance(hash,ingestion,original_source,ingested_ts) VALUES(?,?,?,?)`, b.Hash, p.Ingestion, p.OriginalSource, p.IngestedTS); err != nil {
				return "", err
			}
		} else if err != nil {
			return "", err
		}
		if generatedFindingImage(p.OriginalSource) {
			b.Source = p.OriginalSource
		}
		if existed && prev.Provenance != nil && b.Source == prev.Source {
			p.ClassifiedBy = prev.Provenance.ClassifiedBy
			p.ClassifiedTS = prev.Provenance.ClassifiedTS
		} else if capturedFindingImage(b.Source) || existed && b.Source != prev.Source {
			p.ClassifiedBy = change.Actor
			if p.ClassifiedBy == "" {
				p.ClassifiedBy = "local writer"
			}
			p.ClassifiedTS = time.Now().UnixMilli()
		}
		b.Provenance = &p
	}
	data, err := json.Marshal(blocks)
	return string(data), err
}

// Missing image mappings must agree with the referenced evidence blocks.
func reconcileFindingImageReferences(f *Finding) {
	present := map[string]bool{}
	for _, b := range f.Blocks {
		if b.Type == "image" && !b.Missing {
			present[b.Hash] = true
		}
	}
	for key, ref := range f.ProofReview.Evidence {
		if ref.Hash != "" && !present[ref.Hash] {
			ref.Missing = true
			f.ProofReview.Evidence[key] = ref
		}
	}
	visitClaimReferences(f, func(ref *FindingEvidenceReference) {
		if ref.Hash != "" && !present[ref.Hash] {
			ref.Missing = true
		}
	})
}
