package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gunnaringe/sermon-studio/blob"
	"github.com/gunnaringe/sermon-studio/web"
)

// blobFlags are the bucket's settings (#86): any S3-compatible provider. The secret key is only ever read from
// a file, never a flag or an environment variable of its own.
type blobFlags struct {
	BlobEndpoint      string `name:"blob-endpoint" group:"Object storage" help:"The provider's S3 address, with https:// (R2: https://<account>.r2.cloudflarestorage.com; Contabo: https://eu2.contabostorage.com)."`
	BlobRegion        string `name:"blob-region" group:"Object storage" help:"The bucket's region (R2: auto; none: us-east-1)."`
	BlobBucket        string `name:"blob-bucket" group:"Object storage" help:"The bucket."`
	BlobPrefix        string `name:"blob-prefix" group:"Object storage" help:"Keep everything under this folder in the bucket."`
	BlobPathStyle     bool   `name:"blob-path-style" group:"Object storage" help:"Address the bucket as <endpoint>/<bucket> (Contabo, MinIO, Garage, RustFS need it)."`
	BlobAccessKey     string `name:"blob-access-key" group:"Object storage" help:"The access key's id."`
	BlobSecretKeyFile string `name:"blob-secret-key-file" type:"existingfile" group:"Object storage" help:"A file with the secret key."`
}

