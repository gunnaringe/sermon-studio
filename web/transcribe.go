package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gunnaringe/sermon-studio/models"
	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Transcripts: what is said in a sermon, made by ffmpeg's whisper filter from the files: by itself after a
// run when the church has chosen the language its sermons are in (Settings → Processing → Transcripts), and
// when asked, in the language and with the model asked for (the editor's Transcript; the church's language
// and the model for it by default). The studio only uses
// the models in the models folder (models.json, package models; `sermon-studio models pull` fills it),
// read each time, so a model that arrives or changes is used from the next transcript on. A transcript is
// never made again by itself: one made with another version of the model, or from an earlier cut, says so
// and is made again when asked. It runs on the CPU and takes about as long as the sermon, so one is made
// at a time, across all churches.

// TranscriptionConfig turns transcripts on (AppConfig.Transcription).
type TranscriptionConfig struct {
	// ModelsDir is the models folder (models.json and the models).
	ModelsDir string
}

// transcriber is what the churches share: the models folder and the one transcript made at a time.
type transcriber struct {
	dir string
	sem chan struct{}
}

func newTranscriber(cfg TranscriptionConfig) (*transcriber, error) {
	if cfg.ModelsDir == "" {
		return nil, errors.New("transcripts need a models folder")
	}
	return &transcriber{dir: cfg.ModelsDir, sem: make(chan struct{}, 1)}, nil
}

// installed are the models there now (none when models.json cannot be read).
func (tr *transcriber) installed() []models.Model {
	m, err := models.Read(tr.dir)
	if err != nil {
		return nil
	}
	return m.Installed(tr.dir)
}

// whisperLanguage is whisper's code for a language: Bokmål is "no" there (NB-Whisper writes Bokmål for it,
// and Nynorsk for "nn"), and finding the language is "auto".
func whisperLanguage(lang string) string {
	switch lang {
	case "nb":
		return "no"
	case models.AnyLanguage:
		return "auto"
	}
	return lang
}

// transcriptFormat is the transcript's record in recording.json's outputs: the cut and the sound it was
// made from (its fingerprint, From).
const transcriptFormat = "transcript"

// transcriptFile is <date>.transcript.json: the transcript itself, what the text and the captions are
// written from.
type transcriptFile struct {
	Language string        `json:"language"` // the church's choice: "nb", or "*" (found by whisper)
	Model    models.Model  `json:"model"`    // the version that made it
	Made     time.Time     `json:"made"`
	Segments []segmentJSON `json:"segments"`
}

type segmentJSON struct {
	Start float64 `json:"start"` // seconds
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// transcriptWork is a transcript being made: its stage ("waiting" for its turn, "running"), how far it
// is (0..1), or the error it ended with ("failed").
type transcriptWork struct {
	Language string  `json:"-"` // what it is made in
	Model    string  `json:"-"` // the file of the model asked for; "" = the one for the language
	State    string  `json:"state"`
	Progress float64 `json:"progress,omitempty"`
	Error    string  `json:"error,omitempty"`
	cancel   context.CancelFunc
}

// transcriptInfo is the sermon's transcript in the folder info.
type transcriptInfo struct {
	Language string `json:"language,omitempty"` // the church's
	// State is "", "waiting", "running" or "failed" (the work), else "done" when it is there.
	State    string    `json:"state"`
	Progress float64   `json:"progress,omitempty"`
	Error    string    `json:"error,omitempty"`
	Text     *fileInfo `json:"text,omitempty"`
	Captions *fileInfo `json:"captions,omitempty"`
	// Spoken is the language of the transcript there, or of the one being made (or failed): what Make again
	// makes it in.
	Spoken string `json:"spoken,omitempty"`
	// Model is the model it was made with (its name and date); OtherModel when the model for the
	// language now is another one, or another version; EarlierCut when it is of another cut than the
	// saved one.
	Model      *modelInfo `json:"model,omitempty"`
	OtherModel bool       `json:"otherModel,omitempty"`
	EarlierCut bool       `json:"earlierCut,omitempty"`
	// HasFiles tells whether the files it is made from are there; CanMake whether one can be made in
	// Spoken, else the church's language: the files, and a model for it.
	HasFiles bool `json:"hasFiles"`
	CanMake  bool `json:"canMake"`
}

type modelInfo struct {
	Name string     `json:"name"`
	File string     `json:"file,omitempty"`
	Date *time.Time `json:"date,omitempty"`
}

// transcriptPaths are the transcript's files: the text, the captions and the transcript itself.
func transcriptPaths(p normalizer.Paths) (text, captions, data string) {
	base := filepath.Join(p.Folder, p.Date)
	return base + ".txt", base + ".vtt", base + ".transcript.json"
}

// isTranscript tells whether a file is a sermon's text or captions (no other text file is served).
func isTranscript(abs string) bool {
	p, err := normalizer.NewPaths(filepath.Dir(abs))
	if err != nil {
		return false
	}
	text, captions, _ := transcriptPaths(p)
	return abs == text || abs == captions
}

// transcriptLanguage is the language the church's transcripts are in; "" = none are made.
func (s *Server) transcriptLanguage() string {
	if s.cfg.transcriber == nil {
		return ""
	}
	s.transcriptMu.Lock()
	defer s.transcriptMu.Unlock()
	return s.transcriptLang
}

// transcriptSource is the file a transcript is made from, and its record: the sermon without the intro and
// outro, so the times are the sermon's (the sermon-only file of a run with clips, else a main file).
func transcriptSource(p normalizer.Paths, st *folderState) (string, outputMade, bool) {
	for _, o := range sermonOnlyOutputs(p) {
		if rec, ok := st.Outputs[o.format]; ok && fileExists(o.path) {
			return o.path, rec, true
		}
	}
	for _, o := range outputsOf(p) {
		if rec, ok := st.Outputs[o.format]; ok && fileExists(o.path) && rec.Intro == nil && rec.Outro == nil {
			return o.path, rec, true
		}
	}
	return "", outputMade{}, false
}

func readTranscript(path string) (*transcriptFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tf transcriptFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, err
	}
	return &tf, nil
}

