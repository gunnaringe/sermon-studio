package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gunnaringe/sermon-studio/normalizer"
)

// downloadTest is a church with one finished sermon (an MP3 and a FLAC) and a download share of it.
func downloadTest(t *testing.T, body string) (*shareTest, string) {
	t.Helper()
	st := newShareTest(t, RateLimits{RequestsPerIP: 1000, RequestsTotal: 1000})
	dir := filepath.Join(st.church, "Taler", "2026", "2026-10-18 Advent")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "2026-10-18 Advent.mp3"), []byte("ID3 the mp3 file"), 0o644)
	os.WriteFile(filepath.Join(dir, "2026-10-18 Advent.flac"), []byte("fLaC the flac file"), 0o644)
	st.save(dir)
	code, _ := st.makeShare(body)
	return st, code
}

// save gives the recording in dir a saved version: only a saved sermon is shared.
func (st *shareTest) save(dir string) {
	st.t.Helper()
	saved := sermonSettings{cutting: cutting{Sections: []span{{Start: 30, End: 3600}}, Cuts: []span{}}}
	if _, err := st.app.tenants[0].srv.updateState(dir, func(s *folderState) error { s.Saved = &saved; return nil }); err != nil {
		st.t.Fatal(err)
	}
}

type downloadInfoView struct {
	Kind, Title, Date, State string
	Preparing                []string
	Follow                   map[string]float64
	Files                    []struct {
		Format string
		Size   int64
	}
}

