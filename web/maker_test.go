package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// makerServer is a server whose one run slot is taken, so the maker's runs wait (queued) until release.
func makerServer(t *testing.T) (ts *testServer, release func()) {
	t.Helper()
	ts = newTestServerWith(t, normalizer.FFmpeg{}, func(c *Config) { c.limits = newLimits(1) })
	<-ts.scanned // its orders would come between the test's
	ts.jobs.sem <- struct{}{}
	var once bool
	release = func() {
		if !once {
			once = true
			<-ts.jobs.sem
		}
	}
	t.Cleanup(release)
	return ts, release
}

// saveSermon keeps a sermon of folder (relative) with the formats and saves it, as the editor does.
func saveSermon(t *testing.T, ts *testServer, folder string, end int, formats string) {
	t.Helper()
	path := "?path=" + url.QueryEscape(folder)
	body := `{"sections":[{"start":1,"end":` + strings.TrimSpace(itoa(end)) + `}],"cuts":[],"intro":"","outro":"","formats":` + formats + `}`
	if rec := ts.do(t, "PUT", "/api/sermon"+path, json.RawMessage(body)); rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if rec := ts.do(t, "POST", "/api/sermon/save"+path, nil); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// eventually waits up to 5 s for ok.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	// Long enough for a busy CI machine (the image build runs every package's tests at once).
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("not seen: %s", what)
}

// Save makes the formats the sermon keeps, in the background; a Save during the run stops it and starts
// again from the new version. One that fails keeps its reason and is not tried again by itself.
func TestSaveMakes(t *testing.T) {
	ts, release := makerServer(t)
	ts.file(t, "2026-10-04/original/2026-10-04_raw.flac", "x")
	folder := filepath.Join(ts.root, "2026-10-04")
	saveSermon(t, ts, "2026-10-04", 9, `{"flac":true,"mp3":false,"opus":true}`)
	if f := readState(folder).Saved.Formats; f == nil || *f != (outputFormats{Flac: true, Opus: true}) {
		t.Fatalf("kept formats: %+v", f)
	}
	if _, todo := ts.toMake(folder); todo == nil || todo.outputFormats != (outputFormats{Flac: true, Opus: true}) || todo.again {
		t.Errorf("to make: %+v", todo)
	}
	var first *job
	eventually(t, "a run", func() bool { first = ts.jobs.active("2026-10-04"); return first != nil })

	saveSermon(t, ts, "2026-10-04", 12, `{"flac":true,"mp3":false,"opus":true}`)
	<-first.finished
	if st := first.snapshot().State; st != "cancelled" {
		t.Errorf("the run of the version before: %s", st)
	}
	var second *job
	eventually(t, "a new run", func() bool { second = ts.jobs.active("2026-10-04"); return second != nil && second != first })

	release() // the test's ffmpeg cannot make it: it fails
	<-second.finished
	eventually(t, "the failure kept", func() bool { return ts.maker.failure("2026-10-04") != "" })
	info := decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path=2026-10-04", nil))
	if info.MakeError == "" || info.Making != "" {
		t.Errorf("folder info: making %q, error %q", info.Making, info.MakeError)
	}
	time.Sleep(100 * time.Millisecond)
	if ts.jobs.latest("2026-10-04") != second {
		t.Error("a failed run was tried again")
	}
	// Saved again (nothing changed), it is tried again.
	if rec := ts.do(t, "POST", "/api/sermon/save?path=2026-10-04", nil); rec.Code != http.StatusOK {
		t.Fatalf("save: %d", rec.Code)
	}
	eventually(t, "a run after Save", func() bool { return ts.jobs.latest("2026-10-04") != second })
}

