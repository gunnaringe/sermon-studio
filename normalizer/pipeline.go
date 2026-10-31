package normalizer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

// Pipeline runs the steps for one date folder.
type Pipeline struct {
	P   Paths
	Opt Options
	FF  FFmpeg
	Out io.Writer

	// Optional hooks for a UI: OnStep gets each step title, OnProgress the
	// fraction (0..1) done of the current ffmpeg encode.
	OnStep     func(title string)
	OnProgress func(fraction float64)

	raw     string   // the whole recording, if any
	master  string   // the master of the sermon only
	full    string   // the master with the intro and outro (p.master without them)
	cleanup []string // temporary files to remove when the run ends
	stepN   int      // the number of the last numbered step
}

func (p *Pipeline) logf(format string, a ...any) { fmt.Fprintf(p.Out, format+"\n", a...) }

// step starts a step of the run. A numbered one gets the next number (1, 2, 3 … in
// the order they run, with or without an intro); the result is not numbered.
func (p *Pipeline) step(title string) {
	p.logf("\n== %s", title)
	if p.OnStep != nil {
		p.OnStep(title)
	}
}

func (p *Pipeline) numbered(title string) {
	p.stepN++
	p.step(fmt.Sprintf("%d. %s", p.stepN, title))
}

func (p *Pipeline) warnf(format string, a ...any) {
	p.logf("WARNING: "+format, a...)
}

// runTo runs ffmpeg into dst, reporting progress against total seconds when
// OnProgress is set and total is known.
func (p *Pipeline) runTo(ctx context.Context, dst string, total float64, args ...string) error {
	if p.OnProgress == nil || total <= 0 {
		return p.FF.RunTo(ctx, dst, args...)
	}
	p.OnProgress(0)
	return p.FF.RunToProgress(ctx, dst, func(s float64) { p.OnProgress(math.Min(s/total, 1)) }, args...)
}

// lengthFor is the length of path in seconds if progress is reported, else 0.
func (p *Pipeline) lengthFor(ctx context.Context, path string) float64 {
	if p.OnProgress == nil {
		return 0
	}
	d, _ := p.FF.Duration(ctx, path)
	return d
}

// Run runs the whole flow.
func (p *Pipeline) Run(ctx context.Context) error {
	defer func() {
		for _, f := range p.cleanup {
			Remove(f)
		}
	}()
	if !p.Opt.cutting() {
		return errors.New("select where the sermon starts and ends")
	}
	for _, c := range [][2]string{{"intro", p.Opt.Intro}, {"outro", p.Opt.Outro}} {
		if c[1] != "" && !exists(c[1]) {
			return fmt.Errorf("the %s (%s) is missing", c[0], filepath.Base(c[1]))
		}
	}
	p.stepN = 0
	if err := p.stepRaw(ctx); err != nil {
		return err
	}
	if err := p.stepMaster(ctx); err != nil {
		return err
	}
	if err := p.stepClips(ctx); err != nil {
		return err
	}
	if err := p.stepLossy(ctx); err != nil {
		return err
	}
	return p.report(ctx)
}

// --- raw: the whole recording, of any of the InputTypes -----------------

func (p *Pipeline) stepRaw(ctx context.Context) error {
	p.numbered("Raw recording")
	if err := os.MkdirAll(p.P.Original, 0o755); err != nil {
		return err
	}
	raw, err := p.ingest(ctx)
	if err != nil {
		return err
	}
	if raw == "" {
		return errors.New("no raw recording to cut")
	}
	p.raw = raw
	p.logf("Cutting %s from %s to %s%s while making the master.", filepath.Base(p.raw), p.cutFrom(), p.cutTo(), p.cutsNote())
	return nil
}

// ingest brings the recording in the date folder into original/ and returns
// where it is there ("" if there is none).
//   - One that is kept as it is (FLAC, compressed audio) is moved.
//   - Other lossless audio (WAV, AIFF) is made into FLAC there, and a video's
//     sound copied out (both are normally done as it arrives); the source is
//     deleted only when the audio is checked to be identical.
//
// A recording already in original/ is replaced only with Force.
func (p *Pipeline) ingest(ctx context.Context) (string, error) {
	have := p.P.RawOriginal()
	src := p.P.findRaw(p.P.Folder)
	switch {
	case src == "":
		if have != "" {
			p.logf("Using %s.", filepath.Base(have))
		}
		return have, nil
	case have != "" && !p.Opt.Force:
		p.warnf("%s is already in original/; leaving %s in the folder.", filepath.Base(have), filepath.Base(src))
		return have, nil
	}
	dst := filepath.Join(p.P.Original, p.originalFile(filepath.Ext(src)))
	if to := ConvertsTo(src); to == "" {
		if err := MoveFile(src, dst); err != nil {
			return "", err
		}
		p.logf("Moved %s to %s.", filepath.Base(src), dst)
	} else {
		dst = filepath.Join(p.P.Original, p.originalFile(filepath.Ext(to)))
		var progress func(float64)
		if total := p.lengthFor(ctx, src); total > 0 {
			p.OnProgress(0)
			progress = func(s float64) { p.OnProgress(math.Min(s/total, 1)) }
		}
		if err := p.FF.Convert(ctx, src, dst, progress); err != nil {
			return "", err
		}
		if err := Remove(src); err != nil {
			return "", err
		}
		p.logf("OK: %s has exactly the audio of %s. Deleted %s.", filepath.Base(dst), filepath.Base(src), filepath.Base(src))
	}
	if have != "" && have != dst { // the one it replaces, of another type
		Remove(have)
	}
	return dst, nil
}

