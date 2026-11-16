package web

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Keeping the disk to what is used (#86): with object storage, a recording's file not used for DiskOptions.KeepFor
// is taken off the disk and kept only in the bucket (evict, fetch.go), and with DiskOptions.Size the disk holds
// at most that much of the recordings' files: the ones used longest ago go first. Used is when the file was last
// made, fetched, or read by the studio (a run, an analysis, a sample, a transcript, a download from the disk);
// listening to or downloading one only in the bucket does not fetch it (bucketLink). The times are kept in
// .blob/used.json, by key; a file not there counts from its own time.

// DiskOptions are how much of the recordings' files the disk keeps, with object storage.
type DiskOptions struct {
	// KeepFor is how long a file not used stays on the disk; 0 = no limit.
	KeepFor time.Duration
	// Size is the most the recordings' files may take on the disk; 0 = no limit.
	Size int64
}

const keepDiskEvery = time.Hour

func init() {
	normalizer.Reading = func(path string) {
		if m := mirrorFor(path); m != nil {
			if key, ok := m.key(m.norm(path)); ok {
				m.used(key)
			}
		}
	}
}

func (m *mirror) usePath() string { return filepath.Join(m.root, mirrorDir, "used.json") }

func (m *mirror) loadUse() {
	m.lastUse = map[string]time.Time{}
	if data, err := os.ReadFile(m.usePath()); err == nil {
		json.Unmarshal(data, &m.lastUse)
	}
}

func (m *mirror) saveUse() {
	m.useMu.Lock()
	defer m.useMu.Unlock()
	if !m.useDirty {
		return
	}
	data, _ := json.Marshal(m.lastUse)
	tmp := m.usePath() + ".partial"
	if os.WriteFile(tmp, data, 0o600) == nil && os.Rename(tmp, m.usePath()) == nil {
		m.useDirty = false
	}
}

// used notes that the file of key is used now.
func (m *mirror) used(key string) {
	if m == nil {
		return
	}
	m.useMu.Lock()
	m.lastUse[key] = time.Now().UTC()
	m.useDirty = true
	m.useMu.Unlock()
}

// moveUse follows a rename: of a file, or of a folder with what is in it.
func (m *mirror) moveUse(from, to string) {
	m.useMu.Lock()
	defer m.useMu.Unlock()
	for k, t := range m.lastUse {
		if rest, ok := strings.CutPrefix(k, from); ok && (rest == "" || rest[0] == '/') {
			delete(m.lastUse, k)
			m.lastUse[to+rest] = t
			m.useDirty = true
		}
	}
}

// forgetUse drops a file removed, or a folder's files.
func (m *mirror) forgetUse(key string) {
	m.useMu.Lock()
	defer m.useMu.Unlock()
	for k := range m.lastUse {
		if k == key || strings.HasPrefix(k, key+"/") {
			delete(m.lastUse, k)
			m.useDirty = true
		}
	}
}

// lastUsed is when the file of key was last used, else when it last changed (its record).
func (m *mirror) lastUsed(key string, r fileRecord) time.Time {
	m.useMu.Lock()
	defer m.useMu.Unlock()
	if t, ok := m.lastUse[key]; ok && t.After(r.Modified) {
		return t
	}
	return r.Modified
}

// onDisk is a recording's file on the disk that may be kept only in the bucket.
type onDisk struct {
	s           *Server
	folder, rel string
	size        int64
	used        time.Time
}

// keepDisk takes files off the disk by DiskOptions, at start (after the first sweep) and every keepDiskEvery,
// until the app closes.
func (a *App) keepDisk() {
	defer a.diskWG.Done()
	m, opt := a.mirror, a.cfg.Disk
	select {
	case <-m.swept:
	case <-a.diskStop:
		return
	}
	tick := time.NewTicker(keepDiskEvery)
	defer tick.Stop()
	for {
		a.keepDiskOnce(time.Now(), opt)
		m.saveUse()
		select {
		case <-a.diskStop:
			return
		case <-tick.C:
		}
	}
}

// keepDiskOnce takes off the disk the files not used for opt.KeepFor, then, while the files on the disk take
// more than opt.Size, the ones used longest ago. A file that cannot be (not in the bucket yet, in use) stays and
// counts; it is tried again the next time.
func (a *App) keepDiskOnce(now time.Time, opt DiskOptions) {
	m := a.mirror
	var files []onDisk
	var total int64
	a.mu.Lock()
	tenants := slices.Clone(a.tenants)
	a.mu.Unlock()
	for _, t := range tenants {
		s := t.srv
		if s == nil || !s.cfg.RecordFiles {
			continue
		}
		for _, e := range s.allRecordings() {
			folder := filepath.Join(s.root, filepath.FromSlash(e.Path))
			for rel, r := range readState(folder).Files {
				if r.Bucket {
					continue
				}
				key, ok := m.key(filepath.Join(folder, filepath.FromSlash(rel)))
				if !ok {
					continue
				}
				files = append(files, onDisk{s, folder, rel, r.Size, m.lastUsed(key, r)})
				total += r.Size
			}
		}
	}
	slices.SortFunc(files, func(x, y onDisk) int { return x.used.Compare(y.used) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var n int
	var freed int64
	for _, f := range files {
		old := opt.KeepFor > 0 && now.Sub(f.used) > opt.KeepFor
		full := opt.Size > 0 && total > opt.Size
		if !old && !full {
			break // the rest were used later
		}
		if a.closing() {
			return
		}
		if err := f.s.evict(ctx, f.folder, f.rel); err != nil {
			continue
		}
		n, freed, total = n+1, freed+f.size, total-f.size
	}
	if n > 0 {
		a.cfg.Base.Logger.Printf("storage: %d files (%s) taken off the disk, kept in the bucket; %s of recordings on the disk",
			n, sizeText(freed), sizeText(total))
	}
}
