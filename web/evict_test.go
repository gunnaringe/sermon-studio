package web

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

const testDay = 24 * time.Hour

// usedAt sets when a file of the fetch test's recording was last used.
func (ft *fetchTest) usedAt(rel string, t time.Time) {
	m := ft.app.mirror
	key, _ := m.key(filepath.Join(ft.folder, filepath.FromSlash(rel)))
	m.useMu.Lock()
	m.lastUse[key] = t
	m.useMu.Unlock()
}

// made gives the recording's files an old time, as if made then, and records them so.
func (ft *fetchTest) made(at time.Time) {
	for rel := range readState(ft.folder).Files {
		os.Chtimes(filepath.Join(ft.folder, filepath.FromSlash(rel)), at, at)
	}
	ft.srv.syncFiles(ft.folder)
	settleMirror(ft.t, ft.app)
}

func (ft *fetchTest) onDisk(rel string) bool {
	_, err := os.Stat(filepath.Join(ft.folder, filepath.FromSlash(rel)))
	return err == nil
}

// A file not used for KeepFor goes to the bucket only; one used since stays. With Size, the ones used longest ago
// go first until the rest fit. The church's storage still counts what is only in the bucket.
func TestKeepDisk(t *testing.T) {
	ft := newFetchTest(t)
	now := time.Now()
	ft.made(now.Add(-60 * testDay))
	ft.usedAt("original/2026-10-18_raw.flac", now.Add(-40*testDay))
	ft.usedAt("2026-10-18.mp3", now.Add(-2*testDay))
	ft.srv.storageChanged()
	before, _ := ft.srv.storageNow()

	ft.app.keepDiskOnce(now, DiskOptions{KeepFor: 30 * testDay})
	if ft.onDisk("original/2026-10-18_raw.flac") || !ft.onDisk("2026-10-18.mp3") {
		t.Errorf("after 30 days: original %v, mp3 %v", ft.onDisk("original/2026-10-18_raw.flac"), ft.onDisk("2026-10-18.mp3"))
	}
	ft.srv.storageChanged()
	if after, _ := ft.srv.storageNow(); after.total() < before.total() { // more: the record notes the bucket
		t.Errorf("storage %d, before %d", after.total(), before.total())
	}

	// Fetched again: used now, so it stays; the disk's limit takes the one used longest ago.
	if err := fetchFor(context.Background(), filepath.Join(ft.folder, "original", "2026-10-18_raw.flac")); err != nil {
		t.Fatal(err)
	}
	ft.app.keepDiskOnce(now, DiskOptions{KeepFor: 30 * testDay, Size: int64(len("the original"))})
	if !ft.onDisk("original/2026-10-18_raw.flac") || ft.onDisk("2026-10-18.mp3") {
		t.Errorf("with the limit: original %v, mp3 %v", ft.onDisk("original/2026-10-18_raw.flac"), ft.onDisk("2026-10-18.mp3"))
	}
}

// Last use follows the studio: a file read is used now, a rename takes its time along, a removal drops it, and
// it is kept in .blob/used.json over a restart.
func TestLastUse(t *testing.T) {
	ft := newFetchTest(t)
	m := ft.app.mirror
	old := time.Now().Add(-40 * testDay)
	ft.made(old.Add(-testDay))
	ft.usedAt("2026-10-18.mp3", old)
	mp3 := filepath.Join(ft.folder, "2026-10-18.mp3")
	key, _ := m.key(mp3)
	r, _ := ft.record("2026-10-18.mp3")
	if got := m.lastUsed(key, r); !got.Equal(old) {
		t.Errorf("set: %v", got)
	}
	fetchFor(context.Background(), mp3) // read from the disk, as by a run
	if got := m.lastUsed(key, r); time.Since(got) > time.Minute {
		t.Errorf("read: %v", got)
	}

	ft.usedAt("2026-10-18.mp3", old)
	if w := do(ft.studio, "POST", "/api/rename?path="+url.QueryEscape("2026-10-18"), `{"date":"2026-10-18","name":"Advent"}`); w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	moved := "kirke/Taler/2026/2026-10-18 Advent/2026-10-18 Advent.mp3"
	m.useMu.Lock()
	got, ok := m.lastUse[moved]
	_, stale := m.lastUse[key]
	m.useMu.Unlock()
	if !ok || !got.Equal(old) || stale {
		t.Errorf("renamed: %v %v (old key kept: %v)", got, ok, stale)
	}

	m.saveUse()
	var saved map[string]time.Time
	data, _ := os.ReadFile(m.usePath())
	if json.Unmarshal(data, &saved); !saved[moved].Equal(old) {
		t.Errorf("used.json: %s", data)
	}

	normalizer.Remove(filepath.Join(ft.church, "Taler", "2026", "2026-10-18 Advent", "2026-10-18 Advent.mp3"))
	m.useMu.Lock()
	_, kept := m.lastUse[moved]
	m.useMu.Unlock()
	if kept {
		t.Error("a removed file keeps its time")
	}
}