// pending reports whether an output file needs to be made.
func (p *Pipeline) pending(path string) bool { return p.Opt.Force || !exists(path) }

// version is one set of outputs: the main files, or with an intro or outro also the sermon only.
type version struct {
	label             string // for the log: "" for the main files
	master, mp3, opus string
	from              string // the master MP3 and Opus are made from: p.full or p.master
}

// versions are the sets of outputs a run makes.
func (p *Pipeline) versions() []version {
	main := version{"", p.P.Master, p.P.Mp3, p.P.Opus, p.full}
	if !p.Opt.clips() {
		return []version{main}
	}
	return []version{main, {" (sermon only)", p.P.SermonMaster, p.P.SermonMp3, p.P.SermonOpus, p.master}}
}

// lossyPending reports whether an MP3 or Opus of v needs to be made.
func (p *Pipeline) lossyPending(v version) bool {
	o := p.Opt.Outputs
	return (o.Mp3 && p.pending(v.mp3)) || (o.Opus && p.pending(v.opus))
}

// --- master ------------------------------------------------------------

// stepMaster makes the master of the sermon: <date>.flac, or with an intro or outro <date>_sermon.flac,
// which the one with them is made from.
func (p *Pipeline) stepMaster(ctx context.Context) error {
	o := p.Opt.Outputs
	vs := p.versions()
	p.master = vs[len(vs)-1].master
	needed := false // by an output made from it
	for _, v := range vs {
		needed = needed || p.lossyPending(v)
	}
	if p.Opt.clips() {
		needed = needed || (o.Flac && p.pending(p.P.Master))
	}
	switch {
	case o.Flac:
		p.numbered(fmt.Sprintf("Master (%s, %g LUFS)", p.Opt.Channels(), p.Opt.TargetLUFS))
		if !p.pending(p.master) {
			p.logf("Already exists, skipping (Save and generate redoes it).")
			return nil
		}
	case !needed:
		return nil // no master wanted, and nothing to make from one
	case !p.pending(p.master):
		p.numbered("Master")
		p.logf("Using the existing %s for MP3/Opus.", filepath.Base(p.master))
		return nil
	default:
		// Not wanted as output, but MP3/Opus are made from it. Kept in the
		// date folder (it can be large) under a hidden name, removed after the run.
		p.numbered(fmt.Sprintf("Master (temporary, for MP3/Opus; %s, %g LUFS)", p.Opt.Channels(), p.Opt.TargetLUFS))
		p.master = filepath.Join(p.P.Folder, "."+p.P.Date+".master.flac")
		p.cleanup = append(p.cleanup, p.master)
	}
	if p.Opt.Stereo {
		p.checkStereo(ctx)
	}
	var length float64
	if p.Opt.FadeIn > 0 || p.Opt.FadeOut > 0 {
		var err error
		if length, err = p.sermonLength(ctx); err != nil {
			return err
		}
		if p.Opt.FadeIn+p.Opt.FadeOut > length {
			return fmt.Errorf("fades (%g s + %g s) are longer than the sermon (%.1f s)", p.Opt.FadeIn, p.Opt.FadeOut, length)
		}
		p.logf("Fade in %g s, fade out %g s (sermon is %s).", p.Opt.FadeIn, p.Opt.FadeOut, FormatTime(math.Round(length)))
	}
	if length == 0 && p.OnProgress != nil {
		length, _ = p.sermonLength(ctx)
	}
	return p.renderMaster(ctx, p.master, length, []string{"-c:a", "flac", "-sample_fmt", "s32"})
}

// --- intro and outro ---------------------------------------------------

// stepClips makes the master with the intro and outro (<date>.flac) from the sermon's.
func (p *Pipeline) stepClips(ctx context.Context) error {
	p.full = p.master
	if !p.Opt.clips() {
		return nil
	}
	o := p.Opt.Outputs
	p.full = p.P.Master
	switch {
	case o.Flac:
		p.numbered("Intro and outro")
		if !p.pending(p.full) {
			p.logf("Already exists, skipping (Save and generate redoes it).")
			return nil
		}
	case !p.lossyPending(p.versions()[0]):
		return nil
	case !p.pending(p.full):
		p.numbered("Intro and outro")
		p.logf("Using the existing %s for MP3/Opus.", filepath.Base(p.full))
		return nil
	default:
		p.numbered("Intro and outro")
		p.full = filepath.Join(p.P.Folder, "."+p.P.Date+".full.flac")
		p.cleanup = append(p.cleanup, p.full)
	}
	return p.join(ctx, p.full)
}

