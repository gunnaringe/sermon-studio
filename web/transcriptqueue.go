package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gunnaringe/sermon-studio/models"
	"github.com/gunnaringe/sermon-studio/normalizer"
)

// The transcript queue is the server's, shared by its churches: one transcript is made at a time, in the order
// they were asked for. It is kept in <queue dir>/queue.json with each job's parts in <queue dir>/<job id>/
// (normalizer.Whisper), so a restart (an update, a reboot) loses nothing: the jobs wait again in their order, and
// the one being made goes on from its last part done. A recording renamed or moved takes its job along; one
// deleted loses it. Nothing about the queue is in the recordings' folders: a transcript, once made, is
// (transcriptFormat in recording.json, and its files).

// queueFile is the queue in the queue dir.
const queueFile = "queue.json"

// transcriptJob is a transcript asked for: whose (the church's id, "" for --root), of which recording (its
// folder, a slash path under the church's root), in which language, with which model (its key; "" = the first
// for the language), with the church's words then, and when it was asked for (its place in the queue).
type transcriptJob struct {
	ID       string    `json:"id"`
	Church   string    `json:"church"`
	Folder   string    `json:"folder"`
	Language string    `json:"language"`
	Model    string    `json:"model,omitempty"`
	Words    string    `json:"words,omitempty"`
	At       time.Time `json:"at"`
	Attempts int       `json:"attempts,omitempty"` // failed attempts so far (one is tried again)

	// While it is being made (guarded by transcriber.mu):
	progress float64   // 0..1
	from     float64   // how far it was when it began this time (parts made before a restart); -1 = not yet known
	since    time.Time // when it began this time
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{} // closed when this turn ends
	server   *Server
	stopped  bool // ✕, or its recording was deleted: it is over
	moved    bool // its recording was renamed or moved: it goes on (from its parts) at its new place
	held     bool // its recording is being renamed or moved: it waits
}

// transcriber is what the churches share: the models, whisper-cli, and the queue with the one worker.
type transcriber struct {
	dir      string // the models
	whisper  normalizer.Whisper
	queueDir string
	logger   func(format string, args ...any)

	mu       sync.Mutex
	queue    []*transcriptJob // by At; the first may be running
	running  *transcriptJob
	churches map[string]*Server
	wake     chan struct{}

	ctx  context.Context
	stop context.CancelFunc
	done chan struct{}
}

func newTranscriber(cfg TranscriptionConfig) (*transcriber, error) {
	if cfg.ModelsDir == "" {
		return nil, errors.New("transcripts need a models folder")
	}
	if cfg.QueueDir == "" {
		return nil, errors.New("transcripts need a folder for their queue")
	}
	if err := os.MkdirAll(cfg.QueueDir, 0o755); err != nil {
		return nil, err
	}
	ctx, stop := context.WithCancel(context.Background())
	tr := &transcriber{dir: cfg.ModelsDir, whisper: cfg.Whisper, queueDir: cfg.QueueDir, logger: func(string, ...any) {},
		churches: map[string]*Server{}, wake: make(chan struct{}, 1), ctx: ctx, stop: stop, done: make(chan struct{})}
	if data, err := os.ReadFile(filepath.Join(cfg.QueueDir, queueFile)); err == nil {
		if err := json.Unmarshal(data, &tr.queue); err != nil {
			return nil, err
		}
	}
	tr.sweep()
	go tr.work()
	return tr, nil
}

// Close stops the worker; the job being made stays in the queue, with its parts.
func (tr *transcriber) Close() {
	tr.stop()
	<-tr.done
}

// installed are the models there now (none when models.json cannot be read).
func (tr *transcriber) installed() []models.Model {
	m, err := models.Read(tr.dir)
	if err != nil {
		return nil
	}
	return m.Installed(tr.dir)
}

// sweep removes the parts folders of jobs that are not in the queue.
func (tr *transcriber) sweep() {
	des, _ := os.ReadDir(tr.queueDir)
	for _, de := range des {
		if de.IsDir() && !slices.ContainsFunc(tr.queue, func(j *transcriptJob) bool { return j.ID == de.Name() }) {
			normalizer.RemoveAll(filepath.Join(tr.queueDir, de.Name()))
		}
	}
}

