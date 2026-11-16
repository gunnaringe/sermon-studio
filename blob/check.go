package blob

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

// Result is one thing Check tried.
type Result struct {
	Name string
	OK   bool
	// Needed: without it the studio cannot use the bucket. The others have a way round (the lease without
	// conditional writes, downloads through the studio without CORS).
	Needed bool
	Detail string
}

// Check tries what the studio needs of the bucket, under a key of its own that it removes after: a put, head
// and list, a multipart upload, a presigned GET with Range and a file name, CORS for origin (the studio's
// address, e.g. https://my.sermon-studio.app; "" = not tried), a copy, a delete, and conditional puts.
func (s *Store) Check(ctx context.Context, origin string) []Result {
	var out []Result
	add := func(name string, needed bool, err error, ok string) bool {
		r := Result{Name: name, OK: err == nil, Needed: needed, Detail: ok}
		if err != nil {
			r.Detail = err.Error()
		}
		out = append(out, r)
		return err == nil
	}
	id := make([]byte, 6)
	rand.Read(id)
	dir := fmt.Sprintf("_check-%x/", id)
	defer func() { // what it made, also when a step failed
		for o, err := range s.List(context.WithoutCancel(ctx), dir) {
			if err != nil {
				break
			}
			s.Delete(context.WithoutCancel(ctx), o.Key)
		}
	}()

	body := []byte("0123456789 sermon studio")
	key := dir + "small.txt"
	_, err := s.Put(ctx, key, bytes.NewReader(body), int64(len(body)))
	if !add("put", true, err, "stored 24 bytes") {
		return out // the rest needs it
	}
	in, err := s.Head(ctx, key)
	if err == nil && in.Size != int64(len(body)) {
		err = fmt.Errorf("size %d, want %d", in.Size, len(body))
	}
	add("head", true, err, "size and etag")
	if _, err := s.Head(ctx, dir+"nothing"); !errors.Is(err, ErrNotFound) {
		add("head of a missing key", true, fmt.Errorf("want not found, got %v", err), "")
	}
	r, _, err := s.Get(ctx, key)
	if err == nil {
		var got []byte
		got, err = io.ReadAll(r)
		r.Close()
		if err == nil && !bytes.Equal(got, body) {
			err = errors.New("got other bytes")
		}
	}
	add("get", true, err, "the same bytes back")

	big := make([]byte, 6<<20) // more than one 5 MB part
	rand.Read(big)
	_, err = s.put(ctx, dir+"big.bin", bytes.NewReader(big), -1, minio.PutObjectOptions{PartSize: 5 << 20})
	if err == nil {
		in, err = s.Head(ctx, dir+"big.bin")
		if err == nil && in.Size != int64(len(big)) {
			err = fmt.Errorf("size %d, want %d", in.Size, len(big))
		}
	}
	add("multipart upload", true, err, "6 MB in two parts")

	n := 0
	for _, err = range s.List(ctx, dir) {
		if err != nil {
			break
		}
		n++
	}
	if err == nil && n != 2 {
		err = fmt.Errorf("listed %d objects, want 2", n)
	}
	add("list", true, err, "both objects")

	err = s.Copy(ctx, key, dir+"copy.txt")
	if err == nil {
		_, err = s.Head(ctx, dir+"copy.txt")
	}
	add("copy", true, err, "copied in the bucket")
	err = s.Delete(ctx, dir+"copy.txt")
	if err == nil {
		if _, herr := s.Head(ctx, dir+"copy.txt"); !errors.Is(herr, ErrNotFound) {
			err = fmt.Errorf("still there after delete (%v)", herr)
		}
	}
	add("delete", true, err, "gone")

	out = append(out, s.checkPresign(ctx, key, body, origin)...)

	// Conditional puts, for the lease: a second PutNew must fail, and PutIfMatch with an old etag.
	lease := dir + "lease"
	first, err := s.PutNew(ctx, lease, []byte("a"))
	if err == nil {
		_, err = s.PutNew(ctx, lease, []byte("b"))
		if err == nil {
			err = errors.New("a second put with If-None-Match: * replaced the object")
		} else if errors.Is(err, ErrPrecondition) {
			err = nil
		}
	}
	add("conditional put (If-None-Match)", false, err, "a second one is refused")
	if err == nil {
		_, err = s.PutIfMatch(ctx, lease, []byte("c"), first.ETag)
		if err == nil {
			_, err = s.PutIfMatch(ctx, lease, []byte("d"), first.ETag)
			if err == nil {
				err = errors.New("a put with an old etag (If-Match) replaced the object")
			} else if errors.Is(err, ErrPrecondition) {
				err = nil
			}
		}
		add("conditional put (If-Match)", false, err, "an old etag is refused")
	}
	return out
}

// checkPresign gets key through a presigned address, as a browser would: a part of it (Range), with the file
// name asked for, and, with an origin, CORS (the preflight a player's Range request needs, and the header on
// the answer).
func (s *Store) checkPresign(ctx context.Context, key string, body []byte, origin string) []Result {
	var out []Result
	add := func(name string, needed bool, err error, ok string) {
		r := Result{Name: name, OK: err == nil, Needed: needed, Detail: ok}
		if err != nil {
			r.Detail = err.Error()
		}
		out = append(out, r)
	}
	u, err := s.Presign(ctx, key, 5*time.Minute, Download{Name: "Advent 2026.txt"})
	if err != nil {
		add("presigned GET", true, err, "")
		return out
	}
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.Header.Set("Range", "bytes=11-")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := client.Do(req)
	if err != nil {
		add("presigned GET with Range", true, err, "")
		return out
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	err = nil
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(got, body[11:]) {
		err = fmt.Errorf("status %d, %q; want 206 and %q", resp.StatusCode, got, body[11:])
	}
	add("presigned GET with Range", true, err, "206 with the part asked for")
	cd := resp.Header.Get("Content-Disposition")
	err = nil
	if !strings.Contains(cd, "Advent 2026.txt") {
		err = fmt.Errorf("Content-Disposition %q", cd)
	}
	add("file name of a download", false, err, cd)
	if origin == "" {
		return out
	}
	err = nil
	if a := resp.Header.Get("Access-Control-Allow-Origin"); a != origin && a != "*" {
		err = fmt.Errorf("no Access-Control-Allow-Origin for %s on the answer (got %q): set the bucket's CORS rule", origin, a)
	}
	add("CORS on GET", false, err, "allowed from "+origin)
	pre, _ := http.NewRequestWithContext(ctx, "OPTIONS", u.String(), nil)
	pre.Header.Set("Origin", origin)
	pre.Header.Set("Access-Control-Request-Method", "GET")
	pre.Header.Set("Access-Control-Request-Headers", "range")
	resp, err = client.Do(pre)
	if err == nil {
		resp.Body.Close()
		a := resp.Header.Get("Access-Control-Allow-Origin")
		h := strings.ToLower(resp.Header.Get("Access-Control-Allow-Headers"))
		switch {
		case resp.StatusCode >= 300:
			err = fmt.Errorf("preflight: status %d", resp.StatusCode)
		case a != origin && a != "*":
			err = fmt.Errorf("preflight: no Access-Control-Allow-Origin for %s (got %q)", origin, a)
		case !strings.Contains(h, "range") && h != "*":
			err = fmt.Errorf("preflight: Range is not an allowed header (got %q)", h)
		}
	}
	add("CORS preflight with Range", false, err, "GET with Range allowed")
	return out
}
