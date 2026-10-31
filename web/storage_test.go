package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

type storageAnswer struct {
	Use      storageUse
	Total    int64
	Quota    int64
	Measured bool
}

// What a church keeps is counted by kind; the inbox and half-written files are not.
func TestStorageCounts(t *testing.T) {
	ts := newTestServerWith(t, normalizer.FFmpeg{}, func(c *Config) { c.ClipsDir = filepath.Join(t.TempDir(), clipsDir) })
	raw := ts.file(t, "2026/2026-10-04/2026-10-04_raw.flac", strings.Repeat("r", 1000))
	ts.file(t, "2026/2026-10-04/recording.json", strings.Repeat("j", 10))
	ts.file(t, "2026/2026-10-04/2026-10-04.mp3", strings.Repeat("m", 300))
	ts.file(t, "2026/2026-10-04/2026-10-04.flac", strings.Repeat("f", 500))
	ts.file(t, "2026/2026-10-04/clips/intro.flac", strings.Repeat("c", 40))
	ts.file(t, "2026/2026-10-11/original/service.wav", strings.Repeat("o", 2000))
	ts.file(t, "2026/2026-10-18/.2026-10-18_raw.upload", strings.Repeat("u", 5000)) // half-written
	ts.file(t, ".trash/0123456789abcdef2026-09-27/2026-09-27_raw.flac", strings.Repeat("t", 700))
	os.WriteFile(filepath.Join(ts.inbox, "new.wav"), make([]byte, 9000), 0o644)
	os.MkdirAll(ts.cfg.ClipsDir, 0o755)
	os.WriteFile(filepath.Join(ts.cfg.ClipsDir, "abc.flac"), make([]byte, 60), 0o644)
	key, _ := cacheKey(raw)
	os.MkdirAll(ts.analyses.dir(key), 0o755)
	os.WriteFile(filepath.Join(ts.analyses.dir(key), "preview.mp3"), make([]byte, 200), 0o644)

	ts.storageChanged()
	got := decode[storageAnswer](t, ts.do(t, "GET", "/api/storage", nil))
	want := storageUse{Recordings: 1000 + 10 + 200 + 2000, Files: 800, Clips: 100, Trash: 700}
	if !got.Measured || got.Use != want || got.Total != want.total() || got.Quota != 0 {
		t.Errorf("storage: %+v, want %+v", got, want)
	}
}

// Removing from the trash frees the space at once: the next answer has it.
func TestStorageAfterChange(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026/2026-10-04/2026-10-04_raw.flac", strings.Repeat("r", 1000))
	ts.storageChanged()
	if got := decode[storageAnswer](t, ts.do(t, "GET", "/api/storage", nil)); got.Use.Recordings != 1000 {
		t.Fatalf("before: %+v", got)
	}
	if w := ts.do(t, "POST", "/api/recordings/delete?path=2026/2026-10-04", nil); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if got := decode[storageAnswer](t, ts.do(t, "GET", "/api/storage", nil)); got.Use.Recordings != 0 || got.Use.Trash < 1000 { // and its trash.json
		t.Errorf("in the trash: %+v", got)
	}
	id := ts.trashItems(time.Now())[0].ID
	if w := ts.do(t, "POST", "/api/trash/remove?id="+id, nil); w.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
	if got := decode[storageAnswer](t, ts.do(t, "GET", "/api/storage", nil)); got.Total != 0 {
		t.Errorf("after removing it: %+v", got)
	}
	ts.SetQuota(50 << 30)
	if got := decode[storageAnswer](t, ts.do(t, "GET", "/api/storage", nil)); got.Quota != 50<<30 {
		t.Errorf("quota: %+v", got)
	}
}
