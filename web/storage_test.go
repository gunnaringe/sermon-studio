package web

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
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

// An upload holds its whole size while it comes: one that does not fit is refused at the start, and two
// cannot both take the last room. A finished one counts.
func TestUploadQuota(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.storageChanged()
	ts.SetQuota(1000)
	if w := chunk(t, ts, "2026-10-04", "a.flac", 2000, 0, "x", time.Time{}); w.Code != http.StatusInsufficientStorage ||
		!strings.Contains(w.Body.String(), "the church's storage is full: 0 MB does not fit, 0 MB is left") {
		t.Errorf("too big: %d %s", w.Code, w.Body)
	}
	if w := chunk(t, ts, "2026-10-04", "a.flac", 600, 0, strings.Repeat("a", 100), time.Time{}); w.Code != http.StatusOK {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	if w := chunk(t, ts, "2026-10-11", "b.flac", 600, 0, "b", time.Time{}); w.Code != http.StatusInsufficientStorage {
		t.Errorf("second while the first comes: %d %s", w.Code, w.Body)
	}
	if w := chunk(t, ts, "2026-10-04", "a.flac", 600, 100, strings.Repeat("a", 500), time.Time{}); w.Code != http.StatusCreated {
		t.Fatalf("rest of the first: %d %s", w.Code, w.Body)
	}
	if w := chunk(t, ts, "2026-10-11", "b.flac", 600, 0, "b", time.Time{}); w.Code != http.StatusInsufficientStorage {
		t.Errorf("second after the first: %d %s", w.Code, w.Body)
	}
	if w := chunk(t, ts, "2026-10-11", "b.flac", 200, 0, strings.Repeat("b", 200), time.Time{}); w.Code != http.StatusCreated {
		t.Errorf("one that fits: %d %s", w.Code, w.Body) // 600, its recording.json and 200
	}
	ts.SetQuota(0)
	if ts.over() {
		t.Error("over without a quota")
	}
	ts.SetQuota(800)
	if !ts.over() {
		t.Error("not over")
	}
}

// The public page says only that the church's storage is full.
func TestShareUploadQuota(t *testing.T) {
	st := newShareTest(t, RateLimits{})
	code, _ := st.makeShare(`{"label":"Sound desk","types":[".wav"]}`)
	srv := st.app.tenants[0].srv
	srv.storageChanged()
	srv.SetQuota(1500)
	w := st.start(code, "1.1.1.1", "service.wav", 2000)
	if w.Code != http.StatusInsufficientStorage || decode[map[string]string](t, w)["error"] != "the church's storage is full" {
		t.Errorf("start: %d %s", w.Code, w.Body)
	}
	w = st.start(code, "1.1.1.1", "service.wav", 1200)
	if w.Code != http.StatusCreated {
		t.Fatalf("start that fits: %d %s", w.Code, w.Body)
	}
	if err := srv.fits(400); err == nil {
		t.Error("an upload in progress holds no room")
	}
	id := decode[map[string]any](t, w)["id"].(string)
	if w := st.call("PUT", "/api/share/uploads/"+id+"?offset=0", code, "1.1.1.1", wav(1200)); w.Code != http.StatusCreated {
		t.Fatalf("chunk: %d %s", w.Code, w.Body)
	}
	if err := srv.fits(1200); err != nil { // in the inbox now: it counts once taken in
		t.Errorf("after it came: %v", err)
	}
}

// A run checks its files fit before it starts: what it writes, less what it writes over.
func TestRunQuota(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026/2026-10-04/2026-10-04_raw.flac", "x")
	ts.file(t, "2026/2026-10-04/recording.json", `{"recording":{"format":{"duration":3600}}}`)
	folder := filepath.Join(ts.root, "2026", "2026-10-04")
	q := defaultRequest()
	q.Sections = []span{{Start: 100, End: 3100}} // 50 minutes
	q.Flac, q.Mp3, q.Opus = true, true, true
	pl := ts.check(folder, q)
	// FLAC 57.6 kB/s, MP3 96k 12 kB/s, Opus 48k 6 kB/s, for 3000 s.
	if need := ts.runNeeds(pl); need != (57600+12000+6000)*3000 {
		t.Errorf("needs %d", need)
	}
	ts.file(t, "2026/2026-10-04/2026-10-04.mp3", strings.Repeat("m", 1000))
	if need := ts.runNeeds(pl); need != (57600+6000)*3000 {
		t.Errorf("with the MP3 there, it is kept: %d", need)
	}
	q.Force = true
	if need := ts.runNeeds(ts.check(folder, q)); need != (57600+12000+6000)*3000-1000 {
		t.Errorf("saved again: the MP3 is written over: %d", need)
	}

	ts.storageChanged()
	ts.SetQuota(100 << 20)
	if pl := decode[plan](t, ts.do(t, "POST", "/api/check?path=2026/2026-10-04", q)); !strings.Contains(pl.Error,
		"the files do not fit in the church's storage: they take about 216 MB, and 99 MB is left") {
		t.Errorf("check: %+v", pl.Error)
	}
	if w := ts.do(t, "POST", "/api/run?path=2026/2026-10-04", q); w.Code != http.StatusBadRequest {
		t.Errorf("run: %d %s", w.Code, w.Body)
	}
	ts.SetQuota(1 << 30)
	if pl := ts.check(folder, q); pl.Error != "" {
		t.Errorf("with room: %s", pl.Error)
	}
}

// An inbox file that does not fit stays there, and the recordings page says so; it goes in once there is room.
func TestInboxQuota(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.storageChanged()
	ts.SetQuota(1000)
	os.WriteFile(filepath.Join(ts.inbox, "2026-10-04.flac"), make([]byte, 2000), 0o644)
	if err := ts.ingest("2026-10-04.flac"); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(ts.inbox, "2026-10-04.flac")) {
		t.Fatal("taken in")
	}
	got := decode[struct{ Waiting []string }](t, ts.do(t, "GET", "/api/recordings", nil))
	if !slices.Equal(got.Waiting, []string{"2026-10-04.flac"}) {
		t.Errorf("waiting: %v", got.Waiting)
	}
	ts.SetQuota(0)
	if err := ts.ingest("2026-10-04.flac"); err != nil || fileExists(filepath.Join(ts.inbox, "2026-10-04.flac")) {
		t.Fatalf("with room: %v", err)
	}
	if got := decode[struct{ Waiting []string }](t, ts.do(t, "GET", "/api/recordings", nil)); len(got.Waiting) != 0 {
		t.Errorf("still waiting: %v", got.Waiting)
	}
}

// Only those who see the storage hear that it is full.
func TestFullForAdmins(t *testing.T) {
	a, h, _, _ := newTestApp(t)
	srv := a.tenants[0].srv
	os.WriteFile(filepath.Join(srv.root, "x_raw.flac"), make([]byte, 100), 0o644)
	srv.storageChanged()
	srv.SetQuota(50)
	got := decode[struct{ Tenants []TenantInfo }](t, do(h, "GET", "/api/tenants", "")).Tenants
	if !got[0].Full {
		t.Errorf("admin: %+v", got[0])
	}
	a.cfg.VisitorRole = RoleMember
	if got := decode[struct{ Tenants []TenantInfo }](t, do(h, "GET", "/api/tenants", "")).Tenants; got[0].Full {
		t.Errorf("member: %+v", got[0])
	}
}
