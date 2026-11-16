package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// defaultsServer is a server with two church clips (the first the default intro) and a title template that
// uses the sermon's name.
func defaultsServer(t *testing.T) *testServer {
	t.Helper()
	ts, _ := clipServer(t, normalizer.FFmpeg{}, &clipLibrary{Intro: "0123456789ab", Clips: []clipInfo{
		{ID: "0123456789ab", Name: "Jingle", Version: "v1", Seconds: 3},
		{ID: "ba9876543210", Name: "Ny", Version: "v1", Seconds: 4},
	}})
	for _, c := range ts.clips.lib.Clips {
		os.MkdirAll(filepath.Dir(ts.clips.clipFile(c.ID)), 0o755)
		os.WriteFile(ts.clips.clipFile(c.ID), []byte("x"), 0o644)
	}
	ts.meta.meta = Meta{Title: "{yyyy}-{mm}-{dd} {name}", Artist: "Kirken"}
	return ts
}

// fingerprints are the madeFrom of each format a run of the saved version would make (as savedStatus
// compares them).
func fingerprints(t *testing.T, ts *testServer, folder string) map[string]string {
	t.Helper()
	q, err := ts.savedRequest(folder)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprintsOf(t, ts, folder, q)
}

func fingerprintsOf(t *testing.T, ts *testServer, folder string, q runRequest) map[string]string {
	t.Helper()
	q.Flac, q.Mp3, q.Opus = true, true, true
	o, err := q.options()
	if err != nil {
		t.Fatal(err)
	}
	clips, err := ts.applyClips(folder, q.Intro, q.Outro, q.clipPause, &o)
	if err != nil {
		t.Fatal(err)
	}
	o.Tags, _ = ts.runTags(folder, q)
	out := map[string]string{}
	for _, f := range []string{"flac", "mp3", "opus"} {
		out[f] = madeFrom(o, f, clips.key())
	}
	return out
}

// changeDefaults changes every church default a sermon could take.
func changeDefaults(ts *testServer) {
	ts.clips.lib.Intro = "ba9876543210"
	pause := 2.0
	ts.clips.lib.Pause = &pause
	ts.meta.meta = Meta{Title: "Preken {dd}.{mm}.", Artist: "Andre"}
	p := builtinProcessing()
	p.NoiseReduction = 17
	ts.proc.proc = &p
}

