package web

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	// reserved is held by uploads in progress, by upload (their declared size); one not heard from for
	// uploadStale no longer holds anything.
	reserved map[string]reservation
	// waiting are the inbox files that do not fit, by name: left in the inbox (their size).
	waiting map[string]int64
}

type reservation struct {
	bytes int64
	at    time.Time
}

// errFull is what does not fit in the church's quota. Its message has the sizes, for the studio; the public
// share page says only that the storage is full (errStorageFull).
type errFull struct{ need, left int64 }

func (e errFull) Error() string {
	return fmt.Sprintf("the church's storage is full: %s does not fit, %s is left", sizeText(e.need), sizeText(e.left))
}

var errStorageFull = errors.New("the church's storage is full")

// room checks that need more bytes fit in the quota, besides what the uploads in progress (but key's own)
// hold; with st.mu held. Before the first measuring, everything fits: nothing is known yet.
func (st *storage) room(key string, need int64, now time.Time) error {
	if st.quota == 0 || !st.measured {
		return nil
	}
	used := st.use.total()
	for k, r := range st.reserved {
		if now.Sub(r.at) > uploadStale {
			delete(st.reserved, k)
		} else if k != key {
			used += r.bytes
		}
	}
	if used+need > st.quota {
		return errFull{need: need, left: max(st.quota-used, 0)}
	}
	return nil
}

// fits checks that need more bytes fit in the church's quota.
func (s *Server) fits(need int64) error {
	s.settled()
	s.storage.mu.Lock()
	defer s.storage.mu.Unlock()
	return s.storage.room("", need, time.Now())
}

// reserve holds bytes for an upload in progress, if they fit. Every chunk calls it again: that keeps the
// reservation, and stops an upload when what it holds no longer fits (the quota was lowered).
func (s *Server) reserve(key string, bytes int64) error {
	s.settled()
	st := &s.storage
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	if err := st.room(key, bytes, now); err != nil {
		return err
	}
	if st.reserved == nil {
		st.reserved = map[string]reservation{}
	}
	st.reserved[key] = reservation{bytes, now}
	return nil
}

// release ends an upload's reservation: it is done (its file now counts) or gone.
func (s *Server) release(key string) {
	s.storage.mu.Lock()
	delete(s.storage.reserved, key)
	s.storage.mu.Unlock()
}

// over reports whether the church keeps more than its quota.
func (s *Server) over() bool {
	s.settled()
	st := &s.storage
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.quota > 0 && st.measured && st.use.total() >= st.quota
}

// wait notes that an inbox file does not fit (true the first time), or that it went in or is gone (fit).
func (s *Server) wait(name string, size int64, fit bool) (first bool) {
	st := &s.storage
	st.mu.Lock()
	defer st.mu.Unlock()
	if fit {
		delete(st.waiting, name)
		return false
	}
	if st.waiting == nil {
		st.waiting = map[string]int64{}
	}
	_, had := st.waiting[name]
	st.waiting[name] = size
	return !had
}

// waitingFiles are the inbox files that do not fit, by name.
func (s *Server) waitingFiles(keep map[string]bool) []string {
	st := &s.storage
	st.mu.Lock()
	defer st.mu.Unlock()
	names := []string{}
	for name := range st.waiting {
		if keep != nil && !keep[name] {
			delete(st.waiting, name) // no longer in the inbox
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
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
	s.settled()
	st := &s.storage
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.use, st.measured
}

// settled waits (a little) for a measuring that is running: a change just made is then counted.
func (s *Server) settled() {
	s.storage.mu.Lock()
	running := s.storage.running
	s.storage.mu.Unlock()
	if running != nil {
		select {
		case <-running:
		case <-time.After(5 * time.Second):
		}
	}
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

// flacBytesPerSecond is about what a second of the made FLAC takes per channel (48 kHz, 16 bit, compressed to
// about 60 %).
const flacBytesPerSecond = 48000 * 2 * 6 / 10

// runNeeds is about how many more bytes a run's files take: what it writes, less what it writes over. 0 when
// the recording's length is not known yet.
func (s *Server) runNeeds(pl plan) int64 {
	st := readState(pl.paths.Folder)
	if st.Recording == nil || st.Recording.Format.Duration <= 0 {
		return 0
	}
	sermon := pl.opt.Length(st.Recording.Format.Duration)
	withClips := sermon
	for _, c := range []*clipRef{pl.Clips.Intro, pl.Clips.Outro} {
		if c != nil {
			withClips += c.Seconds + pl.Clips.Pause
		}
	}
	channels := 1.0
	if pl.opt.Stereo {
		channels = 2
	}
	perSecond := map[string]float64{
		"flac": flacBytesPerSecond * channels,
		"mp3":  bitsPerSecond(pl.opt.Mp3Bitrate) / 8,
		"opus": bitsPerSecond(pl.opt.OpusBitrate) / 8,
	}
	on := map[string]bool{"flac": pl.opt.Outputs.Flac, "mp3": pl.opt.Outputs.Mp3, "opus": pl.opt.Outputs.Opus}
	var need int64
	for _, x := range runOutputs(pl.paths, pl.Clips) {
		f := baseFormat(x.format)
		if !on[f] {
			continue
		}
		length := withClips
		if x.format != f { // the sermon only
			length = sermon
		}
		bytes := int64(perSecond[f] * length)
		if info, err := os.Stat(x.path); err == nil {
			if !pl.opt.Force {
				continue // kept as it is
			}
			bytes -= info.Size()
		}
		need += max(bytes, 0)
	}
	return need
}

// bitsPerSecond reads a bitrate as ffmpeg takes it: "96k", "128000".
func bitsPerSecond(rate string) float64 {
	n, k := strings.CutSuffix(strings.ToLower(rate), "k")
	f, err := strconv.ParseFloat(n, 64)
	if err != nil {
		return 0
	}
	if k {
		f *= 1000
	}
	return f
}
