package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// fakeBlob is a bucket in memory, with conditional puts, for the mirror's tests (the real one: package blob).
type fakeBlob struct {
	mu       sync.Mutex
	objects  map[string]fakeObject
	n        int
	failPuts bool // every put fails, as in an outage
	noCond   bool // conditional puts are not checked, as by a provider without them
	// hold, when set, keeps a PutFile waiting after it read its file (one in flight); started is told then.
	hold, started chan struct{}
	// holdPut, when set, keeps a Put of that key waiting the same way.
	holdPut string
}

type fakeObject struct {
	data     []byte
	etag     string
	modified time.Time
}

func newFakeBlob() *fakeBlob { return &fakeBlob{objects: map[string]fakeObject{}} }

func (b *fakeBlob) info(key string, o fakeObject) BlobInfo {
	return BlobInfo{Key: key, Size: int64(len(o.data)), ETag: o.etag, Modified: o.modified}
}

func (b *fakeBlob) store(key string, data []byte, cond func(o fakeObject, ok bool) bool) (BlobInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failPuts {
		return BlobInfo{}, errors.New("the provider is down")
	}
	if cond != nil && !b.noCond {
		o, ok := b.objects[key]
		if !cond(o, ok) {
			return BlobInfo{}, fmt.Errorf("%w: %s", ErrBlobPrecondition, key)
		}
	}
	b.n++
	o := fakeObject{data: slices.Clone(data), etag: fmt.Sprint(b.n), modified: time.Now()}
	b.objects[key] = o
	return b.info(key, o), nil
}

func (b *fakeBlob) Put(_ context.Context, key string, r io.Reader, _ int64) (BlobInfo, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return BlobInfo{}, err
	}
	b.mu.Lock()
	hold, started := b.hold, b.started
	held := b.holdPut != "" && b.holdPut == key
	b.mu.Unlock()
	if held {
		started <- struct{}{}
		<-hold
	}
	return b.store(key, data, nil)
}

func (b *fakeBlob) PutFile(ctx context.Context, key, path string) (BlobInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BlobInfo{}, err
	}
	b.mu.Lock()
	hold, started := b.hold, b.started
	b.mu.Unlock()
	if hold != nil {
		started <- struct{}{}
		<-hold
	}
	return b.store(key, data, nil)
}

func (b *fakeBlob) object(key string) (fakeObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	o, ok := b.objects[key]
	if !ok {
		return o, fmt.Errorf("%w: %s", ErrBlobNotFound, key)
	}
	return o, nil
}

func (b *fakeBlob) Get(_ context.Context, key string) (io.ReadCloser, BlobInfo, error) {
	o, err := b.object(key)
	if err != nil {
		return nil, BlobInfo{}, err
	}
	return io.NopCloser(bytes.NewReader(o.data)), b.info(key, o), nil
}

func (b *fakeBlob) GetFile(_ context.Context, key, path string) (BlobInfo, error) {
	o, err := b.object(key)
	if err != nil {
		return BlobInfo{}, err
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	return b.info(key, o), os.WriteFile(path, o.data, 0o644)
}

func (b *fakeBlob) Head(_ context.Context, key string) (BlobInfo, error) {
	o, err := b.object(key)
	if err != nil {
		return BlobInfo{}, err
	}
	return b.info(key, o), nil
}

func (b *fakeBlob) Copy(_ context.Context, from, to string) error {
	o, err := b.object(from)
	if err != nil {
		return err
	}
	_, err = b.store(to, o.data, nil)
	return err
}

func (b *fakeBlob) Delete(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, key)
	return nil
}

func (b *fakeBlob) List(_ context.Context, prefix string) iter.Seq2[BlobInfo, error] {
	return func(yield func(BlobInfo, error) bool) {
		b.mu.Lock()
		var out []BlobInfo
		for k, o := range b.objects {
			if strings.HasPrefix(k, prefix) {
				out = append(out, b.info(k, o))
			}
		}
		b.mu.Unlock()
		slices.SortFunc(out, func(x, y BlobInfo) int { return strings.Compare(x.Key, y.Key) })
		for _, o := range out {
			if !yield(o, nil) {
				return
			}
		}
	}
}

