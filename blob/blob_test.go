package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

// testStore is a Store on the bucket of BLOB_TEST_ENDPOINT (e.g. MinIO: scripts/test.sh blob), under a prefix
// of the test's own. Without it the test is skipped; with BLOB_TEST_REQUIRED set (CI), it fails instead.
func testStore(t *testing.T) *Store {
	t.Helper()
	endpoint := os.Getenv("BLOB_TEST_ENDPOINT")
	if endpoint == "" {
		if os.Getenv("BLOB_TEST_REQUIRED") != "" {
			t.Fatal("BLOB_TEST_ENDPOINT is not set")
		}
		t.Skip("no bucket: set BLOB_TEST_ENDPOINT, BLOB_TEST_BUCKET, BLOB_TEST_ACCESS_KEY, BLOB_TEST_SECRET_KEY")
	}
	bucket := os.Getenv("BLOB_TEST_BUCKET")
	if bucket == "" {
		bucket = "sermon-studio-test"
	}
	s, err := New(Config{Endpoint: endpoint, Region: os.Getenv("BLOB_TEST_REGION"), Bucket: bucket, PathStyle: true,
		Prefix:    "test-" + strings.ToLower(t.Name()) + "-" + time.Now().Format("150405.000"),
		AccessKey: os.Getenv("BLOB_TEST_ACCESS_KEY"), SecretKey: os.Getenv("BLOB_TEST_SECRET_KEY")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if ok, err := s.client.BucketExists(ctx, bucket); err != nil {
		t.Fatal(err)
	} else if !ok {
		if err := s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: s.region}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for o, err := range s.List(ctx, "") {
			if err != nil {
				break
			}
			s.Delete(ctx, o.Key)
		}
	})
	return s
}

func TestNewChecksConfig(t *testing.T) {
	good := Config{Endpoint: "https://eu2.contabostorage.com", Bucket: "b", AccessKey: "a", SecretKey: "s"}
	if _, err := New(good); err != nil {
		t.Errorf("good: %v", err)
	}
	for name, change := range map[string]func(*Config){
		"no scheme":  func(c *Config) { c.Endpoint = "eu2.contabostorage.com" },
		"a path":     func(c *Config) { c.Endpoint = "https://eu2.contabostorage.com/bucket" },
		"no bucket":  func(c *Config) { c.Bucket = "" },
		"no secret":  func(c *Config) { c.SecretKey = "" },
		"ftp scheme": func(c *Config) { c.Endpoint = "ftp://host" },
	} {
		c := good
		change(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestContentType(t *testing.T) {
	for name, want := range map[string]string{"a.flac": "audio/flac", "a.MP3": "audio/mpeg", "a.opus": "audio/ogg",
		"recording.json": "application/json", "a.vtt": "text/vtt; charset=utf-8", "a": "application/octet-stream"} {
		if got := contentType(name); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
}

func TestStore(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	dir := t.TempDir()

	src := filepath.Join(dir, "sermon.flac")
	data := bytes.Repeat([]byte("fLaC sermon "), 1000)
	os.WriteFile(src, data, 0o644)
	in, err := s.PutFile(ctx, "kirke/2026/2026-10-18/2026-10-18.flac", src)
	if err != nil || in.Size != int64(len(data)) || in.ETag == "" {
		t.Fatalf("put file: %+v %v", in, err)
	}
	s.Put(ctx, "kirke/church.json", strings.NewReader(`{"name":"Kirke"}`), -1)
	s.Put(ctx, "other/church.json", strings.NewReader(`{}`), 2)

	var keys []string
	for o, err := range s.List(ctx, "kirke/") {
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, o.Key)
	}
	if strings.Join(keys, " ") != "kirke/2026/2026-10-18/2026-10-18.flac kirke/church.json" {
		t.Errorf("list: %q", keys)
	}
	for range s.List(ctx, "") {
		break // stopping early is fine
	}

	back := filepath.Join(dir, "cache", "2026-10-18.flac")
	if got, err := s.GetFile(ctx, "kirke/2026/2026-10-18/2026-10-18.flac", back); err != nil || got.Size != int64(len(data)) {
		t.Fatalf("get file: %+v %v", got, err)
	}
	if b, _ := os.ReadFile(back); !bytes.Equal(b, data) {
		t.Error("get file: other bytes")
	}
	if _, err := s.GetFile(ctx, "kirke/nothing.flac", filepath.Join(dir, "nothing.flac")); !errors.Is(err, ErrNotFound) {
		t.Errorf("get of a missing key: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 { // sermon.flac and cache/: no .partial left
		t.Errorf("left in the folder: %v", entries)
	}

	// Rename: copy, then delete.
	if err := s.Copy(ctx, "kirke/church.json", "kirke/church-copy.json"); err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Get(ctx, "kirke/church-copy.json")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(r); string(b) != `{"name":"Kirke"}` {
		t.Errorf("copy: %q", b)
	}
	r.Close()
	if err := s.Delete(ctx, "kirke/church-copy.json"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "kirke/church-copy.json"); err != nil {
		t.Errorf("delete of a missing key: %v", err)
	}
	if _, err := s.Head(ctx, "kirke/church-copy.json"); !errors.Is(err, ErrNotFound) {
		t.Errorf("head after delete: %v", err)
	}

	// A presigned address: a part, with the file name asked for, without credentials.
	u, err := s.Presign(ctx, "kirke/2026/2026-10-18/2026-10-18.flac", time.Minute, Download{Name: "2026-10-18 Advent.flac"})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", u.String(), nil)
	req.Header.Set("Range", "bytes=5-11")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(b) != "sermon " || resp.Header.Get("Content-Type") != "audio/flac" ||
		resp.Header.Get("Content-Disposition") != `attachment; filename="2026-10-18 Advent.flac"` {
		t.Errorf("presigned: %d %q %v", resp.StatusCode, b, resp.Header)
	}
}

// The lease's conditional puts.
func TestConditionalPut(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	first, err := s.PutNew(ctx, "_lease", []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutNew(ctx, "_lease", []byte("b")); !errors.Is(err, ErrPrecondition) {
		t.Errorf("a second PutNew: %v", err)
	}
	second, err := s.PutIfMatch(ctx, "_lease", []byte("c"), first.ETag)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutIfMatch(ctx, "_lease", []byte("d"), first.ETag); !errors.Is(err, ErrPrecondition) {
		t.Errorf("PutIfMatch with an old etag: %v", err)
	}
	if _, err := s.PutIfMatch(ctx, "_lease", []byte("e"), second.ETag); err != nil {
		t.Errorf("PutIfMatch with the etag: %v", err)
	}
}

func TestCheck(t *testing.T) {
	s := testStore(t)
	if err := s.SetCORS(context.Background(), []string{"https://my.sermon-studio.app"}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	results := s.Check(context.Background(), "https://my.sermon-studio.app")
	t.Logf("check: %v", time.Since(start))
	for _, r := range results {
		if !r.OK {
			t.Errorf("%s: %s", r.Name, r.Detail)
		}
	}
	for o, err := range s.List(context.Background(), "") {
		t.Errorf("left in the bucket: %s %v", o.Key, err)
	}
}
