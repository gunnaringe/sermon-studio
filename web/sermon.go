package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// sermonSettings are what the editor keeps of a sermon: its sections of the
// recording, the parts cut out of them, its fades and its sound.
type sermonSettings struct {
	cutting
	FadeIn  float64 `json:"fadeIn"`
	FadeOut float64 `json:"fadeOut"`
	// Sound is how the files are made from it; nil for one saved before the studio kept it (the defaults).
	Sound *soundSettings `json:"sound,omitempty"`
	// Intro and Outro are the clips around the sermon (clips.go): clipDefault, clipOwn, a church clip's id,
	// or "" for none. Only the form is checked here: a clip deleted since is not an error (the sermon must
	// not be lost), the run says so.
	Intro string `json:"intro,omitempty"`
	Outro string `json:"outro,omitempty"`
	// Tags are the tags written into its files; nil = the church's defaults for its folder (defaults.go:
	// Save fills them in, as it does the intro and outro and the sound).
	Tags *sermonTags `json:"tags,omitempty"`
	// ClipPause is the pause between the clips and the sermon (seconds), filled in by Save when it has a
	// clip; nil = the church's.
	ClipPause *float64 `json:"clipPause,omitempty"`
	// Formats are the files kept made of it (maker.go); nil = the church's, filled in by Save.
	Formats *outputFormats `json:"formats,omitempty"`
}

// soundSettings are the processing of a sermon: channels, noise, levels, loudness and bitrates.
// They are kept with the sermon, so the files can be made again the same way.
type soundSettings struct {
	Stereo         bool   `json:"stereo"`
	NoiseReduction int    `json:"noiseReduction"`
	NoiseMethod    string `json:"noiseMethod,omitempty"` // "" = standard
	RoomSound      int    `json:"roomSound"`
	normalizer.Repair
	Leveling    string  `json:"leveling"`
	Compression string  `json:"compression"`
	Expander    string  `json:"expander,omitempty"` // "" = off
	TargetLUFS  float64 `json:"targetLufs"`
	TruePeak    float64 `json:"truePeak"`
	Mp3Bitrate  string  `json:"mp3Bitrate"`
	OpusBitrate string  `json:"opusBitrate"`
}

// applyTo puts the sound in a run request.
func (s *soundSettings) applyTo(q *runRequest) {
	if s == nil {
		return
	}
	q.Stereo, q.NoiseReduction, q.NoiseMethod, q.RoomSound, q.Leveling, q.Compression = s.Stereo, s.NoiseReduction, s.NoiseMethod, s.RoomSound, s.Leveling, s.Compression
	q.TargetLUFS, q.TruePeak, q.Mp3Bitrate, q.OpusBitrate, q.Repair, q.Expander = s.TargetLUFS, s.TruePeak, s.Mp3Bitrate, s.OpusBitrate, s.Repair, s.Expander
}

// withSound is c with the church's processing as its sound when it has none (saved before the sound was
// kept): the editor shows it with those, so the same sermon sent back is not an adjustment.
func (c sermonSettings) withSound(p processing) sermonSettings {
	if c.Sound == nil {
		q := p.start()
		c.Sound = &soundSettings{Stereo: q.Stereo, NoiseReduction: q.NoiseReduction, NoiseMethod: q.NoiseMethod, RoomSound: q.RoomSound, Repair: q.Repair, Leveling: q.Leveling,
			Compression: q.Compression, Expander: q.Expander, TargetLUFS: q.TargetLUFS, TruePeak: q.TruePeak, Mp3Bitrate: q.Mp3Bitrate, OpusBitrate: q.OpusBitrate}
	}
	return c
}

// sermonFile is the sermon as the API gives it: the saved version (recording.json) and the adjustments the
// editor keeps as you make them (draft.json); saving makes them the saved version, and reverting drops them.
type sermonFile struct {
	Saved    *sermonSettings `json:"saved"`
	SavedAt  *time.Time      `json:"savedAt,omitempty"`
	Adjusted *sermonSettings `json:"adjusted"` // nil when it is the same as Saved
}

// validSermon is c normalized, or nil if it is not valid (any more).
func validSermon(c *sermonSettings) *sermonSettings {
	if c == nil {
		return nil
	}
	c.normalize()
	c.Tags.normalize()
	if c.Validate() != nil {
		return nil
	}
	return c
}

// Validate checks the settings the way a run would. c must be normalized.
func (c sermonSettings) Validate() error {
	if len(c.Sections) == 0 {
		return errors.New("select the sermon: drag on the waveform")
	}
	if !validClipChoice(c.Intro) || !validClipChoice(c.Outro) {
		return errors.New("the intro or outro is not one that can be chosen")
	}
	if err := c.Tags.check(); err != nil {
		return err
	}
	if c.ClipPause != nil && !(*c.ClipPause >= 0 && *c.ClipPause <= normalizer.MaxClipPause) {
		return fmt.Errorf("the pause at the intro and outro can be from 0 to %g seconds", normalizer.MaxClipPause)
	}
	q := requestFrom(normalizer.DefaultOptions())
	c.Sound.applyTo(&q)
	o, err := q.options()
	if err != nil {
		return err
	}
	o.FadeIn, o.FadeOut = c.FadeIn, c.FadeOut
	if err := c.apply(&o); err != nil {
		return err
	}
	return o.Validate()
}

