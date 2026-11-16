package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// fetchTest is a church with a bucket and the files record, and one recording (an original and an MP3) whose
// files are recorded and in the bucket.
type fetchTest struct {
	*shareTest
	b      *fakeBlob
	srv    *Server
	folder string
}

func newFetchTest(t *testing.T) *fetchTest {
	t.Helper()
	b := newFakeBlob()
	st := newShareTestWith(t, RateLimits{}, func(c *AppConfig) { c.Blob, c.Base.RecordFiles = b, true })
	ft := &fetchTest{shareTest: st, b: b, srv: st.app.tenants[0].srv, folder: filepath.Join(st.church, "Taler", "2026-10-18")}
	ft.put("original/2026-10-18_raw.flac", "the original")
	ft.put("2026-10-18.mp3", "ID3 mp3")
	ft.srv.syncFiles(ft.folder)
	settleMirror(t, st.app)
	return ft
}

// put writes a file of the recording as the studio does (through a rename, which the mirror follows).
func (ft *fetchTest) put(rel, data string) {
	p := filepath.Join(ft.folder, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p+".partial", []byte(data), 0o644)
	if err := normalizer.Rename(p+".partial", p); err != nil {
		ft.t.Fatal(err)
	}
}

func (ft *fetchTest) evict(rel string) {
	ft.t.Helper()
	if err := ft.srv.evict(context.Background(), ft.srv.record(ft.folder), rel); err != nil {
		ft.t.Fatalf("evict %s: %v", rel, err)
	}
	if _, err := os.Stat(filepath.Join(ft.folder, filepath.FromSlash(rel))); err == nil {
		ft.t.Fatalf("%s still on the disk", rel)
	}
}

func (ft *fetchTest) record(rel string) (fileRecord, bool) {
	r, ok := readState(ft.folder).Files[rel]
	return r, ok
}