// store is the bucket of the flags; an error says which is missing.
func (f blobFlags) store() (*blob.Store, error) {
	var missing []string
	for _, m := range []struct{ name, value string }{{"--blob-endpoint", f.BlobEndpoint}, {"--blob-bucket", f.BlobBucket},
		{"--blob-access-key", f.BlobAccessKey}, {"--blob-secret-key-file", f.BlobSecretKeyFile}} {
		if m.value == "" {
			missing = append(missing, m.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("object storage needs %s", strings.Join(missing, ", "))
	}
	secret, err := os.ReadFile(f.BlobSecretKeyFile)
	if err != nil {
		return nil, fmt.Errorf("--blob-secret-key-file: %w", err)
	}
	return blob.New(blob.Config{Endpoint: f.BlobEndpoint, Region: f.BlobRegion, Bucket: f.BlobBucket, Prefix: f.BlobPrefix,
		PathStyle: f.BlobPathStyle, AccessKey: f.BlobAccessKey, SecretKey: strings.TrimSpace(string(secret))})
}

// blobCmd is `sermon-studio admin blob`: the bucket, before the studio uses it.
type blobCmd struct {
	Check blobCheckCmd `cmd:"" help:"Try what the studio needs of the bucket (a few objects under _check-…, removed after) and print what works."`
	Cors  blobCorsCmd  `cmd:"" help:"Let the studio's pages play and download from the bucket: sets its CORS rule (replacing the ones it has)."`
}

type blobCheckCmd struct {
	blobFlags
	Origin string `help:"The studio's address, to check CORS from (e.g. https://my.sermon-studio.app)."`
}

type blobCorsCmd struct {
	blobFlags
	Origin []string `required:"" help:"The studio's addresses (each with https://), e.g. --origin https://my.sermon-studio.app --origin https://kirke.sermon-studio.app."`
}

func (c *blobCheckCmd) Run(a *adminCmd) error {
	s, err := c.store()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	results := s.Check(ctx, c.Origin)
	failed := false
	err = a.print(results, func(w io.Writer) {
		for _, r := range results {
			mark := "ok"
			if !r.OK {
				mark = "no"
				if r.Needed {
					mark, failed = "FAILED", true
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", mark, r.Name, r.Detail)
		}
		some := false
		for _, r := range results {
			some = some || !r.OK
		}
		switch {
		case failed:
			fmt.Fprintln(w, "\nThe studio cannot use this bucket.")
		case c.Origin == "":
			fmt.Fprintln(w, "\nThe studio can use this bucket. Give --origin to check CORS too.")
		case some:
			fmt.Fprintln(w, "\nThe studio can use this bucket; each \"no\" has a way round.")
		default:
			fmt.Fprintln(w, "\nThe studio can use this bucket: everything works.")
		}
	})
	if err == nil && failed {
		err = errors.New("the bucket lacks what the studio needs")
	}
	return err
}

func (c *blobCorsCmd) Run(a *adminCmd) error {
	for _, o := range c.Origin {
		if !strings.HasPrefix(o, "https://") && !strings.HasPrefix(o, "http://") {
			return fmt.Errorf("--origin %q: give the address with https://", o)
		}
	}
	s, err := c.store()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := s.SetCORS(ctx, c.Origin); err != nil {
		return err
	}
	return a.print(map[string]any{"origins": c.Origin}, func(w io.Writer) {
		fmt.Fprintf(w, "The bucket now allows GET and HEAD with Range from %s.\n", strings.Join(c.Origin, ", "))
	})
}

// blobStore is a blob.Store as the studio uses it (web.Blob): web does not import package blob, so the desktop
// app is built without it.
type blobStore struct{ s *blob.Store }

// werr makes the bucket's errors the studio's.
func werr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, blob.ErrNotFound):
		return fmt.Errorf("%w: %v", web.ErrBlobNotFound, err)
	case errors.Is(err, blob.ErrPrecondition):
		return fmt.Errorf("%w: %v", web.ErrBlobPrecondition, err)
	}
	return err
}

func winfo(i blob.Info) web.BlobInfo {
	return web.BlobInfo{Key: i.Key, Size: i.Size, ETag: i.ETag, Modified: i.Modified}
}

func (b blobStore) Put(ctx context.Context, key string, r io.Reader, size int64) (web.BlobInfo, error) {
	i, err := b.s.Put(ctx, key, r, size)
	return winfo(i), werr(err)
}

func (b blobStore) PutFile(ctx context.Context, key, path string) (web.BlobInfo, error) {
	i, err := b.s.PutFile(ctx, key, path)
	return winfo(i), werr(err)
}

func (b blobStore) Get(ctx context.Context, key string) (io.ReadCloser, web.BlobInfo, error) {
	r, i, err := b.s.Get(ctx, key)
	return r, winfo(i), werr(err)
}

func (b blobStore) GetFile(ctx context.Context, key, path string) (web.BlobInfo, error) {
	i, err := b.s.GetFile(ctx, key, path)
	return winfo(i), werr(err)
}

func (b blobStore) Head(ctx context.Context, key string) (web.BlobInfo, error) {
	i, err := b.s.Head(ctx, key)
	return winfo(i), werr(err)
}

func (b blobStore) Copy(ctx context.Context, from, to string) error {
	return werr(b.s.Copy(ctx, from, to))
}

func (b blobStore) Delete(ctx context.Context, key string) error { return werr(b.s.Delete(ctx, key)) }

func (b blobStore) List(ctx context.Context, prefix string) iter.Seq2[web.BlobInfo, error] {
	return func(yield func(web.BlobInfo, error) bool) {
		for i, err := range b.s.List(ctx, prefix) {
			if !yield(winfo(i), werr(err)) {
				return
			}
		}
	}
}

func (b blobStore) Presign(ctx context.Context, key string, expiry time.Duration, name string, inline bool) (*url.URL, error) {
	u, err := b.s.Presign(ctx, key, expiry, blob.Download{Name: name, Inline: inline})
	return u, werr(err)
}

func (b blobStore) PutNew(ctx context.Context, key string, data []byte) (web.BlobInfo, error) {
	i, err := b.s.PutNew(ctx, key, data)
	return winfo(i), werr(err)
}

func (b blobStore) PutIfMatch(ctx context.Context, key string, data []byte, etag string) (web.BlobInfo, error) {
	i, err := b.s.PutIfMatch(ctx, key, data, etag)
	return winfo(i), werr(err)
}
