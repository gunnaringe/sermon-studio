package web

import (
	"cmp"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// The maker keeps a saved recording's files made: the formats it keeps (sermonSettings.Formats) that are missing
// or out of date are made in the background, after Save and when the server starts (which also goes on with runs
// a restart stopped). Nothing else starts it, so a run that fails is not tried again until the next Save or
// start: its reason is kept (makeError in the folder info). One recording at a time per church, in the order
// asked (a Save goes first); the plan is made as it starts, so it is of the saved version then.

// makeOrder is a recording to bring up to date.
type makeOrder struct {
	folder string         // relative to the root
	ask    *transcriptAsk // the transcript to make after the files, if any
	// saved: asked by Save, which also deletes the files of the formats it no longer keeps (clean) and stops a
	// run of the recording that is going on; else from the start's scan.
	saved, clean bool
}

type maker struct {
	s       *Server
	mu      sync.Mutex
	queue   []makeOrder
	current string // the folder being started (its job not yet there)
	running bool
	failed  map[string]string // folder → why its files could not be made
}

func newMaker(s *Server) *maker { return &maker{s: s, failed: map[string]string{}} }

// add puts an order in the queue (one from Save first, replacing any other of the folder) and starts the
// worker if it is not running.
func (m *maker) add(o makeOrder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i := slices.IndexFunc(m.queue, func(x makeOrder) bool { return x.folder == o.folder }); i >= 0 {
		if !o.saved {
			return // already asked
		}
		m.queue = slices.Delete(m.queue, i, i+1)
	}
	if o.saved {
		delete(m.failed, o.folder)
		m.queue = slices.Insert(m.queue, 0, o)
	} else {
		m.queue = append(m.queue, o)
	}
	if !m.running {
		m.running = true
		m.s.background(m.work)
	}
}

// work makes the orders until the queue is empty (so an idle church is not busy).
func (m *maker) work() {
	for m.s.ctx.Err() == nil {
		m.mu.Lock()
		if len(m.queue) == 0 {
			m.running = false
			m.mu.Unlock()
			return
		}
		o := m.queue[0]
		m.queue = m.queue[1:]
		m.current = o.folder
		m.mu.Unlock()
		m.make(o)
		m.mu.Lock()
		m.current = ""
		m.mu.Unlock()
	}
	m.mu.Lock()
	m.running = false
	m.mu.Unlock()
}

// waiting is whether the folder's files are still to be made: in the queue, or being started.
func (m *maker) waiting(folder string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current == folder || slices.ContainsFunc(m.queue, func(o makeOrder) bool { return o.folder == folder })
}

// failure is why the folder's files could not be made the last time, "" if they could.
func (m *maker) failure(folder string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failed[folder]
}

func (m *maker) setFailure(folder, why string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if why == "" {
		delete(m.failed, folder)
	} else {
		m.failed[folder] = why
	}
}

// moved follows a rename: its order and its failure go with it.
func (m *maker) moved(src, dst string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.queue {
		if m.queue[i].folder == src {
			m.queue[i].folder = dst
		}
	}
	if why, ok := m.failed[src]; ok {
		delete(m.failed, src)
		m.failed[dst] = why
	}
}

// make brings one recording up to date.
func (m *maker) make(o makeOrder) {
	s := m.s
	abs := filepath.Join(s.root, filepath.FromSlash(o.folder))
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return // renamed or deleted since
	}
	if j := s.jobs.active(o.folder); j != nil {
		if !o.saved {
			return // a run of it is going on: that one makes it
		}
		<-j.finished // Save stopped the one before it; one started since is let finish, then what is left is made
	}
	if o.clean {
		if err := s.dropUnkept(abs); err != nil {
			m.setFailure(o.folder, err.Error())
			return
		}
	}
	q, todo := s.toMake(abs)
	if todo == nil {
		m.setFailure(o.folder, "")
		if o.ask != nil {
			s.afterRun(abs, o.ask)
		}
		return
	}
	q.Flac, q.Mp3, q.Opus, q.Force = todo.Flac, todo.Mp3, todo.Opus, todo.again
	q.Transcript = o.ask // none from the scan: a transcript is made only when asked for (afterRun)
	pl := s.check(abs, q)
	if pl.Error != "" {
		m.setFailure(o.folder, pl.Error)
		s.cfg.Logger.Printf("%s: the files could not be made: %s", s.display(abs), pl.Error)
		return
	}
	j, err := s.jobs.start(pl)
	if err != nil {
		m.setFailure(o.folder, err.Error())
		return
	}
	m.mu.Lock()
	m.current = ""
	m.mu.Unlock()
	<-j.finished
	switch snap := j.snapshot(); snap.State {
	case "done":
		m.setFailure(o.folder, "")
	case "failed":
		m.setFailure(o.folder, snap.Error)
	}
}

