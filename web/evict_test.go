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
	ft.put("2026-10-18.transcript.json", `{"segments":[]}`) // a JSON file stays on the disk, however old
	ft.srv.syncFiles(ft.folder)
	ft.made(now.Add(-60 * testDay))
	ft.usedAt("original/2026-10-18_raw.flac", now.Add(-40*testDay))
	ft.usedAt("2026-10-18.mp3", now.Add(-2*testDay))
	ft.srv.storageChanged()
	before, _ := ft.srv.storageNow()

	ft.app.keepDiskOnce(now, DiskOptions{KeepFor: 30 * testDay})
	if !ft.onDisk("2026-10-18.transcript.json") {
		t.Error("the transcript's JSON taken off the disk")
	}
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

// A new machine starts from the bucket with only what must be on the disk: the records (recording.json,
// church.json) and what no record has; the recorded files stay in the bucket, marked so, and are fetched when
// needed. A file a record has that the
// bucket lacks is dropped from the record. A restore cut short is done again; a data dir with files is never
// filled from the bucket.
func TestRestore(t *testing.T) {
	b := newFakeBlob()
	modified := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	files := map[string]string{"original/2026-10-18_raw.flac": "the original", "2026-10-18.mp3": "ID3 mp3",
		"2026-10-18.transcript.json": `{"segments":[]}`}
	record := map[string]fileRecord{"2026-10-18.opus": {Size: 4, Modified: modified, SHA256: sha("lost")}}
	for rel, data := range files {
		b.store("kirke/Taler/2026/2026-10-18/"+rel, []byte(data), nil)
		record[rel] = fileRecord{Size: int64(len(data)), Modified: modified, SHA256: sha(data)}
	}
	state, _ := json.Marshal(map[string]any{"id": "rec-1", "files": record})
	b.store("kirke/Taler/2026/2026-10-18/recording.json", state, nil)
	church, _ := json.Marshal(map[string]any{"name": "Kirka",
		"files": map[string]fileRecord{"clips/intro.flac": {Size: 6, Modified: modified, SHA256: sha("jingle")}}})
	b.store("kirke/church.json", church, nil)
	b.store("kirke/clips/intro.flac", []byte("jingle"), nil)
	b.store("kirke/clips/outro.flac", []byte("not recorded"), nil)
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
		_, err := os.Stat(filepath.Join(folder, filepath.FromSlash(rel)))
		if json := filepath.Ext(rel) == ".json"; json != (err == nil) {
			t.Errorf("%s: on the disk %v", rel, err == nil) // only the JSON is fetched
		}
	}
	st := readState(folder)
	if st.ID != "rec-1" || !st.Files["2026-10-18.mp3"].Bucket || !st.Files["original/2026-10-18_raw.flac"].Bucket {
		t.Errorf("the record: %+v", st)
	}
	if _, ok := st.Files["2026-10-18.opus"]; ok {
		t.Error("a file the bucket lacks is still recorded")
	}
	for f, want := range map[string]bool{"church.json": true, "clips/outro.flac": true, "clips/intro.flac": false} {
		if _, err := os.Stat(filepath.Join(data, "kirke", filepath.FromSlash(f))); (err == nil) != want {
			t.Errorf("%s: on the disk %v", f, err == nil) // the church's recorded clip stays in the bucket
		}
	}
	if cc, _ := readChurch(filepath.Join(data, "kirke")); !cc.Files["clips/intro.flac"].Bucket || cc.Name != "Kirka" {
		t.Errorf("church.json: %+v", cc)
	}
	if !fileExists(filepath.Join(data, "kirke", "clips", "intro.flac")) {
		t.Error("the clip in the bucket is not found")
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

// The church's clips and logo are recorded in church.json and can be kept only in the bucket, as a recording's
// files: still listed and counted, fetched when served, removed from the bucket when deleted.
func TestChurchFilesInBucket(t *testing.T) {
	ft := newFetchTest(t)
	const id = "0123456789ab"
	clip := filepath.Join(ft.church, "clips", id+".flac")
	logo := filepath.Join(ft.church, "logo.png")
	for p, data := range map[string]string{clip: "fLaC jingle", logo: "\x89PNG logo"} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p+".partial", []byte(data), 0o644)
		if err := normalizer.Rename(p+".partial", p); err != nil {
			t.Fatal(err)
		}
	}
	s := ft.srv
	s.clips.mu.Lock()
	s.clips.lib.Clips = append(s.clips.lib.Clips, clipInfo{ID: id, Name: "Jingle", Version: "1"})
	s.clips.mu.Unlock()
	s.syncChurchFiles()
	settleMirror(t, ft.app)
	fr, _ := s.churchRecord()
	if got := fr.files(); got["clips/"+id+".flac"].SHA256 != sha("fLaC jingle") || got["logo.png"].SHA256 != sha("\x89PNG logo") {
		t.Fatalf("church.json's files: %+v", got)
	}
	s.storageChanged()
	before, _ := s.storageNow()
	tn := ft.app.tenants[0]
	version := tn.logoVersion()

	for _, rel := range []string{"clips/" + id + ".flac", "logo.png"} {
		if err := s.evict(context.Background(), fr, rel); err != nil {
			t.Fatalf("evict %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(clip); err == nil {
		t.Fatal("the clip is still on the disk")
	}
	if tn.logoFile() != logo || tn.logoVersion() != version {
		t.Errorf("the logo: %q %q, before %q", tn.logoFile(), tn.logoVersion(), version)
	}
	if !fileExists(clip) {
		t.Error("the clip is not found")
	}
	s.storageChanged()
	if after, _ := s.storageNow(); after.Clips != before.Clips {
		t.Errorf("the clips' storage %d, before %d", after.Clips, before.Clips)
	}

	if w := do(ft.studio, "GET", "/api/clips/"+id+"/audio", ""); w.Code != 200 || w.Body.String() != "fLaC jingle" {
		t.Errorf("the clip's audio: %d %q", w.Code, w.Body)
	}
	if w := do(ft.studio, "GET", "/api/logo", ""); w.Code != 200 || w.Body.String() != "\x89PNG logo" {
		t.Errorf("the logo: %d %q", w.Code, w.Body)
	}
	if fr.files()["clips/"+id+".flac"].Bucket || fr.files()["logo.png"].Bucket {
		t.Errorf("fetched, still marked: %+v", fr.files())
	}

	// Deleted while only in the bucket: gone from the record and the bucket.
	if err := s.evict(context.Background(), fr, "clips/"+id+".flac"); err != nil {
		t.Fatal(err)
	}
	if w := do(ft.studio, "POST", "/api/clips/"+id+"/delete", ""); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	settleMirror(t, ft.app)
	if _, ok := fr.files()["clips/"+id+".flac"]; ok {
		t.Error("a deleted clip is still recorded")
	}
	if ft.b.text("kirke/clips/"+id+".flac") != "" {
		t.Error("a deleted clip is still in the bucket")
	}
}