func (b *fakeBlob) Presign(_ context.Context, key string, _ time.Duration, name string, inline bool) (*url.URL, error) {
	return url.Parse("https://bucket.example/" + key + "?name=" + url.QueryEscape(name))
}

func (b *fakeBlob) PutNew(_ context.Context, key string, data []byte) (BlobInfo, error) {
	return b.store(key, data, func(_ fakeObject, ok bool) bool { return !ok })
}

func (b *fakeBlob) PutIfMatch(_ context.Context, key string, data []byte, etag string) (BlobInfo, error) {
	return b.store(key, data, func(o fakeObject, ok bool) bool { return ok && o.etag == etag })
}

// text is an object's bytes, or "" when it is not there.
func (b *fakeBlob) text(key string) string {
	o, err := b.object(key)
	if err != nil {
		return ""
	}
	return string(o.data)
}

func (b *fakeBlob) keys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for k := range b.objects {
		if k != leaseKey {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// mirrorTest is the studio with a data dir and a bucket.
func mirrorTest(t *testing.T, b *fakeBlob) *shareTest {
	t.Helper()
	return newShareTestWith(t, RateLimits{}, func(c *AppConfig) { c.Blob = b })
}

func settleMirror(t *testing.T, a *App) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.mirror.settle(ctx); err != nil {
		t.Fatalf("the mirror did not settle: %v", err)
	}
}

// The bucket follows the data dir: what is there at start, a state file before its request is answered, a
// file made, a rename (a folder too), a removal, the trash; not the sessions, the inbox, hidden and
// half-written files.
func TestMirror(t *testing.T) {
	b := newFakeBlob()
	b.store("kirke/old-only.txt", []byte("only in the bucket"), nil) // never deleted by a sweep
	st := mirrorTest(t, b)
	data := filepath.Dir(st.church)
	os.WriteFile(filepath.Join(data, "sessions.json"), []byte("{}"), 0o600)
	settleMirror(t, st.app)
	if got := b.text("kirke/old-only.txt"); got != "only in the bucket" {
		t.Errorf("the sweep deleted an object only the bucket has: %q", got)
	}

	// A state file: in the bucket when its request is answered.
	st.makeShare(`{"label":"Sound desk","allTypes":true}`)
	if !strings.Contains(b.text("kirke/shares.json"), "Sound desk") {
		t.Errorf("shares.json in the bucket: %q", b.text("kirke/shares.json"))
	}

	// A file made (from its .partial), renamed, then the folder renamed, put in the trash, removed.
	rec := filepath.Join(st.church, "Taler", "2026", "2026-10-18")
	os.MkdirAll(filepath.Join(rec, "original"), 0o755)
	os.WriteFile(filepath.Join(rec, ".2026-10-18.mp3.partial"), []byte("ID3 the mp3"), 0o644)
	if err := normalizer.Rename(filepath.Join(rec, ".2026-10-18.mp3.partial"), filepath.Join(rec, "2026-10-18.mp3")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(rec, "recording.json"), []byte(`{"saved":null}`)); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(rec, "original", ".x.upload"), []byte("half an upload"), 0o644)
	settleMirror(t, st.app)
	if got := b.text("kirke/Taler/2026/2026-10-18/2026-10-18.mp3"); got != "ID3 the mp3" {
		t.Errorf("the file made: %q (%v)", got, b.keys())
	}
	moved := filepath.Join(st.church, "Taler", "2026", "2026-10-18 Advent")
	if err := normalizer.Rename(rec, moved); err != nil {
		t.Fatal(err)
	}
	settleMirror(t, st.app)
	if b.text("kirke/Taler/2026/2026-10-18/2026-10-18.mp3") != "" || b.text("kirke/Taler/2026/2026-10-18 Advent/2026-10-18.mp3") != "ID3 the mp3" ||
		b.text("kirke/Taler/2026/2026-10-18 Advent/recording.json") == "" {
		t.Errorf("after the folder's rename: %v", b.keys())
	}
	trash := filepath.Join(st.church, "Taler", ".trash", "abc")
	os.MkdirAll(trash, 0o755)
	if err := normalizer.Rename(moved, filepath.Join(trash, "2026-10-18 Advent")); err != nil {
		t.Fatal(err)
	}
	settleMirror(t, st.app)
	if b.text("kirke/Taler/.trash/abc/2026-10-18 Advent/2026-10-18.mp3") != "ID3 the mp3" {
		t.Errorf("in the trash: %v", b.keys())
	}
	if err := normalizer.RemoveAll(trash); err != nil {
		t.Fatal(err)
	}
	settleMirror(t, st.app)
	for _, k := range b.keys() {
		switch {
		case strings.Contains(k, "2026-10-18"):
			t.Errorf("left after the removal: %s", k)
		case strings.Contains(k, "sessions") || strings.Contains(k, ".upload") || strings.Contains(k, ".partial") ||
			strings.Contains(k, "/Innboks/") || strings.HasPrefix(k, ".blob"):
			t.Errorf("in the bucket, but only for this machine: %s", k)
		}
	}
}

