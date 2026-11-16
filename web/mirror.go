package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// Object storage (#86, docs/object-storage.md). With a bucket (AppConfig.Blob), the bucket is a copy of the
// data dir, kept as the studio works, so a new machine with the same settings has everything. The disk stays
// the studio's working copy: it reads and writes files there as before, and the mirror follows.
//
//   - A state file (recording.json, church.json, shares.json …, written by writeFileAtomic or writeJSONFile)
//     is in the bucket before the request is answered; if the bucket does not take it, the write fails and
//     the file is as it was.
//   - draft.json goes at most every draftEvery.
//   - Other files (the original, the files made, transcripts, clips, the logo) go in the background once they
//     are whole: the journal (journal.json in .blob/), which a restart goes on with.
//   - A rename or removal by the studio (normalizer.Rename, Remove, RemoveAll; normalizer.Changed tells the
//     mirror) is done in the bucket too: a copy then a delete, as S3 has no rename.
//   - The bucket is never told to delete a file because the disk lacks it: the sweep (at start, then hourly)
//     only uploads what the bucket lacks, and reports what only the bucket has.
//   - Only on this machine: sessions (users log in again), the inboxes (an upload is taken in first), the
//     cache, the models, the transcript queue's parts, and hidden and half-written files (not the trash).
//   - One studio writes to a bucket at a time: the lease (lease.go).

// Blob is a bucket as the studio uses it; cmd/sermon-studio gives it a blob.Store (web does not import that,
// so the desktop app is without it). Errors for a missing key and a failed condition wrap ErrBlobNotFound and
// ErrBlobPrecondition.
type Blob interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) (BlobInfo, error)
	PutFile(ctx context.Context, key, path string) (BlobInfo, error)
	Get(ctx context.Context, key string) (io.ReadCloser, BlobInfo, error)
	GetFile(ctx context.Context, key, path string) (BlobInfo, error)
	Head(ctx context.Context, key string) (BlobInfo, error)
	Copy(ctx context.Context, from, to string) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) iter.Seq2[BlobInfo, error]
	// Presign is an address that gets key without credentials until expiry, naming the file name for the browser.
	Presign(ctx context.Context, key string, expiry time.Duration, name string, inline bool) (*url.URL, error)
	// PutNew stores data only if key is not there; PutIfMatch only if it is still the version etag.
	PutNew(ctx context.Context, key string, data []byte) (BlobInfo, error)
	PutIfMatch(ctx context.Context, key string, data []byte, etag string) (BlobInfo, error)
}

// BlobInfo is what the bucket says of an object.
type BlobInfo struct {
	Key      string
	Size     int64
	ETag     string
	Modified time.Time
}

var (
	ErrBlobNotFound     = errors.New("not in the bucket")
	ErrBlobPrecondition = errors.New("the object is not as expected")
)

const (
	// putTimeout is how long a state file's put may take: it holds the request (and, for recording.json, the
	// church's state lock), well under the ~100 s a request may take behind Cloudflare.
	putTimeout = 15 * time.Second
	// draftEvery is how often a recording's draft.json goes to the bucket while it is edited.
	draftEvery = 30 * time.Second
	// mirrorDir is the mirror's own folder in the data dir, only on this machine.
	mirrorDir = ".blob"
	// leaseKey and checkPrefix are keys of the studio's own in the bucket, not files of the data dir.
	leaseKey = "_lease"
)

// mirror keeps a bucket a copy of one data dir.
type mirror struct {
	blob   Blob
	root   string // the data dir, as an absolute, real path
	local  []string
	logger *log.Logger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	lease  *lease

	mu      sync.Mutex
	journal []mirrorOp
	drafts  map[string]time.Time // draft.json changed (key → since), waiting for draftEvery
	kick    chan struct{}
	failing string // the error the journal is stuck on, logged once
	swept   chan struct{}
}

// mirrorOp is one thing for the bucket to do, in order.
type mirrorOp struct {
	Kind string `json:"kind"` // "upload" (a file or folder as it is on the disk), "move" (Key to To), "delete"
	Key  string `json:"key"`
	To   string `json:"to,omitempty"`
}

// The mirrors of the data dirs in this process (tests run several), for the writes and renames that find them
// by the path (mirrorFor).
var (
	mirrorsMu sync.RWMutex
	mirrors   []*mirror
)

func init() {
	normalizer.Changed = blobChanged
}

func mirrorFor(path string) *mirror {
	mirrorsMu.RLock()
	defer mirrorsMu.RUnlock()
	for _, m := range mirrors {
		if path == m.root || strings.HasPrefix(path, m.root+string(filepath.Separator)) {
			return m
		}
	}
	return nil
}

