package normalizer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// joinFade is the fade (seconds) on each side of a join where a part was cut
// out, so the join does not click.
const joinFade = 0.015

// keep returns the parts of the recording the sermon is made of: Start..end
// without the Cuts, in order. end is where the recording ends; an End past it,
// or 0, runs to the end. Cuts may be given in any order and may overlap.
func (o Options) keep(end float64) []Span {
	if o.End > 0 && o.End < end {
		end = o.End
	}
	cuts := append([]Span(nil), o.Cuts...)
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].Start < cuts[j].Start })
	var kept []Span
	at := o.Start
	for _, c := range cuts {
		if c.Start > at {
			kept = append(kept, Span{at, math.Min(c.Start, end)})
		}
		at = math.Max(at, c.End)
	}
	if at < end {
		kept = append(kept, Span{at, end})
	}
	// Leave out slivers that a cut right next to the edge or another cut leaves.
	out := kept[:0]
	for _, k := range kept {
		if k.End-k.Start >= 0.05 {
			out = append(out, k)
		}
	}
	return out
}

// parts is keep with the pauses worked in: the parts of the recording the sermon is made of, cut
// where a pause falls inside one, and the silence (seconds) that follows each (the last one's is
// not made).
func (o Options) parts(end float64) (kept []Span, after []float64) {
	kept = o.keep(end)
	after = make([]float64, len(kept))
	pauses := slices.Clone(o.Pauses)
	sort.Slice(pauses, func(i, j int) bool { return pauses[i].At < pauses[j].At })
	for _, p := range pauses {
		if p.Seconds <= 0 {
			continue
		}
		for i, k := range kept {
			if math.Abs(p.At-k.End) < 0.001 {
				after[i] += p.Seconds
				break
			}
			if p.At >= k.Start+0.05 && p.At <= k.End-0.05 {
				kept = slices.Insert(kept, i+1, Span{p.At, k.End})
				kept[i].End = p.At
				after = slices.Insert(after, i+1, after[i])
				after[i] = p.Seconds
				break
			}
		}
	}
	return kept, after
}

// pauseTotal is the silence the sermon has in it, in seconds.
func pauseTotal(after []float64) float64 {
	var n float64
	for i, s := range after {
		if i < len(after)-1 {
			n += s
		}
	}
	return n
}

// cutInput is how ffmpeg reads the sermon from the raw recording: the input
// options and, when parts are cut out, a filter graph that joins what is kept
// and ends in the label [cut].
// Each kept part is its own input, so ffmpeg never has to hold the audio
// between two parts in memory.
func (p *Pipeline) cutInput(ctx context.Context) (args []string, graph string, err error) {
	if len(p.Opt.Cuts) == 0 && len(p.Opt.Pauses) == 0 {
		return append(p.cutArgs(), "-i", p.raw), "", nil
	}
	end, err := p.FF.Duration(ctx, p.raw)
	if err != nil {
		return nil, "", err
	}
	kept, after := p.Opt.parts(end)
	if len(kept) == 0 {
		return nil, "", errors.New("the cuts leave nothing of the sermon")
	}
	// With silence between them the parts must agree on rate and channels with it.
	same := ""
	for _, s := range after[:len(after)-1] {
		if s > 0 {
			same = "aresample=48000,aformat=channel_layouts=" + p.Opt.Channels()
		}
	}
	var parts []string
	var order string // the parts and the silences in the order they are joined
	n := 0
	for i, k := range kept {
		args = append(args, "-ss", secs(k.Start), "-to", secs(k.End), "-i", p.raw)
		f := []string{"asetpts=PTS-STARTPTS"}
		if same != "" {
			f = append(f, same)
		}
		if i > 0 {
			f = append(f, fmt.Sprintf("afade=t=in:d=%g", joinFade))
		}
		if i < len(kept)-1 {
			f = append(f, fmt.Sprintf("afade=t=out:st=%.3f:d=%g", math.Max(k.End-k.Start-joinFade, 0), joinFade))
		}
		parts = append(parts, fmt.Sprintf("[%d:a]%s[p%d]", i, strings.Join(f, ","), i))
		order += fmt.Sprintf("[p%d]", i)
		n++
		if i < len(kept)-1 && after[i] > 0 {
			parts = append(parts, fmt.Sprintf("anullsrc=r=48000:cl=%s:d=%g[s%d]", p.Opt.Channels(), after[i], i))
			order += fmt.Sprintf("[s%d]", i)
			n++
		}
	}
	return args, strings.Join(parts, ";") + fmt.Sprintf(";%sconcat=n=%d:v=0:a=1[cut]", order, n), nil
}

// cutsNote is " without 2 cuts" for the log, or nothing.
func (p *Pipeline) cutsNote() string {
	switch n := len(p.Opt.Cuts); n {
	case 0:
		return ""
	case 1:
		return ", leaving out 1 part"
	default:
		return fmt.Sprintf(", leaving out %d parts", n)
	}
}

// cutArgs are the input options for the cut. Placed before -i, ffmpeg
// decodes and discards the audio before start, so the cut is exact.
func (p *Pipeline) cutArgs() []string {
	var args []string
	if p.Opt.Start > 0 {
		args = append(args, "-ss", secs(p.Opt.Start))
	}
	if p.Opt.End > 0 {
		args = append(args, "-to", secs(p.Opt.End))
	}
	return args
}

func (p *Pipeline) cutFrom() string {
	if p.Opt.Start > 0 {
		return FormatTime(p.Opt.Start)
	}
	return "the beginning"
}

func (p *Pipeline) cutTo() string {
	if p.Opt.End > 0 {
		return FormatTime(p.Opt.End)
	}
	return "the end"
}

func secs(s float64) string { return strconv.FormatFloat(s, 'f', 3, 64) }
