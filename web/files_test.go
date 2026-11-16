package web

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// The files record: every file of a recording with its size, time and hash; read again only when it changed,
// kept through a rename, dropped when the file is gone, and recording.json not written when nothing changed.
func TestFilesRecord(t *testing.T) {
	skipIfEvicting(t)
	ts := newTestServer(t, normalizer.FFmpeg{})
	folder := filepath.Join(ts.root, "2026", "2026-10-18")
	ts.file(t, "2026/2026-10-18/original/2026-10-18_raw.flac", "the original")
	ts.file(t, "2026/2026-10-18/2026-10-18.mp3", "ID3 mp3")
	ts.file(t, "2026/2026-10-18/2026-10-18.txt", "Hei.\n")
	ts.file(t, "2026/2026-10-18/draft.json", "{}")
	ts.file(t, "2026/2026-10-18/.2026-10-18.flac.partial", "half")
	ts.file(t, "2026/2026-10-18/.hidden/x", "x")

	if added, _, _ := ts.syncFiles(folder); added != 3 {
		t.Errorf("added %d", added)
	}
	files := readState(folder).Files
	if len(files) != 3 || files["original/2026-10-18_raw.flac"].SHA256 != sha("the original") ||
		files["2026-10-18.mp3"].Size != int64(len("ID3 mp3")) || files["2026-10-18.txt"].Modified.IsZero() {
		t.Fatalf("files: %+v", files)
	}

	// Nothing changed: recording.json is not written.
	st, _ := os.Stat(statePath(folder))
	time.Sleep(20 * time.Millisecond)
	if a, c, g := ts.syncFiles(folder); a+c+g != 0 {
		t.Errorf("unchanged: %d %d %d", a, c, g)
	}
	if again, _ := os.Stat(statePath(folder)); !again.ModTime().Equal(st.ModTime()) {
		t.Error("recording.json written with nothing changed")
	}

	// Changed: read again.
	mp3 := filepath.Join(folder, "2026-10-18.mp3")
	os.WriteFile(mp3, []byte("ID3 another mp3"), 0o644)
	if _, c, _ := ts.syncFiles(folder); c != 1 || readState(folder).Files["2026-10-18.mp3"].SHA256 != sha("ID3 another mp3") {
		t.Errorf("changed: %d %+v", c, readState(folder).Files["2026-10-18.mp3"])
	}

	// Renamed (the same size and time): the record goes with it, and the file is not read again. Here its bytes
	// changed with the same size and time, so a read would give another hash.
	info, _ := os.Stat(mp3)
	renamed := filepath.Join(folder, "2026-10-18 Advent.mp3")
	os.Rename(mp3, renamed)
	os.WriteFile(renamed, []byte("ID3 ANOTHER MP3"), 0o644)
	os.Chtimes(renamed, info.ModTime(), info.ModTime())
	if a, c, g := ts.syncFiles(folder); a+c+g != 0 {
		t.Errorf("renamed: added %d, changed %d, gone %d", a, c, g)
	}
	files = readState(folder).Files
	if _, old := files["2026-10-18.mp3"]; old || files["2026-10-18 Advent.mp3"].SHA256 != sha("ID3 another mp3") {
		t.Errorf("renamed: %+v", files)
	}

	// Gone.
	os.Remove(filepath.Join(folder, "2026-10-18.txt"))
	if _, _, g := ts.syncFiles(folder); g != 1 || len(readState(folder).Files) != 2 {
		t.Errorf("gone: %d %+v", g, readState(folder).Files)
	}
}

// The record is kept by itself: after a change through the studio (here a request that measures the storage).
func TestFilesRecordFollows(t *testing.T) {
	ts := newTestServerWith(t, normalizer.FFmpeg{}, func(c *Config) { c.RecordFiles = true })
	folder := filepath.Join(ts.root, "2026-10-04")
	ts.file(t, "2026-10-04/original/2026-10-04_raw.flac", "x")
	ts.file(t, "2026-10-04/2026-10-04.mp3", "mp3")
	ts.storageChanged()
	eventually(t, "the files recorded", func() bool { return len(readState(folder).Files) == 2 })
}

// Without RecordFiles (the desktop app) nothing is recorded by itself.
func TestFilesRecordOff(t *testing.T) {
	skipIfEvicting(t)
	ts := newTestServer(t, normalizer.FFmpeg{})
	folder := filepath.Join(ts.root, "2026-10-04")
	ts.file(t, "2026-10-04/2026-10-04.mp3", "mp3")
	ts.storageChanged()
	ts.storageNow()
	time.Sleep(100 * time.Millisecond)
	if f := readState(folder).Files; len(f) != 0 {
		t.Errorf("recorded: %+v", f)
	}
}
