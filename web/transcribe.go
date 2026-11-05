package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gunnaringe/sermon-studio/models"
	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Transcripts: what is said in a sermon, made by ffmpeg's whisper filter from the files the run made of the
// cut sermon, never from the recording: when asked, in the language and with the model asked for. The
// editor's Transcript asks with Save, when it is on; it is on, in the language, that the church chose (Settings
// → Processing → Transcripts), unless the recording has its own, and made with the church's model for the
// language. A language without one of the church's gets the first model for it. The studio only uses
// the models in the models folder (models.json, package models; `sermon-studio models pull` fills it),
// read each time, so a model that arrives or changes is used from the next transcript on. A transcript is
// never made again by itself: one made with another version of the model, or from an earlier cut, says so
// and is made again when asked. It runs on the CPU and takes about as long as the sermon, so one is made
// at a time, across all churches.

// TranscriptionConfig turns transcripts on (AppConfig.Transcription).
type TranscriptionConfig struct {
	// ModelsDir is the models folder (models.json and the models).
	ModelsDir string
	// Whisper makes the transcripts.
	Whisper normalizer.Whisper
}

// transcriber is what the churches share: the models folder and the one transcript made at a time, in the
// order they were asked for (a transcript can take hours, and waits across restarts: transcriptOrder).
type transcriber struct {
	dir     string
	whisper normalizer.Whisper
	mu      sync.Mutex
	busy    bool
	waiting []*turnWaiter // by when they were asked for
}

type turnWaiter struct {
	at    time.Time
	ready chan struct{}
}

// turn waits until it is the transcript's turn: the one asked for first (at) goes first. The returned
// function ends the turn.
func (tr *transcriber) turn(ctx context.Context, at time.Time) (func(), error) {
	tr.mu.Lock()
	if !tr.busy && len(tr.waiting) == 0 {
		tr.busy = true
		tr.mu.Unlock()
		return tr.next, nil
	}
	w := &turnWaiter{at: at, ready: make(chan struct{})}
	i := sort.Search(len(tr.waiting), func(i int) bool { return tr.waiting[i].at.After(at) })
	tr.waiting = slices.Insert(tr.waiting, i, w)
	tr.mu.Unlock()
	select {
	case <-w.ready:
		return tr.next, nil
	case <-ctx.Done():
		tr.mu.Lock()
		defer tr.mu.Unlock()
		if i := slices.Index(tr.waiting, w); i >= 0 {
			tr.waiting = slices.Delete(tr.waiting, i, i+1)
			return nil, ctx.Err()
		}
		// It got its turn just now: pass it on.
		tr.mu.Unlock()
		tr.next()
		tr.mu.Lock()
		return nil, ctx.Err()
	}
}

// next ends a turn: the first waiting gets it.
func (tr *transcriber) next() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.waiting) == 0 {
		tr.busy = false
		return
	}
	w := tr.waiting[0]
	tr.waiting = tr.waiting[1:]
	close(w.ready)
}

func newTranscriber(cfg TranscriptionConfig) (*transcriber, error) {
	if cfg.ModelsDir == "" {
		return nil, errors.New("transcripts need a models folder")
	}
	return &transcriber{dir: cfg.ModelsDir, whisper: cfg.Whisper}, nil
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
	Language string    `json:"-"` // what it is made in
	Model    string    `json:"-"` // the key of the model asked for; "" = the first for the language
	State    string    `json:"state"`
	Progress float64   `json:"progress,omitempty"`
	Error    string    `json:"error,omitempty"`
	since    time.Time // when it began running
	from     float64   // how far it was then: a transcript stopped by a restart goes on where it was
	cancel   context.CancelFunc
}

// transcriptInfo is the sermon's transcript in the folder info.
type transcriptInfo struct {
	Language string `json:"language,omitempty"` // the church's
	// State is "", "waiting", "running" or "failed" (the work), else "done" when it is there.
	State    string  `json:"state"`
	Progress float64 `json:"progress,omitempty"`
	// Remaining is about how many seconds it has left, from how fast it has gone; 0 = not known yet.
	Remaining float64   `json:"remaining,omitempty"`
	Error     string    `json:"error,omitempty"`
	Text      *fileInfo `json:"text,omitempty"`
	Captions  *fileInfo `json:"captions,omitempty"`
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
	Key  string     `json:"key,omitempty"`
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
	lang, _ := s.transcriptChoice()
	return lang
}

// transcriptChoice is the church's language and its models' keys by language.
func (s *Server) transcriptChoice() (lang string, models map[string]string) {
	if s.cfg.transcriber == nil {
		return "", nil
	}
	s.transcriptMu.Lock()
	defer s.transcriptMu.Unlock()
	return s.transcriptLang, maps.Clone(s.transcriptModels)
}

