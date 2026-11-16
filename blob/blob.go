// Package blob keeps files in an S3-compatible bucket (Cloudflare R2, Contabo, Backblaze B2, Hetzner, MinIO,
// Garage, AWS): what the studio needs of one, and nothing about the studio. Keys are paths with slashes; a
// Store with a Prefix keeps everything under it. Only the S3 API is used, so any provider that has it works;
// Check says what a provider has.
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/cors"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config is a bucket and how to reach it.
type Config struct {
	// Endpoint is the provider's S3 address: https://<account>.r2.cloudflarestorage.com,
	// https://eu2.contabostorage.com, http://localhost:9000 (MinIO). A scheme is required.
	Endpoint string
	// Region is the bucket's region; "auto" for R2, "us-east-1" when the provider has none.
	Region string
	Bucket string
	// Prefix puts every key under it (no slash at either end), so one bucket can hold more than one studio.
	Prefix string
	// PathStyle addresses the bucket as <endpoint>/<bucket> instead of <bucket>.<endpoint>: Contabo, MinIO
	// and Garage need it; R2, B2 and AWS take either.
	PathStyle bool
	AccessKey string
	SecretKey string
}

// ErrNotFound is returned for a key that is not in the bucket.
var ErrNotFound = errors.New("not in the bucket")

// ErrPrecondition is returned by a conditional put whose condition did not hold: the key was there (PutNew),
// or had changed (PutIfMatch).
var ErrPrecondition = errors.New("the object is not as expected")

// Info is what the bucket says of an object.
type Info struct {
	Key      string // without the Store's prefix
	Size     int64
	ETag     string // without quotes
	Modified time.Time
}

// Store is a bucket. It is safe for concurrent use.
type Store struct {
	client *minio.Client
	bucket string
	prefix string // "" or ending in "/"
	region string
}

// New connects to the bucket described by c. It does not contact the provider: Check does.
func New(c Config) (*Store, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("blob endpoint %q: want https://host[:port]", c.Endpoint)
	}
	if c.Bucket == "" {
		return nil, errors.New("no blob bucket")
	}
	if c.AccessKey == "" || c.SecretKey == "" {
		return nil, errors.New("no blob access key or secret key")
	}
	lookup := minio.BucketLookupDNS
	if c.PathStyle {
		lookup = minio.BucketLookupPath
	}
	region := c.Region
	if region == "" {
		region = "us-east-1"
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure:       u.Scheme == "https",
		Region:       region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, err
	}
	prefix := strings.Trim(c.Prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	return &Store{client: client, bucket: c.Bucket, prefix: prefix, region: region}, nil
}

func (s *Store) key(k string) string { return s.prefix + strings.TrimPrefix(k, "/") }

// wrap turns the provider's errors into ErrNotFound and ErrPrecondition where they are that.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	r := minio.ToErrorResponse(err)
	switch {
	case r.StatusCode == http.StatusNotFound || r.Code == "NoSuchKey":
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case r.StatusCode == http.StatusPreconditionFailed || r.Code == "PreconditionFailed":
		return fmt.Errorf("%w: %v", ErrPrecondition, err)
	}
	return err
}

func info(key string, o minio.ObjectInfo) Info {
	return Info{Key: key, Size: o.Size, ETag: strings.Trim(o.ETag, `"`), Modified: o.LastModified}
}

// Put stores size bytes from r under key (size -1: unknown, read to the end). Large ones go as a multipart
// upload, which is aborted when it fails; the provider checks each part's MD5.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64) (Info, error) {
	return s.put(ctx, key, r, size, minio.PutObjectOptions{})
}

func (s *Store) put(ctx context.Context, key string, r io.Reader, size int64, opts minio.PutObjectOptions) (Info, error) {
	opts.SendContentMd5 = true
	if opts.ContentType == "" {
		opts.ContentType = contentType(key)
	}
	up, err := s.client.PutObject(ctx, s.bucket, s.key(key), r, size, opts)
	if err != nil {
		return Info{}, wrap(err)
	}
	return Info{Key: key, Size: up.Size, ETag: strings.Trim(up.ETag, `"`), Modified: up.LastModified}, nil
}

// PutFile stores the file at path under key, and checks that the bucket has all of it.
func (s *Store) PutFile(ctx context.Context, key, path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Info{}, err
	}
	if _, err := s.Put(ctx, key, f, st.Size()); err != nil {
		return Info{}, err
	}
	got, err := s.Head(ctx, key)
	if err != nil {
		return Info{}, err
	}
	if got.Size != st.Size() {
		return Info{}, fmt.Errorf("%s: the bucket has %d bytes of %d", key, got.Size, st.Size())
	}
	return got, nil
}

// PutNew stores data under key only if there is nothing there (If-None-Match: *): ErrPrecondition if there is.
// A provider without conditional writes may store it anyway; Check says whether it has them.
func (s *Store) PutNew(ctx context.Context, key string, data []byte) (Info, error) {
	var opts minio.PutObjectOptions
	opts.SetMatchETagExcept("*")
	return s.put(ctx, key, strings.NewReader(string(data)), int64(len(data)), opts)
}

