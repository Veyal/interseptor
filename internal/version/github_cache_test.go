package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLatestCacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	writeLatestCache("1.2.3", "")
	c, ok := readLatestCache()
	if !ok || c.Latest != "1.2.3" {
		t.Fatalf("cache miss: %+v ok=%v", c, ok)
	}

	path, _ := latestCachePath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw latestCheckCache
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw.CheckedAt = time.Now().UTC().Add(-2 * time.Hour)
	stale, _ := json.Marshal(raw)
	if err := os.WriteFile(path, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readLatestCache(); ok {
		t.Fatal("expected expired cache to miss")
	}
}

func TestLatestCachePathUnderInterceptor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, err := latestCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(path)) != ".interseptor" {
		t.Fatalf("path=%s", path)
	}
}