// A state file the bucket does not take: the request fails, and the file is as it was.
func TestMirrorWriteFails(t *testing.T) {
	b := newFakeBlob()
	st := mirrorTest(t, b)
	settleMirror(t, st.app)
	st.makeShare(`{"label":"First","allTypes":true}`)
	before, _ := os.ReadFile(filepath.Join(st.church, "shares.json"))
	b.mu.Lock()
	b.failPuts = true
	b.mu.Unlock()
	if w := do(st.studio, "POST", "/api/shares", `{"label":"Second","allTypes":true}`); w.Code < 500 {
		t.Errorf("made while the bucket is down: %d %s", w.Code, w.Body)
	}
	if after, _ := os.ReadFile(filepath.Join(st.church, "shares.json")); !bytes.Equal(after, before) {
		t.Errorf("shares.json changed: %s", after)
	}
}

// draft.json goes later, not with every keystroke.
func TestMirrorDraftLater(t *testing.T) {
	b := newFakeBlob()
	st := mirrorTest(t, b)
	settleMirror(t, st.app)
	rec := filepath.Join(st.church, "Taler", "2026-10-18")
	os.MkdirAll(rec, 0o755)
	if err := writeFileAtomic(filepath.Join(rec, draftName), []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := b.text("kirke/Taler/2026-10-18/draft.json"); got != "" {
		t.Errorf("the draft went at once: %q", got)
	}
	settleMirror(t, st.app) // hands over the drafts waiting
	if got := b.text("kirke/Taler/2026-10-18/draft.json"); got != `{"v":1}` {
		t.Errorf("the draft later: %q", got)
	}
}

// The journal is kept on the disk: what was left when the studio stopped (the bucket down) is done at the
// next start.
func TestMirrorJournalAfterRestart(t *testing.T) {
	b := newFakeBlob()
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "kirke", "Taler"), 0o755)
	open := func() *App {
		a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: newTestLogger(t)}, DataDir: data, Blob: b})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := open()
	settleMirror(t, a)
	b.mu.Lock()
	b.failPuts = true
	b.mu.Unlock()
	file := filepath.Join(data, "kirke", "Taler", "2026-10-18", "2026-10-18.mp3")
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.WriteFile(file+".partial", []byte("mp3"), 0o644)
	normalizer.Rename(file+".partial", file)
	time.Sleep(50 * time.Millisecond)
	a.Close()
	b.mu.Lock()
	b.failPuts = false
	b.mu.Unlock()
	a = open()
	defer a.Close()
	settleMirror(t, a)
	if got := b.text("kirke/Taler/2026-10-18/2026-10-18.mp3"); got != "mp3" {
		t.Errorf("after the restart: %q", got)
	}
}

