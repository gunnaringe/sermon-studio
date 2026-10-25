package normalizer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Paths are the files for one date folder.
type Paths struct {
	Folder   string
	Date     string
	Original string // where the whole recording is kept: <folder>/original
	Master   string
	Mp3      string
	Opus     string
	// The sermon only, without the intro and outro: made when a run has them, <date>_sermon.<ext>.
	SermonMaster, SermonMp3, SermonOpus string
}

// SermonOnly is what the names of the sermon-only outputs end in, before the extension.
const SermonOnly = "_sermon"

// OriginalDir is the folder in a date folder that holds the whole recording
// once a run has been made.
const OriginalDir = "original"

// NewPaths resolves the files for a date folder.
func NewPaths(folder string) (Paths, error) {
	abs, err := filepath.Abs(folder)
	if err != nil {
		return Paths{}, err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return Paths{}, fmt.Errorf("%s is not a directory", abs)
	}
	date := filepath.Base(abs)
	return Paths{
		Folder:   abs,
		Date:     date,
		Original: filepath.Join(abs, OriginalDir),
		Master:   filepath.Join(abs, date+".flac"),
		Mp3:      filepath.Join(abs, date+".mp3"),
		Opus:     filepath.Join(abs, date+".opus"),

		SermonMaster: filepath.Join(abs, date+SermonOnly+".flac"),
		SermonMp3:    filepath.Join(abs, date+SermonOnly+".mp3"),
		SermonOpus:   filepath.Join(abs, date+SermonOnly+".opus"),
	}, nil
}

// RawOriginal is the whole recording kept in original/ after a run, of any
// input type, or "" if there is none: <date>_raw.<ext>, else the one file of an
// input type there is (a recording keeps the name it arrived with).
func (p Paths) RawOriginal() string {
	if f := p.findRaw(p.Original); f != "" {
		return f
	}
	des, _ := os.ReadDir(p.Original)
	best, rank := "", len(inputTypes)
	for _, de := range des {
		if !de.Type().IsRegular() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		for i, t := range inputTypes {
			if i < rank && strings.EqualFold(filepath.Ext(de.Name()), t.ext) {
				best, rank = filepath.Join(p.Original, de.Name()), i
			}
		}
	}
	return best
}

// originalFile is the name a recording being moved into original/ gets: the one it arrived
// with (ext is the extension of what is kept), else <date>_raw.<ext>.
func (p *Pipeline) originalFile(ext string) string {
	if p.Opt.OriginalName == "" {
		return p.P.Date + rawName + ext
	}
	return p.Opt.OriginalName + ext
}

// rawName is the name of the whole recording: <date>_raw.<ext>, ext one of
// InputTypes.
const rawName = "_raw"

// findRaw is the whole recording in dir, or "".
func (p Paths) findRaw(dir string) string {
	for _, ext := range InputTypes() {
		if f := filepath.Join(dir, p.Date+rawName+ext); exists(f) {
			return f
		}
	}
	return ""
}

// inFolder is <date><suffix> in the date folder.
func (p *Pipeline) inFolder(suffix string) string {
	return filepath.Join(p.P.Folder, p.P.Date+suffix)
}

// Source is the whole recording a run would use, found the same way as the
// pipeline: in the date folder first (a new arrival), then in original/.
func (p Paths) Source() string {
	if raw := p.findRaw(p.Folder); raw != "" {
		return raw
	}
	return p.RawOriginal()
}