// startMirror takes the bucket's lease, goes on with what the journal has left, and starts the sweep. local are
// folders under the data dir that stay on this machine (the cache, the models).
func startMirror(b Blob, dataDir string, local []string, logger *log.Logger, lost func(error)) (*mirror, error) {
	root, err := filepath.Abs(dataDir)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, mirrorDir), 0o700); err != nil {
		return nil, err
	}
	m := &mirror{blob: b, root: root, logger: logger, drafts: map[string]time.Time{}, kick: make(chan struct{}, 1),
		swept: make(chan struct{})}
	for _, l := range local {
		if abs, err := filepath.Abs(l); err == nil {
			if real, err := filepath.EvalSymlinks(abs); err == nil {
				abs = real
			}
			m.local = append(m.local, abs)
		}
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	if m.lease, err = takeLease(m.ctx, b, root, logger, lost); err != nil {
		m.cancel()
		return nil, err
	}
	if data, err := os.ReadFile(m.journalPath()); err == nil {
		json.Unmarshal(data, &m.journal)
	}
	mirrorsMu.Lock()
	mirrors = append(mirrors, m)
	mirrorsMu.Unlock()
	m.wg.Add(3)
	go m.work()
	go m.keepDrafts()
	go m.keepSweeping()
	return m, nil
}

// close stops the mirror: what the journal has left is done at the next start. The lease is given back, so
// the studio that comes next (an update) need not wait for it.
func (m *mirror) close() {
	if m == nil {
		return
	}
	mirrorsMu.Lock()
	mirrors = slices.DeleteFunc(mirrors, func(x *mirror) bool { return x == m })
	mirrorsMu.Unlock()
	m.flushDrafts()
	m.cancel()
	m.wg.Wait()
	m.lease.release()
}

// key is the bucket's key of a path, and whether the path goes to the bucket at all.
func (m *mirror) key(path string) (string, bool) {
	rel, err := filepath.Rel(m.root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	for _, l := range m.local {
		if path == l || strings.HasPrefix(path, l+string(filepath.Separator)) {
			return "", false
		}
	}
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if (strings.HasPrefix(p, ".") && p != trashName) || leftover(p) {
			return "", false
		}
	}
	switch {
	case rel == sessionsFile:
		return "", false
	case len(parts) >= 2 && parts[1] == tenantInbox: // <church>/Innboks: an arrival counts once taken in
		return "", false
	}
	return rel, true
}

func (m *mirror) path(key string) string { return filepath.Join(m.root, filepath.FromSlash(key)) }

func (m *mirror) journalPath() string { return filepath.Join(m.root, mirrorDir, "journal.json") }

// stateFile tells whether a write of path goes to the bucket before it is answered (mirrorWrite): every one
// but a draft, which goes later (draftEvery).
func (m *mirror) stateFile(path string) (string, bool) {
	key, ok := m.key(path)
	return key, ok && filepath.Base(path) != draftName
}

// mirrorWrite puts a state file the studio has just written in the bucket; old is what was there before (nil:
// nothing), put back when the bucket does not take it, so the disk and the bucket do not disagree.
func mirrorWrite(path string, data, old []byte, hadOld bool) error {
	writing.Delete(path)
	m := mirrorFor(path)
	if m == nil {
		return nil
	}
	key, ok := m.stateFile(path)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(m.ctx, putTimeout)
	defer cancel()
	if _, err := m.blob.Put(ctx, key, bytes.NewReader(data), int64(len(data))); err != nil {
		if hadOld {
			putBack(path, old)
		} else {
			os.Remove(path)
		}
		m.logger.Printf("storage: %s not saved: %v", key, err)
		return fmt.Errorf("could not save in the storage: %w", err)
	}
	return nil
}

// oldForMirror is what path holds before a state file's write, when it goes to the bucket (mirrorWrite). The
// write's rename is then left to mirrorWrite (writing), not the journal.
func oldForMirror(path string) ([]byte, bool) {
	if m := mirrorFor(path); m != nil {
		if _, ok := m.stateFile(path); ok {
			writing.Store(path, true)
			old, err := os.ReadFile(path)
			return old, err == nil
		}
	}
	return nil, false
}

// writing are the state files being written by writeFileAtomic or writeJSONFile, between oldForMirror and
// mirrorWrite.
var writing sync.Map

// putBack writes the old content of a file again, as writeFileAtomic does, but without telling the mirror.
func putBack(path string, old []byte) {
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".partial")
	if os.WriteFile(tmp, old, 0o600) == nil {
		os.Rename(tmp, path)
	}
}