// save writes the queue (under mu).
func (tr *transcriber) save() {
	data, _ := json.MarshalIndent(tr.queue, "", "  ")
	if err := writeFileAtomic(filepath.Join(tr.queueDir, queueFile), append(data, '\n')); err != nil {
		tr.logger("transcripts: the queue was not saved: %v", err)
	}
}

func (tr *transcriber) parts(j *transcriptJob) string { return filepath.Join(tr.queueDir, j.ID) }

// register makes a church's jobs its own to run; its jobs wait while it is not there.
func (tr *transcriber) register(church string, s *Server) {
	tr.mu.Lock()
	tr.churches[church] = s
	tr.logger = s.cfg.Logger.Printf // the studio's, the same for every church
	tr.mu.Unlock()
	tr.poke()
}

// unregister: the church stops (its Server's context is done, which stops a job of its being made, keeping
// it); it waits for that job's turn to end.
func (tr *transcriber) unregister(church string, s *Server) {
	tr.mu.Lock()
	if tr.churches[church] == s {
		delete(tr.churches, church)
	}
	var done chan struct{}
	if j := tr.running; j != nil && j.server == s {
		done = j.done
	}
	tr.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (tr *transcriber) poke() {
	select {
	case tr.wake <- struct{}{}:
	default:
	}
}

// add queues a transcript. One the recording has waiting is asked for again: it keeps its place, in the
// language, with the model and words asked for now. One being made stays as it is: add tells whether it is
// the transcript asked for (false: another language or model).
func (tr *transcriber) add(j transcriptJob) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if q := tr.find(j.Church, j.Folder); q != nil {
		if q == tr.running {
			return q.Language == j.Language && q.Model == j.Model
		}
		q.Language, q.Model, q.Words, q.Attempts = j.Language, j.Model, j.Words, 0
		tr.save()
		return true
	}
	if j.ID == "" {
		b := make([]byte, 8)
		rand.Read(b)
		j.ID = hex.EncodeToString(b)
	}
	i, _ := slices.BinarySearchFunc(tr.queue, j.At, func(q *transcriptJob, at time.Time) int {
		if q.At.After(at) {
			return 1
		}
		return -1
	})
	tr.queue = slices.Insert(tr.queue, i, &j)
	tr.save()
	tr.poke()
	return true
}

// find is the recording's job (under mu).
func (tr *transcriber) find(church, folder string) *transcriptJob {
	for _, j := range tr.queue {
		if j.Church == church && j.Folder == folder {
			return j
		}
	}
	return nil
}

// drop takes a job out of the queue and removes its parts (under mu).
func (tr *transcriber) drop(j *transcriptJob) {
	if i := slices.Index(tr.queue, j); i >= 0 {
		tr.queue = slices.Delete(tr.queue, i, i+1)
		tr.save()
	}
	normalizer.RemoveAll(tr.parts(j))
}

