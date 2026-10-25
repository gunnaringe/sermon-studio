package normalizer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

// renderMaster makes dst, the sermon with the processing of Opt, in three steps: the cut and the noise
// and levels (the slow part) into a temporary file; the loudness of that, measured, to find the gain that
// brings it to the target (with the compressor in between, if it is on); and the gain, the limiter and
// the fades into dst, measured again, with a correction if the limiter took some of it back.
// codec is for dst; length (seconds) is the sermon's, for the fade out and progress.
func (p *Pipeline) renderMaster(ctx context.Context, dst string, length float64, codec []string) error {
	tidy := tidyName(dst, "1")
	p.cleanup = append(p.cleanup, tidy)
	args, graph, err := p.cutInput(ctx)
	if err != nil {
		return err
	}
	if graph == "" {
		args = append(args, "-map", "0:a:0", "-af", p.tidyFilter()) // not a cover picture or video
	} else {
		args = append(args, "-filter_complex", graph+";[cut]"+p.tidyFilter()+"[out]", "-map", "[out]")
	}
	args = append(args, "-c:a", "flac", "-sample_fmt", "s32")
	if err := p.runTo(ctx, tidy, length, args...); err != nil {
		return err
	}

	src := tidy
	m, err := p.FF.Measure(ctx, tidy)
	if err != nil {
		return err
	}
	if m.LUFS < -70 {
		return errors.New("the sermon is silent: there is nothing to bring to the target loudness")
	}
	gain := math.Min(p.Opt.TargetLUFS-m.LUFS, 70)
	p.logf("Level after noise reduction and leveling: %.1f LUFS; gain %+.1f dB to reach %g.", m.LUFS, gain, p.Opt.TargetLUFS)
	if comp := compressionFilters[p.Opt.CompressionName()]; comp != "" {
		squeezed := tidyName(dst, "2")
		p.cleanup = append(p.cleanup, squeezed)
		if err := p.runTo(ctx, squeezed, length, "-i", tidy, "-af", fmt.Sprintf("volume=%.2fdB,%s", gain, comp), "-c:a", "flac", "-sample_fmt", "s32"); err != nil {
			return err
		}
		if m, err = p.FF.Measure(ctx, squeezed); err != nil {
			return err
		}
		src = squeezed
		gain = math.Min(p.Opt.TargetLUFS-m.LUFS, 70)
		p.logf("%s compression: %.1f LUFS; gain %+.1f dB to reach %g.", p.Opt.CompressionName(), m.LUFS, gain, p.Opt.TargetLUFS)
	}
	for try := 0; ; try++ {
		out := append([]string{"-i", src, "-af", p.finalFilter(gain, length)}, codec...)
		out = append(out, p.Opt.Tags.args()...)
		if err := p.runTo(ctx, dst, length, out...); err != nil {
			return err
		}
		got, err := p.FF.Measure(ctx, dst)
		if err != nil {
			return err
		}
		miss := p.Opt.TargetLUFS - got.LUFS
		if math.Abs(miss) <= 0.3 || try == 2 {
			return nil
		}
		// The limiter took some of the gain back: add it, and again.
		p.logf("%.1f LUFS after the limiter; %+.1f dB more.", got.LUFS, miss)
		gain += miss
	}
}

// Sample renders seconds of the recording from start with the processing in o (no cuts, pauses or fades) as an
// MP3 at dst, to hear what the settings do.
func (f FFmpeg) Sample(ctx context.Context, raw string, o Options, start, seconds float64, dst string) error {
	o.Start, o.End, o.Cuts, o.Pauses, o.FadeIn, o.FadeOut = start, start+seconds, nil, nil, 0, 0
	p := &Pipeline{FF: f, Opt: o, Out: io.Discard, raw: raw}
	defer func() {
		for _, name := range p.cleanup {
			Remove(name)
		}
	}()
	return p.renderMaster(ctx, dst, seconds, []string{"-c:a", "libmp3lame", "-b:a", "96k"})
}

// JoinClipSeconds and JoinSermonSeconds are how much of the clip and of the sermon a join sample has.
const (
	JoinClipSeconds   = 6.0
	JoinSermonSeconds = 10.0
)

// JoinSample makes an MP3 at dst to hear how o's sermon joins its intro (which = "intro") or outro: the end of
// the intro, the pause and the start of the sermon, or the end of the sermon, the pause and the start of the
// outro. That part of the sermon is made as the files are (its processing, and its fade at that end), from its
// first or last kept part.
func (f FFmpeg) JoinSample(ctx context.Context, raw string, o Options, which, dst string) error {
	clip := map[string]string{"intro": o.Intro, "outro": o.Outro}[which]
	if clip == "" {
		return fmt.Errorf("this sermon has no %s", which)
	}
	end, err := f.Duration(ctx, raw)
	if err != nil {
		return err
	}
	kept := o.keep(end)
	if len(kept) == 0 {
		return errors.New("the cuts leave nothing of the sermon")
	}
	w := o
	w.Cuts, w.Pauses = nil, nil
	if which == "intro" {
		w.Start, w.End, w.Outro, w.FadeOut = kept[0].Start, math.Min(kept[0].End, kept[0].Start+JoinSermonSeconds), "", 0
	} else {
		last := kept[len(kept)-1]
		w.Start, w.End, w.Intro, w.FadeIn = math.Max(last.Start, last.End-JoinSermonSeconds), last.End, "", 0
	}
	length := w.End - w.Start
	w.FadeIn, w.FadeOut = math.Min(w.FadeIn, length), math.Min(w.FadeOut, length)
	p := &Pipeline{FF: f, Opt: w, Out: io.Discard, raw: raw}
	defer func() {
		for _, name := range p.cleanup {
			Remove(name)
		}
	}()
	stem := filepath.Join(filepath.Dir(dst), "."+strings.TrimSuffix(filepath.Base(dst), filepath.Ext(dst)))
	p.master = stem + ".m.flac"
	joined := stem + ".j.flac"
	p.cleanup = append(p.cleanup, p.master, joined)
	if err := p.renderMaster(ctx, p.master, length, []string{"-c:a", "flac", "-sample_fmt", "s32"}); err != nil {
		return err
	}
	if err := p.join(ctx, joined); err != nil {
		return err
	}
	clipLength, err := f.Duration(ctx, clip)
	if err != nil {
		return err
	}
	from, to := math.Max(0, clipLength-JoinClipSeconds), clipLength+w.ClipPause+length
	if which == "outro" {
		from, to = 0, length+w.ClipPause+math.Min(clipLength, JoinClipSeconds)
	}
	return f.RunTo(ctx, dst, "-ss", secs(from), "-to", secs(to), "-i", joined, "-c:a", "libmp3lame", "-b:a", "128k")
}