func (st *shareTest) info(code string) downloadInfoView {
	st.t.Helper()
	w := st.call("GET", "/api/share", code, "1.1.1.1", nil)
	if w.Code != http.StatusOK {
		st.t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	return decode[downloadInfoView](st.t, w)
}

func (st *shareTest) download(code, ip, format string) (int, string) {
	b, _ := json.Marshal(map[string]string{"format": format})
	w := st.call("POST", "/api/share/downloads", code, ip, b)
	var out struct{ URL string }
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out.URL
}

// fetch gets a download's address the way a browser does: no code.
func (st *shareTest) fetch(addr, rangeHeader string) *http.Response {
	r := httptest.NewRequest("GET", "/"+addr, nil)
	r.Header.Set("CF-Connecting-IP", "9.9.9.9")
	if rangeHeader != "" {
		r.Header.Set("Range", rangeHeader)
	}
	w := httptest.NewRecorder()
	st.public.ServeHTTP(w, r)
	return w.Result()
}

func readAll(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

var escape = url.QueryEscape

func TestShareDownload(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3","flac"],"label":"For Anna"}`)
	info := st.info(code)
	if info.Kind != "download" || info.Title == "" || info.Date != "2026-10-18" || info.State != "" ||
		len(info.Files) != 2 || info.Files[0].Format != "flac" || info.Files[1].Size != int64(len("ID3 the mp3 file")) {
		t.Fatalf("info: %+v", info)
	}
	// A download code takes no uploads, and gives only its formats.
	if w := st.start(code, "1.1.1.1", "x.wav", 4096); w.Code != http.StatusForbidden {
		t.Errorf("upload with a download code: %d", w.Code)
	}
	if c, _ := st.download(code, "1.1.1.1", "opus"); c != http.StatusBadRequest {
		t.Errorf("a format it does not give: %d", c)
	}

	c, addr := st.download(code, "1.1.1.1", "mp3")
	if c != http.StatusCreated || !strings.HasPrefix(addr, "api/share/downloads/") {
		t.Fatalf("download: %d %q", c, addr)
	}
	// Pressing again (the download broke) gives the same one, and counts nothing.
	if c, again := st.download(code, "1.1.1.1", "mp3"); c != http.StatusOK || again != addr {
		t.Errorf("again: %d %q", c, again)
	}
	resp := st.fetch(addr, "")
	if b := readAll(resp); resp.StatusCode != http.StatusOK || b != "ID3 the mp3 file" || resp.Header.Get("Content-Type") != "audio/mpeg" ||
		resp.Header.Get("Content-Disposition") != `attachment; filename="2026-10-18 Advent.mp3"` {
		t.Errorf("file: %d %q %v", resp.StatusCode, b, resp.Header)
	}
	// Resuming, as many times as it takes, without the code.
	for range 3 {
		if resp := st.fetch(addr, "bytes=4-"); resp.StatusCode != http.StatusPartialContent || readAll(resp) != "the mp3 file" {
			t.Errorf("resume: %d", resp.StatusCode)
		}
	}

	if c, _ := st.download(code, "2.2.2.2", "flac"); c != http.StatusCreated {
		t.Errorf("second download: %d", c)
	}
	if c, _ := st.download(code, "3.3.3.3", "mp3"); c != http.StatusCreated {
		t.Errorf("a third: there is no limit on downloads: %d", c)
	}
	if info := st.info(code); info.State != "" {
		t.Errorf("still open: %+v", info)
	}
	list := decode[struct{ Shares []shareView }](t, do(st.studio, "GET", "/api/shares", ""))
	if s := list.Shares[0]; s.Kind != "download" || s.Count != 3 || len(s.Log) != 3 || s.Log[0].Name != "2026-10-18 Advent.mp3" || s.Resumable != 3 || s.Downloading != 0 || s.Missing {
		t.Errorf("in the studio: %+v", s)
	}
	// A wrong ticket counts as a wrong code; an expired one has ended.
	if resp := st.fetch("api/share/downloads/nope", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("wrong ticket: %d", resp.StatusCode)
	}
	st.app.shareMu.Lock()
	for _, tk := range st.app.tenants[0].shares[0].Tickets {
		tk.Expires = time.Now().Add(-time.Second)
	}
	st.app.shareMu.Unlock()
	if resp := st.fetch(addr, ""); resp.StatusCode != http.StatusGone {
		t.Errorf("expired ticket: %d", resp.StatusCode)
	}
	st.app.sweepShares(time.Now())
	if n := len(st.app.tenants[0].shares[0].Tickets); n != 0 {
		t.Errorf("the sweep left %d tickets", n)
	}
}

func TestShareDownloadFollowsTheRecording(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Advent"}`)
	_, addr := st.download(code, "1.1.1.1", "mp3")

	// Renamed: the share and the download follow it.
	if w := do(st.studio, "POST", "/api/rename?path="+escape("2026/2026-10-18 Advent"), `{"date":"2026-10-19","name":"First Advent"}`); w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if info := st.info(code); info.Date != "2026-10-19" || len(info.Files) != 1 {
		t.Errorf("after the rename: %+v", info)
	}
	if resp := st.fetch(addr, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("download after the rename: %d", resp.StatusCode)
	}
	if rec := st.app.tenants[0].shares[0].Recording; rec != "2026/2026-10-19 First Advent" {
		t.Errorf("the share's recording: %q", rec)
	}

	// In the trash: not there. Another recording in its place is not it.
	w := do(st.studio, "POST", "/api/recordings/delete?path="+escape("2026/2026-10-19 First Advent"), "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	trashID := decode[struct{ ID string }](t, w).ID
	other := filepath.Join(st.church, "Taler", "2026", "2026-10-19 First Advent")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "2026-10-19 First Advent.mp3"), []byte("ID3 another sermon"), 0o644)
	if info := st.info(code); info.State != "missing" || len(info.Files) != 0 {
		t.Errorf("in the trash: %+v", info)
	}
	if c, _ := st.download(code, "2.2.2.2", "mp3"); c != http.StatusGone {
		t.Errorf("download from the trash: %d", c)
	}
	if resp := st.fetch(addr, ""); resp.StatusCode != http.StatusGone {
		t.Errorf("a counted download of one in the trash: %d", resp.StatusCode)
	}
	list := decode[struct{ Shares []shareView }](t, do(st.studio, "GET", "/api/shares", ""))
	if !list.Shares[0].Missing {
		t.Errorf("the studio does not see it is missing: %+v", list.Shares[0])
	}

	// Restored: there again.
	os.RemoveAll(other)
	if w := do(st.studio, "POST", "/api/trash/restore?id="+escape(trashID), ""); w.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	if info := st.info(code); info.State != "" || len(info.Files) != 1 {
		t.Errorf("restored: %+v", info)
	}
	if resp := st.fetch(addr, ""); resp.StatusCode != http.StatusOK || readAll(resp) != "ID3 the mp3 file" {
		t.Errorf("download when restored: %d", resp.StatusCode)
	}
}

func TestShareDownloadRefusals(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Advent"}`)
	for _, body := range []string{
		`{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["opus"],"label":"Not made"}`,
		`{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["wav"],"label":"No such format"}`,
		`{"kind":"download","recording":"2026/2026-10-18 Advent","formats":[],"label":"None"}`,
		`{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":""}`,
		`{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Past","expires":"2020-01-01T00:00:00Z"}`,
		`{"kind":"download","recording":"2026/2026-11-01","formats":["mp3"],"label":"No such recording"}`,
		`{"kind":"download","recording":"","formats":["mp3"],"label":"The root"}`,
		`{"kind":"download","recording":"../kirke","formats":["mp3"],"label":"Outside"}`,
		`{"kind":"other","label":"Unknown kind"}`,
	} {
		if w := do(st.studio, "POST", "/api/shares", body); w.Code < 400 {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	// An upload code downloads nothing.
	up, _ := st.makeShare(`{"label":"Sound desk","allTypes":true}`)
	if c, _ := st.download(up, "1.1.1.1", "mp3"); c != http.StatusForbidden {
		t.Errorf("download with an upload code: %d", c)
	}
	if info := st.info(up); info.Kind != "upload" {
		t.Errorf("an upload code's kind: %q", info.Kind)
	}
	// Paused: no downloads, and the counted ones stop.
	_, addr := st.download(code, "1.1.1.1", "mp3")
	id := st.app.tenants[0].shares[0].ID
	if w := do(st.studio, "POST", "/api/shares/"+id+"/pause", ""); w.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", w.Code, w.Body)
	}
	if c, _ := st.download(code, "1.1.1.1", "mp3"); c != http.StatusForbidden {
		t.Errorf("download when paused: %d", c)
	}
	if resp := st.fetch(addr, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a counted download when paused: %d", resp.StatusCode)
	}
	// The recording's id is kept in its recording.json, and the share survives a restart.
	if readState(filepath.Join(st.church, "Taler", "2026", "2026-10-18 Advent")).ID == "" {
		t.Error("no id in recording.json")
	}
	shares, err := readShares(st.church)
	if err != nil || len(shares) != 2 || shares[0].Kind != "download" || shares[0].RecordingID == "" || shares[0].Formats[0] != "mp3" {
		t.Errorf("shares.json: %v %+v", err, shares)
	}
}

// A download takes as long as it takes: the listener's write timeout (minutes, for the upload chunks) does not cut it off.
func TestShareDownloadOutlastsTheWriteTimeout(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Advent"}`)
	big := bytes.Repeat([]byte("sermon "), 16<<20/7)
	os.WriteFile(filepath.Join(st.church, "Taler", "2026", "2026-10-18 Advent", "2026-10-18 Advent.mp3"), big, 0o644)
	srv := httptest.NewUnstartedServer(st.public)
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	// Counted in-process: only the file goes through the listener with the short timeout.
	c, addr := st.download(code, "1.1.1.1", "mp3")
	if c != http.StatusCreated {
		t.Fatalf("download: %d", c)
	}
	resp, err := http.Get(srv.URL + "/" + addr)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got int64
	buf := make([]byte, 256<<10)
	for {
		n, err := resp.Body.Read(buf)
		got += int64(n)
		if err != nil {
			break
		}
		time.Sleep(5 * time.Millisecond) // a slow reader: well past the 100 ms in all
	}
	if got != int64(len(big)) {
		t.Errorf("got %d of %d bytes", got, len(big))
	}
}

// The number on the shares icon: uploads going on (a chunk now or in the last minutes) and files being sent.
func TestSharesActive(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Advent"}`)
	active := func() int {
		return decode[struct{ Active int }](t, do(st.studio, "GET", "/api/shares/active", "")).Active
	}
	if n := active(); n != 0 {
		t.Errorf("nothing going on: %d", n)
	}
	up, _ := st.makeShare(`{"label":"Sound desk","allTypes":true}`)
	id := decode[struct{ ID string }](t, st.start(up, "1.1.1.1", "x.wav", 8192)).ID
	if w := st.call("PUT", "/api/share/uploads/"+id+"?offset=0", up, "1.1.1.1", wav(4096)); w.Code != http.StatusOK {
		t.Fatalf("chunk: %d %s", w.Code, w.Body)
	}
	if n := active(); n != 1 {
		t.Errorf("an upload going on: %d", n)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(st.church, "Innboks", ".share-"+id+".partial"), old, old)
	if n := active(); n != 0 {
		t.Errorf("an upload that stopped an hour ago: %d", n)
	}
	// A file being sent counts while it is.
	_, addr := st.download(code, "1.1.1.1", "mp3")
	st.app.shareMu.Lock()
	shareID := st.app.tenants[0].shares[0].ID
	st.app.share.sending[shareID]++ // as during a long response
	st.app.shareMu.Unlock()
	if n := active(); n != 1 {
		t.Errorf("a download going on: %d", n)
	}
	st.fetch(addr, "")
	st.app.shareMu.Lock()
	n := st.app.share.sending[shareID]
	st.app.shareMu.Unlock()
	if n != 1 {
		t.Errorf("a finished response is not counted down: %d", n)
	}
}

// A download share of the sermon only gives the files without the intro and outro, and needs them made.
func TestShareDownloadSermonOnly(t *testing.T) {
	const body = `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"sermonOnly":true,"label":"x"}`
	st, _ := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"x"}`)
	if w := do(st.studio, "POST", "/api/shares", body); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "without its intro and outro") {
		t.Fatalf("a share of a sermon only that has not been made: %d %s", w.Code, w.Body)
	}
	dir := filepath.Join(st.church, "Taler", "2026", "2026-10-18 Advent")
	os.WriteFile(filepath.Join(dir, "2026-10-18 Advent_sermon.mp3"), []byte("ID3 the sermon only"), 0o644)
	code, _ := st.makeShare(body)
	if info := st.info(code); len(info.Files) != 1 || info.Files[0].Size != int64(len("ID3 the sermon only")) {
		t.Fatalf("info: %+v", info)
	}
	_, addr := st.download(code, "1.1.1.1", "mp3")
	resp := st.fetch(addr, "")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != "ID3 the sermon only" || !strings.Contains(resp.Header.Get("Content-Disposition"), "_sermon.mp3") {
		t.Errorf("downloaded %q as %q", got, resp.Header.Get("Content-Disposition"))
	}
}

// Only a saved sermon is shared. A share of all formats gives what there is, also formats made later; one of
// chosen formats needs them made. The transcript is given as text and captions, not counted as downloads.
func TestShareDownloadAllFormatsAndTranscript(t *testing.T) {
	modelsDir := t.TempDir()
	writeModels(t, modelsDir, nbMedium)
	st := newShareTestWith(t, RateLimits{RequestsPerIP: 1000, RequestsTotal: 1000}, func(c *AppConfig) {
		c.Transcription = &TranscriptionConfig{ModelsDir: modelsDir, QueueDir: t.TempDir()}
	})
	dir := filepath.Join(st.church, "Taler", "2026", "2026-10-18")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "2026-10-18.mp3"), []byte("ID3 the mp3 file"), 0o644)
	const rec = `"recording":"2026/2026-10-18","label":"x"`
	refused := func(body, want string) {
		t.Helper()
		if w := do(st.studio, "POST", "/api/shares", `{"kind":"download",`+rec+`,`+body+`}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	refused(`"allFormats":true`, "save the sermon in the editor first")
	st.save(dir)

	code, view := st.makeShare(`{"kind":"download",` + rec + `,"allFormats":true}`)
	if !view.AllFormats || strings.Join(view.Formats, ",") != "flac,mp3,opus" {
		t.Errorf("view: %+v", view)
	}
	if info := st.info(code); len(info.Files) != 1 || info.Files[0].Format != "mp3" || len(info.Preparing) != 0 {
		t.Errorf("info: %+v", info)
	}
	os.WriteFile(filepath.Join(dir, "2026-10-18.opus"), []byte("OggS"), 0o644)
	if info := st.info(code); len(info.Files) != 2 {
		t.Errorf("a format made later is given: %+v", info)
	}
	refused(`"formats":["flac","mp3"]`, "not made yet: FLAC (make it from the Download menu)")
	refused(`"allFormats":true,"transcript":true`, "there is no transcript of this sermon yet")

	// With a transcript there, the share gives its text and captions.
	p, _ := normalizer.NewPaths(dir)
	text, captions, data := transcriptPaths(p)
	tf, _ := json.Marshal(transcriptFile{Language: "nb", Model: nbMedium, Segments: []segmentJSON{{0, 2, "Hei."}}})
	os.WriteFile(data, tf, 0o644)
	os.WriteFile(text, []byte("Hei.\n"), 0o644)
	os.WriteFile(captions, []byte("WEBVTT\n"), 0o644)
	code, view = st.makeShare(`{"kind":"download",` + rec + `,"allFormats":true,"transcript":true}`)
	if !view.Transcript {
		t.Errorf("view: %+v", view)
	}
	info := st.info(code)
	var formats []string
	for _, f := range info.Files {
		formats = append(formats, f.Format)
	}
	if strings.Join(formats, ",") != "mp3,opus,txt,vtt" {
		t.Fatalf("info: %+v", info)
	}
	c, addr := st.download(code, "1.1.1.1", "txt")
	resp := st.fetch(addr, "")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if c != http.StatusCreated || string(got) != "Hei.\n" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("text: %d %q %s", c, got, resp.Header.Get("Content-Type"))
	}
	counted := func() int {
		for _, s := range decode[struct{ Shares []shareView }](t, do(st.studio, "GET", "/api/shares", "")).Shares {
			if s.ID == view.ID {
				return s.Count
			}
		}
		return -1
	}
	if n := counted(); n != 0 {
		t.Errorf("the transcript was counted: %d", n)
	}
	// Read on the page: the text inline, not counted, nor in the share's log; the captions are not read there.
	read := func(format string) (int, string) {
		b, _ := json.Marshal(map[string]any{"format": format, "listen": true})
		w := st.call("POST", "/api/share/downloads", code, "2.2.2.2", b)
		var out struct{ URL string }
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out.URL
	}
	c, addr = read("txt")
	resp = st.fetch(addr, "")
	got, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if c != http.StatusCreated || string(got) != "Hei.\n" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline") {
		t.Errorf("read: %d %q %v", c, got, resp.Header)
	}
	for _, s := range decode[struct{ Shares []shareView }](t, do(st.studio, "GET", "/api/shares", "")).Shares {
		if s.ID == view.ID && (s.Count != 0 || len(s.Log) != 1) {
			t.Errorf("reading was counted or logged: %+v", s)
		}
	}
	// The captions too, for following the player; with files and a transcript of the same cut, where the
	// sermon starts in each.
	c, addr = read("vtt")
	resp = st.fetch(addr, "")
	got, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if c != http.StatusCreated || string(got) != "WEBVTT\n" {
		t.Errorf("captions: %d %q", c, got)
	}
	if f := st.info(code).Follow; len(f) != 0 {
		t.Errorf("followed without records: %v", f)
	}
	pause := 1.5
	st.app.tenants[0].srv.updateState(dir, func(fs *folderState) error {
		fs.Outputs["mp3"] = outputMade{Cut: "c1", Intro: &clipRef{ID: "i", Seconds: 10}, Pause: &pause}
		fs.Outputs["opus"] = outputMade{Cut: "c0"}
		fs.Outputs[transcriptFormat] = outputMade{Cut: "c1"}
		return nil
	})
	if f := st.info(code).Follow; len(f) != 1 || f["mp3"] != 11.5 {
		t.Errorf("follow: %v", f)
	}
	st.download(code, "1.1.1.1", "mp3")
	if n := counted(); n != 1 {
		t.Errorf("the MP3 was not counted: %d", n)
	}
	// A share without the transcript does not give it.
	plain, _ := st.makeShare(`{"kind":"download",` + rec + `,"formats":["mp3"]}`)
	if c, _ := st.download(plain, "1.1.1.1", "txt"); c != http.StatusBadRequest {
		t.Errorf("the transcript from a share without it: %d", c)
	}
}