// remove ends the recording's job (✕, or the recording deleted): one waiting leaves the queue, the one being made
// stops. It tells whether there was one.
func (tr *transcriber) remove(church, folder string) bool {
	if tr == nil {
		return false
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	j := tr.find(church, folder)
	if j == nil {
		return false
	}
	if j == tr.running {
		j.stopped = true
		j.cancel()
		return true
	}
	tr.drop(j)
	return true
}

// moving follows a recording's folder (or one above it) that is being renamed or moved, from one slash path to
// another: its job waits (the one being made stops) until done is called, with whether it was moved; then
// it goes on, from its parts, at the new place (or the old one).
func (tr *transcriber) moving(church, from, to string) (done func(moved bool)) {
	if tr == nil {
		return func(bool) {}
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	var held []*transcriptJob
	for _, j := range tr.queue {
		if j.Church != church {
			continue
		}
		if rest, ok := strings.CutPrefix(j.Folder, from); ok && (rest == "" || rest[0] == '/') {
			j.held = true
			held = append(held, j)
			if j == tr.running {
				j.moved = true
				j.cancel()
			}
		}
	}
	return func(moved bool) {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		for _, j := range held {
			j.held = false
			if moved {
				j.Folder = to + strings.TrimPrefix(j.Folder, from)
			}
		}
		if moved && len(held) > 0 {
			tr.save()
		}
		tr.poke()
	}
}

// transcriptStatus is a recording's job as its page shows it.
type transcriptStatus struct {
	Language  string
	State     string  // "waiting" or "running"
	Ahead     int     // waiting: how many are made before it
	Progress  float64 // running: 0..1
	Remaining float64 // running: about how many seconds are left; 0 = not known yet
}

// status is the recording's job, if it has one.
func (tr *transcriber) status(church, folder string) (transcriptStatus, bool) {
	if tr == nil {
		return transcriptStatus{}, false
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for i, j := range tr.queue {
		if j.Church != church || j.Folder != folder {
			continue
		}
		st := transcriptStatus{Language: j.Language, State: "waiting", Ahead: i}
		if j == tr.running {
			st.State, st.Ahead, st.Progress, st.Remaining = "running", 0, j.progress, remainingFrom(j.since, j.from, j.progress)
		}
		return st, true
	}
	return transcriptStatus{}, false
}

// work makes the transcripts, one at a time, first asked first made, until Close.
func (tr *transcriber) work() {
	defer close(tr.done)
	for {
		j, s := tr.next()
		if j == nil {
			select {
			case <-tr.ctx.Done():
				return
			case <-tr.wake:
				continue
			}
		}
		err := tr.make(j, s)
		tr.finish(j, s, err)
	}
}

// next is the first job whose church is there, now running; nil when there is none.
func (tr *transcriber) next() (*transcriptJob, *Server) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.ctx.Err() != nil {
		return nil, nil
	}
	for _, j := range tr.queue {
		if s := tr.churches[j.Church]; s != nil && s.ctx.Err() == nil {
			if j.held { // being renamed: it goes first when it is done (moving pokes)
				return nil, nil
			}
			ctx, cancel := context.WithCancel(tr.ctx)
			stopWith := context.AfterFunc(s.ctx, cancel) // the church stops
			j.cancel = func() { stopWith(); cancel() }
			j.progress, j.from, j.since, j.stopped, j.moved = 0, -1, time.Now(), false, false
			j.server, j.done = s, make(chan struct{})
			tr.running, j.ctx = j, ctx
			return j, s
		}
	}
	return nil, nil
}

// make runs the job in its church; a source that changed while it was being made starts it again.
func (tr *transcriber) make(j *transcriptJob, s *Server) error {
	ctx := j.ctx
	report := func(done float64) {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		if j.from < 0 { // the first report: where it begins (parts made before a restart)
			j.from, j.since = done, time.Now()
		}
		j.progress = done
	}
	for {
		err := s.makeTranscript(ctx, j, tr.parts(j), report)
		if !errors.Is(err, errSourceChanged) || ctx.Err() != nil {
			return err
		}
		s.cfg.Logger.Printf("transcript of %s: the sermon was made again; starting again", j.Folder)
		tr.mu.Lock()
		j.progress, j.from = 0, -1
		tr.mu.Unlock()
	}
}

// finish ends a job's turn: made, or stopped (✕, deleted), it leaves the queue with its parts; stopped by the
// studio stopping, or moved, it stays, with them; failed, it is tried once more, then leaves and the page says
// why.
func (tr *transcriber) finish(j *transcriptJob, s *Server, err error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	j.cancel()
	tr.running = nil
	defer close(j.done)
	folder := filepath.Join(s.root, filepath.FromSlash(j.Folder))
	switch {
	case j.stopped:
		tr.drop(j)
	case j.moved:
		// at its new place, from its parts
	case err == nil:
		tr.drop(j)
	case tr.ctx.Err() != nil || s.ctx.Err() != nil:
		// the studio or the church stops: it goes on when it starts again
	case j.Attempts == 0 && retryable(err):
		j.Attempts++
		tr.save()
		s.cfg.Logger.Printf("transcript of %s: %v; trying once more", s.display(folder), err)
	default:
		tr.drop(j)
		s.setTranscriptFailed(folder, j.Language, err)
		s.cfg.Logger.Printf("transcript of %s: %v", s.display(folder), err)
	}
	if s.ctx.Err() == nil {
		s.storageChanged() // the church's storage is measured again
	}
}

// retryable tells whether a failure may go away when tried again (not: no files to make it from, no model).
func retryable(err error) bool {
	return !errors.Is(err, errNoSource) && !errors.Is(err, errNoModel) && !errors.Is(err, errNotForIt) &&
		!errors.Is(err, errModelGone) && !errors.Is(err, os.ErrNotExist)
}
