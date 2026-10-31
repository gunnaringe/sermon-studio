package web

import (
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Storage: how much a church keeps, against its quota (set by the site administrator, see quota.go). The
// total is kept in memory: measured when the church starts, measured again after every change (a request
// that is not a GET, or background work: Server.background) and every hour, so nothing walks the
// folder per request. It is never written to a file: recording.json and church.json are what a sync tool
// carries (#50).

// storageUse is what a church keeps, in bytes.
type storageUse struct {
	Recordings int64 `json:"recordings"` // the recordings as they came, their waveform and preview, their state
	Files      int64 `json:"files"`      // the made files
	Clips      int64 `json:"clips"`      // the church's intro and outro clips, and the sermons' own
	Trash      int64 `json:"trash"`
}

func (u storageUse) total() int64 { return u.Recordings + u.Files + u.Clips + u.Trash }

// storage is a church's measured use; Server.storage.
type storage struct {
	mu       sync.Mutex
	use      storageUse
	measured bool          // the first measuring is done
	dirty    bool          // something changed since the measuring that is running started
	running  chan struct{} // closed when the measuring that is running ends; nil = none is
	quota    int64         // bytes; 0 = no limit
}

// measureStorage walks the church's files. Half-written files (hidden) and the inbox do not count: an inbox
// file counts once it is taken in.
func (s *Server) measureStorage() storageUse {
	var u storageUse
	trash := s.trashRoot()
	filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == trash {
				u.Trash += dirSize(path)
				return filepath.SkipDir
			}
			if path != s.root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		dir := filepath.Base(filepath.Dir(path))
		switch {
		case dir == clipsDir:
			u.Clips += info.Size()
		case dir == normalizer.OriginalDir || strings.Contains(d.Name(), "_raw."):
			u.Recordings += info.Size()
			if key, err := cacheKey(path); err == nil {
				u.Recordings += dirSize(s.analyses.dir(key))
			}
		case strings.HasSuffix(d.Name(), ".json"):
			u.Recordings += info.Size()
		default:
			u.Files += info.Size()
		}
		return nil
	})
	if s.cfg.ClipsDir != "" {
		u.Clips += dirSize(s.cfg.ClipsDir)
	}
	return u
}

// storageChanged measures the church again in the background. Changes that come while it measures are
// measured once more when it is done, so a burst of them costs two walks, not one each.
func (s *Server) storageChanged() {
	st := &s.storage
	st.mu.Lock()
	defer st.mu.Unlock()
	if s.ctx.Err() != nil {
		return // closing
	}
	if st.running != nil {
		st.dirty = true
		return
	}
	done := make(chan struct{})
	st.running = done
	s.wg.Add(1) // not s.background: that measures again when it ends
	go func() {
		defer s.wg.Done()
		for {
			u := s.measureStorage()
			st.mu.Lock()
			st.use, st.measured = u, true
			if !st.dirty || s.ctx.Err() != nil {
				st.running = nil
				close(done)
				st.mu.Unlock()
				return
			}
			st.dirty = false
			st.mu.Unlock()
		}
	}()
}

// storageNow is the church's use, waiting (a little) for a measuring that is running, so a page that asks
// right after a change sees it.
func (s *Server) storageNow() (storageUse, bool) {
	st := &s.storage
	st.mu.Lock()
	running := st.running
	st.mu.Unlock()
	if running != nil {
		select {
		case <-running:
		case <-time.After(5 * time.Second):
		}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.use, st.measured
}

// SetQuota sets the church's quota in bytes; 0 = no limit.
func (s *Server) SetQuota(bytes int64) {
	s.storage.mu.Lock()
	s.storage.quota = bytes
	s.storage.mu.Unlock()
}

func (s *Server) quota() int64 {
	s.storage.mu.Lock()
	defer s.storage.mu.Unlock()
	return s.storage.quota
}

// handleStorage is the church's use and quota: GET api/storage.
func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	u, measured := s.storageNow()
	writeJSON(w, http.StatusOK, map[string]any{"use": u, "total": u.total(), "quota": s.quota(), "measured": measured})
}

// changesStorage measures the church again after every request that may have written or removed files.
func (s *Server) changesStorage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			s.storageChanged()
		}
	})
}