// transcript is the sermon's transcript as the page shows it.
func (s *Server) transcript(p normalizer.Paths, st *folderState) *transcriptInfo {
	lang := s.transcriptLanguage()
	info := &transcriptInfo{Language: lang}
	text, captions, data := transcriptPaths(p)
	_, _, info.HasFiles = transcriptSource(p, st)
	installed := s.cfg.transcriber.installed()
	if tf, err := readTranscript(data); err == nil {
		info.State, info.Spoken = "done", tf.Language
		info.Text, info.Captions = s.fileInfo(text), s.fileInfo(captions)
		info.Model = &modelInfo{Name: tf.Model.Name, File: tf.Model.File, Date: tf.Model.Date}
		// Another version of its model is there now, or (it is gone) another model for the language.
		current, ok := models.Choose(installed, tf.Language)
		for _, m := range installed {
			if m.Same(tf.Model) {
				current, ok = m, true
				break
			}
		}
		info.OtherModel = ok && current.Version() != tf.Model.Version()
		if rec, ok := st.Outputs[transcriptFormat]; ok && st.Saved != nil {
			info.EarlierCut = rec.Cut != "" && rec.Cut != savedCut(*st.Saved)
		}
	}
	s.transcriptMu.Lock()
	if w := s.transcripts[p.Folder]; w != nil {
		info.State, info.Progress, info.Error, info.Spoken = w.State, w.Progress, w.Error, w.Language
	}
	s.transcriptMu.Unlock()
	makeIn := info.Spoken
	if makeIn == "" {
		makeIn = lang
	}
	_, haveModel := models.Choose(installed, makeIn)
	info.CanMake = makeIn != "" && info.HasFiles && haveModel
	return info
}

var (
	errNoTranscript = errors.New("choose the language of the sermon")
	errNoSource     = errors.New("make the files first: the transcript is made from them")
	errNoModel      = errors.New("no speech model for this language is installed")
	errNotForIt     = errors.New("this speech model is not for this language")
)

// pickModel is the model a transcript in lang is made with: the installed one in file, else the one for
// the language.
func pickModel(installed []models.Model, lang, file string) (models.Model, error) {
	if file == "" {
		if m, ok := models.Choose(installed, lang); ok {
			return m, nil
		}
		return models.Model{}, errNoModel
	}
	m, ok := models.Find(installed, file)
	switch {
	case !ok:
		return m, errors.New("this speech model is not installed")
	case !m.Can(lang) || lang == models.AnyLanguage && !m.For(models.AnyLanguage):
		return m, errNotForIt
	}
	return m, nil
}