// A file only in the bucket is still there for the studio: the recording is found, its MP3 is listed, and the
// MP3 is fetched (whole, with its time) when something reads it; the bucket keeps its copy.
func TestFetchOnDemand(t *testing.T) {
	ft := newFetchTest(t)
	mp3 := filepath.Join(ft.folder, "2026-10-18.mp3")
	before, _ := os.Stat(mp3)
	ft.evict("original/2026-10-18_raw.flac")
	ft.evict("2026-10-18.mp3")
	if r, _ := ft.record("2026-10-18.mp3"); !r.Bucket {
		t.Fatalf("not marked as in the bucket: %+v", r)
	}
	if ft.b.text("kirke/Taler/2026-10-18/2026-10-18.mp3") != "ID3 mp3" {
		t.Fatal("evicting removed the bucket's copy")
	}

	paths, _ := normalizer.NewPaths(ft.folder)
	if raw := paths.Source(); filepath.Base(raw) != "2026-10-18_raw.flac" {
		t.Errorf("the original only in the bucket is not the source: %q", raw)
	}
	if st, err := normalizer.Stat(mp3); err != nil || st.Size() != int64(len("ID3 mp3")) || !st.ModTime().Equal(before.ModTime().UTC()) {
		t.Errorf("Stat of a file in the bucket: %v %v", st, err)
	}
	var list struct {
		Recordings []struct {
			Path    string
			Raw     bool
			Outputs []string
		}
	}
	json.Unmarshal(do(ft.studio, "GET", "/api/recordings", "").Body.Bytes(), &list)
	if len(list.Recordings) != 1 || !list.Recordings[0].Raw || !slices.Contains(list.Recordings[0].Outputs, "mp3") {
		t.Errorf("the list: %+v", list.Recordings)
	}

	// The syncing of the record keeps them.
	if a, c, g := ft.srv.syncFiles(ft.folder); a+c+g != 0 {
		t.Errorf("synced: %d %d %d", a, c, g)
	}
	if r, ok := ft.record("2026-10-18.mp3"); !ok || !r.Bucket {
		t.Errorf("the record lost the file in the bucket: %+v", r)
	}

	if err := fetchFor(context.Background(), mp3); err != nil { // as before a run reads it
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(mp3); string(got) != "ID3 mp3" {
		t.Fatalf("fetched: %q", got)
	}
	if st, err := os.Stat(mp3); err != nil || !st.ModTime().Equal(before.ModTime()) {
		t.Errorf("fetched: %v %v", st, err)
	}
	if r, _ := ft.record("2026-10-18.mp3"); r.Bucket {
		t.Error("still marked as in the bucket once fetched")
	}
	settleMirror(t, ft.app)
	puts := ft.b.n
	ft.srv.syncFiles(ft.folder)
	settleMirror(t, ft.app)
	if ft.b.n != puts { // the file is the one in the bucket, and the record has it already
		t.Errorf("%d puts after the fetch", ft.b.n-puts)
	}
}

// A file fetched that is not the one recorded is not used.
func TestFetchChecked(t *testing.T) {
	ft := newFetchTest(t)
	ft.evict("2026-10-18.mp3")
	ft.b.store("kirke/Taler/2026-10-18/2026-10-18.mp3", []byte("ID3 MP3"), nil) // the same size, other bytes
	if err := fetchFor(context.Background(), filepath.Join(ft.folder, "2026-10-18.mp3")); err == nil {
		t.Error("a wrong file fetched")
	}
	if _, err := os.Stat(filepath.Join(ft.folder, "2026-10-18.mp3")); err == nil {
		t.Error("the wrong file is left on the disk")
	}
}

// Renaming a recording renames its files in the bucket too, and the record follows; deleting the original only
// in the bucket deletes it there; removing a folder left empty on the disk leaves what the bucket has in it.
func TestFetchRenameAndRemove(t *testing.T) {
	ft := newFetchTest(t)
	ft.evict("2026-10-18.mp3")
	ft.evict("original/2026-10-18_raw.flac")

	// An empty original/ removed (as by the cleanup) removes nothing from the bucket.
	if err := normalizer.Remove(filepath.Join(ft.folder, normalizer.OriginalDir)); err != nil {
		t.Fatal(err)
	}
	settleMirror(t, ft.app)
	if ft.b.text("kirke/Taler/2026-10-18/original/2026-10-18_raw.flac") == "" {
		t.Fatal("removing an empty folder removed the original from the bucket")
	}

	if w := do(ft.studio, "POST", "/api/rename?path="+url.QueryEscape("2026-10-18"), `{"date":"2026-10-18","name":"Advent"}`); w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	settleMirror(t, ft.app)
	ft.folder = filepath.Join(ft.church, "Taler", "2026", "2026-10-18 Advent") // the layout puts it in its year
	if got := ft.b.text("kirke/Taler/2026/2026-10-18 Advent/2026-10-18 Advent.mp3"); got != "ID3 mp3" {
		t.Errorf("the MP3 in the bucket after the rename: %q %v", got, ft.b.keys())
	}
	ft.srv.syncFiles(ft.folder) // the record follows by itself; here at once
	files := readState(ft.folder).Files
	if _, ok := files["2026-10-18 Advent.mp3"]; !ok || len(files) != 2 {
		t.Errorf("the record after the rename: %+v", files)
	}
	for rel, r := range files {
		if _, err := os.Stat(filepath.Join(ft.folder, filepath.FromSlash(rel))); r.Bucket || err != nil {
			t.Errorf("%s after the rename: %+v %v", rel, r, err) // a rename fetches the file it renames
		}
	}

	settleMirror(t, ft.app)
	ft.evict("original/2026-10-18 Advent_raw.flac")
	if w := do(ft.studio, "POST", "/api/recordings/delete-original?path="+url.QueryEscape("2026/2026-10-18 Advent"), ""); w.Code != http.StatusOK {
		t.Fatalf("delete original: %d %s", w.Code, w.Body)
	}
	settleMirror(t, ft.app)
	if ft.b.text("kirke/Taler/2026/2026-10-18 Advent/original/2026-10-18 Advent_raw.flac") != "" {
		t.Error("the original deleted is still in the bucket")
	}
	if _, ok := ft.record("original/2026-10-18 Advent_raw.flac"); ok {
		t.Error("the original deleted is still in the record")
	}
	paths, _ := normalizer.NewPaths(ft.folder)
	if raw := paths.Source(); raw != "" {
		t.Errorf("a source after the original was deleted: %q", raw)
	}
}

// A file is only evicted once the bucket has it as it is, with nothing waiting in the journal.
func TestEvictRefuses(t *testing.T) {
	ft := newFetchTest(t)
	mp3 := filepath.Join(ft.folder, "2026-10-18.mp3")

	// Changed since it was recorded.
	os.WriteFile(mp3, []byte("ID3 another"), 0o644)
	if err := ft.srv.evict(context.Background(), ft.srv.record(ft.folder), "2026-10-18.mp3"); !errors.Is(err, errNotEvicted) {
		t.Errorf("changed: %v", err)
	}

	// Recorded, but not in the bucket yet.
	ft.b.mu.Lock()
	ft.b.failPuts = true
	ft.b.mu.Unlock()
	ft.put("2026-10-18.txt", "Hei.")
	ft.srv.syncFiles(ft.folder)
	if err := ft.srv.evict(context.Background(), ft.srv.record(ft.folder), "2026-10-18.txt"); !errors.Is(err, errNotEvicted) {
		t.Errorf("not uploaded: %v", err)
	}
	if !fileExists(filepath.Join(ft.folder, "2026-10-18.txt")) {
		t.Error("removed though not in the bucket")
	}

	// Things that stay on the disk.
	if err := ft.srv.evict(context.Background(), ft.srv.record(ft.folder), "recording.json"); !errors.Is(err, errNotEvicted) {
		t.Errorf("recording.json: %v", err)
	}
}

// Without a bucket nothing is elsewhere.
func TestFetchNoBucket(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	folder := filepath.Join(ts.root, "2026-10-18")
	ts.file(t, "2026-10-18/2026-10-18.mp3", "mp3")
	if err := ts.evict(context.Background(), ts.record(folder), "2026-10-18.mp3"); !errors.Is(err, errNotEvicted) {
		t.Errorf("evicted without a bucket: %v", err)
	}
	if got := elsewhereFiles(folder); got != nil {
		t.Errorf("elsewhere: %v", got)
	}
}

// evictAll, with SERMON_TEST_EVICT set, gives a test server a bucket and keeps every recording's files only in
// it, removed from the disk as soon as they are recorded and in the bucket: the other tests then show what reads
// a file without fetching it (SERMON_TEST_EVICT=1 go test ./web/). Test code that reads the disk itself fails
// too: look at what the studio does.
var evicting sync.Map // the mirrors of evictAll

// skipIfEvicting skips a test that reads the disk itself, with SERMON_TEST_EVICT.
func skipIfEvicting(t *testing.T) {
	if os.Getenv("SERMON_TEST_EVICT") != "" {
		t.Skip("reads the disk itself (SERMON_TEST_EVICT)")
	}
}

func evictAll(t *testing.T, cfg *Config) {
	if os.Getenv("SERMON_TEST_EVICT") == "" {
		return
	}
	cfg.RecordFiles = true
	m, err := startMirror(newFakeBlob(), filepath.Dir(cfg.Root), []string{cfg.CacheDir}, cfg.Logger, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.close)
	evicting.Store(m, true)
	afterSync = func(s *Server, fr filesRecord) {
		folder := fr.folder
		m := mirrorFor(folder)
		if m == nil {
			return
		}
		if _, ok := evicting.Load(m); !ok {
			return
		}
		key, ok := m.key(folder)
		if !ok {
			return
		}
		m.add(mirrorOp{Kind: "upload", Key: key})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.settle(ctx)
		for rel, r := range fr.files() {
			if !r.Bucket {
				if err := s.evict(ctx, fr, rel); err == nil {
					s.cfg.Logger.Printf("test: evicted %s", rel)
				}
			}
		}
	}
}

// Audio kept only in the bucket is got there by the browser: the studio's file and a share's download and
// player are sent to an address of the bucket, and nothing is fetched onto the disk for them; the share page lets
// its player follow it. A file on the disk, and a transcript, are served as before.
func TestBucketLinks(t *testing.T) {
	ft := newFetchTest(t)
	ft.put("2026-10-18.txt", "Hei.")
	ft.srv.syncFiles(ft.folder)
	settleMirror(t, ft.app)
	ft.save(ft.folder)
	code, _ := ft.makeShare(`{"kind":"download","recording":"2026-10-18","formats":["mp3"],"label":"Lytt"}`)

	file := func(rel string, download bool) *httptest.ResponseRecorder {
		q := "/api/file?path=" + url.QueryEscape(rel)
		if download {
			q += "&download=1"
		}
		return do(ft.studio, "GET", q, "")
	}
	if w := file("2026-10-18/2026-10-18.mp3", false); w.Code != http.StatusOK {
		t.Errorf("on the disk: %d", w.Code)
	}
	ft.evict("2026-10-18.mp3")
	ft.evict("2026-10-18.txt")
	want := "https://bucket.example/kirke/Taler/2026-10-18/2026-10-18.mp3"
	for _, download := range []bool{false, true} {
		if w := file("2026-10-18/2026-10-18.mp3", download); w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), want) {
			t.Errorf("download %v: %d %v", download, w.Code, w.Header())
		}
	}
	if _, err := os.Stat(filepath.Join(ft.folder, "2026-10-18.mp3")); err == nil {
		t.Error("fetched onto the disk for a link")
	}
	if w := file("2026-10-18/2026-10-18.txt", false); w.Code != http.StatusOK || w.Body.String() != "Hei." {
		t.Errorf("the transcript: %d %q", w.Code, w.Body)
	}

	for _, listen := range []bool{false, true} {
		b, _ := json.Marshal(map[string]any{"format": "mp3", "listen": listen})
		w := ft.call("POST", "/api/share/downloads", code, "1.1.1.1", b)
		var out struct{ URL string }
		json.Unmarshal(w.Body.Bytes(), &out)
		resp := ft.fetch(out.URL, "")
		if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), want) {
			t.Errorf("share (listen %v): %d %v", listen, resp.StatusCode, resp.Header)
		}
	}
	if csp := ft.fetch("share", "").Header.Get("Content-Security-Policy"); !strings.Contains(csp, "media-src 'self' https://bucket.example;") {
		t.Errorf("the share page's CSP: %s", csp)
	}
}
