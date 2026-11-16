package web

import (
	"path/filepath"
	"testing"
	"time"
)

// The tasks: a run with its step, a recording waiting for one, a recording being converted, and the church's
// transcripts (not another church's); the running ones first.
func TestTasks(t *testing.T) {
	ts, _ := newTranscriptServer(t, nbMedium)
	idle(t, ts)
	ts.jobs.mu.Lock()
	ts.jobs.all = append(ts.jobs.all,
		&job{Folder: "2026/2026-11-15", Formats: []string{"mp3"}, state: "running", steps: []string{"1. Raw recording", "2. Master"}, progress: 0.4},
		&job{Folder: "2026/2026-11-01", state: "done"})
	ts.jobs.mu.Unlock()
	ts.maker.mu.Lock()
	ts.maker.queue = append(ts.maker.queue, makeOrder{folder: "2026/2026-11-15"}, makeOrder{folder: "2026/2026-11-08 Kveld"})
	ts.maker.mu.Unlock()
	raw := filepath.Join(ts.root, "2026", "2026-11-16", "original", "2026-11-16_raw.wav")
	ts.analyses.mu.Lock()
	ts.analyses.current["k"] = &analysisState{State: "running", Stage: "convert", Progress: 0.5, raw: raw}
	ts.analyses.current["clip"] = &analysisState{State: "running", Stage: "analyze", raw: filepath.Join(t.TempDir(), "clip.flac")}
	ts.analyses.mu.Unlock()
	tr := ts.cfg.transcriber
	tr.add(transcriptJob{Church: "other", Folder: "2026-10-01", At: time.Now().Add(-time.Hour)})
	tr.add(transcriptJob{Folder: "2026/2026-10-04", Language: "nb", At: time.Now()})

	got := decode[struct {
		Tasks            []task
		Running, Waiting int
	}](t, ts.do(t, "GET", "/api/tasks", nil))
	want := []task{
		{Kind: "files", Path: "2026/2026-11-15", Name: "2026-11-15", State: "running", Step: "2. Master", Formats: []string{"mp3"}, Progress: 0.4},
		{Kind: "convert", Path: "2026/2026-11-16", Name: "2026-11-16", State: "running", Progress: 0.5},
		{Kind: "files", Path: "2026/2026-11-08 Kveld", Name: "2026-11-08 Kveld", State: "waiting"},
		{Kind: "transcript", Path: "2026/2026-10-04", Name: "2026-10-04", State: "waiting", Ahead: 1},
	}
	if len(got.Tasks) != len(want) || got.Running != 2 || got.Waiting != 2 {
		t.Fatalf("tasks: %+v", got)
	}
	for i := range want {
		g, w := got.Tasks[i], want[i]
		if g.Kind != w.Kind || g.Path != w.Path || g.Name != w.Name || g.State != w.State || g.Step != w.Step || g.Progress != w.Progress || g.Ahead != w.Ahead || len(g.Formats) != len(w.Formats) {
			t.Errorf("task %d: %+v, want %+v", i, g, w)
		}
	}
}