// PutIfMatch replaces key with data only if it is still the version etag: ErrPrecondition if it changed.
func (s *Store) PutIfMatch(ctx context.Context, key string, data []byte, etag string) (Info, error) {
	var opts minio.PutObjectOptions
	opts.SetMatchETag(etag)
	return s.put(ctx, key, strings.NewReader(string(data)), int64(len(data)), opts)
}

// Get reads key; the caller closes it. ErrNotFound when it is not there.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, Info, error) {
	o, err := s.client.GetObject(ctx, s.bucket, s.key(key), minio.GetObjectOptions{})
	if err != nil {
		return nil, Info{}, wrap(err)
	}
	st, err := o.Stat() // GetObject is lazy: this is the request
	if err != nil {
		o.Close()
		return nil, Info{}, wrap(err)
	}
	return o, info(key, st), nil
}

// GetFile writes key to path, through a hidden .partial next to it that is renamed when it is whole, so path
// is never a part of the object.
func (s *Store) GetFile(ctx context.Context, key, path string) (Info, error) {
	r, in, err := s.Get(ctx, key)
	if err != nil {
		return Info{}, err
	}
	defer r.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Info{}, err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".partial")
	f, err := os.Create(tmp)
	if err != nil {
		return Info{}, err
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != in.Size {
		err = fmt.Errorf("%s: got %d bytes of %d", key, n, in.Size)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return Info{}, err
	}
	return in, nil
}

// Head is what the bucket says of key, without its bytes. ErrNotFound when it is not there.
func (s *Store) Head(ctx context.Context, key string) (Info, error) {
	o, err := s.client.StatObject(ctx, s.bucket, s.key(key), minio.StatObjectOptions{})
	if err != nil {
		return Info{}, wrap(err)
	}
	return info(key, o), nil
}

// Copy makes to a copy of from, in the bucket (the bytes do not come here). S3 has no rename: a rename is a
// Copy, then a Delete.
func (s *Store) Copy(ctx context.Context, from, to string) error {
	_, err := s.client.CopyObject(ctx, minio.CopyDestOptions{Bucket: s.bucket, Object: s.key(to)},
		minio.CopySrcOptions{Bucket: s.bucket, Object: s.key(from)})
	return wrap(err)
}

// Delete removes key. A key that is not there is not an error.
func (s *Store) Delete(ctx context.Context, key string) error {
	err := wrap(s.client.RemoveObject(ctx, s.bucket, s.key(key), minio.RemoveObjectOptions{}))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// List is every object under prefix ("" = all), in key order, page by page as the provider gives them.
func (s *Store) List(ctx context.Context, prefix string) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel() // stops the listing when the loop ends early
		for o := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: s.key(prefix), Recursive: true}) {
			if o.Err != nil {
				yield(Info{}, wrap(o.Err))
				return
			}
			if !yield(info(strings.TrimPrefix(o.Key, s.prefix), o), nil) {
				return
			}
		}
	}
}

// Download is how a presigned address hands out a file: its name, and whether the browser shows it (inline,
// for a player) or saves it.
type Download struct {
	Name   string // the file name the browser gets; "" = the key's
	Inline bool
}

// Presign is an address that gets key without credentials until expiry (at most 7 days), with Range. The
// response names the file (response-content-disposition) and has its type.
func (s *Store) Presign(ctx context.Context, key string, expiry time.Duration, d Download) (*url.URL, error) {
	name := d.Name
	if name == "" {
		name = filepath.Base(key)
	}
	disposition := "attachment"
	if d.Inline {
		disposition = "inline"
	}
	q := url.Values{}
	q.Set("response-content-disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	q.Set("response-content-type", contentType(name))
	return s.client.PresignedGetObject(ctx, s.bucket, s.key(key), expiry, q)
}

// contentType is the type of a file by its name, as the studio serves them.
func contentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".flac":
		return "audio/flac"
	case ".mp3":
		return "audio/mpeg"
	case ".opus", ".ogg":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".json":
		return "application/json"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".vtt":
		return "text/vtt; charset=utf-8"
	}
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// SetCORS lets pages from origins (the studio's addresses, e.g. https://my.sermon-studio.app) get objects
// through presigned addresses with Range, as the editor's player does. It replaces the bucket's CORS rules.
func (s *Store) SetCORS(ctx context.Context, origins []string) error {
	if len(origins) == 0 {
		return errors.New("no origins")
	}
	return wrap(s.client.SetBucketCors(ctx, s.bucket, cors.NewConfig([]cors.Rule{{
		AllowedOrigin: origins,
		AllowedMethod: []string{"GET", "HEAD"},
		AllowedHeader: []string{"range"},
		ExposeHeader:  []string{"Content-Length", "Content-Range", "Content-Disposition", "Accept-Ranges", "ETag"},
		MaxAgeSeconds: 3600,
	}})))
}
