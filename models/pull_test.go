package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hub is a fake Hugging Face: one repository whose main is commit, with one file, data, and its scans.
type hub struct {
	commit   string
	data     []byte
	security string // securityFileStatus
	sha      string // the SHA-256 it says the file has; "" = the right one
	gets     int    // downloads of the file
}

func (h *hub) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sum := sha256.Sum256(h.data)
		sha := hex.EncodeToString(sum[:])
		if h.sha != "" {
			sha = h.sha
		}
		switch {
		case r.URL.Path == "/api/models/Org/nb-whisper-medium/revision/main":
			json.NewEncoder(w).Encode(map[string]string{"sha": h.commit})
		case r.URL.Path == "/api/models/Org/nb-whisper-medium/paths-info/"+h.commit && r.Method == http.MethodPost:
			io.WriteString(w, `[{"type":"file","path":"ggml-model-q5_0.bin","lfs":{"oid":"`+sha+`","size":`+
				itoa(len(h.data))+`},"lastCommit":{"date":"2024-02-13T12:19:11.000Z"},"securityFileStatus":`+h.security+`}]`)
		case r.URL.Path == "/Org/nb-whisper-medium/resolve/"+h.commit+"/ggml-model-q5_0.bin":
			h.gets++
			http.ServeContent(w, r, "m.bin", time.Time{}, strings.NewReader(string(h.data)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

const safe = `{"status":"safe","avScan":{"status":"safe"},"pickleImportScan":{"status":"unscanned"}}`

func newPuller(t *testing.T, srv *httptest.Server) *Puller {
	return &Puller{Dir: t.TempDir(), Logger: log.New(io.Discard, "", 0), Client: srv.Client(), Hub: srv.URL}
}

var spec = Spec{Languages: []string{"nb", "nn"}, Repo: "Org/nb-whisper-medium", Path: "ggml-model-q5_0.bin", Revision: "main"}

func TestPull(t *testing.T) {
	h := &hub{commit: strings.Repeat("a", 40), data: []byte(strings.Repeat("ggml", 1000)), security: safe}
	p := newPuller(t, h.serve(t))
	// A model written by hand, and a stray file, are left alone.
	os.WriteFile(filepath.Join(p.Dir, "mine.bin"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(p.Dir, ManifestName), []byte(`{"models":[{"name":"mine","file":"mine.bin","languages":["da"]}]}`), 0o644)
	os.WriteFile(filepath.Join(p.Dir, "notes.bin"), []byte("x"), 0o644)
	if err := p.Pull(context.Background(), []Spec{spec}); err != nil {
		t.Fatal(err)
	}
	m, err := Read(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Models) != 2 || m.Models[0].Name != "nb-whisper-medium" || m.Models[1].Name != "mine" ||
		m.Models[0].Commit != h.commit || m.Models[0].Date == nil || m.Models[0].Date.Year() != 2024 {
		t.Fatalf("models.json: %+v", m)
	}
	first := m.Models[0]
	if got, _ := os.ReadFile(filepath.Join(p.Dir, first.File)); string(got) != string(h.data) {
		t.Error("the file")
	}
	if c, ok := Choose(m.Installed(p.Dir), "nn"); !ok || c.File != first.File {
		t.Errorf("choose nn: %+v", c)
	}
	// Again, unchanged: nothing is downloaded.
	if err := p.Pull(context.Background(), []Spec{spec}); err != nil || h.gets != 1 {
		t.Errorf("again: %v, %d downloads", err, h.gets)
	}
	// A new version on main: downloaded, and the old one removed. A half download goes on from where it was.
	h.commit, h.data = strings.Repeat("b", 40), []byte(strings.Repeat("GGML", 1000))
	sum := sha256.Sum256(h.data)
	part := filepath.Join(p.Dir, fileName(spec, hex.EncodeToString(sum[:]))+".partial")
	os.WriteFile(part, h.data[:1500], 0o644)
	if err := p.Pull(context.Background(), []Spec{spec}); err != nil {
		t.Fatal(err)
	}
	m, _ = Read(p.Dir)
	if m.Models[0].File == first.File || m.Models[0].Commit != h.commit || fileExists(filepath.Join(p.Dir, first.File)) || fileExists(part) {
		t.Errorf("new version: %+v", m.Models[0])
	}
	if got, _ := os.ReadFile(filepath.Join(p.Dir, m.Models[0].File)); string(got) != string(h.data) {
		t.Error("the resumed file")
	}
	if !fileExists(filepath.Join(p.Dir, "notes.bin")) || !fileExists(filepath.Join(p.Dir, "mine.bin")) {
		t.Error("removed a file pull did not make")
	}
	// Asked for no more: taken out, with its file.
	if err := p.Pull(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if m, _ = Read(p.Dir); len(m.Models) != 1 || m.Models[0].Name != "mine" {
		t.Errorf("after: %+v", m)
	}
}

// A file the scans do not call safe, or that is not what Hugging Face says, is not taken; the version
// there is kept.
func TestPullRefuses(t *testing.T) {
	for name, h := range map[string]*hub{
		"unsafe":       {security: `{"status":"unsafe","avScan":{"status":"unsafe"}}`},
		"not scanned":  {security: `{"status":"queued"}`},
		"a scan warns": {security: `{"status":"safe","protectAiScan":{"status":"caution"}}`},
		"no scans":     {security: `null`},
		"wrong hash":   {security: safe, sha: strings.Repeat("0", 64)},
	} {
		t.Run(name, func(t *testing.T) {
			h.commit, h.data = strings.Repeat("c", 40), []byte("ggml-model")
			p := newPuller(t, h.serve(t))
			err := p.Pull(context.Background(), []Spec{spec})
			if err == nil {
				t.Fatal("taken")
			}
			m, _ := Read(p.Dir)
			left, _ := filepath.Glob(filepath.Join(p.Dir, "*.bin*"))
			if len(m.Models) != 0 || len(left) != 0 {
				t.Errorf("left: %+v %v", m, left)
			}
		})
	}
}

func TestParseSpec(t *testing.T) {
	sp, err := ParseSpec("nb,nn=NbAiLab/nb-whisper-medium/ggml-model-q5_0.bin")
	if err != nil || sp.Repo != "NbAiLab/nb-whisper-medium" || sp.Path != "ggml-model-q5_0.bin" || sp.Revision != "main" || len(sp.Languages) != 2 {
		t.Errorf("%+v %v", sp, err)
	}
	if sp.name() != "nb-whisper-medium" {
		t.Errorf("name %q", sp.name())
	}
	sp, err = ParseSpec("*=https://huggingface.co/ggerganov/whisper.cpp/resolve/5359861c/ggml-small-q5_1.bin")
	if err != nil || sp.Repo != "ggerganov/whisper.cpp" || sp.Revision != "5359861c" || sp.Languages[0] != "*" || sp.name() != "whisper.cpp ggml-small-q5_1" {
		t.Errorf("%+v %v", sp, err)
	}
	for _, bad := range []string{"NbAiLab/x/y.bin", "nb=NbAiLab/x", "NB=a/b/c.bin", "nb=https://example.org/a/b/resolve/main/c.bin",
		"nb=http://huggingface.co/a/b/resolve/main/c.bin", "nb=a/b/../c.bin", "nb=a/b/resolve/main/c.bin?x", "nb=https://huggingface.co/a/b/blob/main/c.bin"} {
		if _, err := ParseSpec(bad); err == nil {
			t.Errorf("%q taken", bad)
		}
	}
}

func TestChoose(t *testing.T) {
	nb := Model{Name: "nb", File: "nb.bin", Languages: []string{"nb", "nn"}}
	all := Model{Name: "all", File: "all.bin", Languages: []string{"*"}}
	if m, _ := Choose([]Model{all, nb}, "nn"); m.Name != "nb" {
		t.Errorf("nn: %s", m.Name)
	}
	if m, _ := Choose([]Model{nb, all}, "sv"); m.Name != "all" {
		t.Errorf("sv: %s", m.Name)
	}
	if _, ok := Choose([]Model{nb}, "sv"); ok {
		t.Error("sv without a model")
	}
	if got := Languages([]Model{all, nb}); strings.Join(got, ",") != "nb,nn,*" {
		t.Errorf("languages %v", got)
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