// startTranscript makes the folder's transcript in lang with the model in file ("" = the one for lang) in
// the background, unless it is being made.
func (s *Server) startTranscript(folder, lang, file string) error {
	if lang == "" {
		return errNoTranscript
	}
	if lang != models.AnyLanguage && !normalizer.ValidLanguage(lang) {
		return fmt.Errorf("not a language: %q", lang)
	}
	p, err := normalizer.NewPaths(folder)
	if err != nil {
		return err
	}
	if _, _, ok := transcriptSource(p, readState(folder)); !ok {
		return errNoSource
	}
	if _, err := pickModel(s.cfg.transcriber.installed(), lang, file); err != nil {
		return err
	}
	s.transcriptMu.Lock()
	defer s.transcriptMu.Unlock()
	if w := s.transcripts[folder]; w != nil && w.State != "failed" {
		return nil
	}
	ctx, cancel := context.WithCancel(s.ctx)
	w := &transcriptWork{Language: lang, Model: file, State: "waiting", cancel: cancel}
	s.transcripts[folder] = w
	s.background(func() {
		defer cancel()
		err := s.makeTranscript(ctx, folder, lang, file, w)
		s.transcriptMu.Lock()
		defer s.transcriptMu.Unlock()
		switch {
		case err == nil, ctx.Err() != nil:
			delete(s.transcripts, folder)
		default:
			w.State, w.Error, w.Progress = "failed", err.Error(), 0
			s.cfg.Logger.Printf("transcript of %s: %v", s.display(folder), err)
		}
	})
	return nil
}

// setTranscript changes a transcript's work under the lock.
func (s *Server) setTranscript(fn func()) {
	s.transcriptMu.Lock()
	defer s.transcriptMu.Unlock()
	fn()
}