// checkStereo warns when stereo output would add nothing: the part that
// becomes the sermon is mono, or its two channels are (nearly) the same.
func (p *Pipeline) checkStereo(ctx context.Context) {
	c, err := p.FF.CheckChannels(ctx, p.raw, p.cutArgs()...)
	switch {
	case err != nil:
		p.warnf("could not check the channels: %v", err)
	case c.Channels == 1:
		p.warnf("the source is mono, so stereo output is the same signal on both sides: " +
			"same sound, bigger files. Choose mono.")
	case c.Channels == 2 && math.IsInf(c.Side, -1):
		p.warnf("the two channels of the source are identical, so stereo output adds nothing: " +
			"same sound, bigger files. Choose mono.")
	case c.EffectivelyMono():
		p.warnf("the two channels of the source are nearly identical (their difference is %.0f dB below the signal), "+
			"so stereo output adds next to nothing. Choose mono.", -c.Side)
	}
	if side, by := c.Quieter(); side != "" {
		how := fmt.Sprintf("%.0f dB quieter than the other", by)
		if math.IsInf(by, 1) {
			how = "silent"
		}
		p.warnf("the %s channel of the source is %s, so stereo output has the sermon on one side only. "+
			"Choose mono, which mixes both channels.", side, how)
	}
}

// sermonLength is the length in seconds of what goes into the master.
func (p *Pipeline) sermonLength(ctx context.Context) (float64, error) {
	end, err := p.FF.Duration(ctx, p.raw)
	if err != nil {
		return 0, err
	}
	return p.Opt.Length(end), nil
}

// Length is how long the sermon is, in seconds, cut from a recording of duration seconds: the parts kept
// and the pauses put in, without the clips.
func (o Options) Length(duration float64) float64 {
	var length float64
	kept, after := o.parts(duration) // an end past the end of the recording just runs to the end
	for _, k := range kept {
		length += k.End - k.Start
	}
	return length + pauseTotal(after)
}

// --- mp3 + opus --------------------------------------------------------

func (p *Pipeline) stepLossy(ctx context.Context) error {
	o := p.Opt.Outputs
	var names []string
	if o.Mp3 {
		names = append(names, fmt.Sprintf("MP3 (%s)", p.Opt.Mp3Bitrate))
	}
	if o.Opus {
		names = append(names, fmt.Sprintf("Opus (%s)", p.Opt.OpusBitrate))
	}
	if len(names) == 0 {
		return nil
	}
	p.numbered(strings.Join(names, " and ") + ", " + p.Opt.Channels())
	ac := "1"
	if p.Opt.Stereo {
		ac = "2"
	}
	for _, v := range p.versions() {
		if o.Mp3 {
			if err := p.encodeLossy(ctx, "mp3"+v.label, v.from, v.mp3,
				[]string{"-ar", "44100", "-ac", ac, "-c:a", "libmp3lame", "-b:a", p.Opt.Mp3Bitrate, "-id3v2_version", "3"}); err != nil {
				return err
			}
		}
		if o.Opus {
			if err := p.encodeLossy(ctx, "opus"+v.label, v.from, v.opus,
				[]string{"-ar", "48000", "-ac", ac, "-c:a", "libopus", "-b:a", p.Opt.OpusBitrate, "-application", "voip"}); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- report ---------------------------------------------------------------

func (p *Pipeline) report(ctx context.Context) error {
	p.step(fmt.Sprintf("Result (target: %g LUFS, max %g dBTP)", p.Opt.TargetLUFS, p.Opt.TruePeak))
	tw := tabwriter.NewWriter(p.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "File\tLUFS\tTP\tLRA\tMB")
	var files []string
	for _, v := range p.versions() {
		for _, f := range []struct {
			on   bool
			path string
		}{{p.Opt.Outputs.Flac, v.master}, {p.Opt.Outputs.Mp3, v.mp3}, {p.Opt.Outputs.Opus, v.opus}} {
			if f.on {
				files = append(files, f.path)
			}
		}
	}
	for _, f := range files {
		m, err := p.FF.Measure(ctx, f)
		if err != nil {
			return err
		}
		st, _ := os.Stat(f)
		fmt.Fprintf(tw, "%s\t%.1f\t%.1f\t%.1f\t%.1f\n", filepath.Base(f), m.LUFS, m.TruePeak, m.LRA, float64(st.Size())/1048576)
		if m.TruePeak > -1.0 {
			defer p.warnf("%s: true peak %.1f dBTP is above -1.", filepath.Base(f), m.TruePeak)
		}
		if math.Abs(m.LUFS-p.Opt.TargetLUFS) > 1.5 {
			defer p.warnf("%s: %.1f LUFS is more than 1.5 from the target (loud peaks in the recording can keep it from getting louder: try compression).", filepath.Base(f), m.LUFS)
		}
	}
	return tw.Flush()
}
