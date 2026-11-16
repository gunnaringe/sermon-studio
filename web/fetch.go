package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Files only in the bucket (#86): with object storage a recorded file can be kept in the bucket alone, not
// on the disk. Its files record says so (fileRecord.Bucket); the studio still finds it (normalizer.Elsewhere:
// Source, Exists, Stat, fileExists), and fetches it before anything reads it (normalizer.Need: every ffmpeg
// input, a copy, a rename; fetchFor before the studio serves one), checked against its SHA-256. Removing it
// (normalizer.Remove, normalizer.Forget) removes it from the bucket and the record. evict is the way a file
// leaves the disk; it is never removed with normalizer.Remove, which would remove it from the bucket too.
// A recording's files are kept so, and the church's clips and logo (recorded in church.json); never a JSON file
// (a record, a transcript), a draft or the cache.

func init() {
	normalizer.Elsewhere = elsewhereFiles
	normalizer.Need = fetchFile
	normalizer.Forget = forgetFile
}

// The churches' servers in this process, by root: who keeps the record of a file (forgetFile).
var (
	serversMu sync.RWMutex
	servers   []*Server
)

func registerServer(s *Server) {
	serversMu.Lock()
	servers = append(servers, s)
	serversMu.Unlock()
}

func unregisterServer(s *Server) {
	serversMu.Lock()
	for i, x := range servers {
		if x == s {
			servers = append(servers[:i], servers[i+1:]...)
			break
		}
	}
	serversMu.Unlock()
}

// churchServerOf is the server of the church whose folder is dir.
func churchServerOf(dir string) *Server {
	serversMu.RLock()
	defer serversMu.RUnlock()
	for _, s := range servers {
		if s.churchDir != "" && dir == s.churchDir { // both resolved, as serverOf compares (callers norm dir)
			return s
		}
	}
	return nil
}

// serverOf is the church's server whose root holds path.
func serverOf(path string) *Server {
	serversMu.RLock()
	defer serversMu.RUnlock()
	for _, s := range servers {
		if under(path, s.root) {
			return s
		}
	}
	return nil
}

// inBucket is a file kept only in the bucket, as fs.FileInfo.
type inBucket struct {
	name string
	r    fileRecord
}

func (f inBucket) Name() string       { return f.name }
func (f inBucket) Size() int64        { return f.r.Size }
func (f inBucket) Mode() fs.FileMode  { return 0o644 }
func (f inBucket) ModTime() time.Time { return f.r.Modified }
func (f inBucket) IsDir() bool        { return false }
func (f inBucket) Sys() any           { return nil }

// filesRecord is where the record of a file is kept: a recording's recording.json, or the church's church.json
// for the church's files (its clips and logo, churchFilesOf). Paths in it are relative to folder.
type filesRecord struct {
	folder string
	file   string  // the JSON file it is in
	srv    *Server // who changes it; nil when no church of this process has the folder
	church bool
}

// files is the record as it is saved.
func (fr filesRecord) files() map[string]fileRecord {
	if fr.church {
		cc, _ := readChurch(fr.folder)
		return cc.Files
	}
	return readState(fr.folder).Files
}

// change changes the record as one step and saves it (not when fn fails, nor when nothing changed).
func (fr filesRecord) change(fn func(map[string]fileRecord) error) error {
	if fr.srv == nil {
		return fmt.Errorf("%w: %s", fs.ErrNotExist, fr.file)
	}
	if fr.church {
		return fr.srv.cfg.SaveChurchFiles(fn)
	}
	_, err := fr.srv.updateState(fr.folder, func(st *folderState) error {
		if st.Files == nil {
			st.Files = map[string]fileRecord{}
		}
		err := fn(st.Files)
		if len(st.Files) == 0 {
			st.Files = nil
		}
		return err
	})
	return err
}

// recordOf is the record of a recording folder, for a path as the mirror has it (normed).
func recordOf(folder string) filesRecord {
	return filesRecord{folder: folder, file: statePath(folder), srv: serverOf(folder)}
}

// record is the record of one of the server's own recording folders (a path under its root as it has it).
func (s *Server) record(folder string) filesRecord {
	return filesRecord{folder: folder, file: statePath(folder), srv: s}
}

// churchRecord is the record of a church's files, for its server.
func (s *Server) churchRecord() (filesRecord, bool) {
	if s.churchDir == "" || s.cfg.SaveChurchFiles == nil || !s.cfg.RecordFiles {
		return filesRecord{}, false
	}
	return filesRecord{folder: s.churchDir, file: filepath.Join(s.churchDir, churchFile), srv: s, church: true}, true
}

// recordFolder is the record that has the files of dir: a recording's (dir itself, or the folder above:
// original/, clips/), or a church's (its folder, its clips/); ok is false when none has.
func recordFolder(dir string) (fr filesRecord, relDir string, ok bool) {
	for _, f := range []string{dir, filepath.Dir(dir)} {
		rel := "."
		if f != dir {
			rel = filepath.Base(dir)
		}
		if _, err := os.Stat(statePath(f)); err == nil {
			return recordOf(f), rel, true
		}
		if s := churchServerOf(f); s != nil {
			if fr, ok := s.churchRecord(); ok {
				return fr, rel, true
			}
		}
	}
	return filesRecord{}, "", false
}

// elsewhereFiles is normalizer.Elsewhere: the files of dir kept only in the bucket and not on the disk.
func elsewhereFiles(dir string) map[string]fs.FileInfo {
	m := mirrorFor(dir)
	if m == nil {
		return nil
	}
	dir = m.norm(dir)
	fr, relDir, ok := recordFolder(dir)
	if !ok {
		return nil
	}
	var out map[string]fs.FileInfo
	for rel, r := range inBucketOf(fr) {
		if path.Dir(rel) != relDir {
			continue
		}
		name := path.Base(rel)
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			continue // fetched; the record follows at the next syncFiles
		}
		if out == nil {
			out = map[string]fs.FileInfo{}
		}
		out[name] = inBucket{name, r}
	}
	return out
}