// readSermon returns the sermon of a folder, or nil if nothing is kept.
func readSermon(folder string) *sermonFile {
	st := readState(folder)
	f := sermonFile{Saved: st.Saved, SavedAt: st.SavedAt, Adjusted: readDraft(folder)}
	if f.Saved == nil && f.Adjusted == nil {
		return nil
	}
	return &f
}

// changeSermon changes the sermon in the folder (recording.json and draft.json) and returns it.
// then, if given, runs once it is kept, before the answer.
func (s *Server) changeSermon(w http.ResponseWriter, r *http.Request, fn func(abs string, f *sermonFile) error, then ...func(abs string)) {
	abs, ok := s.pathParam(w, r)
	if !ok {
		return
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		writeError(w, http.StatusBadRequest, errors.New("there is no such folder"))
		return
	}
	var f sermonFile
	_, err := s.updateState(abs, func(st *folderState) error {
		f = sermonFile{Saved: st.Saved, SavedAt: st.SavedAt, Adjusted: readDraft(abs)}
		if err := fn(abs, &f); err != nil {
			return err
		}
		st.Saved, st.SavedAt = f.Saved, f.SavedAt
		if writeDraft(abs, f.Adjusted) != nil {
			return errStateWrite
		}
		return nil
	})
	switch {
	case errors.Is(err, errStateWrite):
		writeError(w, http.StatusInternalServerError, errors.New("could not save in the folder"))
	case err != nil:
		writeError(w, http.StatusBadRequest, err)
	default:
		for _, fn := range then {
			fn(abs)
		}
		writeJSON(w, http.StatusOK, f)
	}
}

// PUT api/sermon keeps the adjustments the editor has made.
func (s *Server) handleSermonPut(w http.ResponseWriter, r *http.Request) {
	var c sermonSettings
	if err := decodeRequest(r, &c); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	c.normalize()
	c.Tags.normalize()
	if err := c.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.changeSermon(w, r, func(abs string, f *sermonFile) error {
		if f.Saved != nil && c.ClipPause == nil {
			c.ClipPause = f.Saved.ClipPause // the editor does not set it: the saved version's
		}
		if f.Saved != nil && c.Formats == nil {
			c.Formats = f.Saved.Formats
		}
		// The same as the saved version, with what each takes from the church as it would be saved now.
		if f.Saved != nil && reflect.DeepEqual(s.withDefaults(abs, *f.Saved), s.withDefaults(abs, c)) {
			f.Adjusted = nil
		} else {
			f.Adjusted = &c
		}
		return nil
	})
}

// POST api/sermon/save makes the adjustments the saved version, with the church's defaults it takes filled
// in as they are now (withDefaults): it keeps them. Then the files are brought up to date in the background
// (maker.go): the formats it keeps made, the others deleted (by one who may delete files), and the transcript
// in the body ({"transcript": {...}}, optional) made after them. Saved again, it only does that.
func (s *Server) handleSermonSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Transcript *transcriptAsk `json:"transcript"`
	}
	if err := decodeRequest(r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Transcript != nil {
		if !allowed(r, actTranscribe) {
			writeError(w, http.StatusForbidden, refusal(actTranscribe))
			return
		}
		if err := s.checkTranscript(*body.Transcript); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	s.changeSermon(w, r, func(abs string, f *sermonFile) error {
		if f.Adjusted == nil {
			if f.Saved == nil {
				return errors.New("there is nothing to save yet")
			}
			return nil // already saved
		}
		now := time.Now().UTC().Truncate(time.Second)
		kept := s.withDefaults(abs, *f.Adjusted)
		f.Saved, f.SavedAt, f.Adjusted = &kept, &now, nil
		return nil
	}, func(abs string) {
		if j := s.jobs.active(s.relPath(abs)); j != nil {
			j.cancel() // made from the version before
		}
		s.maker.add(makeOrder{folder: s.relPath(abs), ask: body.Transcript, saved: true, clean: allowed(r, actDelete)})
	})
}

// POST api/sermon/revert drops the adjustments, back to the saved version.
func (s *Server) handleSermonRevert(w http.ResponseWriter, r *http.Request) {
	s.changeSermon(w, r, func(_ string, f *sermonFile) error {
		if f.Saved == nil {
			return errors.New("there is no saved version to go back to")
		}
		f.Adjusted = nil
		return nil
	})
}
