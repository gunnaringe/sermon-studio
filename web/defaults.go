package web

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// The church's defaults (processing, tags, intro and outro, the pause around them) are what a recording starts
// with, not what it follows: Save copies them into the saved version, which keeps them. A change to a default
// only reaches recordings saved after it; "Use the defaults" in the editor takes them into one.

// sermonTags are the tags written into a sermon's files, kept with it once saved; an empty one is left out.
type sermonTags struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
}

// maxTag is the longest tag, in characters (as the editor's fields allow).
const maxTag = 200

// normalize makes each tag one line with single spaces, as a run writes it.
func (t *sermonTags) normalize() {
	if t == nil {
		return
	}
	for _, v := range []*string{&t.Title, &t.Artist, &t.Album} {
		*v = strings.Join(strings.Fields(*v), " ")
	}
}

// check refuses tags that are too long or hold control characters.
func (t *sermonTags) check() error {
	if t == nil {
		return nil
	}
	for _, v := range []string{t.Title, t.Artist, t.Album} {
		if utf8.RuneCountInString(v) > maxTag {
			return fmt.Errorf("a tag can be at most %d characters", maxTag)
		}
		if strings.ContainsFunc(v, unicode.IsControl) {
			return errors.New("a tag cannot hold control characters")
		}
	}
	return nil
}

// outputFormats are which files are made of a sermon.
type outputFormats struct {
	Flac bool `json:"flac"`
	Mp3  bool `json:"mp3"`
	Opus bool `json:"opus"`
}

var allFormats = outputFormats{Flac: true, Mp3: true, Opus: true}

func (f outputFormats) none() bool { return !f.Flac && !f.Mp3 && !f.Opus }

// set turns format (flac, mp3 or opus) on or off.
func (f *outputFormats) set(format string, on bool) {
	switch format {
	case "flac":
		f.Flac = on
	case "mp3":
		f.Mp3 = on
	case "opus":
		f.Opus = on
	}
}

// has is whether format (flac, mp3 or opus) is one of them.
func (f outputFormats) has(format string) bool {
	return map[string]bool{"flac": f.Flac, "mp3": f.Mp3, "opus": f.Opus}[format]
}

// defaultClip is the id of the church's default intro or outro now, "" for none.
func (s *Server) defaultClip(which string) string {
	s.clips.mu.Lock()
	defer s.clips.mu.Unlock()
	if which == "intro" {
		return s.clips.lib.Intro
	}
	return s.clips.lib.Outro
}

// withDefaults is c with what it would take from the church filled in as it is now: the processing (one
// saved before the sound was kept), the default intro and outro as the clips they are, the tags for its
// folder, and the pause around the clips. What c has itself is kept. The tags are kept as the template makes
// them, so the files' fingerprints (madeFrom) stay what they were.
func (s *Server) withDefaults(folder string, c sermonSettings) sermonSettings {
	c = c.withSound(s.processing())
	if c.Intro == clipDefault {
		c.Intro = s.defaultClip("intro")
	}
	if c.Outro == clipDefault {
		c.Outro = s.defaultClip("outro")
	}
	if c.Tags == nil {
		if def, _, err := s.defaultTags(folder); err == nil {
			c.Tags = &sermonTags{Title: def.Title, Artist: def.Artist, Album: def.Album}
		}
	}
	if c.ClipPause == nil && (c.Intro != "" || c.Outro != "") {
		s.clips.mu.Lock()
		p := s.clips.lib.pause()
		s.clips.mu.Unlock()
		c.ClipPause = &p
	}
	if c.Formats == nil {
		f := s.processing().formats()
		c.Formats = &f
	}
	if c.TranscriptWords == nil {
		w := s.transcriptWords()
		c.TranscriptWords = &w
	}
	return c
}

// formatsThere are the formats a recording has files of; nil for none.
func formatsThere(folder string) *outputFormats {
	paths, err := normalizer.NewPaths(folder)
	if err != nil {
		return nil
	}
	var f outputFormats
	for _, o := range outputsOf(paths) {
		if fileExists(o.path) {
			switch o.format {
			case "flac":
				f.Flac = true
			case "mp3":
				f.Mp3 = true
			case "opus":
				f.Opus = true
			}
		}
	}
	if f.none() {
		return nil
	}
	return &f
}

// keepDefaults copies the defaults into the saved version of every recording that still follows the
// church's (saved before Save did that), as they are now: nothing it makes changes. Done once as the
// server starts; a recording is written only when something was filled in.
func (s *Server) keepDefaults() {
	filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path != s.root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "original" || d.Name() == clipsDir) {
			return filepath.SkipDir
		}
		if st := readState(path); st.Saved != nil {
			if _, err := s.updateState(path, func(st *folderState) error { // written only if it changed
				if st.Saved != nil {
					kept := *st.Saved
					if kept.Formats == nil { // the files it has are the formats it keeps made
						kept.Formats = formatsThere(path)
					}
					kept = s.withDefaults(path, kept)
					st.Saved = &kept
				}
				return nil
			}); err != nil {
				s.cfg.Logger.Printf("%s: %v", s.display(path), err)
			}
		}
		return nil
	})
}

// retagRenamed keeps the kept tags of a recording renamed from oldBase to dst in step with its new date and
// name: a tag that is what the church's template made for the old folder becomes what it makes for the new
// one. A tag written by hand stays.
func (s *Server) retagRenamed(oldBase, dst string) {
	oldDate, oldName, ok1 := parseFolder(oldBase)
	newDate, newName, ok2 := parseFolder(filepath.Base(dst))
	if !ok1 || !ok2 {
		return
	}
	tpl := s.meta.get()
	before, err1 := tpl.expand(oldDate, oldName)
	after, err2 := tpl.expand(newDate, newName)
	if err1 != nil || err2 != nil {
		return
	}
	follow := func(t *sermonTags) {
		if t == nil {
			return
		}
		for _, f := range []struct {
			v        *string
			was, now string
		}{
			{&t.Title, before.Title, after.Title}, {&t.Artist, before.Artist, after.Artist}, {&t.Album, before.Album, after.Album},
		} {
			if *f.v == f.was {
				*f.v = f.now
			}
		}
	}
	if _, err := s.updateState(dst, func(st *folderState) error {
		if st.Saved != nil {
			follow(st.Saved.Tags)
		}
		return nil
	}); err != nil {
		s.cfg.Logger.Printf("%s: %v", s.display(dst), err)
	}
	if d := readDraft(dst); d != nil && d.Tags != nil {
		follow(d.Tags)
		if err := writeDraft(dst, d); err != nil {
			s.cfg.Logger.Printf("%s: %v", s.display(dst), err)
		}
	}
}