// blobChanged is normalizer.Changed: a rename or removal by the studio, done in the bucket too.
func blobChanged(from, to string) {
	m := mirrorFor(from)
	if m == nil && to != "" {
		m = mirrorFor(to)
	}
	if m == nil {
		return
	}
	fk, fok := m.key(from)
	if to == "" {
		if fok {
			m.add(mirrorOp{Kind: "delete", Key: fk})
		}
		return
	}
	tk, tok := m.key(to)
	if _, ok := writing.Load(to); ok && tok {
		return // writeFileAtomic or writeJSONFile: mirrorWrite puts it
	}
	switch {
	case fok && tok:
		m.add(mirrorOp{Kind: "move", Key: fk, To: tk})
	case tok && filepath.Base(to) == draftName:
		m.mu.Lock()
		if _, ok := m.drafts[tk]; !ok {
			m.drafts[tk] = time.Now()
		}
		m.mu.Unlock()
	case tok: // a file made whole (from its .partial), or one that came in (an upload, the inbox)
		m.add(mirrorOp{Kind: "upload", Key: tk})
	case fok: // out of what goes to the bucket
		m.add(mirrorOp{Kind: "delete", Key: fk})
	}
}

// add puts an op in the journal, kept on the disk before it is done.
func (m *mirror) add(op mirrorOp) {
	m.mu.Lock()
	if n := len(m.journal); n > 0 && m.journal[n-1] == op && op.Kind == "upload" {
		m.mu.Unlock()
		return // the same file again before the last was done
	}
	m.journal = append(m.journal, op)
	m.saveJournal()
	m.mu.Unlock()
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// saveJournal writes the journal; m.mu is held.
func (m *mirror) saveJournal() {
	data, _ := json.Marshal(m.journal)
	tmp := m.journalPath() + ".partial"
	if os.WriteFile(tmp, data, 0o600) == nil {
		os.Rename(tmp, m.journalPath())
	}
}

// work does the journal's ops one at a time, in order; one that fails is tried again, ever later, so an outage
// of the provider only delays them.
func (m *mirror) work() {
	defer m.wg.Done()
	wait := time.Second
	for {
		m.mu.Lock()
		var op *mirrorOp
		if len(m.journal) > 0 {
			o := m.journal[0]
			op = &o
		}
		m.mu.Unlock()
		if op == nil {
			select {
			case <-m.ctx.Done():
				return
			case <-m.kick:
				continue
			}
		}
		if err := m.do(*op); err != nil {
			if m.ctx.Err() != nil {
				return
			}
			if msg := err.Error(); msg != m.failing {
				m.failing = msg
				m.logger.Printf("storage: %s %s: %v (trying again)", op.Kind, op.Key, err)
			}
			select {
			case <-m.ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = min(wait*2, 5*time.Minute)
			continue
		}
		if m.failing != "" {
			m.logger.Printf("storage: working again")
			m.failing = ""
		}
		wait = time.Second
		m.mu.Lock()
		if len(m.journal) > 0 && m.journal[0] == *op {
			m.journal = m.journal[1:]
		}
		m.saveJournal()
		m.mu.Unlock()
	}
}

// do does one op in the bucket.
func (m *mirror) do(op mirrorOp) error {
	switch op.Kind {
	case "upload":
		return m.upload(op.Key)
	case "move":
		objects, err := m.under(op.Key)
		if err != nil {
			return err
		}
		for _, o := range objects {
			to := op.To + strings.TrimPrefix(o.Key, op.Key)
			if err := m.blob.Copy(m.ctx, o.Key, to); err != nil && !errors.Is(err, ErrBlobNotFound) {
				return err
			}
			if err := m.blob.Delete(m.ctx, o.Key); err != nil {
				return err
			}
		}
		return m.upload(op.To) // what the bucket did not have yet, or changed since
	case "delete":
		objects, err := m.under(op.Key)
		if err != nil {
			return err
		}
		for _, o := range objects {
			if err := m.blob.Delete(m.ctx, o.Key); err != nil {
				return err
			}
		}
	}
	return nil
}

// under are the bucket's objects of a file or folder: key itself and those under key/.
func (m *mirror) under(key string) ([]BlobInfo, error) {
	var out []BlobInfo
	for o, err := range m.blob.List(m.ctx, key) {
		if err != nil {
			return nil, err
		}
		if o.Key == key || strings.HasPrefix(o.Key, key+"/") {
			out = append(out, o)
		}
	}
	return out, nil
}

// upload puts a file, or every file of a folder, in the bucket as it is on the disk now, unless the bucket has
// it already (the same size, and not older). One not there any more is nothing to do.
func (m *mirror) upload(key string) error {
	path := m.path(key)
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if !st.IsDir() {
		return m.uploadFile(key, path, st, nil)
	}
	have, err := m.listing(key + "/")
	if err != nil {
		return err
	}
	return m.walk(path, func(k, p string, st fs.FileInfo) error {
		info, ok := have[k]
		return m.uploadFile(k, p, st, &struct {
			info BlobInfo
			ok   bool
		}{info, ok})
	})
}

func (m *mirror) uploadFile(key, path string, st fs.FileInfo, known *struct {
	info BlobInfo
	ok   bool
}) error {
	var info BlobInfo
	var there bool
	if known != nil {
		info, there = known.info, known.ok
	} else {
		var err error
		info, err = m.blob.Head(m.ctx, key)
		switch {
		case err == nil:
			there = true
		case !errors.Is(err, ErrBlobNotFound):
			return err
		}
	}
	if there && uploaded(info, st) {
		return nil
	}
	if _, err := m.blob.PutFile(m.ctx, key, path); err != nil {
		if _, gone := os.Stat(path); gone != nil {
			return nil // removed meanwhile: its removal is in the journal
		}
		return err
	}
	return nil
}

// uploaded tells whether the bucket's object is the file: the same size, and stored after it was last changed
// (the provider's clock may be a second off).
func uploaded(info BlobInfo, st fs.FileInfo) bool {
	return info.Size == st.Size() && !info.Modified.Before(st.ModTime().Add(-2*time.Second))
}

// listing is the bucket's objects under prefix, by key.
func (m *mirror) listing(prefix string) (map[string]BlobInfo, error) {
	out := map[string]BlobInfo{}
	for o, err := range m.blob.List(m.ctx, prefix) {
		if err != nil {
			return nil, err
		}
		out[o.Key] = o
	}
	return out, nil
}

// walk calls fn for every file under dir that goes to the bucket.
func (m *mirror) walk(dir string, fn func(key, path string, st fs.FileInfo) error) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if m.ctx.Err() != nil {
			return m.ctx.Err()
		}
		key, ok := m.key(path)
		if path != dir && !ok {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() || !ok {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return nil
		}
		return fn(key, path, st)
	})
}

