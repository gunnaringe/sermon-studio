package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A recording's files, as the studio knows them (#86): every file in its folder (the original, the files made,
// the transcript, its own clips), not recording.json and draft.json, nor hidden and half-written files, with
// its size, when it was last changed and its SHA-256. recording.json keeps them under files, by the path in the
// folder (original/2026-10-18_raw.flac). With object storage the bucket's key is the folder's path and that, and
// the hash tells a file fetched back is whole; already now, a file that changed or went missing outside the
// studio is noticed.
//
// syncFiles keeps the record: it runs with the measuring of the storage (storageChanged), which comes after
// every request that may write or remove files, when background work ends, and hourly. A file is only read
// (hashed) when its size or time changed; one renamed keeps both, so a rename reads nothing again.

// fileRecord is one file of a recording.
type fileRecord struct {
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	SHA256   string    `json:"sha256"`
	// Bucket: kept only in the bucket, not on the disk (object storage, fetch.go).
	Bucket bool `json:"bucket,omitempty"`
}

// filesOf are the files of a recording folder as they are on the disk now: the path in the folder (with
// slashes) and what os.Stat says, without their hashes.
func filesOf(folder string) map[string]fileRecord {
	out := map[string]fileRecord{}
	filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if path != folder && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() || leftover(name) {
			return nil
		}
		rel, err := filepath.Rel(folder, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == stateName || rel == draftName {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out[rel] = fileRecord{Size: info.Size(), Modified: info.ModTime().UTC()}
		return nil
	})
	return out
}

// hashFile is the SHA-256 of a file, in hex.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// same tells whether a file on the disk is the one recorded: the same size and time.
func (r fileRecord) same(disk fileRecord) bool {
	return r.SHA256 != "" && r.Size == disk.Size && r.Modified.Equal(disk.Modified)
}

// syncFiles brings a recording's files record up to date with its folder; it says what changed, for the log.
// A file that changes while it is read is left for the next time.
func (s *Server) syncFiles(folder string) (added, changed, gone int) {
	s.filesMu.Lock() // one at a time: the disk is what limits it
	defer s.filesMu.Unlock()
	old := readState(folder).Files
	disk := filesOf(folder)
	next := make(map[string]fileRecord, len(disk))
	moved := map[string]bool{} // old names of files renamed
	for rel, d := range disk {
		if r, ok := old[rel]; ok && r.same(d) {
			r.Bucket = false // on the disk again: fetched
			next[rel] = r
			continue
		}
		if from, ok := renamedFrom(old, disk, d, moved); ok { // a rename keeps the size and time
			next[rel], moved[from] = old[from], true
			continue
		}
		if s.ctx.Err() != nil {
			return // closing; what is done is not written, the next start does it again
		}
		path := filepath.Join(folder, filepath.FromSlash(rel))
		sum, err := hashFile(path)
		if err != nil {
			continue
		}
		if info, err := os.Stat(path); err != nil || info.Size() != d.Size || !info.ModTime().UTC().Equal(d.Modified) {
			continue // changed while it was read
		}
		d.SHA256 = sum
		next[rel] = d
	}
	for rel, r := range old {
		if _, there := disk[rel]; r.Bucket && !there {
			next[rel] = r // kept in the bucket only
		}
	}
	for rel := range next {
		switch r, ok := old[rel]; {
		case ok && r != next[rel]:
			changed++
		case !ok && !renamed(rel, old, next, moved):
			added++
		}
	}
	for rel := range old {
		if _, ok := next[rel]; !ok && !moved[rel] {
			gone++
		}
	}
	if maps.Equal(old, next) {
		return
	}
	if _, err := os.Stat(folder); err != nil {
		return // the folder was renamed or put in the trash meanwhile: its record went with it
	}
	if _, err := s.updateState(folder, func(st *folderState) error {
		st.Files = next
		if len(next) == 0 {
			st.Files = nil
		}
		return nil
	}); err != nil {
		s.cfg.Logger.Printf("%s: files: %v", s.display(folder), err)
	}
	if afterSync != nil {
		afterSync(s, folder)
	}
	return
}

// afterSync, set only by tests, is called when a recording's files record changed (SERMON_TEST_EVICT).
var afterSync func(s *Server, folder string)

// renamedFrom is the old name of a file that is not on the disk any more under it but has the size and time of
// d: the same file, renamed (the studio renames a recording's files with it). Each old name is taken once.
func renamedFrom(old, disk map[string]fileRecord, d fileRecord, taken map[string]bool) (string, bool) {
	for rel, r := range old {
		if _, still := disk[rel]; !still && !taken[rel] && !r.Bucket && r.same(d) {
			return rel, true
		}
	}
	return "", false
}

// renamed tells whether rel in next is a file renamed from one of old's.
func renamed(rel string, old, next map[string]fileRecord, moved map[string]bool) bool {
	for from := range moved {
		if old[from] == next[rel] {
			return true
		}
	}
	return false
}

// syncAllFiles brings every recording's files record up to date, and logs what changed outside the studio's own
// work only as numbers: what a run made is in the log already.
func (s *Server) syncAllFiles() {
	for _, e := range s.allRecordings() {
		if s.ctx.Err() != nil {
			return
		}
		folder := filepath.Join(s.root, filepath.FromSlash(e.Path))
		if added, changed, gone := s.syncFiles(folder); gone > 0 {
			s.cfg.Logger.Printf("%s: files: %d added, %d changed, %d not there any more", s.display(folder), added, changed, gone)
		}
	}
}

// filesChanged has the files record brought up to date soon (keepFilesRecorded); asked again meanwhile, it is
// done once more after.
func (s *Server) filesChanged() {
	select {
	case s.filesKick <- struct{}{}:
	default:
	}
}

// keepFilesRecorded runs syncAllFiles whenever filesChanged asks, until the server closes.
func (s *Server) keepFilesRecorded() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.filesKick:
			s.syncAllFiles()
		}
	}
}
