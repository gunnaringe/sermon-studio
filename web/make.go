package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// handleMake makes one format (flac, mp3 or opus) of a sermon from its saved version, with the default levels
// and bitrates: one that is missing, or with again=1 one that is out of date. Other files are kept: it is an
// ordinary run with only that format on (and Force, to make it again).
func (s *Server) handleMake(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format != "flac" && format != "mp3" && format != "opus" {
		writeError(w, http.StatusBadRequest, errors.New("format must be flac, mp3 or opus"))
		return
	}
	q, err := s.savedRequest(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	q.Flac, q.Mp3, q.Opus = format == "flac", format == "mp3", format == "opus"
	q.Force = r.URL.Query().Get("again") == "1"
	pl := s.check(abs, q)
	if pl.Error != "" {
		writeError(w, http.StatusBadRequest, errors.New(pl.Error))
		return
	}
	j, err := s.jobs.start(pl)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	s.keepFormat(abs, format)
	writeJSON(w, http.StatusAccepted, j.snapshot())
}

// keepFormat adds a format to those the saved version keeps made (one made by hand stays).
func (s *Server) keepFormat(abs, format string) {
	if _, err := s.updateState(abs, func(st *folderState) error {
		if st.Saved != nil {
			kept := s.withDefaults(abs, *st.Saved)
			f := *kept.Formats
			switch format {
			case "flac":
				f.Flac = true
			case "mp3":
				f.Mp3 = true
			case "opus":
				f.Opus = true
			}
			kept.Formats = &f
			st.Saved = &kept
		}
		return nil
	}); err != nil {
		s.cfg.Logger.Printf("%s: %v", s.display(abs), err)
	}
}

// savedRequest is a run of the sermon's saved version, with no format on.
func (s *Server) savedRequest(abs string) (runRequest, error) {
	sf := readSermon(abs)
	if sf == nil || sf.Saved == nil {
		return runRequest{}, errors.New("this sermon is not saved yet: open it in the editor and press Save")
	}
	saved := s.withDefaults(abs, *sf.Saved) // for one saved before Save kept them
	q := s.processing().start()
	q.cutting = saved.cutting
	q.FadeIn, q.FadeOut = saved.FadeIn, saved.FadeOut
	q.Intro, q.Outro = saved.Intro, saved.Outro
	saved.Sound.applyTo(&q)
	q.tags, q.clipPause, q.kept = saved.Tags, saved.ClipPause, *saved.Formats
	q.Flac, q.Mp3, q.Opus = false, false, false
	return q, nil
}

// handleRemoveOutputs deletes outputs of a sermon (format=flac, mp3, opus;
// repeated for several) and forgets how they were made. Only the outputs: the
// recording and its original are never touched. Not while a run is busy.
func (s *Server) handleRemoveOutputs(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	formats := r.URL.Query()["format"]
	if len(formats) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("which format?"))
		return
	}
	if err := s.removeOutputs(abs, formats); err != nil {
		writeStatusError(w, err)
		return
	}
	info, err := s.folderInfo(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// removeOutputs deletes the outputs of the formats (with the sermon-only ones) and forgets how they were made;
// not while a run of the recording is going on.
func (s *Server) removeOutputs(abs string, formats []string) error {
	paths, err := normalizer.NewPaths(abs)
	if err != nil {
		return statusError{http.StatusBadRequest, err}
	}
	byFormat, sermonOnly := map[string]string{}, map[string]string{}
	for _, o := range outputsOf(paths) {
		byFormat[o.format] = o.path
	}
	for _, o := range sermonOnlyOutputs(paths) {
		sermonOnly[baseFormat(o.format)] = o.path
	}
	for _, f := range formats {
		if byFormat[f] == "" {
			return statusError{http.StatusBadRequest, errors.New("format must be flac, mp3 or opus")}
		}
	}
	if s.jobs.active(s.relPath(abs)) != nil {
		return statusError{http.StatusConflict, errBusy}
	}
	for _, f := range formats {
		for _, p := range []string{byFormat[f], sermonOnly[f]} { // with the intro and outro, and the sermon only
			if err := normalizer.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return errors.New("could not delete " + filepath.Base(p))
			} else if err == nil {
				s.cfg.Logger.Printf("deleted %s", s.display(p))
			}
		}
	}
	_, err = s.updateState(abs, func(st *folderState) error {
		for _, f := range formats {
			delete(st.Outputs, f)
			delete(st.Outputs, f+normalizer.SermonOnly)
		}
		return nil
	})
	return err
}
