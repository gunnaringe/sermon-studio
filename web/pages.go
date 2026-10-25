package web

import (
	"bytes"
	"io/fs"
	"net/http"
	"strings"
	"unicode"
)

// The studio's pages have plain addresses, handled in the browser (app.js). The page of a church is one of
// pageNames, after /c/<church> when the church is not known from the host (no church hosts):
//
//	/                                   the recordings (at a church's own host; elsewhere / is the start)
//	/recordings/<slug>                  the recordings, that one opened
//	/recordings/<slug>/edit             the editor
//	/settings, /settings/<part>         the settings (a part: one of its sections)
//	/shares, /about
//
// The other pages are their own files: the start (churches.html: the churches of the user, or Log in), at /
// when the address is not a church's own host, and /invite#<token> (an invite link: the token after # never
// reaches the server).
var pageNames = map[string]bool{"recordings": true, "settings": true, "shares": true, "about": true}

// otherPages are the pages that are not the app, by address.
var otherPages = map[string]string{"invite": "invite.html"}

// IsPage reports whether path (from the root of the studio) is a page of the app, served as index.html.
func IsPage(path string) bool {
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	switch {
	case segs[0] == "" || segs[0] == "index.html":
		return len(segs) == 1
	case segs[0] == "c":
		return len(segs) >= 2 && segs[1] != "" && (len(segs) == 2 || segs[2] == "" || pageNames[segs[2]])
	}
	return pageNames[segs[0]]
}

// servePages wraps the static files: the app's pages get index.html, the other pages their files. start
// reports whether / is the start (churches.html) for this request; nil: never (one church, no picker).
func servePages(files fs.FS, start func(*http.Request) bool) http.Handler {
	next := http.FileServerFS(files)
	index, _ := fs.ReadFile(files, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && start != nil && start(r) {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, files, "churches.html")
			return
		}
		if file, ok := otherPages[strings.TrimPrefix(r.URL.Path, "/")]; ok {
			http.ServeFileFS(w, r, files, file)
			return
		}
		if !IsPage(r.URL.Path) || r.URL.Path == "/index.html" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(withBase(index, r.URL.Path))
	})
}

// withBase puts a <base> into the page, so its relative addresses (style.css, api/...) are from the root of
// the studio, whatever page it is at. It is relative too (../../ from /recordings/<slug>/edit): the studio
// also works behind a proxy under a sub-path it does not know.
func withBase(page []byte, path string) []byte {
	base := strings.Repeat("../", max(strings.Count(path, "/")-1, 0))
	if base == "" {
		base = "./"
	}
	return bytes.Replace(page, []byte("<head>"), []byte(`<head>`+"\n  "+`<base href="`+base+`">`), 1)
}

// slugOf is a recording's name in its address: the folder's name in lower case, with a dash for every run of
// other characters than letters and digits ("2026-10-19 Morning service" is 2026-10-19-morning-service).
func slugOf(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	return b.String()
}
