package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

func TestIsPage(t *testing.T) {
	for path, want := range map[string]bool{
		"/": true, "/index.html": true, "/settings": true, "/settings/members": true, "/shares": true, "/about": true,
		"/recordings/2026-10-19": true, "/recordings/2026-10-19/edit": true,
		"/c/kirke-a": true, "/c/kirke-a/": true, "/c/kirke-a/settings/trash": true, "/c/kirke-a/recordings/x/edit": true,
		"/c/": false, "/c/kirke-a/nope": false, "/app.js": false, "/icons/logo.svg": false, "/api/tenants": false,
		"/churches": false, "/invite": false, "/folder/x": false, "/nope": false,
	} {
		if IsPage(path) != want {
			t.Errorf("IsPage(%q) = %v", path, !want)
		}
	}
}

func TestPages(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	for path, base := range map[string]string{
		"/": "./", "/settings": "./", "/settings/members": "../", "/recordings/2026-10-19/edit": "../../",
		"/c/kirke-a": "../", "/c/kirke-a/": "../../", "/c/kirke-a/recordings/x/edit": "../../../../",
	} {
		rec := ts.do(t, "GET", path, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<base href="`+base+`">`) || !strings.Contains(rec.Body.String(), "app.js") {
			t.Errorf("%s: %d, want the app with base %s", path, rec.Code, base)
		}
	}
	for path, file := range map[string]string{"/churches": "churches.js", "/invite": "invite.js"} {
		if rec := ts.do(t, "GET", path, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), file) {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if rec := ts.do(t, "GET", "/nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("/nope: %d", rec.Code)
	}
}

func TestSlugOf(t *testing.T) {
	for name, want := range map[string]string{
		"2026-10-19": "2026-10-19", "2026-10-19 Morning service": "2026-10-19-morning-service",
		"2026-10-19 Høsttakkefest!": "2026-10-19-høsttakkefest", "2026-10-19  2": "2026-10-19-2",
	} {
		if got := slugOf(name); got != want {
			t.Errorf("slugOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestRecordingBySlug(t *testing.T) {
	ts := newTestServer(t, normalizer.FFmpeg{})
	ts.file(t, "2026/2026-10-19 Morning service/2026-10-19 Morning service_raw.flac", "x")
	ts.file(t, "2026/2026-10-12/2026-10-12_raw.flac", "x")
	ts.file(t, "2026/2026-10-12 Evening/2026-10-12 Evening_raw.flac", "x")
	find := func(slug string) (int, string) {
		rec := ts.do(t, "GET", "/api/recording?slug="+url.QueryEscape(slug), nil)
		var e folderEntry
		json.Unmarshal(rec.Body.Bytes(), &e)
		return rec.Code, e.Path
	}
	if c, p := find("2026-10-19-morning-service"); c != http.StatusOK || p != "2026/2026-10-19 Morning service" {
		t.Errorf("by slug: %d %q", c, p)
	}
	// After a rename the old slug still finds the only recording of its date, not one of two.
	if c, p := find("2026-10-19-evening"); c != http.StatusOK || p != "2026/2026-10-19 Morning service" {
		t.Errorf("only one of the date: %d %q", c, p)
	}
	if c, _ := find("2026-10-12-morning"); c != http.StatusNotFound {
		t.Errorf("one of two: %d", c)
	}
	// A name with the same slug as another of the date is refused, in a rename and an upload.
	rec := ts.do(t, "POST", "/api/rename?path="+url.QueryEscape("2026/2026-10-12"), json.RawMessage(`{"date":"2026-10-12","name":"evening!"}`))
	if rec.Code != http.StatusConflict {
		t.Errorf("rename to the same slug: %d %s", rec.Code, rec.Body)
	}
	if rec := chunkNamed(t, ts, "2026-10-12", "EVENING", "a.flac", "x"); rec.Code != http.StatusConflict {
		t.Errorf("upload with the same slug: %d %s", rec.Code, rec.Body)
	}
	if rec := chunkNamed(t, ts, "2026-10-12", "Kveld", "a.flac", "x"); rec.Code != http.StatusCreated || decode[map[string]any](t, rec)["slug"] != "2026-10-12-kveld" {
		t.Errorf("upload: %d %s", rec.Code, rec.Body)
	}
}