// While a run of the recording is making a format the share gives, the share page says it is being prepared,
// and does not offer the file that is being replaced.
func TestShareDownloadPreparing(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","allFormats":true,"label":"x"}`)
	dir := filepath.Join(st.church, "Taler", "2026", "2026-10-18 Advent")
	os.WriteFile(filepath.Join(dir, "2026-10-18 Advent_raw.wav"), []byte("x"), 0o644)
	srv := st.app.tenants[0].srv
	srv.jobs.sem <- struct{}{} // another encode is running: this one stays queued
	defer func() { <-srv.jobs.sem }()
	if w := do(st.studio, "POST", "/api/make?path="+url.QueryEscape("2026/2026-10-18 Advent")+"&format=opus", ""); w.Code != http.StatusAccepted {
		t.Fatalf("make: %d %s", w.Code, w.Body)
	}
	if info := st.info(code); len(info.Files) != 2 || strings.Join(info.Preparing, ",") != "opus" {
		t.Errorf("info: %+v", info)
	}
	srv.jobs.active("2026/2026-10-18 Advent").cancel()
}

// Listening gets an address for the page's player: the file inline, not counted as a download, nor in the
// log or the studio's downloads; only for the sermon's formats, and only on a share without a limit on its
// downloads (the player's file could be kept).
func TestShareListen(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Lytt"}`)
	listen := func(code, ip, format string) (int, string) {
		b, _ := json.Marshal(map[string]any{"format": format, "listen": true})
		w := st.call("POST", "/api/share/downloads", code, ip, b)
		var out struct{ URL string }
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out.URL
	}
	c, addr := listen(code, "1.1.1.1", "mp3")
	if c != http.StatusCreated {
		t.Fatalf("listen: %d", c)
	}
	if c, again := listen(code, "1.1.1.1", "mp3"); c != http.StatusOK || again != addr {
		t.Errorf("again: %d %q", c, again)
	}
	resp := st.fetch(addr, "bytes=4-")
	if b := readAll(resp); resp.StatusCode != http.StatusPartialContent || b != "the mp3 file" ||
		resp.Header.Get("Content-Disposition") != `inline; filename="2026-10-18 Advent.mp3"` {
		t.Errorf("file: %d %q %v", resp.StatusCode, b, resp.Header)
	}
	list := decode[struct{ Shares []shareView }](t, do(st.studio, "GET", "/api/shares", ""))
	if s := list.Shares[0]; s.Count != 0 || len(s.Log) != 0 || s.Resumable != 0 {
		t.Errorf("in the studio: %+v", s)
	}
	if c, _ := listen(code, "1.1.1.1", "txt"); c != http.StatusBadRequest {
		t.Errorf("reading a transcript the share does not give: %d", c)
	}
}