// inBucketCache keeps the files only in the bucket per record, read again when its JSON file changes: the list
// of recordings asks for each one's files, many times.
var inBucketCache sync.Map // the record's file → inBucketEntry

type inBucketEntry struct {
	size  int64
	mtime time.Time
	files map[string]fileRecord
}

// inBucketOf is the files of a record kept only in the bucket, by their path in its folder.
func inBucketOf(fr filesRecord) map[string]fileRecord {
	st, err := os.Stat(fr.file)
	if err != nil {
		inBucketCache.Delete(fr.file)
		return nil
	}
	if e, ok := inBucketCache.Load(fr.file); ok {
		if e := e.(inBucketEntry); e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
			return e.files
		}
	}
	var files map[string]fileRecord
	for rel, r := range fr.files() {
		if r.Bucket {
			if files == nil {
				files = map[string]fileRecord{}
			}
			files[rel] = r
		}
	}
	inBucketCache.Store(fr.file, inBucketEntry{st.Size(), st.ModTime(), files})
	return files
}

// recorded is the record of a file kept only in the bucket: the record it is in, the path in its folder, the
// file's.
func recorded(path string) (fr filesRecord, rel string, r fileRecord, ok bool) {
	fr, relDir, ok := recordFolder(filepath.Dir(path))
	if !ok {
		return fr, "", r, false
	}
	rel = filepath.Base(path)
	if relDir != "." {
		rel = relDir + "/" + rel
	}
	r, ok = fr.files()[rel]
	return fr, rel, r, ok && r.Bucket
}

// setBucket marks a recorded file as kept only in the bucket, or as on the disk.
func (fr filesRecord) setBucket(rel string, bucket bool) error {
	return fr.change(func(files map[string]fileRecord) error {
		if x, ok := files[rel]; ok {
			x.Bucket = bucket
			files[rel] = x
		}
		return nil
	})
}

// fetching holds a lock per file being fetched, so two that need it fetch it once.
var fetching sync.Map