// makeTodo is the formats a run must make, and whether it makes any that are there again (Force).
type makeTodo struct {
	outputFormats
	again bool
}

// toMake is the run of the saved version and what it must make: the formats it keeps whose file (or, with
// clips, the sermon only) is missing or made from something else; nil when nothing, or the files cannot be
// made (not saved, the original deleted). A file the studio has no record of is taken as it is.
func (s *Server) toMake(abs string) (runRequest, *makeTodo) {
	q, err := s.savedRequest(abs)
	if err != nil {
		return q, nil
	}
	paths, err := normalizer.NewPaths(abs)
	if err != nil || paths.Source() == "" {
		return q, nil
	}
	status := s.savedStatus(abs, paths)
	if status == nil {
		return q, nil
	}
	var todo makeTodo
	for _, f := range []string{"flac", "mp3", "opus"} {
		if !q.kept.has(f) {
			continue
		}
		for _, x := range []string{f, f + normalizer.SermonOnly} {
			st, ok := status[x]
			if !ok || (st.Exists && !(st.Known && st.Stale)) {
				continue
			}
			switch f {
			case "flac":
				todo.Flac = true
			case "mp3":
				todo.Mp3 = true
			case "opus":
				todo.Opus = true
			}
			todo.again = todo.again || st.Exists
		}
	}
	if todo.none() {
		return q, nil
	}
	return q, &todo
}

// dropUnkept deletes the files of the formats the saved version does not keep, and their records.
func (s *Server) dropUnkept(abs string) error {
	q, err := s.savedRequest(abs)
	if err != nil {
		return nil
	}
	var drop []string
	for _, f := range []string{"flac", "mp3", "opus"} {
		if !q.kept.has(f) {
			drop = append(drop, f)
		}
	}
	return s.removeOutputs(abs, drop)
}

// scanToMake queues every saved recording whose files are not up to date, newest first. Done once as the
// server starts, in the background.
func (s *Server) scanToMake() {
	found := s.toMakeFolders()
	if len(found) == 0 {
		return
	}
	s.cfg.Logger.Printf("%d recordings have files to make: queued", len(found))
	for _, f := range found {
		s.maker.add(makeOrder{folder: f})
	}
}

// toMakeFolders are the saved recordings whose files are not up to date (relative), newest first.
func (s *Server) toMakeFolders() []string {
	var found []string
	filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path != s.root && (strings.HasPrefix(d.Name(), ".") || d.Name() == normalizer.OriginalDir || d.Name() == clipsDir) {
			return filepath.SkipDir
		}
		if _, _, ok := parseFolder(d.Name()); ok && readState(path).Saved != nil {
			if _, todo := s.toMake(path); todo != nil {
				found = append(found, s.relPath(path))
			}
		}
		return nil
	})
	slices.SortStableFunc(found, func(a, b string) int { return cmp.Compare(pathpkg.Base(b), pathpkg.Base(a)) })
	return found
}
