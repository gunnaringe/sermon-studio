package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Spec is a model to pull: a file of a Hugging Face repository at a revision, for some languages.
type Spec struct {
	Languages []string
	Repo      string // owner/name
	Path      string // the file in it
	Revision  string // "main" unless the URL names one
}

var (
	languageCode = regexp.MustCompile(`^([a-z]{2,3}|\*)$`)
	repoName     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	revisionName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	filePath     = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`) // as it goes into the URL
)

// ParseSpec reads LANGS=MODEL: the languages, comma separated ("nb,nn", or "*" for any), and the model as
// owner/name/file (at main) or the URL of the file on Hugging Face (…/resolve/<revision>/<file>).
func ParseSpec(s string) (Spec, error) {
	langs, model, ok := strings.Cut(s, "=")
	if !ok {
		return Spec{}, fmt.Errorf("%q: give the languages and the model, like nb,nn=NbAiLab/nb-whisper-medium/ggml-model-q5_0.bin", s)
	}
	var sp Spec
	for _, l := range strings.Split(langs, ",") {
		if !languageCode.MatchString(l) {
			return Spec{}, fmt.Errorf("%q: %q is not a language code (nb, nn, sv, en ..., or *)", s, l)
		}
		sp.Languages = append(sp.Languages, l)
	}
	sp.Revision = "main"
	if u, err := url.Parse(model); err == nil && u.Scheme != "" {
		if u.Scheme != "https" || u.Host != "huggingface.co" {
			return Spec{}, fmt.Errorf("%q: only files on https://huggingface.co/", s)
		}
		parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 5)
		if len(parts) != 5 || parts[2] != "resolve" {
			return Spec{}, fmt.Errorf("%q: the URL of a file is https://huggingface.co/<owner>/<name>/resolve/<revision>/<file>", s)
		}
		sp.Repo, sp.Revision, sp.Path = parts[0]+"/"+parts[1], parts[3], parts[4]
	} else {
		parts := strings.SplitN(model, "/", 3)
		if len(parts) != 3 {
			return Spec{}, fmt.Errorf("%q: the model is <owner>/<name>/<file>", s)
		}
		sp.Repo, sp.Path = parts[0]+"/"+parts[1], parts[2]
	}
	if !repoName.MatchString(sp.Repo) || !revisionName.MatchString(sp.Revision) || !filePath.MatchString(sp.Path) ||
		path.Clean(sp.Path) != sp.Path || strings.HasPrefix(sp.Path, "/") || strings.HasPrefix(sp.Path, "..") {
		return Spec{}, fmt.Errorf("%q: not a model on Hugging Face", s)
	}
	return sp, nil
}

// name is the model's name: the repository's ("nb-whisper-medium"), with the file's when it is one of
// several models there (whisper.cpp's own: "whisper.cpp ggml-small-q5_1").
func (sp Spec) name() string {
	base := path.Base(sp.Repo)
	file := strings.TrimSuffix(path.Base(sp.Path), path.Ext(sp.Path))
	if strings.HasPrefix(file, "ggml-model") {
		return base
	}
	return base + " " + file
}

func (sp Spec) key() string { return sp.Repo + "/" + sp.Path }

// remote is what Hugging Face says of the file at the revision.
type remote struct {
	Commit string
	SHA256 string
	Size   int64
	Date   time.Time
}

// Puller downloads models into a folder.
type Puller struct {
	Dir    string
	Logger *log.Logger
	Client *http.Client
	// Hub is Hugging Face's address; a test's server in tests.
	Hub string
}

func (p *Puller) hub() string {
	if p.Hub != "" {
		return p.Hub
	}
	return "https://huggingface.co"
}

func (p *Puller) get(ctx context.Context, method, u string, body []byte, v any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "sermon-studio (https://sermon-studio.app)")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", u, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// scanStatus is a security scan's verdict in Hugging Face's API.
type scanStatus struct {
	Status string `json:"status"`
}

// ErrUnsafe is a file Hugging Face's security scans have not found safe.
var ErrUnsafe = errors.New("not marked safe by Hugging Face's security scans")

// lookup asks Hugging Face which commit the revision is, and the file's SHA-256, size, date and scans.
// A file is taken only when the scans call it safe (securityFileStatus.status) and none of them finds
// anything (a scan that has not run, "unscanned" or "queued", finds nothing).
func (p *Puller) lookup(ctx context.Context, sp Spec) (remote, error) {
	var rev struct {
		SHA string `json:"sha"`
	}
	if err := p.get(ctx, http.MethodGet, p.hub()+"/api/models/"+sp.Repo+"/revision/"+url.PathEscape(sp.Revision), nil, &rev); err != nil {
		return remote{}, err
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(rev.SHA) {
		return remote{}, fmt.Errorf("%s: no commit for %s", sp.Repo, sp.Revision)
	}
	body, _ := json.Marshal(map[string]any{"paths": []string{sp.Path}, "expand": true})
	var files []struct {
		Path string `json:"path"`
		LFS  *struct {
			OID  string `json:"oid"`
			Size int64  `json:"size"`
		} `json:"lfs"`
		LastCommit *struct {
			Date time.Time `json:"date"`
		} `json:"lastCommit"`
		Security map[string]json.RawMessage `json:"securityFileStatus"`
	}
	if err := p.get(ctx, http.MethodPost, p.hub()+"/api/models/"+sp.Repo+"/paths-info/"+rev.SHA, body, &files); err != nil {
		return remote{}, err
	}
	if len(files) != 1 || files[0].Path != sp.Path || files[0].LFS == nil {
		return remote{}, fmt.Errorf("%s: no file %s", sp.Repo, sp.Path)
	}
	f := files[0]
	var overall string
	if raw, ok := f.Security["status"]; ok {
		json.Unmarshal(raw, &overall)
	}
	if overall != "safe" {
		return remote{}, fmt.Errorf("%s: %w (%q)", sp.key(), ErrUnsafe, overall)
	}
	for name, raw := range f.Security {
		var s scanStatus
		if name == "status" || json.Unmarshal(raw, &s) != nil {
			continue
		}
		if s.Status != "safe" && s.Status != "unscanned" && s.Status != "queued" {
			return remote{}, fmt.Errorf("%s: %w (%s: %q)", sp.key(), ErrUnsafe, name, s.Status)
		}
	}
	r := remote{Commit: rev.SHA, SHA256: f.LFS.OID, Size: f.LFS.Size}
	if f.LastCommit != nil {
		r.Date = f.LastCommit.Date.UTC()
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(r.SHA256) || r.Size <= 0 {
		return remote{}, fmt.Errorf("%s: no SHA-256 for %s", sp.Repo, sp.Path)
	}
	return r, nil
}

// fileName is where a version of a model is kept: its name and the start of its hash.
func fileName(sp Spec, sha string) string {
	clean := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(strings.ReplaceAll(sp.name(), " ", "-"), "-")
	return clean + "-" + sha[:12] + ".bin"
}

// Pull brings the folder in line with the specs: each one's file at its revision now, checked by its
// SHA-256, and models.json listing them in that order, then the models written there by hand. A model
// pull fetched before and that is no longer asked for is taken out, with its file. One that fails (no
// network, not safe) keeps the version that is there and is tried again the next time; the error says
// which.
func (p *Puller) Pull(ctx context.Context, specs []Spec) error {
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return err
	}
	old, err := Read(p.Dir)
	if err != nil {
		return err
	}
	byKey := map[string]Model{}
	var manual []Model
	for _, m := range old.Models {
		if m.Repo == "" {
			manual = append(manual, m)
		} else {
			byKey[m.Repo+"/"+m.Path] = m
		}
	}
	var next Manifest
	var errs []error
	for _, sp := range specs {
		m, err := p.pullOne(ctx, sp, byKey[sp.key()])
		if err != nil {
			errs = append(errs, err)
			if have, ok := byKey[sp.key()]; ok {
				have.Languages = sp.Languages
				next.Models = append(next.Models, have)
			}
			continue
		}
		next.Models = append(next.Models, m)
	}
	next.Models = append(next.Models, manual...)
	if err := next.write(p.Dir); err != nil {
		return err
	}
	p.sweep(next)
	return errors.Join(errs...)
}

// pullOne makes sure the folder has the spec's file as it is now: downloaded unless the same version is
// there.
func (p *Puller) pullOne(ctx context.Context, sp Spec, have Model) (Model, error) {
	r, err := p.lookup(ctx, sp)
	if err != nil {
		return Model{}, err
	}
	m := Model{Name: sp.name(), File: fileName(sp, r.SHA256), Languages: sp.Languages, Repo: sp.Repo, Path: sp.Path,
		Commit: r.Commit, Revision: sp.Revision, SHA256: r.SHA256, Size: r.Size}
	if !r.Date.IsZero() {
		m.Date = &r.Date
	}
	if have.SHA256 == r.SHA256 && have.File == m.File {
		if st, err := os.Stat(filepath.Join(p.Dir, m.File)); err == nil && st.Size() == r.Size {
			return m, nil
		}
	}
	p.Logger.Printf("downloading %s (%s, %d MB)", m.Name, r.Commit[:12], r.Size>>20)
	u := p.hub() + "/" + sp.Repo + "/resolve/" + r.Commit + "/" + sp.Path
	if err := p.download(ctx, u, filepath.Join(p.Dir, m.File), r); err != nil {
		return Model{}, fmt.Errorf("%s: %w", m.Name, err)
	}
	p.Logger.Printf("%s: downloaded", m.Name)
	return m, nil
}

// ErrHash is a download that is not the file Hugging Face says it is.
var ErrHash = errors.New("the downloaded file does not have the SHA-256 Hugging Face gives for it")

// download fetches the file into dst.partial, going on from where an earlier download stopped; checks its
// size and SHA-256 and renames it to dst.
func (p *Puller) download(ctx context.Context, u, dst string, r remote) error {
	tmp := dst + ".partial"
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	have, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if have > r.Size {
		if err := f.Truncate(0); err != nil {
			return err
		}
		have = 0
	}
	if have < r.Size {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "sermon-studio (https://sermon-studio.app)")
		if have > 0 {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
		}
		resp, err := p.Client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusPartialContent && have > 0:
			if _, err := f.Seek(have, io.SeekStart); err != nil {
				return err
			}
		case resp.StatusCode == http.StatusOK: // from the start
			if err := f.Truncate(0); err != nil {
				return err
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
		default:
			return fmt.Errorf("download: %s", resp.Status)
		}
		if _, err := io.Copy(f, io.LimitReader(resp.Body, r.Size)); err != nil {
			return fmt.Errorf("download: %w", err)
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != r.Size || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
		f.Close()
		os.Remove(tmp)
		return ErrHash
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// pulledFile is the name of a file pull makes (fileName, and its download).
var pulledFile = regexp.MustCompile(`-[0-9a-f]{12}\.bin(\.partial)?$`)

// sweep removes the files no model in m has: earlier versions and models no longer asked for. Only the
// files pull makes; a half download of a model in m is kept for next time.
func (p *Puller) sweep(m Manifest) {
	keep := map[string]bool{}
	for _, x := range m.Models {
		keep[x.File], keep[x.File+".partial"] = true, true
	}
	names, _ := filepath.Glob(filepath.Join(p.Dir, "*.bin*"))
	for _, n := range names {
		base := filepath.Base(n)
		if !keep[base] && pulledFile.MatchString(base) {
			if os.Remove(n) == nil {
				p.Logger.Printf("removed %s", base)
			}
		}
	}
}
