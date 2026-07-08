package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const latestCheckTTL = time.Hour

type latestCheckCache struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
	ETag      string    `json:"etag,omitempty"`
}

func latestCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".interseptor", "update-check.json"), nil
}

func readLatestCache() (latestCheckCache, bool) {
	path, err := latestCachePath()
	if err != nil {
		return latestCheckCache{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return latestCheckCache{}, false
	}
	var c latestCheckCache
	if err := json.Unmarshal(data, &c); err != nil || c.Latest == "" || c.CheckedAt.IsZero() {
		return latestCheckCache{}, false
	}
	if time.Since(c.CheckedAt) > latestCheckTTL {
		return latestCheckCache{}, false
	}
	return c, true
}

func writeLatestCache(latest, etag string) {
	path, err := latestCachePath()
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	c := latestCheckCache{
		Latest:    latest,
		CheckedAt: time.Now().UTC(),
		ETag:      etag,
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
