package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Files only in the bucket (#86): with object storage a recording's file can be kept in the bucket alone, not
// on the disk. Its files record says so (fileRecord.Bucket); the studio still finds it (normalizer.Elsewhere:
// Source, Exists, Stat, fileExists), and fetches it before anything reads it (normalizer.Need: every ffmpeg
// input, a copy, a rename; fetchFor before the studio serves one), checked against its SHA-256. Removing it
// (normalizer.Remove, normalizer.Forget) removes it from the bucket and the record. evict is the way a file
// leaves the disk; it is never removed with normalizer.Remove, which would remove it from the bucket too.
// Only a recording's files are kept so; a recording.json, a draft, the church's files and the cache never.

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

// recordFolder is the recording folder whose files record has the files of dir: dir itself, or the folder
// above (original/, clips/); "" when neither is a recording's.
func recordFolder(dir string) (folder, relDir string) {
	for _, f := range []string{dir, filepath.Dir(dir)} {
		if _, err := os.Stat(statePath(f)); err == nil {
			if f == dir {
				return f, "."
			}
			return f, filepath.Base(dir)
		}
	}
	return "", ""
}

// elsewhereFiles is normalizer.Elsewhere: the files of dir kept only in the bucket and not on the disk.
func elsewhereFiles(dir string) map[string]fs.FileInfo {
	m := mirrorFor(dir)
	if m == nil {
		return nil
	}
	dir = m.norm(dir)
	folder, relDir := recordFolder(dir)
	if folder == "" {
		return nil
	}
	var out map[string]fs.FileInfo
	for rel, r := range inBucketOf(folder) {
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

// inBucketCache keeps the files only in the bucket per recording folder, read again when its recording.json
// changes: the list of recordings asks for each one's files, many times.
var inBucketCache sync.Map // folder → inBucketEntry

type inBucketEntry struct {
	size  int64
	mtime time.Time
	files map[string]fileRecord
}

// inBucketOf is the files of a recording kept only in the bucket, by their path in its folder.
func inBucketOf(folder string) map[string]fileRecord {
	st, err := os.Stat(statePath(folder))
	if err != nil {
		inBucketCache.Delete(folder)
		return nil
	}
	if e, ok := inBucketCache.Load(folder); ok {
		if e := e.(inBucketEntry); e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
			return e.files
		}
	}
	var files map[string]fileRecord
	for rel, r := range readState(folder).Files {
		if r.Bucket {
			if files == nil {
				files = map[string]fileRecord{}
			}
			files[rel] = r
		}
	}
	inBucketCache.Store(folder, inBucketEntry{st.Size(), st.ModTime(), files})
	return files
}

// recorded is the record of a file kept only in the bucket: its folder, the path in it, the record.
func recorded(path string) (folder, rel string, r fileRecord, ok bool) {
	folder, relDir := recordFolder(filepath.Dir(path))
	if folder == "" {
		return "", "", r, false
	}
	rel = filepath.Base(path)
	if relDir != "." {
		rel = relDir + "/" + rel
	}
	r, ok = readState(folder).Files[rel]
	return folder, rel, r, ok && r.Bucket
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
	folder, rel, r, ok := recorded(p)
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
	if _, err := m.blob.GetFile(ctx, key, p); err != nil {
		return fmt.Errorf("storage: fetching %s: %w", key, err)
	}
	sum, err := hashFile(p)
	if st, serr := os.Stat(p); err != nil || serr != nil || sum != r.SHA256 || st.Size() != r.Size {
		os.Remove(p) // not the file recorded: never used (not normalizer.Remove: the bucket keeps its copy)
		return fmt.Errorf("storage: %s from the bucket is not the file recorded", key)
	}
	os.Chtimes(p, r.Modified, r.Modified)
	if s := serverOf(folder); s != nil { // on the disk again: at once, so a rename after takes its record along
		s.updateState(folder, func(st *folderState) error {
			if x, ok := st.Files[rel]; ok {
				x.Bucket = false
				st.Files[rel] = x
			}
			return nil
		})
	}
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
	folder, rel, _, ok := recorded(p)
	key, mirrored := m.key(p)
	s := serverOf(folder)
	if !ok || !mirrored || s == nil {
		return fmt.Errorf("%w: %s", fs.ErrNotExist, p)
	}
	if _, err := s.updateState(folder, func(st *folderState) error {
		delete(st.Files, rel)
		if len(st.Files) == 0 {
			st.Files = nil
		}
		return nil
	}); err != nil {
		return err
	}
	m.add(mirrorOp{Kind: "delete", Key: key})
	return nil
}

// errNotEvicted is why a file is not removed from the disk by evict.
var errNotEvicted = errors.New("storage: not removed from the disk")

// evict removes a recording's file from the disk, keeping it only in the bucket: only a file the record has as
// it is on the disk now (size, time, SHA-256), that the bucket has as uploaded after it last changed, with
// nothing for it waiting in the journal. It is marked in the record first, so a stop in between leaves a
// file on the disk that syncFiles marks there again.
func (s *Server) evict(ctx context.Context, folder, rel string) error {
	p := filepath.Join(folder, filepath.FromSlash(rel))
	m := mirrorFor(p)
	if m == nil {
		return fmt.Errorf("%w: no bucket", errNotEvicted)
	}
	key, ok := m.key(p)
	if !ok {
		return fmt.Errorf("%w: %s stays on the disk", errNotEvicted, rel)
	}
	r, ok := readState(folder).Files[rel]
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
	case s.jobs.active(s.relPath(folder)) != nil || s.maker.waiting(s.relPath(folder)) || s.analyses.busyIn(folder):
		return fmt.Errorf("%w: %s is in use", errNotEvicted, rel)
	}
	info, err := m.blob.Head(ctx, key)
	if err != nil || !uploaded(info, st) {
		return fmt.Errorf("%w: %s is not in the bucket as it is (%v)", errNotEvicted, rel, err)
	}
	if sum, err := hashFile(p); err != nil || sum != r.SHA256 {
		return fmt.Errorf("%w: %s is not the file recorded", errNotEvicted, rel)
	}
	if _, err := s.updateState(folder, func(st *folderState) error {
		x := st.Files[rel]
		x.Bucket = true
		st.Files[rel] = x
		return nil
	}); err != nil {
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
		s.updateState(folder, func(st *folderState) error {
			if x, ok := st.Files[rel]; ok {
				x.Bucket = false
				st.Files[rel] = x
			}
			return nil
		})
		return fmt.Errorf("%w: %s changed while it was removed", errNotEvicted, rel)
	}
	return os.Remove(aside)
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
