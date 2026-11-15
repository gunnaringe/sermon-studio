package normalizer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	return bestInput(p.Original, func(string) bool { return true })
}

// arrival is the whole recording in the date folder, not yet moved into original/, or "": <date>_raw.<ext>
// (as all were named before), else the file of an input type there that the studio did not make (Ours).
// Only a folder named after a date has one.
func (p Paths) arrival() string {
	if f := p.findRaw(p.Folder); f != "" {
		return f
	}
	if !datedName(p.Date) {
		return ""
	}
	return bestInput(p.Folder, func(name string) bool { return !Ours(p.Date, name) })
}

// bestInput is the file in dir of an input type that ok takes, the first type of InputTypes if there are
// several (the FLAC a conversion made before its WAV, the sound of a video before the video). Hidden files
// and ones being written are never recordings.
func bestInput(dir string, ok func(name string) bool) string {
	des, _ := os.ReadDir(dir)
	best, rank := "", len(inputTypes)
	for _, de := range des {
		name := de.Name()
		if !de.Type().IsRegular() || strings.HasPrefix(name, ".") || IsPartial(name) || !ok(name) {
			continue
		}
		for i, t := range inputTypes {
			if i < rank && strings.EqualFold(filepath.Ext(name), t.ext) {
				best, rank = filepath.Join(dir, name), i
			}
		}
	}
	return best
}

// datedName reports whether a folder name is that of a recording: <date> or <date> <name>.
func datedName(name string) bool {
	if len(name) < 10 || (len(name) > 10 && name[10] != ' ') {
		return false
	}
	_, err := time.Parse("2006-01-02", name[:10])
	return err == nil
}

// Ours reports whether name is a file the studio makes in the folder of a recording called folder (its
// outputs <folder>.<ext>, <folder>_sermon.<ext>, and hidden work files), not the recording.
// A recording that arrives with such a name is given another (RawName).
func Ours(folder, name string) bool {
	if strings.HasPrefix(name, ".") || IsPartial(name) {
		return true
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	return strings.EqualFold(stem, folder) || strings.EqualFold(stem, folder+SermonOnly)
}

// IsArrival reports whether name, in the folder of a recording called folder, is the recording (of an input
// type, not one the studio made), as arrival finds it.
func IsArrival(folder, name string) bool {
	_, ok := KindOf(name)
	return ok && datedName(folder) && !Ours(folder, name)
}

// RawName is the name of a recording in its folder (called folder): stem, the name it arrived with, and
// ext, unless stem is empty or the studio uses that name itself (Ours); then <folder>_raw<ext>.
func RawName(folder, stem, ext string) string {
	if stem == "" || Ours(folder, stem+ext) {
		return folder + rawName + ext
	}
	return stem + ext
}

// originalFile is the name a recording src being moved into original/ gets, with the extension of what is
// kept (ext): the name it arrived with (OriginalName), else the one it has.
func (p *Pipeline) originalFile(src, ext string) string {
	if p.Opt.OriginalName != "" {
		return p.Opt.OriginalName + ext
	}
	return strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)) + ext
}

// rawName is what the name of a recording without one of its own ends in: <date>_raw.<ext>, ext one of
// InputTypes.
const rawName = "_raw"

// findRaw is the recording named <date>_raw.<ext> in dir, or "".
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
	if raw := p.arrival(); raw != "" {
		return raw
	}
	return p.RawOriginal()
}