// defaultModel is the key of the model a transcript in lang is made with when none is asked for: the church's
// for the language while it is there; else "" (the first for the language).
func (s *Server) defaultModel(lang string) string {
	_, keys := s.transcriptChoice()
	model := keys[lang]
	if model == "" {
		return ""
	}
	if _, err := pickModel(s.cfg.transcriber.installed(), lang, model); err != nil {
		return ""
	}
	return model
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
		info.Model = &modelInfo{Name: tf.Model.Name, Key: tf.Model.Key(), Date: tf.Model.Date}
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
		if w.State == "running" {
			info.Remaining = remainingFrom(w.since, w.from, w.Progress)
		}
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

// remainingFrom is about how many seconds are left of work that was at from when it began (since) and is at
// done now; 0 = not known yet.
func remainingFrom(since time.Time, from, done float64) float64 {
	elapsed := time.Since(since).Seconds()
	if since.IsZero() || from < 0 || done-from < 0.02 || done >= 1 || elapsed < 5 {
		return 0
	}
	return math.Round(elapsed * (1 - done) / (done - from))
}

// pickModel is the model a transcript in lang is made with: the installed version of the one with the key,
// else the first for the language.
func pickModel(installed []models.Model, lang, key string) (models.Model, error) {
	if key == "" {
		if m, ok := models.Choose(installed, lang); ok {
			return m, nil
		}
		return models.Model{}, errNoModel
	}
	m, ok := models.Find(installed, key)
	switch {
	case !ok:
		return m, errors.New("this speech model is not installed")
	case !m.Can(lang) || lang == models.AnyLanguage && !m.For(models.AnyLanguage):
		return m, errNotForIt
	}
	return m, nil
}

// transcriptOrder is a transcript asked for (folderState.TranscriptOrder): the language, the key of the model
// ("" = the first for the language) and when, which is its place in the queue.
type transcriptOrder struct {
	Language string    `json:"language"`
	Model    string    `json:"model,omitempty"`
	At       time.Time `json:"at"`
}

// startTranscript makes the folder's transcript in lang with the model with the key ("" = the church's for
// its language, else the first for lang) in the background, unless it is being made.
func (s *Server) startTranscript(folder, lang, key string) error {
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
	if key == "" {
		key = s.defaultModel(lang)
	}
	if _, err := pickModel(s.cfg.transcriber.installed(), lang, key); err != nil {
		return err
	}
	s.queueTranscript(folder, transcriptOrder{Language: lang, Model: key, At: time.Now().UTC()})
	return nil
}

// queueTranscript makes the transcript of the order in the background, in its turn, unless one is being made;
// the order is kept in recording.json until it is made, stopped or fails, so a restart does not lose it.
func (s *Server) queueTranscript(folder string, order transcriptOrder) {
	s.transcriptMu.Lock()
	if w := s.transcripts[folder]; w != nil && w.State != "failed" {
		s.transcriptMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	w := &transcriptWork{Language: order.Language, Model: order.Model, State: "waiting", cancel: cancel}
	s.transcripts[folder] = w
	s.transcriptMu.Unlock()
	if _, err := s.updateState(folder, func(st *folderState) error { st.TranscriptOrder = &order; return nil }); err != nil {
		s.cfg.Logger.Printf("transcript of %s: keeping the order: %v", s.display(folder), err)
	}
	s.background(func() {
		defer cancel()
		err := s.makeTranscript(ctx, folder, order, w)
		if s.ctx.Err() == nil {
			// Made, stopped (✕) or failed: the order is over. When the studio stops, it and the parts done stay.
			if p, perr := normalizer.NewPaths(folder); perr == nil {
				normalizer.RemoveAll(transcriptParts(p))
			}
			if fileExists(statePath(folder)) {
				s.updateState(folder, func(st *folderState) error {
					if st.TranscriptOrder != nil && st.TranscriptOrder.At.Equal(order.At) {
						st.TranscriptOrder = nil
					}
					return nil
				})
			}
		}
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
}

// resumeTranscripts queues the transcripts asked for and not made when the studio last stopped, each in its
// place (when it was asked for); one that was being made goes on from its last part done.
func (s *Server) resumeTranscripts() {
	filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || s.ctx.Err() != nil {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path != s.root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "original") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != stateName {
			return nil
		}
		folder := filepath.Dir(path)
		if order := readState(folder).TranscriptOrder; order != nil {
			s.cfg.Logger.Printf("transcript of %s: asked for %s, made now", s.display(folder), order.At.Format(time.RFC3339))
			s.queueTranscript(folder, *order)
		}
		return nil
	})
}

// transcriptParts is the folder a transcript is kept in part by part while it is made (normalizer.Whisper).
func transcriptParts(p normalizer.Paths) string {
	return filepath.Join(p.Folder, "."+p.Date+".transcript.parts")
}

// setTranscript changes a transcript's work under the lock.
func (s *Server) setTranscript(fn func()) {
	s.transcriptMu.Lock()
	defer s.transcriptMu.Unlock()
	fn()
}

// makeTranscript waits for its turn, transcribes the sermon and writes the transcript, its text and its
// captions.
func (s *Server) makeTranscript(ctx context.Context, folder string, order transcriptOrder, w *transcriptWork) error {
	tr := s.cfg.transcriber
	endTurn, err := tr.turn(ctx, order.At)
	if err != nil {
		return err
	}
	defer endTurn()
	lang, key := order.Language, order.Model
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
	model, err := pickModel(tr.installed(), lang, key)
	if err != nil {
		return err
	}
	s.setTranscript(func() { w.State, w.since, w.from = "running", time.Now(), -1 })
	t := normalizer.Transcription{Model: filepath.Join(tr.dir, model.File), Language: whisperLanguage(lang),
		Key: strings.Join([]string{model.Key(), model.Version(), lang, rec.From, rec.Cut}, " ")}
	segs, err := tr.whisper.Transcribe(ctx, s.cfg.FFmpeg, source, transcriptParts(p), t, func(done float64) {
		s.setTranscript(func() {
			if w.from < 0 { // the first report: where it begins (parts done before a restart)
				w.from, w.since = done, time.Now()
			}
			w.Progress = done
		})
	})
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

// transcriptAsk is a transcript asked for with a run (runRequest.Transcript): the language and the model's key
// ("" = the church's for its language, else the first for it).
type transcriptAsk struct {
	Language string `json:"language"`
	Model    string `json:"model"`
}

// checkTranscript tells whether a transcript can be asked for: the studio makes them, and has the model.
func (s *Server) checkTranscript(a transcriptAsk) error {
	if s.cfg.transcriber == nil {
		return errors.New("this studio makes no transcripts")
	}
	if a.Language != models.AnyLanguage && !normalizer.ValidLanguage(a.Language) {
		return errNoTranscript
	}
	if a.Model == "" {
		a.Model = s.defaultModel(a.Language)
	}
	_, err := pickModel(s.cfg.transcriber.installed(), a.Language, a.Model)
	return err
}

// afterRun makes the transcript asked for with a run, from the files it made. None is made by itself: the
// editor asks for one when its Transcript is on (the church's default for a recording) and the one there is
// not of this cut, language and model.
func (s *Server) afterRun(folder string, ask *transcriptAsk) {
	if ask == nil {
		return
	}
	if err := s.startTranscript(folder, ask.Language, ask.Model); err != nil {
		s.cfg.Logger.Printf("transcript of %s: %v", s.display(folder), err)
	}
}

// handleTranscript makes the transcript (again): POST api/transcript?path=&language=&model=, in the church's
// language when none is given, with the model with the key model= (one for the language), else the church's
// for its language, else the first for it.
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

// transcriptionSettings is what Settings → Processing and the editor show of transcripts: whether the studio
// makes them, the church's language and its models by language (defaults), the languages there are models
// for, with the model each one would use, and all the models to choose from.
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
		list = append(list, map[string]any{"name": m.Name, "key": m.Key(), "languages": m.Languages, "size": m.WhisperSize(),
			"bytes": m.Size, "date": m.Date, "repo": m.Repo})
	}
	lang, keys := s.transcriptChoice()
	if keys == nil {
		keys = map[string]string{}
	}
	return map[string]any{"available": true, "language": lang, "defaults": keys, "languages": langs, "models": list}
}

// handleTranscription is GET api/transcription: transcriptionSettings.
func (s *Server) handleTranscription(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.transcriptionSettings())
}