// Save deletes the files of the formats the sermon no longer keeps, when the user may delete files.
func TestSaveDropsFormats(t *testing.T) {
	ts, release := makerServer(t)
	ts.file(t, "2026-10-04/original/2026-10-04_raw.flac", "x")
	mp3 := ts.file(t, "2026-10-04/2026-10-04.mp3", "x")
	ts.file(t, "2026-10-11/original/2026-10-11_raw.flac", "x")
	kept := ts.file(t, "2026-10-11/2026-10-11.mp3", "x")

	saveSermon(t, ts, "2026-10-04", 9, `{"flac":true,"mp3":false,"opus":false}`)
	eventually(t, "the MP3 deleted", func() bool { return !fileExists(mp3) })

	path := "?path=2026-10-11"
	ts.do(t, "PUT", "/api/sermon"+path, json.RawMessage(`{"sections":[{"start":1,"end":9}],"cuts":[],"formats":{"flac":true,"mp3":false,"opus":false}}`))
	req := httptest.NewRequest("POST", "/api/sermon/save"+path, nil)
	req = req.WithContext(context.WithValue(req.Context(), roleKey{}, RoleDemo))
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save as a demo: %d %s", rec.Code, rec.Body)
	}
	// Only the folder whose run waits for the slot is busy: another waiting in the maker's queue is not.
	ts.file(t, "2026-10-18/original/2026-10-18_raw.flac", "x")
	ts.file(t, "2026-10-18/2026-10-18.opus", "x")
	saveSermon(t, ts, "2026-10-18", 9, `{"flac":true,"mp3":false,"opus":true}`)
	if info := decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path=2026-10-18", nil)); info.Making != "waiting" {
		t.Errorf("making: %q", info.Making)
	}
	if rec := ts.do(t, "POST", "/api/outputs/remove?path=2026-10-18&format=opus", nil); rec.Code != http.StatusOK {
		t.Errorf("remove while waiting in the queue: %d %s", rec.Code, rec.Body)
	}
	release()
	eventually(t, "the demo's recording made", func() bool {
		j := ts.jobs.latest("2026-10-11")
		return j != nil && j.snapshot().State == "failed"
	})
	if !fileExists(kept) {
		t.Error("a demo's Save deleted a file")
	}
}

// The start finds the saved recordings with files to make, newest first: not one not saved, whose files are
// up to date (or made before the studio kept track), or whose original is deleted.
func TestScanToMake(t *testing.T) {
	ts, _ := makerServer(t)
	keep := func(folder string, formats *outputFormats) string {
		abs := filepath.Join(ts.root, folder)
		ts.file(t, folder+"/original/"+filepath.Base(folder)[:10]+"_raw.flac", "x")
		saved := sermonSettings{cutting: cutting{Sections: []span{{Start: 1, End: 9}}, Cuts: []span{}}, Formats: formats}
		ts.updateState(abs, func(st *folderState) error { st.Saved = &saved; return nil })
		return abs
	}
	flac := &outputFormats{Flac: true}
	keep("2026-10-04", flac) // no files
	unknown := keep("2026-10-11", flac)
	ts.file(t, "2026-10-11/2026-10-11.flac", "x") // made before the studio kept track
	upToDate := keep("2026-10-18", flac)
	ts.file(t, "2026-10-18/2026-10-18.flac", "x")
	fp := fingerprints(t, ts, upToDate)["flac"]
	ts.updateState(upToDate, func(st *folderState) error { st.Outputs["flac"] = outputMade{From: fp}; return nil })
	stale := keep("2026/2026-10-25 Kveld", flac)
	ts.file(t, "2026/2026-10-25 Kveld/2026-10-25 Kveld.flac", "x")
	ts.updateState(stale, func(st *folderState) error { st.Outputs["flac"] = outputMade{From: "other"}; return nil })
	gone := keep("2026-11-01", flac)
	os.RemoveAll(filepath.Join(gone, "original"))
	ts.file(t, "2026-11-08/original/2026-11-08_raw.flac", "x") // not saved

	if got, want := ts.toMakeFolders(), []string{"2026/2026-10-25 Kveld", "2026-10-04"}; !reflect.DeepEqual(got, want) {
		t.Errorf("to make: %q, want %q", got, want)
	}
	if _, todo := ts.toMake(stale); todo == nil || !todo.again {
		t.Errorf("the stale one is made again: %+v", todo)
	}
	_ = unknown
}

