package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/models"
	"github.com/gunnaringe/sermon-studio/normalizer"
)

// newTranscriptServer is a test church with transcripts on, and a models folder with the models given
// (files of a few bytes: ffmpeg is not run).
func newTranscriptServer(t *testing.T, list ...models.Model) (*testServer, string) {
	t.Helper()
	dir := t.TempDir()
	writeModels(t, dir, list...)
	tr, err := newTranscriber(TranscriptionConfig{ModelsDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return newTestServerWith(t, normalizer.FFmpeg{}, func(c *Config) { c.transcriber = tr }), dir
}

func writeModels(t *testing.T, dir string, list ...models.Model) {
	t.Helper()
	for _, m := range list {
		os.WriteFile(filepath.Join(dir, m.File), []byte("ggml"), 0o644)
	}
	data, _ := json.Marshal(models.Manifest{Models: list})
	if err := os.WriteFile(filepath.Join(dir, models.ManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

type transcriptionView struct {
	Available bool
	Language  string
	Languages []struct {
		Code  string
		Model modelInfo
	}
}

var (
	day      = time.Date(2024, 2, 13, 12, 19, 11, 0, time.UTC)
	nbMedium = models.Model{Name: "nb-whisper-medium", File: "nb.bin", Languages: []string{"nb", "nn"}, SHA256: "aaa", Date: &day}
	whisper  = models.Model{Name: "whisper.cpp ggml-small", File: "small.bin", Languages: []string{"*"}, SHA256: "bbb"}
)

func TestTranscriptionSettings(t *testing.T) {
	ts, dir := newTranscriptServer(t, nbMedium)
	got := decode[transcriptionView](t, ts.do(t, "GET", "/api/transcription", nil))
	if !got.Available || got.Language != "" || len(got.Languages) != 2 || got.Languages[1].Code != "nn" ||
		got.Languages[1].Model.Name != "nb-whisper-medium" || !got.Languages[1].Model.Date.Equal(day) {
		t.Fatalf("settings: %+v", got)
	}
	if rec := ts.do(t, "PUT", "/api/transcription", map[string]string{"language": "nn"}); rec.Code != http.StatusOK {
		t.Fatalf("choose: %d %s", rec.Code, rec.Body)
	}
	if ts.transcriptLanguage() != "nn" {
		t.Errorf("language %q", ts.transcriptLanguage())
	}
	// A language needs a model.
	if rec := ts.do(t, "PUT", "/api/transcription", map[string]string{"language": "sv"}); rec.Code != http.StatusBadRequest {
		t.Errorf("a language without a model: %d", rec.Code)
	}
	// A model that arrives is there at once: models.json is read each time. One for any language adds
	// finding the language.
	writeModels(t, dir, nbMedium, whisper)
	got = decode[transcriptionView](t, ts.do(t, "GET", "/api/transcription", nil))
	if len(got.Languages) != 3 || got.Languages[2].Code != "*" || got.Language != "nn" {
		t.Errorf("with whisper: %+v", got)
	}
	if rec := ts.do(t, "PUT", "/api/transcription", map[string]string{"language": "sv"}); rec.Code != http.StatusOK {
		t.Errorf("any language: %d", rec.Code)
	}
	// Without transcripts in the studio, there is nothing to choose.
	plain := newTestServer(t, normalizer.FFmpeg{})
	if got := decode[transcriptionView](t, plain.do(t, "GET", "/api/transcription", nil)); got.Available {
		t.Errorf("without transcripts: %+v", got)
	}
	if rec := plain.do(t, "PUT", "/api/transcription", map[string]string{"language": "nb"}); rec.Code != http.StatusBadRequest {
		t.Errorf("choose without transcripts: %d", rec.Code)
	}
}

func TestTranscriptState(t *testing.T) {
	ts, dir := newTranscriptServer(t, nbMedium)
	folder := filepath.Join(ts.root, "2026-10-04")
	ts.file(t, "2026-10-04/2026-10-04.mp3", "mp3")
	info := func() *transcriptInfo {
		t.Helper()
		return decode[folderInfo](t, ts.do(t, "GET", "/api/folder?path=2026-10-04", nil)).Transcript
	}
	// Off: nothing to make, and asking for one says how to turn them on.
	if got := info(); got == nil || got.State != "" || got.CanMake {
		t.Errorf("off: %+v", got)
	}
	if rec := ts.do(t, "POST", "/api/transcript?path=2026-10-04", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("make while off: %d", rec.Code)
	}
	ts.transcriptLang = "nb"
	// The MP3 has no record of how it was made: there is nothing to make it from.
	if got := info(); got.CanMake {
		t.Errorf("without a record: %+v", got)
	}
	saved := &sermonSettings{Sections: []span{{Start: 10, End: 50}}}
	ts.updateState(folder, func(st *folderState) error {
		st.Saved = saved
		st.Outputs["mp3"] = outputMade{From: "sound-1", Cut: savedCut(*saved)}
		return nil
	})
	if got := info(); !got.CanMake || got.State != "" {
		t.Errorf("can make: %+v", got)
	}
	// A transcript made with the model there, of the saved cut.
	p, _ := normalizer.NewPaths(folder)
	text, captions, data := transcriptPaths(p)
	tf, _ := json.Marshal(transcriptFile{Language: "nb", Model: nbMedium, Segments: []segmentJSON{{0, 2, "Hei."}}})
	os.WriteFile(data, tf, 0o644)
	os.WriteFile(text, []byte("Hei.\n"), 0o644)
	os.WriteFile(captions, []byte("WEBVTT\n"), 0o644)
	ts.updateState(folder, func(st *folderState) error {
		st.Outputs[transcriptFormat] = outputMade{From: "sound-1", Cut: savedCut(*saved)}
		return nil
	})
	got := info()
	if got.State != "done" || got.OtherModel || got.EarlierCut || got.Model.Name != "nb-whisper-medium" || !got.Model.Date.Equal(day) ||
		got.Text == nil || got.Captions == nil {
		t.Errorf("done: %+v", got)
	}
	// A run does not make it again.
	ts.afterRun(folder)
	if len(ts.transcripts) != 0 {
		t.Errorf("made again after a run: %v", ts.transcripts)
	}
	// Another version of the model is installed: it says so.
	newer := nbMedium
	newer.SHA256, newer.File = "ccc", "nb2.bin"
	writeModels(t, dir, newer)
	if got := info(); !got.OtherModel || got.EarlierCut {
		t.Errorf("another model: %+v", got)
	}
	// The sermon is cut again and saved: an earlier cut.
	ts.updateState(folder, func(st *folderState) error {
		st.Saved = &sermonSettings{Sections: []span{{Start: 12, End: 50}}}
		return nil
	})
	if got := info(); !got.EarlierCut {
		t.Errorf("earlier cut: %+v", got)
	}
	// Deleting it takes the files and the record.
	if rec := ts.do(t, "POST", "/api/transcript/remove?path=2026-10-04", nil); rec.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if fileExists(text) || fileExists(captions) || fileExists(data) || readState(folder).Outputs[transcriptFormat].From != "" {
		t.Error("the transcript is left")
	}
	if got := info(); got.State != "" || !got.CanMake {
		t.Errorf("after delete: %+v", got)
	}
}

// The transcript is made from the sermon without the intro and outro: its times are the sermon's.
func TestTranscriptSource(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "2026-10-04")
	os.MkdirAll(folder, 0o755)
	p, err := normalizer.NewPaths(folder)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{p.Master, p.Mp3, p.SermonMp3} {
		os.WriteFile(f, []byte("x"), 0o644)
	}
	intro := &clipRef{ID: "i"}
	st := &folderState{Outputs: map[string]outputMade{
		"flac": {From: "a", Intro: intro}, "mp3": {From: "b", Intro: intro}, "mp3" + normalizer.SermonOnly: {From: "c"},
	}}
	if src, rec, ok := transcriptSource(p, st); !ok || src != p.SermonMp3 || rec.From != "c" {
		t.Errorf("with clips: %s %+v", src, rec)
	}
	// Without the sermon-only file, a file with the intro is not used.
	delete(st.Outputs, "mp3"+normalizer.SermonOnly)
	if src, _, ok := transcriptSource(p, st); ok {
		t.Errorf("only with clips: %s", src)
	}
	st.Outputs["flac"] = outputMade{From: "d"}
	if src, _, ok := transcriptSource(p, st); !ok || src != p.Master {
		t.Errorf("without clips: %s", src)
	}
	if !isTranscript(filepath.Join(p.Folder, "2026-10-04.txt")) || isTranscript(filepath.Join(p.Folder, "notes.txt")) ||
		isTranscript(filepath.Join(p.Folder, "2026-10-04.transcript.json")) {
		t.Error("isTranscript")
	}
	if whisperLanguage("nb") != "no" || whisperLanguage("nn") != "nn" || whisperLanguage("*") != "auto" {
		t.Error("whisperLanguage")
	}
}