// With Disk set, the studio keeps the disk by itself, after its first sweep.
func TestKeepDiskRuns(t *testing.T) {
	b := newFakeBlob()
	data := t.TempDir()
	folder := filepath.Join(data, "kirke", "Taler", "2026-10-18")
	os.MkdirAll(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "2026-10-18.mp3"), []byte("ID3 mp3"), 0o644)
	old := time.Now().Add(-40 * testDay)
	os.Chtimes(filepath.Join(folder, "2026-10-18.mp3"), old, old)
	open := func() *App {
		a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: newTestLogger(t), RecordFiles: true}, DataDir: data,
			Blob: b, Disk: DiskOptions{KeepFor: 30 * testDay}})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := open()
	a.tenants[0].srv.syncFiles(folder) // recorded (at start it is, in the background)
	a.Close()
	a = open() // the sweep has put it in the bucket; the start's pass takes it off the disk
	defer a.Close()
	eventually(t, "the old file only in the bucket", func() bool {
		_, err := os.Stat(filepath.Join(folder, "2026-10-18.mp3"))
		return err != nil
	})
	if b.text("kirke/Taler/2026-10-18/2026-10-18.mp3") != "ID3 mp3" {
		t.Error("not in the bucket")
	}
}

// A new machine starts from the bucket with only what must be on the disk: the records and the church's files;
// the recordings' files stay in the bucket, marked so, and are fetched when needed. A file a record has that the
// bucket lacks is dropped from the record. A restore cut short is done again; a data dir with files is never
// filled from the bucket.
func TestRestore(t *testing.T) {
	b := newFakeBlob()
	modified := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	files := map[string]string{"original/2026-10-18_raw.flac": "the original", "2026-10-18.mp3": "ID3 mp3"}
	record := map[string]fileRecord{"2026-10-18.opus": {Size: 4, Modified: modified, SHA256: sha("lost")}}
	for rel, data := range files {
		b.store("kirke/Taler/2026/2026-10-18/"+rel, []byte(data), nil)
		record[rel] = fileRecord{Size: int64(len(data)), Modified: modified, SHA256: sha(data)}
	}
	state, _ := json.Marshal(map[string]any{"id": "rec-1", "files": record})
	b.store("kirke/Taler/2026/2026-10-18/recording.json", state, nil)
	b.store("kirke/church.json", []byte(`{"name":"Kirka"}`), nil)
	b.store("kirke/clips/intro.flac", []byte("jingle"), nil)
	b.store("_check-x", []byte("x"), nil)
	open := func(data string) *App {
		a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: newTestLogger(t), RecordFiles: true}, DataDir: data, Blob: b})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	data := t.TempDir()
	a := open(data)
	folder := filepath.Join(data, "kirke", "Taler", "2026", "2026-10-18")
	for rel := range files {
		if _, err := os.Stat(filepath.Join(folder, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s fetched", rel)
		}
	}
	st := readState(folder)
	if st.ID != "rec-1" || !st.Files["2026-10-18.mp3"].Bucket || !st.Files["original/2026-10-18_raw.flac"].Bucket {
		t.Errorf("the record: %+v", st)
	}
	if _, ok := st.Files["2026-10-18.opus"]; ok {
		t.Error("a file the bucket lacks is still recorded")
	}
	for _, f := range []string{"church.json", "clips/intro.flac"} {
		if _, err := os.Stat(filepath.Join(data, "kirke", filepath.FromSlash(f))); err != nil {
			t.Errorf("%s not fetched", f)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "_check-x")); err == nil {
		t.Error("the check's object fetched")
	}
	paths, _ := normalizer.NewPaths(folder)
	if filepath.Base(paths.Source()) != "2026-10-18_raw.flac" {
		t.Errorf("the source: %q", paths.Source())
	}
	mp3 := filepath.Join(folder, "2026-10-18.mp3")
	if err := fetchFor(context.Background(), mp3); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(mp3); string(got) != "ID3 mp3" {
		t.Errorf("fetched: %q", got)
	}
	settleMirror(t, a)
	a.Close()
	if !readState(folder).Files["original/2026-10-18_raw.flac"].Bucket {
		t.Error("the original lost its mark")
	}

	// Not empty: nothing is fetched, also not what the data dir lacks.
	b.store("kirke/Taler/2026/2026-10-25/2026-10-25.mp3", []byte("another"), nil)
	open(data).Close()
	if _, err := os.Stat(filepath.Join(data, "kirke", "Taler", "2026", "2026-10-25")); err == nil {
		t.Error("a data dir with files in it was filled from the bucket")
	}

	// Cut short (the marker is still there): done again.
	os.WriteFile(filepath.Join(data, mirrorDir, "restoring"), nil, 0o600)
	open(data).Close()
	if _, err := os.Stat(filepath.Join(data, "kirke", "Taler", "2026", "2026-10-25", "2026-10-25.mp3")); err != nil {
		t.Errorf("a restore cut short was not done again: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, mirrorDir, "restoring")); err == nil {
		t.Error("the marker is left")
	}
}