// A format made with Make is kept made from then on.
func TestMakeKeepsFormat(t *testing.T) {
	ts, release := makerServer(t)
	defer release()
	ts.file(t, "2026-10-04/original/2026-10-04_raw.flac", "x")
	folder := filepath.Join(ts.root, "2026-10-04")
	only := outputFormats{Flac: true}
	saved := sermonSettings{cutting: cutting{Sections: []span{{Start: 1, End: 9}}, Cuts: []span{}}, Formats: &only}
	ts.updateState(folder, func(st *folderState) error { st.Saved = &saved; return nil })
	if rec := ts.do(t, "POST", "/api/make?path=2026-10-04&format=mp3", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("make: %d %s", rec.Code, rec.Body)
	}
	if f := readState(folder).Saved.Formats; *f != (outputFormats{Flac: true, Mp3: true}) {
		t.Errorf("kept formats: %+v", f)
	}
	// Removed by hand, a format is no longer kept: the start does not make it again.
	release()
	ts.settle(t)
	if rec := ts.do(t, "POST", "/api/outputs/remove?path=2026-10-04&format=flac&format=mp3", nil); rec.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if f := readState(folder).Saved.Formats; *f != (outputFormats{}) {
		t.Errorf("kept formats after removing: %+v", f)
	}
	if got := ts.toMakeFolders(); len(got) != 0 {
		t.Errorf("to make after removing: %q", got)
	}
}

// settle waits until the maker has nothing left to do and no run is going on.
func (ts *testServer) settle(t *testing.T) {
	t.Helper()
	eventually(t, "the runs over", func() bool {
		ts.maker.mu.Lock()
		running := ts.maker.running
		ts.maker.mu.Unlock()
		if running {
			return false
		}
		ts.jobs.mu.Lock()
		defer ts.jobs.mu.Unlock()
		for _, j := range ts.jobs.all {
			if j.snapshot().State == "queued" || j.snapshot().State == "running" {
				return false
			}
		}
		return true
	})
}

// The folder info and the list say what the server makes: the formats of its run, or those waiting to be made;
// a transcript of another cut does not make the files out of date.
func TestMakingInfo(t *testing.T) {
	ts, _ := makerServer(t)
	ts.file(t, "2026-10-04/original/2026-10-04_raw.flac", "x")
	ts.file(t, "2026-10-11/original/2026-10-11_raw.flac", "x")
	saveSermon(t, ts, "2026-10-04", 9, `{"flac":true,"mp3":false,"opus":false}`)
	// Its run first: a Save goes first in the queue, so the second one, saved before the maker took the first,
	// would be the one running.
	eventually(t, "a run", func() bool { return ts.jobs.active("2026-10-04") != nil })
	saveSermon(t, ts, "2026-10-11", 9, `{"flac":false,"mp3":true,"opus":true}`)
	running := decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path=2026-10-04", nil))
	if !reflect.DeepEqual(running.MakingFormats, []string{"flac"}) || running.Making != "" || !reflect.DeepEqual(running.Job.Formats, []string{"flac"}) {
		t.Errorf("running: making %q %v, job %+v", running.Making, running.MakingFormats, running.Job)
	}
	waiting := decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path=2026-10-11", nil))
	if !reflect.DeepEqual(waiting.MakingFormats, []string{"mp3", "opus"}) || waiting.Making != "waiting" {
		t.Errorf("waiting: making %q %v", waiting.Making, waiting.MakingFormats)
	}
	for _, e := range decode[struct {
		Recordings []folderEntry `json:"recordings"`
	}](t, ts.do(t, "GET", "/api/recordings", nil)).Recordings {
		if want := map[string]string{"2026-10-04": "", "2026-10-11": "waiting"}[e.Path]; e.Making != want {
			t.Errorf("%s in the list: making %q, want %q", e.Path, e.Making, want)
		}
	}
	folder := filepath.Join(ts.root, "2026-10-11")
	ts.updateState(folder, func(st *folderState) error {
		st.Outputs[transcriptFormat] = outputMade{Cut: "an earlier cut"}
		return nil
	})
	if e := ts.entry(folder); e.Outdated {
		t.Error("a transcript of another cut makes the files out of date")
	}
}
