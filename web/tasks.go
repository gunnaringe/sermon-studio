package web

import (
	"net/http"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"
)

// The tasks are the church's work going on in the background, for the recordings page (GET api/tasks): runs
// making files (jobs.go) and the recordings waiting for one (maker.go), recordings being converted or analysed
// (analysis.go), and transcripts (transcriptqueue.go). Running ones first, then those waiting, each in order.

// task is one piece of work as the page shows it.
type task struct {
	// Kind is "files" (a run, or waiting for one), "convert", "analyze" or "transcript".
	Kind  string `json:"kind"`
	Path  string `json:"path"` // the recording's folder, relative to the root
	Name  string `json:"name"` // its name: the date, and its own name if it has one
	Slug  string `json:"slug"` // its name in the page's address (slugOf)
	State string `json:"state"`
	// Step is what a run is doing now, as the editor shows it (the pipeline's step, in English).
	Step     string   `json:"step,omitempty"`
	Formats  []string `json:"formats,omitempty"` // what a run makes
	Progress float64  `json:"progress,omitempty"`
	// Ahead is, for a transcript waiting, how many are made before it (across the churches).
	Ahead int `json:"ahead,omitempty"`
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	list := s.tasks()
	running := 0
	for _, t := range list {
		if t.State == "running" {
			running++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": list, "running": running, "waiting": len(list) - running})
}

func (s *Server) tasks() []task {
	var list []task
	named := func(t task) task {
		t.Name = pathpkg.Base(t.Path)
		t.Slug = slugOf(t.Name)
		return t
	}
	// Runs, and the recordings waiting for one.
	s.jobs.mu.Lock()
	jobs := slices.Clone(s.jobs.all)
	s.jobs.mu.Unlock()
	running := map[string]bool{}
	for _, j := range jobs {
		snap := j.snapshot()
		if snap.State != "queued" && snap.State != "running" {
			continue
		}
		running[snap.Folder] = true
		t := task{Kind: "files", Path: snap.Folder, State: "waiting", Formats: snap.Formats}
		if snap.State == "running" {
			t.State, t.Progress = "running", snap.Progress
			if n := len(snap.Steps); n > 0 {
				t.Step = snap.Steps[n-1]
			}
		}
		list = append(list, named(t))
	}
	s.maker.mu.Lock()
	waiting := []string{}
	if s.maker.current != "" {
		waiting = append(waiting, s.maker.current)
	}
	for _, o := range s.maker.queue {
		waiting = append(waiting, o.folder)
	}
	s.maker.mu.Unlock()
	for _, f := range waiting {
		if !running[f] {
			running[f] = true
			list = append(list, named(task{Kind: "files", Path: f, State: "waiting"}))
		}
	}
	// Recordings being converted or analysed (their waveform and preview).
	s.analyses.mu.Lock()
	seen := map[*analysisState]bool{}
	for _, m := range []map[string]*analysisState{s.analyses.pending, s.analyses.current} {
		for _, st := range m {
			if seen[st] || st.State != "running" || st.raw == "" {
				continue
			}
			seen[st] = true
			rel, err := filepath.Rel(s.root, folderOfRecording(st.raw))
			if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
				continue // not one of the church's recordings (a clip, the cache)
			}
			kind := "analyze"
			if st.Stage == "convert" {
				kind = "convert"
			}
			list = append(list, named(task{Kind: kind, Path: filepath.ToSlash(rel), State: "running", Progress: st.Progress}))
		}
	}
	s.analyses.mu.Unlock()
	// Transcripts: the one being made, then those waiting, in their turn.
	if tr := s.cfg.transcriber; tr != nil {
		tr.mu.Lock()
		for i, j := range tr.queue {
			if j.Church != s.cfg.church {
				continue
			}
			t := task{Kind: "transcript", Path: j.Folder, State: "waiting", Ahead: i}
			if j == tr.running {
				t.State, t.Ahead, t.Progress = "running", 0, j.progress
			}
			list = append(list, named(t))
		}
		tr.mu.Unlock()
	}
	slices.SortStableFunc(list, func(a, b task) int {
		if (a.State == "running") == (b.State == "running") {
			return 0
		}
		if a.State == "running" {
			return -1
		}
		return 1
	})
	return list
}