// keepDrafts puts the drafts edited in the bucket, each at most every draftEvery.
func (m *mirror) keepDrafts() {
	defer m.wg.Done()
	tick := time.NewTicker(draftEvery / 3)
	defer tick.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case now := <-tick.C:
			m.mu.Lock()
			var due []string
			for k, since := range m.drafts {
				if now.Sub(since) >= draftEvery {
					due = append(due, k)
					delete(m.drafts, k)
				}
			}
			m.mu.Unlock()
			for _, k := range due {
				m.add(mirrorOp{Kind: "upload", Key: k})
			}
		}
	}
}

// flushDrafts hands the drafts waiting to the journal, done at the next start if not before.
func (m *mirror) flushDrafts() {
	m.mu.Lock()
	keys := make([]string, 0, len(m.drafts))
	for k := range m.drafts {
		keys = append(keys, k)
	}
	m.drafts = map[string]time.Time{}
	m.mu.Unlock()
	for _, k := range keys {
		m.add(mirrorOp{Kind: "upload", Key: k})
	}
}

// keepSweeping runs sweep at start, then hourly.
func (m *mirror) keepSweeping() {
	defer m.wg.Done()
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	first := true
	for {
		if err := m.sweep(); err != nil && m.ctx.Err() == nil {
			m.logger.Printf("storage: sweep: %v", err)
		}
		if first {
			close(m.swept)
			first = false
		}
		select {
		case <-m.ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// sweep puts in the bucket every file it lacks, or has an older one of (one the journal missed: a file changed
// by hand, a crash), and reports the objects only the bucket has. It never deletes.
func (m *mirror) sweep() error {
	have, err := m.listing("")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	uploads := 0
	err = m.walk(m.root, func(key, path string, st fs.FileInfo) error {
		seen[key] = true
		info, ok := have[key]
		if ok && uploaded(info, st) {
			return nil
		}
		uploads++
		return m.uploadFile(key, path, st, &struct {
			info BlobInfo
			ok   bool
		}{info, ok})
	})
	if err != nil {
		return err
	}
	only := 0
	for k := range have {
		if !seen[k] && k != leaseKey && !strings.HasPrefix(k, "_check-") {
			only++
		}
	}
	if uploads > 0 || only > 0 {
		m.logger.Printf("storage: sweep: %d files put in the bucket; %d objects are only in the bucket (kept)", uploads, only)
	}
	return nil
}

// settle waits until the journal is done and the first sweep is over, or ctx ends: for tests and the admin's
// check that the bucket has everything.
func (m *mirror) settle(ctx context.Context) error {
	select {
	case <-m.swept:
	case <-ctx.Done():
		return ctx.Err()
	}
	m.flushDrafts()
	for {
		m.mu.Lock()
		n := len(m.journal)
		m.mu.Unlock()
		if n == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
