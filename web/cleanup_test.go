package web

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Half-written files are removed once they have sat untouched a day, with
// the date folder an abandoned upload leaves empty; nothing else is.
func TestSweepLeftovers(t *testing.T) {
	// The files are there before the server starts, so the sweep at start is the one that finds them: a second
	// sweep at the same time could miss a file the first had just removed, and leave its folder (on Windows,
	// open in the other walk) behind.
	old := time.Now().Add(-leftoverAge - time.Hour)
	var upload, encode, raw, share, fresh, busy string
	ts := newTestServerWith(t, normalizer.FFmpeg{}, func(cfg *Config) {
		mk := func(p string, mtime time.Time) string {
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte("x"), 0o644)
			os.Chtimes(p, mtime, mtime)
			return p
		}
		upload = mk(filepath.Join(cfg.Root, "2026", "2026-11-01", ".2026-11-01_raw.upload"), old)
		encode = mk(filepath.Join(cfg.Root, "2026", "2026-10-04", "2026-10-04.partial.mp3"), old)
		raw = mk(filepath.Join(cfg.Root, "2026", "2026-10-04", "2026-10-04_raw.flac"), old)
		share = mk(filepath.Join(cfg.Inbox, ".share-abc.partial"), old)
		fresh = mk(filepath.Join(cfg.Inbox, ".share-def.partial"), time.Now())
		busy = mk(filepath.Join(cfg.Root, "2026", "2026-10-11", ".2026-10-11_raw.upload"), time.Now())
	})

	gone := []string{upload, encode, share, filepath.Dir(upload)}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !slices.ContainsFunc(gone, fileExists) {
			break
		}
	}
	for _, p := range gone {
		if fileExists(p) {
			t.Errorf("%s is still there", p)
		}
	}
	ts.sweepLeftovers(time.Now()) // again: it keeps what it should
	for _, p := range []string{raw, fresh, busy} {
		if !fileExists(p) {
			t.Errorf("%s was removed", p)
		}
	}
}
