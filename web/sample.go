package web

import (
	"errors"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// sampleSeconds is how much of the recording a sample is.
const sampleSeconds = 30

// handleSample makes a sample to hear what the sound settings do: POST api/sample?path=<date folder>&at=<seconds>
// with the settings as for a run; the answer is an MP3 of sampleSeconds from the recording at that time,
// made as the files are (the cuts, pauses and fades are not applied). It is short enough to be done in the
// request, one at a time; also while the files are being made (sampleAllowed).
func (s *Server) handleSample(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	var q runRequest
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	q.Flac, q.Mp3, q.Opus = false, true, false
	q.cutting = cutting{Sections: []span{}, Cuts: []span{}}
	o, err := q.options()
	if err == nil {
		err = o.Validate()
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	paths, err := normalizer.NewPaths(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	raw := paths.Source()
	switch {
	case raw == "":
		writeError(w, http.StatusNotFound, errors.New("there is no raw recording to listen to"))
		return
	case s.analyses.stage(raw) != "":
		writeError(w, http.StatusConflict, errors.New("the recording is still being converted or prepared; try again in a moment"))
		return
	}
	at, _ := strconv.ParseFloat(r.URL.Query().Get("at"), 64)
	if dur, err := s.cfg.FFmpeg.Duration(r.Context(), raw); err == nil && at > dur-sampleSeconds {
		at = dur - sampleSeconds
	}
	if math.IsNaN(at) || at < 0 {
		at = 0
	}
	if !s.sampleAllowed(w, abs, paths) {
		return
	}
	defer func() { <-s.limits.sample }()
	tmp, err := os.CreateTemp(s.cfg.CacheDir, "sample-*.mp3")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	tmp.Close()
	defer normalizer.Remove(tmp.Name())
	if err := s.cfg.FFmpeg.Sample(r.Context(), raw, o, at, sampleSeconds, tmp.Name()); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	f, err := os.Open(tmp.Name())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "sample.mp3", time.Now(), f)
}

// handleJoinSample makes a sample to hear how the sermon joins its intro or outro: POST
// api/sample/join?path=<date folder>&which=intro|outro with the settings as for a run (the sections and cuts,
// the sound, the intro and outro); the answer is an MP3 (normalizer.JoinSample). Done in the request, as a sample.
func (s *Server) handleJoinSample(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	which, ok := whichParam(w, r)
	if !ok {
		return
	}
	var q runRequest
	if err := decodeRequest(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	q.Flac, q.Mp3, q.Opus = false, true, false
	pl := s.check(abs, q)
	if pl.Error != "" {
		writeError(w, http.StatusBadRequest, errors.New(pl.Error))
		return
	}
	raw := pl.paths.Source()
	if s.analyses.stage(raw) != "" {
		writeError(w, http.StatusConflict, errors.New("the recording is still being converted or prepared; try again in a moment"))
		return
	}
	if !s.sampleAllowed(w, abs, pl.paths) {
		return
	}
	defer func() { <-s.limits.sample }()
	tmp, err := os.CreateTemp(s.cfg.CacheDir, "join-*.mp3")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	tmp.Close()
	defer normalizer.Remove(tmp.Name())
	if err := s.cfg.FFmpeg.JoinSample(r.Context(), raw, pl.opt, which, tmp.Name()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	f, err := os.Open(tmp.Name())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "join.mp3", time.Now(), f)
}

// sampleAllowed takes the sample slot (the caller gives it back), or answers why not. A sample can be made while
// the recording's files are being made, once its recording is in original/ (an import goes there; a recording
// put in the date folder itself is moved there by its first run).
func (s *Server) sampleAllowed(w http.ResponseWriter, abs string, paths normalizer.Paths) bool {
	if s.jobs.active(s.relPath(abs)) != nil && paths.Source() != paths.RawOriginal() {
		writeError(w, http.StatusConflict, errors.New("the files of this recording are being made; try again when that is done"))
		return false
	}
	select {
	case s.limits.sample <- struct{}{}:
		return true
	default:
		writeError(w, http.StatusConflict, errors.New("another sample is being made; try again in a moment"))
		return false
	}
}