// Save keeps the church's defaults with the sermon: the clips they are, the tags, the pause and the sound.
// Defaults changed after it do not change it, nor what its files are made from.
func TestSaveKeepsDefaults(t *testing.T) {
	ts := defaultsServer(t)
	ts.file(t, "2026-10-04 Kveld/original/2026-10-04_raw.flac", "x")
	folder := filepath.Join(ts.root, "2026-10-04 Kveld")
	path := "/api/sermon?path=" + url.QueryEscape("2026-10-04 Kveld")
	body := `{"sections":[{"start":1,"end":9}],"cuts":[],"fadeIn":0,"fadeOut":1,"intro":"default","outro":""}`
	if rec := ts.do(t, "PUT", path, json.RawMessage(body)); rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if rec := ts.do(t, "POST", "/api/sermon/save?path="+url.QueryEscape("2026-10-04 Kveld"), nil); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	saved := readState(folder).Saved
	if saved.Intro != "0123456789ab" || saved.Outro != "" || saved.ClipPause == nil || *saved.ClipPause != defaultClipPause || saved.Sound == nil ||
		saved.Tags == nil || *saved.Tags != (sermonTags{Title: "2026-10-04 Kveld", Artist: "Kirken"}) {
		t.Fatalf("saved: %+v tags %+v", saved, saved.Tags)
	}
	before := fingerprints(t, ts, folder)
	changeDefaults(ts)
	if after := fingerprints(t, ts, folder); !reflect.DeepEqual(after, before) {
		t.Errorf("the church's defaults changed what the files are made from: %v, was %v", after, before)
	}
	// The editor's check sends no pause: the sermon's kept one is used, not the church's new one.
	cq := ts.processing().start()
	cq.Sections, cq.Intro = []span{{Start: 1, End: 9}}, "0123456789ab"
	if pl := ts.check(folder, cq); pl.Clips.Pause != defaultClipPause {
		t.Errorf("the check's pause: %+v %s", pl.Clips, pl.Error)
	}
	// The editor sends what it shows: the kept clip and tags (not the pause). That is not an adjustment.
	same := `{"sections":[{"start":1,"end":9}],"cuts":[],"fadeIn":0,"fadeOut":1,"intro":"0123456789ab","outro":"",` +
		`"sound":` + mustJSON(t, saved.Sound) + `,"tags":{"title":"2026-10-04 Kveld","artist":"Kirken","album":""}}`
	if f := decode[sermonFile](t, ts.do(t, "PUT", path, json.RawMessage(same))); f.Adjusted != nil {
		t.Errorf("the saved version sent back is an adjustment: %+v", f.Adjusted)
	}
	// Tags typed in the editor are kept, one line with single spaces.
	typed := `{"sections":[{"start":1,"end":9}],"cuts":[],"fadeIn":0,"fadeOut":1,"intro":"0123456789ab","outro":"",` +
		`"tags":{"title":"  Påske \n morgen ","artist":"","album":""}}`
	ts.do(t, "PUT", path, json.RawMessage(typed))
	ts.do(t, "POST", "/api/sermon/save?path="+url.QueryEscape("2026-10-04 Kveld"), nil)
	q, _ := ts.savedRequest(folder)
	if tags, _ := ts.runTags(folder, q); tags.Title != "Påske morgen" || tags.Artist != "" || tags.Date != "2026-10-04" {
		t.Errorf("tags of the saved version: %+v", tags)
	}
	long := `{"sections":[{"start":1,"end":9}],"cuts":[],"tags":{"title":"` + string(make([]byte, 201)) + `"}}`
	if rec := ts.do(t, "PUT", path, json.RawMessage(long)); rec.Code != http.StatusBadRequest {
		t.Errorf("a tag too long: %d", rec.Code)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A sermon saved before Save kept the defaults gets those it follows now as the server starts, with the
// files' fingerprints as they were; after that, the church's defaults no longer reach it.
func TestKeepDefaultsOnStart(t *testing.T) {
	ts := defaultsServer(t)
	ts.file(t, "2026-10-04/original/2026-10-04_raw.flac", "x")
	folder := filepath.Join(ts.root, "2026-10-04")
	old := sermonSettings{cutting: cutting{Sections: []span{{Start: 1, End: 9}}, Cuts: []span{}}, FadeOut: 1, Intro: clipDefault, Outro: clipDefault}
	ts.updateState(folder, func(st *folderState) error { st.Saved = &old; return nil })

	// What a run of it was made from before: the church's processing, default clips, pause and tags as they are.
	q := ts.processing().start()
	q.cutting, q.FadeOut, q.Intro, q.Outro = old.cutting, old.FadeOut, old.Intro, old.Outro
	before := fingerprintsOf(t, ts, folder, q)

	ts.file(t, "2026-10-04/2026-10-04.mp3", "x") // the files it has are the formats it keeps
	ts.keepDefaults()
	saved := readState(folder).Saved
	if saved.Intro != "0123456789ab" || saved.Outro != "" || saved.Tags == nil || saved.Sound == nil || saved.ClipPause == nil ||
		saved.Formats == nil || *saved.Formats != (outputFormats{Mp3: true}) {
		t.Fatalf("kept: %+v %+v", saved, saved.Formats)
	}
	if after := fingerprints(t, ts, folder); !reflect.DeepEqual(after, before) {
		t.Errorf("keeping the defaults changed the fingerprints: %v, was %v", after, before)
	}
	// Done again, nothing is written.
	info, _ := os.Stat(statePath(folder))
	os.Chtimes(statePath(folder), info.ModTime().Add(-3600e9), info.ModTime().Add(-3600e9))
	info, _ = os.Stat(statePath(folder))
	ts.keepDefaults()
	if again, _ := os.Stat(statePath(folder)); !again.ModTime().Equal(info.ModTime()) {
		t.Error("recording.json was written again")
	}
	changeDefaults(ts)
	if after := fingerprints(t, ts, folder); !reflect.DeepEqual(after, before) {
		t.Errorf("the church's defaults still reach it: %v, was %v", after, before)
	}
}

// Renamed, a kept tag that is what the template made for the old date and name follows the new one; one
// written by hand stays.
func TestRenameFollowsTemplateTags(t *testing.T) {
	ts := defaultsServer(t)
	ts.file(t, "2026/2026-10-04/original/2026-10-04_raw.flac", "x")
	folder := filepath.Join(ts.root, "2026", "2026-10-04")
	saved := sermonSettings{cutting: cutting{Sections: []span{{Start: 1, End: 9}}, Cuts: []span{}},
		Tags: &sermonTags{Title: "2026-10-04 ", Artist: "Gjest", Album: ""}}
	ts.updateState(folder, func(st *folderState) error { st.Saved = &saved; return nil })
	if rec := ts.do(t, "POST", "/api/rename?path="+url.QueryEscape("2026/2026-10-04"), json.RawMessage(`{"date":"2026-10-04","name":"Kveld"}`)); rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	got := readState(filepath.Join(ts.root, "2026", "2026-10-04 Kveld")).Saved.Tags
	if *got != (sermonTags{Title: "2026-10-04 Kveld", Artist: "Gjest"}) {
		t.Errorf("tags after the rename: %+v", got)
	}
}