// fetchFile is normalizer.Need: it fetches a file kept only in the bucket onto the disk, checks it against its
// record and gives it its time back (so it is not taken for changed, nor put in the bucket again).
func fetchFile(ctx context.Context, p string) error {
	m := mirrorFor(p)
	if m == nil {
		return fmt.Errorf("%w: %s", fs.ErrNotExist, p)
	}
	p = m.norm(p)
	mu, _ := fetching.LoadOrStore(p, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if _, err := os.Lstat(p); err == nil {
		return nil // another fetched it meanwhile
	}
	fr, rel, r, ok := recorded(p)
	key, mirrored := m.key(p)
	if !ok || !mirrored {
		return fmt.Errorf("%w: %s", fs.ErrNotExist, p)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute) // a rename or a copy has no deadline of its own
	defer cancel()
	start := time.Now()
	for wait := 10 * time.Millisecond; m.pending(key); wait = min(2*wait, time.Second) { // a move not done yet
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	// Fetched next to it as a hidden file (which the files record and the mirror pass over), checked and given
	// its time, then put in place: nothing sees it half fetched, nor with the time of the fetch.
	tmp := filepath.Join(filepath.Dir(p), "."+filepath.Base(p)+".fetch")
	defer os.Remove(tmp)
	if _, err := m.blob.GetFile(ctx, key, tmp); err != nil {
		return fmt.Errorf("storage: fetching %s: %w", key, err)
	}
	sum, err := hashFile(tmp)
	if st, serr := os.Stat(tmp); err != nil || serr != nil || sum != r.SHA256 || st.Size() != r.Size {
		return fmt.Errorf("storage: %s from the bucket is not the file recorded", key)
	}
	if err := os.Chtimes(tmp, r.Modified, r.Modified); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil { // not normalizer.Rename: the bucket has it already
		return err
	}
	fr.setBucket(rel, false) // on the disk again: at once, so a rename after takes its record along
	m.logger.Printf("storage: fetched %s (%s) in %s", key, sizeText(r.Size), time.Since(start).Round(time.Millisecond))
	return nil
}

// readDir is os.ReadDir with the files of dir kept only in the bucket.
func readDir(dir string) []fs.DirEntry {
	des, _ := os.ReadDir(dir)
	for _, info := range elsewhereFiles(dir) {
		des = append(des, fs.FileInfoToDirEntry(info))
	}
	return des
}

// fetchFor fetches what path is, if it is kept only in the bucket, before the studio reads or serves it.
func fetchFor(ctx context.Context, path string) error {
	return normalizer.NeedFile(ctx, path)
}

// forgetFile is normalizer.Forget: a file kept only in the bucket is removed: from its record, then the bucket.
func forgetFile(p string) error {
	m := mirrorFor(p)
	if m == nil {
		return fmt.Errorf("%w: %s", fs.ErrNotExist, p)
	}
	p = m.norm(p)
	fr, rel, _, ok := recorded(p)
	key, mirrored := m.key(p)
	if !ok || !mirrored || fr.srv == nil {
		return fmt.Errorf("%w: %s", fs.ErrNotExist, p)
	}
	if err := fr.change(func(files map[string]fileRecord) error {
		delete(files, rel)
		return nil
	}); err != nil {
		return err
	}
	m.add(mirrorOp{Kind: "delete", Key: key})
	return nil
}

// errNotEvicted is why a file is not removed from the disk by evict.
var errNotEvicted = errors.New("storage: not removed from the disk")

// evict removes a recorded file from the disk, keeping it only in the bucket: only a file the record has as
// it is on the disk now (size, time, SHA-256), that the bucket has as uploaded after it last changed, with
// nothing for it waiting in the journal. It is marked in the record first, so a stop in between leaves a
// file on the disk that syncFiles marks there again.
func (s *Server) evict(ctx context.Context, fr filesRecord, rel string) error {
	p := filepath.Join(fr.folder, filepath.FromSlash(rel))
	m := mirrorFor(p)
	if m == nil {
		return fmt.Errorf("%w: no bucket", errNotEvicted)
	}
	key, ok := m.key(p)
	if !ok || strings.HasSuffix(rel, ".json") { // a JSON file (a transcript) is read as it is, always on the disk
		return fmt.Errorf("%w: %s stays on the disk", errNotEvicted, rel)
	}
	r, ok := fr.files()[rel]
	st, err := os.Stat(p)
	switch {
	case !ok || r.Bucket:
		return fmt.Errorf("%w: %s is not recorded", errNotEvicted, rel)
	case err != nil:
		return err
	case !r.same(fileRecord{Size: st.Size(), Modified: st.ModTime().UTC()}):
		return fmt.Errorf("%w: %s changed since it was recorded", errNotEvicted, rel)
	case m.pending(key):
		return fmt.Errorf("%w: %s is not in the bucket yet", errNotEvicted, rel)
	case s.inUse(fr):
		return fmt.Errorf("%w: %s is in use", errNotEvicted, rel)
	}
	info, err := m.blob.Head(ctx, key)
	if err != nil || !uploaded(info, st) {
		return fmt.Errorf("%w: %s is not in the bucket as it is (%v)", errNotEvicted, rel, err)
	}
	if sum, err := hashFile(p); err != nil || sum != r.SHA256 {
		return fmt.Errorf("%w: %s is not the file recorded", errNotEvicted, rel)
	}
	s.filesMu.Lock() // no syncRecord in between: it would see the file still there and mark it so again
	defer s.filesMu.Unlock()
	if err := fr.setBucket(rel, true); err != nil {
		return err
	}
	// Not normalizer.Remove: the bucket keeps it. Moved aside first, so a file put there meanwhile (a run's new
	// one) is not the one removed.
	aside := filepath.Join(filepath.Dir(p), "."+filepath.Base(p)+".evict")
	if err := os.Rename(p, aside); err != nil {
		return err
	}
	if st, err := os.Stat(aside); err != nil || !r.same(fileRecord{Size: st.Size(), Modified: st.ModTime().UTC()}) {
		if _, there := os.Lstat(p); there != nil {
			os.Rename(aside, p)
		} else {
			os.Remove(aside) // replaced again meanwhile: the newer one is there
		}
		fr.setBucket(rel, false)
		return fmt.Errorf("%w: %s changed while it was removed", errNotEvicted, rel)
	}
	return os.Remove(aside)
}

// inUse tells whether a record's files may be read now: a recording's by a run, the making of its files or an
// analysis; the church's (clips, logo) while the church does any background work or takes in a clip.
func (s *Server) inUse(fr filesRecord) bool {
	if fr.church {
		s.clips.mu.Lock()
		defer s.clips.mu.Unlock()
		for _, p := range s.clips.pending {
			if p.Error == "" {
				return true
			}
		}
		return s.Busy()
	}
	folder := fr.folder
	return s.jobs.active(s.relPath(folder)) != nil || s.maker.waiting(s.relPath(folder)) || s.analyses.busyIn(folder)
}

// linkFor is how long an address of the bucket for one file works: a sermon to a slow phone, a download that is
// continued.
const linkFor = 6 * time.Hour

// bucketLink is an address of the bucket for a file kept only there, which the browser gets it from instead of
// the studio (nothing is fetched onto the disk for it); ok is false for a file on the disk, or without a bucket.
func bucketLink(ctx context.Context, p string, inline bool) (link string, ok bool, err error) {
	m := mirrorFor(p)
	if m == nil {
		return "", false, nil
	}
	if _, err := os.Lstat(p); err == nil {
		return "", false, nil
	}
	if _, kept := normalizer.Stat(p); kept != nil {
		return "", false, nil
	}
	key, mirrored := m.key(p)
	if !mirrored {
		return "", false, nil
	}
	u, err := m.blob.Presign(ctx, key, linkFor, filepath.Base(p), inline)
	if err != nil {
		return "", false, fmt.Errorf("storage: an address for %s: %w", key, err)
	}
	return u.String(), true, nil
}

// bucketOrigin is the origin of the bucket's addresses (scheme and host), for a page's Content-Security-Policy;
// "" when it cannot be told.
func bucketOrigin(b Blob) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u, err := b.Presign(ctx, "_origin", time.Minute, "x", true)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
