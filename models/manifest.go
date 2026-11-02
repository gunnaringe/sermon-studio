// Package models keeps a folder of speech recognition models (whisper.cpp's ggml files) for transcripts:
// models.json says which models are there and which languages each one is for. The studio only reads it
// (web/transcribe.go); `sermon-studio models pull` fills it from Hugging Face, or it is written by hand.
package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// ManifestName is the folder's list of its models.
const ManifestName = "models.json"

// AnyLanguage in a model's languages: it can transcribe any language, and find out which one is spoken.
const AnyLanguage = "*"

// Manifest is models.json.
type Manifest struct {
	// Models in the order they are chosen in: for a language, the first one that has it, else the first
	// for any language.
	Models []Model `json:"models"`
}

// Model is a model in the folder. Name, File and Languages are all a model written by hand needs; pull
// fills in the rest, which tells the studio which version made a transcript.
type Model struct {
	Name      string   `json:"name"`
	File      string   `json:"file"`      // in the folder
	Languages []string `json:"languages"` // whisper's codes ("nb" for Bokmål), or "*"
	// Where pull got it: the Hugging Face repository and the file in it, at Commit.
	Repo   string `json:"repo,omitempty"`
	Path   string `json:"path,omitempty"`
	Commit string `json:"commit,omitempty"`
	// Revision is what pull follows ("main", or a commit given in the URL).
	Revision string `json:"revision,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Size     int64  `json:"size,omitempty"`
	// Date is when the file last changed in its repository (its version, as a date).
	Date *time.Time `json:"date,omitempty"`
}

// Version tells two versions of a model apart: its SHA-256, else its file (a model written by hand).
func (m Model) Version() string {
	if m.SHA256 != "" {
		return m.SHA256
	}
	return m.File
}

// whisperSize finds Whisper's size in a model's name or file: tiny ... large-v3-turbo.
var whisperSize = regexp.MustCompile(`(?i)\b(tiny|base|small|medium|large(?:-v\d)?(?:-turbo)?|turbo)\b`)

// WhisperSize is the size of Whisper the model is (tiny, base, small, medium, large, large-v3, large-v3-turbo
// ...), from its name or file; "" when neither says.
func (m Model) WhisperSize() string {
	for _, s := range []string{m.Name, m.Path, m.File} {
		if found := whisperSize.FindString(strings.ReplaceAll(s, "_", "-")); found != "" {
			return strings.ToLower(found)
		}
	}
	return ""
}

// Same tells whether o is the same model as m, in any version: the same file in the same repository, or
// (written by hand) the same name.
func (m Model) Same(o Model) bool {
	if m.Repo != "" || o.Repo != "" {
		return m.Repo == o.Repo && m.Path == o.Path
	}
	return m.Name == o.Name
}

// Can tells whether the model can transcribe the language: it is for it, or for any language.
func (m Model) Can(lang string) bool { return m.For(lang) || m.For(AnyLanguage) }

// For tells whether the model is for the language.
func (m Model) For(lang string) bool { return slices.Contains(m.Languages, lang) }

// Read reads the folder's models.json; none is an empty list.
func Read(dir string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%s: %w", ManifestName, err)
	}
	for _, x := range m.Models {
		if x.Name == "" || len(x.Languages) == 0 || x.File == "" || x.File != filepath.Base(x.File) || strings.HasPrefix(x.File, ".") {
			return Manifest{}, fmt.Errorf("%s: a model needs a name, a file in the folder and its languages", ManifestName)
		}
	}
	return m, nil
}

// write saves models.json through a temporary file, so a reader never sees half of it.
func (m Manifest) write(dir string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+ManifestName+".partial")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, ManifestName))
}

// Installed are the models whose file is there.
func (m Manifest) Installed(dir string) []Model {
	var out []Model
	for _, x := range m.Models {
		if st, err := os.Stat(filepath.Join(dir, x.File)); err == nil && st.Mode().IsRegular() && (x.Size == 0 || st.Size() == x.Size) {
			out = append(out, x)
		}
	}
	return out
}

// Choose is the model for a language among the installed ones: the first for it, else the first for any
// language. AnyLanguage asks for one that finds the language itself.
func Choose(installed []Model, lang string) (Model, bool) {
	if lang != AnyLanguage {
		for _, x := range installed {
			if x.For(lang) {
				return x, true
			}
		}
	}
	for _, x := range installed {
		if x.For(AnyLanguage) {
			return x, true
		}
	}
	return Model{}, false
}

// Find is the installed model in file.
func Find(installed []Model, file string) (Model, bool) {
	for _, x := range installed {
		if x.File == file {
			return x, true
		}
	}
	return Model{}, false
}

// Languages are the languages the installed models can transcribe, in their order; AnyLanguage last
// when one finds the language itself.
func Languages(installed []Model) []string {
	var out []string
	anyLang := false
	for _, x := range installed {
		for _, l := range x.Languages {
			if l == AnyLanguage {
				anyLang = true
			} else if !slices.Contains(out, l) {
				out = append(out, l)
			}
		}
	}
	if anyLang {
		out = append(out, AnyLanguage)
	}
	return out
}