// handleTranscriptionPut chooses the language of the church's transcripts and its models by language: PUT
// api/transcription {"language": "nb", "defaults": {"nb": "NbAiLab/nb-whisper-medium/ggml-model-q5_0.bin"}};
// language "" = none, a language without a model = the first for it. They must be there now; ones chosen
// earlier whose model has gone stay chosen (the language's transcripts wait for one; another model is taken
// instead of the church's).
func (s *Server) handleTranscriptionPut(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Language string            `json:"language"`
		Defaults map[string]string `json:"defaults"`
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
	_, before := s.transcriptChoice()
	for lang, key := range q.Defaults {
		if key == "" {
			delete(q.Defaults, lang)
			continue
		}
		if _, err := pickModel(tr.installed(), lang, key); err != nil && before[lang] != key {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%s: %w", lang, err))
			return
		}
	}
	if len(q.Defaults) == 0 {
		q.Defaults = nil
	}
	if err := s.saveTranscriptChoice(q.Language, q.Defaults); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.transcriptionSettings())
}

func (s *Server) saveTranscriptChoice(lang string, keys map[string]string) error {
	s.transcriptMu.Lock()
	defer s.transcriptMu.Unlock()
	if s.cfg.SaveTranscript != nil {
		if err := s.cfg.SaveTranscript(lang, keys); err != nil {
			return err
		}
	}
	s.transcriptLang, s.transcriptModels = lang, keys
	return nil
}