// The lease: a second studio on the bucket waits for it and gives up; the same data dir takes its own at once;
// a clean stop gives it back; one whose lease was taken is told.
func TestMirrorLease(t *testing.T) {
	defer func(f, r, w time.Duration) { leaseFor, leaseRenew, leaseWait = f, r, w }(leaseFor, leaseRenew, leaseWait)
	leaseFor, leaseRenew, leaseWait = 2*time.Second, 100*time.Millisecond, 300*time.Millisecond
	b := newFakeBlob()
	dir := func() string {
		d := t.TempDir()
		os.MkdirAll(filepath.Join(d, "kirke", "Taler"), 0o755)
		return d
	}
	first, second := dir(), dir()
	lost := make(chan error, 1)
	open := func(data string) (*App, error) {
		return NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: newTestLogger(t)}, DataDir: data, Blob: b,
			StorageLost: func(err error) { lost <- err }})
	}
	a, err := open(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := open(second); err == nil || !strings.Contains(err.Error(), "another studio") {
		t.Errorf("a second studio: %v", err)
	}
	a.Close()
	if b.text(leaseKey) != "" {
		t.Error("the lease was not given back")
	}
	if a, err = open(second); err != nil {
		t.Fatalf("after the first stopped: %v", err)
	}
	// Taken from it: a lease of another studio's, written over this one's.
	b.store(leaseKey, []byte(`{"holder":"someone else","host":"other","until":"2099-01-01T00:00:00Z"}`), nil)
	select {
	case err := <-lost:
		if !strings.Contains(err.Error(), "other") {
			t.Errorf("lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("not told the lease was taken")
	}
	a.Close()
	b.Delete(context.Background(), leaseKey)

	// A crash (no clean stop) leaves the lease live: the same data dir takes it back at once.
	a, err = open(first)
	if err != nil {
		t.Fatal(err)
	}
	a.mirror.lease.stop() // as if it died: no renewing, nothing given back
	<-a.mirror.lease.done
	a.mirror.lease = nil
	a.Close()
	if b.text(leaseKey) == "" {
		t.Fatal("the lease should still be there")
	}
	start := time.Now()
	a, err = open(first)
	if err != nil || time.Since(start) > time.Second {
		t.Errorf("the same data dir again: %v after %v", err, time.Since(start))
	}
	if a != nil {
		a.Close()
	}
}

// Without a bucket nothing is mirrored: no .blob folder.
func TestNoMirror(t *testing.T) {
	st := newShareTest(t, RateLimits{})
	st.makeShare(`{"label":"Sound desk","allTypes":true}`)
	if _, err := os.Stat(filepath.Join(filepath.Dir(st.church), mirrorDir)); err == nil {
		t.Error("a .blob folder without a bucket")
	}
	if st.app.mirror != nil {
		t.Error("a mirror without a bucket")
	}
}

func newTestLogger(t *testing.T) *log.Logger { return log.New(testLog{t}, "", 0) }

// The data dir by another name (a symlink here; on Windows a temporary folder's short name): the app names its
// files both ways, and both reach the bucket.
func TestMirrorDataDirAlias(t *testing.T) {
	b := newFakeBlob()
	real := t.TempDir()
	os.MkdirAll(filepath.Join(real, "kirke", "Taler"), 0o755)
	data := filepath.Join(t.TempDir(), "data")
	if err := os.Symlink(real, data); err != nil {
		t.Skip(err)
	}
	a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: newTestLogger(t)}, DataDir: data, Blob: b,
		Share: &ShareOptions{URL: "https://up.example/"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if w := do(a.Handler(), "POST", "/api/shares", `{"label":"Sound desk","allTypes":true}`); w.Code != http.StatusCreated {
		t.Fatalf("share: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(b.text("kirke/shares.json"), "Sound desk") {
		t.Errorf("shares.json not in the bucket: %v", b.keys())
	}
	file := filepath.Join(real, "kirke", "Taler", "2026-10-18", "2026-10-18.mp3")
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.WriteFile(file+".partial", []byte("mp3"), 0o644)
	normalizer.Rename(file+".partial", file)
	settleMirror(t, a)
	if b.text("kirke/Taler/2026-10-18/2026-10-18.mp3") != "mp3" {
		t.Errorf("the file made by its real path: %v", b.keys())
	}
}

// A file replaced while its upload is going on is put again: the upload in flight may have the old one.
func TestMirrorReplacedWhileUploading(t *testing.T) {
	b := newFakeBlob()
	st := mirrorTest(t, b)
	settleMirror(t, st.app)
	file := filepath.Join(st.church, "Taler", "2026-10-18", "2026-10-18.mp3")
	os.MkdirAll(filepath.Dir(file), 0o755)
	write := func(data string) {
		os.WriteFile(file+".partial", []byte(data), 0o644)
		normalizer.Rename(file+".partial", file)
	}
	hold, started := make(chan struct{}), make(chan struct{}, 1)
	b.mu.Lock()
	b.hold, b.started = hold, started
	b.mu.Unlock()
	write("first")
	<-started // read "first", not yet in the bucket
	write("second")
	b.mu.Lock()
	b.hold = nil
	b.mu.Unlock()
	close(hold)
	settleMirror(t, st.app)
	if got := b.text("kirke/Taler/2026-10-18/2026-10-18.mp3"); got != "second" {
		t.Errorf("the bucket has %q", got)
	}
}

// A file made again at once with the same size is put again: the journal knows it changed, whatever the
// bucket's copy looks like.
func TestMirrorRemadeSameSize(t *testing.T) {
	b := newFakeBlob()
	st := mirrorTest(t, b)
	file := filepath.Join(st.church, "Taler", "2026-10-18", "2026-10-18.mp3")
	os.MkdirAll(filepath.Dir(file), 0o755)
	for _, data := range []string{"first", "fiRst"} {
		os.WriteFile(file+".partial", []byte(data), 0o644)
		normalizer.Rename(file+".partial", file)
		settleMirror(t, st.app)
	}
	if got := b.text("kirke/Taler/2026-10-18/2026-10-18.mp3"); got != "fiRst" {
		t.Errorf("the bucket has %q", got)
	}
}

// A church's church.json waiting for a slow bucket holds up only that church: another saves its settings, and
// the app answers meanwhile.
func TestChurchWriteHoldsOnlyItsChurch(t *testing.T) {
	b := newFakeBlob()
	data := t.TempDir()
	for _, id := range []string{"annen", "kirke"} {
		os.MkdirAll(filepath.Join(data, id, "Taler"), 0o755)
	}
	a, err := NewApp(AppConfig{Base: Config{CacheDir: t.TempDir(), Logger: newTestLogger(t)}, DataDir: data, Blob: b})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	tenant := func(id string) *tenant {
		for _, t := range a.tenants {
			if t.id == id {
				return t
			}
		}
		t.Fatalf("no church %s", id)
		return nil
	}
	slow, other := tenant("kirke"), tenant("annen")
	b.mu.Lock()
	b.holdPut, b.hold, b.started = "kirke/church.json", make(chan struct{}), make(chan struct{}, 1)
	b.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- a.update(slow, func(c *churchConfig) { c.Name = "Kirka" }) }()
	<-b.started

	saved := make(chan error, 1)
	go func() {
		a.mu.Lock() // what every request to the app takes
		a.mu.Unlock()
		saved <- a.update(other, func(c *churchConfig) { c.Name = "Den andre" })
	}()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("another church waited for the slow bucket")
	}
	close(b.hold)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if slow.cfg.Name != "Kirka" || other.cfg.Name != "Den andre" {
		t.Errorf("names: %q, %q", slow.cfg.Name, other.cfg.Name)
	}
}