// makeTranscript waits for its turn, transcribes the sermon and writes the transcript, its text and its
// captions.
func (s *Server) makeTranscript(ctx context.Context, folder, lang, file string, w *transcriptWork) error {
	tr := s.cfg.transcriber
	select {
	case tr.sem <- struct{}{}:
		defer func() { <-tr.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	// What it is made from and with, now that it is its turn: a run may have made the files again, and the
	// model may have changed.
	p, err := normalizer.NewPaths(folder)
	if err != nil {
		return err
	}
	source, rec, ok := transcriptSource(p, readState(folder))
	if !ok {
		return errNoSource
	}
	model, err := pickModel(tr.installed(), lang, file)
	if err != nil {
		return err
	}
	seconds := 0.0
	if pr, err := s.cfg.FFmpeg.ProbeFile(ctx, source); err == nil {
		seconds = pr.Duration
	}
	s.setTranscript(func() { w.State = "running" })
	srt := filepath.Join(folder, "."+p.Date+".transcript.srt")
	defer normalizer.Remove(srt)
	t := normalizer.Transcription{Model: filepath.Join(tr.dir, model.File), Language: whisperLanguage(lang)}
	err = s.cfg.FFmpeg.Transcribe(ctx, source, srt, t, func(done float64) {
		if seconds > 0 {
			s.setTranscript(func() { w.Progress = min(done/seconds, 1) })
		}
	})
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(srt)
	if err != nil {
		return err
	}
	segs, err := normalizer.ReadSRT(raw)
	if err != nil {
		return err
	}
	tf := transcriptFile{Language: lang, Model: model, Made: time.Now().UTC(), Segments: []segmentJSON{}}
	for _, sg := range segs {
		tf.Segments = append(tf.Segments, segmentJSON{Start: sg.Start.Seconds(), End: sg.End.Seconds(), Text: sg.Text})
	}
	text, captions, data := transcriptPaths(p)
	js, _ := json.MarshalIndent(tf, "", "  ")
	for _, f := range []struct {
		path string
		data []byte
	}{{data, append(js, '\n')}, {captions, normalizer.WebVTT(segs)}, {text, normalizer.PlainText(segs)}} {
		if err := writeFileAtomic(f.path, f.data); err != nil {
			return err
		}
	}
	_, err = s.updateState(folder, func(st *folderState) error {
		st.Outputs[transcriptFormat] = outputMade{MadeAt: tf.Made, From: rec.From, Cut: rec.Cut}
		return nil
	})
	return err
}

// afterRun makes the first transcript once a run has made the files, when the church has transcripts and a
// model for its language. One that is there is not made again: that is the user's choice (an earlier cut,
// another model).
func (s *Server) afterRun(folder string) {
	if s.transcriptLanguage() == "" {
		return
	}
	p, err := normalizer.NewPaths(folder)
	if err != nil {
		return
	}
	if _, _, data := transcriptPaths(p); fileExists(data) {
		return
	}
	if err := s.startTranscript(folder, s.transcriptLanguage(), ""); err != nil && !errors.Is(err, errNoModel) {
		s.cfg.Logger.Printf("transcript of %s: %v", s.display(folder), err)
	}
}

// handleTranscript makes the transcript (again): POST api/transcript?path=&language=&model=, in the church's
// language when none is given, with the model in the file model= (one for the language), else the one for it.
func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	lang := r.URL.Query().Get("language")
	if lang == "" {
		lang = s.transcriptLanguage()
	}
	if err := s.startTranscript(abs, lang, r.URL.Query().Get("model")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeFolder(w, abs)
}

// handleTranscriptCancel stops making it: POST api/transcript/cancel?path=.
func (s *Server) handleTranscriptCancel(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	s.transcriptMu.Lock()
	if w := s.transcripts[abs]; w != nil {
		w.cancel()
		if w.State == "failed" {
			delete(s.transcripts, abs)
		}
	}
	s.transcriptMu.Unlock()
	s.writeFolder(w, abs)
}

// handleTranscriptRemove deletes the transcript: POST api/transcript/remove?path=.
func (s *Server) handleTranscriptRemove(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	p, err := normalizer.NewPaths(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.transcriptMu.Lock()
	busy := s.transcripts[abs] != nil && s.transcripts[abs].State != "failed"
	s.transcriptMu.Unlock()
	if busy {
		writeError(w, http.StatusConflict, errBusy)
		return
	}
	text, captions, data := transcriptPaths(p)
	for _, f := range []string{data, text, captions} {
		if err := normalizer.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, errors.New("could not delete "+filepath.Base(f)))
			return
		}
	}
	if _, err := s.updateState(abs, func(st *folderState) error {
		delete(st.Outputs, transcriptFormat)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeFolder(w, abs)
}

func (s *Server) writeFolder(w http.ResponseWriter, abs string) {
	info, err := s.folderInfo(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// transcriptionSettings is what Settings → Processing shows of transcripts: whether the studio makes them,
// the church's language, and the languages there are models for, with the model each one would use; and
// for the editor's choice of a model, all of them.
func (s *Server) transcriptionSettings() map[string]any {
	tr := s.cfg.transcriber
	if tr == nil {
		return map[string]any{"available": false}
	}
	installed := tr.installed()
	langs := []map[string]any{}
	for _, lang := range models.Languages(installed) {
		m, _ := models.Choose(installed, lang)
		langs = append(langs, map[string]any{"code": lang, "model": modelInfo{Name: m.Name, Date: m.Date}})
	}
	list := []map[string]any{}
	for _, m := range installed {
		list = append(list, map[string]any{"name": m.Name, "file": m.File, "languages": m.Languages, "size": m.WhisperSize(),
			"bytes": m.Size, "date": m.Date, "repo": m.Repo})
	}
	return map[string]any{"available": true, "language": s.transcriptLanguage(), "languages": langs, "models": list}
}

// handleTranscription is GET api/transcription: transcriptionSettings.
func (s *Server) handleTranscription(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.transcriptionSettings())
}

// handleTranscriptionPut chooses the language of the church's transcripts: PUT api/transcription
// {"language": "nb"}; "" = none. A language must have a model now; one chosen earlier whose model has
// gone stays chosen (its transcripts wait for one).
func (s *Server) handleTranscriptionPut(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Language string `json:"language"`
	}
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	tr := s.cfg.transcriber
	if tr == nil {
		writeError(w, http.StatusBadRequest, errors.New("this studio makes no transcripts"))
		return
	}
	if _, ok := models.Choose(tr.installed(), q.Language); !ok && q.Language != "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("no speech model for %s is installed", q.Language))
		return
	}
	if err := s.saveTranscriptLanguage(q.Language); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.transcriptionSettings())
}

func (s *Server) saveTranscriptLanguage(lang string) error {
	s.transcriptMu.Lock()
	defer s.transcriptMu.Unlock()
	if s.cfg.SaveTranscript != nil {
		if err := s.cfg.SaveTranscript(lang); err != nil {
			return err
		}
	}
	s.transcriptLang = lang
	return nil
}