// join makes dst: the intro, the sermon's master and the outro, with ClipPause of silence at each join.
// Each clip is brought to the target loudness (gain and the limiter, no other processing) and to the
// sermon's rate and channels; the sermon is used as it is, with its own fades. The tags are the sermon's.
func (p *Pipeline) join(ctx context.Context, dst string) error {
	same := "aformat=sample_fmts=flt:sample_rates=48000:channel_layouts=" + p.Opt.Channels()
	var args, graph []string
	var order string // the parts in the order they are joined
	var total float64
	n, sermonIn := 0, 0
	add := func(filter string, seconds float64) {
		graph = append(graph, fmt.Sprintf("%s,%s[j%d]", filter, same, n))
		order += fmt.Sprintf("[j%d]", n)
		total += seconds
		n++
	}
	pause := func() {
		if p.Opt.ClipPause > 0 {
			add(fmt.Sprintf("anullsrc=r=48000:cl=%s:d=%g", p.Opt.Channels(), p.Opt.ClipPause), p.Opt.ClipPause)
		}
	}
	clip := func(what, path string) error {
		m, err := p.FF.Measure(ctx, path)
		if err != nil {
			return err
		}
		if m.LUFS <= -70 {
			return fmt.Errorf("the %s is silent", what)
		}
		length, err := p.FF.Duration(ctx, path)
		if err != nil {
			return err
		}
		gain := math.Min(p.Opt.TargetLUFS-m.LUFS, 70)
		p.logf("%s %s: %s, %.1f LUFS; gain %+.1f dB.", strings.ToUpper(what[:1])+what[1:], filepath.Base(path), FormatTime(math.Round(length)), m.LUFS, gain)
		add(fmt.Sprintf("[%d:a:0]aformat=channel_layouts=%s,aresample=48000,volume=%.2fdB,%s", len(args)/2, p.Opt.Channels(), gain, p.limiter()), length)
		args = append(args, "-i", path)
		return nil
	}
	if p.Opt.Intro != "" {
		if err := clip("intro", p.Opt.Intro); err != nil {
			return err
		}
		pause()
	}
	length, err := p.FF.Duration(ctx, p.master)
	if err != nil {
		return err
	}
	sermonIn = len(args) / 2
	add(fmt.Sprintf("[%d:a:0]anull", sermonIn), length)
	args = append(args, "-i", p.master)
	if p.Opt.Outro != "" {
		pause()
		if err := clip("outro", p.Opt.Outro); err != nil {
			return err
		}
	}
	graph = append(graph, fmt.Sprintf("%sconcat=n=%d:v=0:a=1[out]", order, n))
	args = append(args, "-filter_complex", strings.Join(graph, ";"), "-map", "[out]", "-map_metadata", strconv.Itoa(sermonIn),
		"-c:a", "flac", "-sample_fmt", "s32")
	progress := total
	if p.OnProgress == nil {
		progress = 0
	}
	if err := p.runTo(ctx, dst, progress, append(args, p.Opt.Tags.args()...)...); err != nil {
		return err
	}
	m, err := p.FF.Measure(ctx, dst)
	if err != nil {
		return err
	}
	p.logf("With intro and outro: %s, %.1f LUFS.", FormatTime(math.Round(total)), m.LUFS)
	return nil
}

func (p *Pipeline) encodeLossy(ctx context.Context, name, src, dst string, codec []string) error {
	if exists(dst) && !p.Opt.Force {
		p.logf("%s exists, skipping.", name)
		return nil
	}
	total := p.lengthFor(ctx, src)
	enc := func(gain *float64) error {
		args := []string{"-i", src}
		if gain != nil {
			args = append(args, "-af", fmt.Sprintf("volume=%.2fdB", *gain))
		}
		args = append(args, codec...)
		return p.runTo(ctx, dst, total, append(args, p.Opt.Tags.args()...)...)
	}
	if err := enc(nil); err != nil {
		return err
	}
	m, err := p.FF.Measure(ctx, dst)
	if err != nil {
		return err
	}
	if gain, needed := correction(p.Opt.TargetLUFS, m); needed {
		p.logf("%s: %.1f LUFS / %.1f dBTP -> adjusting %.1f dB and re-encoding", name, m.LUFS, m.TruePeak, gain)
		return enc(&gain)
	}
	return nil
}