// A studio page loaded before download shares lost their limit still sends maxDownloads: it is ignored, and
// the share gives as many downloads as are asked for.
func TestShareDownloadOldPage(t *testing.T) {
	st, code := downloadTest(t, `{"kind":"download","recording":"2026/2026-10-18 Advent","formats":["mp3"],"label":"Old page","maxDownloads":1}`)
	for _, ip := range []string{"1.1.1.1", "2.2.2.2"} {
		if c, _ := st.download(code, ip, "mp3"); c != http.StatusCreated {
			t.Errorf("download from %s: %d", ip, c)
		}
	}
	id := decode[struct{ Shares []shareView }](t, do(st.studio, "GET", "/api/shares", "")).Shares[0].ID
	if w := do(st.studio, "PUT", "/api/shares/"+id, `{"label":"Old page","formats":["mp3"],"maxDownloads":3}`); w.Code != http.StatusOK {
		t.Errorf("edit from an old page: %d %s", w.Code, w.Body)
	}
}

// Where the sermon starts in a shared file: after the intro and its pause, at 0 without one or for the sermon
// only, and not at all when the file and the transcript are of different cuts.
func TestSermonStart(t *testing.T) {
	two, one := 2.0, 1.0
	intro := &clipRef{ID: "i", Seconds: 8.5}
	st := &folderState{Saved: &sermonSettings{ClipPause: &one}, Outputs: map[string]outputMade{
		transcriptFormat:               {Cut: "c"},
		"mp3":                          {Cut: "c", Intro: intro, Pause: &two},
		"opus":                         {Cut: "c", Intro: intro}, // made before the pause was kept: the saved one
		"flac":                         {Cut: "c"},
		"mp3" + normalizer.SermonOnly:  {Cut: "c"},
		"opus" + normalizer.SermonOnly: {Cut: "old"},
	}}
	for _, c := range []struct {
		format     string
		sermonOnly bool
		at         float64
		ok         bool
	}{{"mp3", false, 10.5, true}, {"opus", false, 9.5, true}, {"flac", false, 0, true},
		{"mp3", true, 0, true}, {"opus", true, 0, false}, {"flac", true, 0, false}} {
		if at, ok := sermonStart(st, c.sermonOnly, c.format); at != c.at || ok != c.ok {
			t.Errorf("%s (sermon only %v): %g %v", c.format, c.sermonOnly, at, ok)
		}
	}
}
