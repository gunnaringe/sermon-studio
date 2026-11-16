package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// One studio writes to a bucket at a time (#86): it holds the lease, the object _lease (who, until when),
// renewed every leaseRenew. A studio is known by an id kept in its data dir (.blob/id), so the same data dir
// takes its own lease back at once (a restart, an update); another waits for a live lease to run out, and a
// studio that finds its lease taken stops (lost) rather than write alongside another. A clean stop gives the
// lease back, so podman auto-update's new container starts without waiting. A conditional put (If-Match,
// If-None-Match) makes taking it safe; without one (a provider that ignores them) it is read back to check.

// Variables, for the tests.
var (
	leaseFor   = 3 * time.Minute
	leaseRenew = time.Minute
	// leaseWait is how long a start waits for another studio's lease to run out before it gives up.
	leaseWait = leaseFor + 30*time.Second
)

type leaseBody struct {
	Holder string    `json:"holder"`
	Host   string    `json:"host"`
	Until  time.Time `json:"until"`
}

type lease struct {
	blob   Blob
	id     string
	logger *log.Logger
	stop   context.CancelFunc
	done   chan struct{}

	mu   sync.Mutex
	etag string
}

// studioID is the data dir's id for the lease, made the first time.
func studioID(root string) (string, error) {
	path := filepath.Join(root, mirrorDir, "id")
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) >= 16 {
		return strings.TrimSpace(string(b)), nil
	}
	b := make([]byte, 12)
	rand.Read(b)
	id := hex.EncodeToString(b)
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// takeLease takes the bucket's lease (waiting up to leaseWait for another studio's to run out) and keeps it,
// calling lost if it is taken from it.
func takeLease(ctx context.Context, b Blob, root string, logger *log.Logger, lost func(error)) (*lease, error) {
	id, err := studioID(root)
	if err != nil {
		return nil, err
	}
	l := &lease{blob: b, id: id, logger: logger, done: make(chan struct{})}
	deadline := time.Now().Add(leaseWait)
	told := false
	for {
		cur, etag, err := l.read(ctx)
		if err != nil && !errors.Is(err, ErrBlobNotFound) {
			return nil, fmt.Errorf("storage: the lease: %w", err)
		}
		if err == nil && cur.Holder != id && time.Now().Before(cur.Until) {
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("storage: another studio (%s) is using the bucket until %s", cur.Host, cur.Until.Format(time.DateTime))
			}
			if !told {
				logger.Printf("storage: another studio (%s) holds the bucket until %s; waiting", cur.Host, cur.Until.Format(time.DateTime))
				told = true
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(min(time.Until(cur.Until)+time.Second, 15*time.Second)):
			}
			continue
		}
		if errors.Is(err, ErrBlobNotFound) {
			etag = ""
		}
		switch err := l.put(ctx, etag); {
		case err == nil:
		case errors.Is(err, ErrBlobPrecondition):
			continue // another took or renewed it meanwhile: look again
		default:
			return nil, fmt.Errorf("storage: the lease: %w", err)
		}
		if mine, err := l.mine(ctx); err != nil {
			return nil, fmt.Errorf("storage: the lease: %w", err)
		} else if !mine {
			continue
		}
		break
	}
	kctx, stop := context.WithCancel(context.Background())
	l.stop = stop
	go l.keep(kctx, lost)
	return l, nil
}

// read is the lease as the bucket has it, and its etag.
func (l *lease) read(ctx context.Context) (leaseBody, string, error) {
	r, info, err := l.blob.Get(ctx, leaseKey)
	if err != nil {
		return leaseBody{}, "", err
	}
	defer r.Close()
	var cur leaseBody
	data, err := io.ReadAll(io.LimitReader(r, 4096))
	if err == nil {
		err = json.Unmarshal(data, &cur)
	}
	if err != nil {
		return leaseBody{}, info.ETag, nil // unreadable: as good as none
	}
	return cur, info.ETag, nil
}

// put writes the lease as this studio's until leaseFor from now: over the version etag, or as a new object
// when etag is "".
func (l *lease) put(ctx context.Context, etag string) error {
	host, _ := os.Hostname()
	data, _ := json.Marshal(leaseBody{Holder: l.id, Host: host, Until: time.Now().Add(leaseFor).UTC()})
	var info BlobInfo
	var err error
	if etag == "" {
		info, err = l.blob.PutNew(ctx, leaseKey, data)
	} else {
		info, err = l.blob.PutIfMatch(ctx, leaseKey, data, etag)
	}
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.etag = info.ETag
	l.mu.Unlock()
	return nil
}

// mine reads the lease back: whether it is this studio's (for a provider that ignores the conditions).
func (l *lease) mine(ctx context.Context) (bool, error) {
	cur, etag, err := l.read(ctx)
	if err != nil {
		return false, err
	}
	if cur.Holder == l.id {
		l.mu.Lock()
		l.etag = etag
		l.mu.Unlock()
	}
	return cur.Holder == l.id, nil
}

// keep renews the lease until stopped; if another studio has it, or it ran out before it could be renewed,
// lost is told and keeping stops.
func (l *lease) keep(ctx context.Context, lost func(error)) {
	defer close(l.done)
	until := time.Now().Add(leaseFor)
	tick := time.NewTicker(leaseRenew)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		l.mu.Lock()
		etag := l.etag
		l.mu.Unlock()
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := l.put(rctx, etag)
		if err == nil {
			var mine bool
			if mine, err = l.mine(rctx); err == nil && !mine {
				err = ErrBlobPrecondition
			}
		}
		cancel()
		switch {
		case err == nil:
			until = time.Now().Add(leaseFor)
		case ctx.Err() != nil:
			return
		case errors.Is(err, ErrBlobPrecondition):
			cur, _, _ := l.read(ctx)
			if cur.Holder != l.id {
				lost(fmt.Errorf("storage: another studio (%s) took the bucket", cur.Host))
				return
			}
			if mine, _ := l.mine(ctx); !mine {
				lost(errors.New("storage: the bucket's lease is not this studio's any more"))
				return
			}
		case time.Now().After(until):
			lost(fmt.Errorf("storage: the bucket's lease ran out, it could not be renewed: %w", err))
			return
		default:
			l.logger.Printf("storage: renewing the lease: %v (trying again)", err)
		}
	}
}

// release stops renewing and gives the lease back, if it is still this studio's.
func (l *lease) release() {
	if l == nil {
		return
	}
	l.stop()
	<-l.done
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if cur, _, err := l.read(ctx); err == nil && cur.Holder == l.id {
		l.blob.Delete(ctx, leaseKey)
	}
}
