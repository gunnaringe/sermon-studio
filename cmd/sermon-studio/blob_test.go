package main

import (
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/blob"
	"github.com/gunnaringe/sermon-studio/web"
)

// The studio with a real bucket (BLOB_TEST_ENDPOINT: scripts/test.sh blob, CI): a state file is in the bucket
// when its request is answered, the lease is given back at the end, and a new machine (an empty data dir) gets it
// back from the bucket.
func TestBlobServe(t *testing.T) {
	endpoint := os.Getenv("BLOB_TEST_ENDPOINT")
	if endpoint == "" {
		if os.Getenv("BLOB_TEST_REQUIRED") != "" {
			t.Fatal("BLOB_TEST_ENDPOINT is not set")
		}
		t.Skip("no bucket: set BLOB_TEST_ENDPOINT (scripts/test.sh blob)")
	}
	bucket := os.Getenv("BLOB_TEST_BUCKET")
	if bucket == "" {
		bucket = "sermon-studio-test"
	}
	st, err := blob.New(blob.Config{Endpoint: endpoint, Region: os.Getenv("BLOB_TEST_REGION"), Bucket: bucket, PathStyle: true,
		Prefix:    "serve-" + time.Now().Format("150405.000"),
		AccessKey: os.Getenv("BLOB_TEST_ACCESS_KEY"), SecretKey: os.Getenv("BLOB_TEST_SECRET_KEY")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	defer func() {
		for o, err := range st.List(ctx, "") {
			if err == nil {
				st.Delete(ctx, o.Key)
			}
		}
	}()
	if err := blob.MakeBucket(ctx, st); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "kirke", "Taler"), 0o755)
	app, err := web.NewApp(web.AppConfig{Base: web.Config{CacheDir: t.TempDir(), Logger: log.New(os.Stderr, "", 0)},
		DataDir: data, Blob: blobStore{st}, Share: &web.ShareOptions{URL: "https://up.example/"}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/shares", strings.NewReader(`{"label":"Sound desk","allTypes":true}`))
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("share: %d %s", w.Code, w.Body)
	}
	if info, err := st.Head(ctx, "kirke/shares.json"); err != nil || info.Size == 0 {
		t.Errorf("shares.json in the bucket: %+v %v", info, err)
	}
	if _, err := st.Head(ctx, "_lease"); err != nil {
		t.Errorf("no lease while it runs: %v", err)
	}
	app.Close()
	if _, err := st.Head(ctx, "_lease"); err == nil {
		t.Error("the lease was not given back")
	}

	again := t.TempDir()
	app, err = web.NewApp(web.AppConfig{Base: web.Config{CacheDir: t.TempDir(), Logger: log.New(os.Stderr, "", 0)},
		DataDir: again, Blob: blobStore{st}, Share: &web.ShareOptions{URL: "https://up.example/"}})
	if err != nil {
		t.Fatal(err)
	}
	app.Close()
	want, _ := os.ReadFile(filepath.Join(data, "kirke", "shares.json"))
	if got, err := os.ReadFile(filepath.Join(again, "kirke", "shares.json")); err != nil || string(got) != string(want) {
		t.Errorf("shares.json fetched back: %v\n%s\nwant\n%s", err, got, want)
	}
}
