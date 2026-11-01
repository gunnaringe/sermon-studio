package normalizer

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
)

// EvenLevels is the dynamic normalization in the master chain: it brings
// quiet speech and loud music to a similar level without shifting time.
const EvenLevels = "dynaudnorm=f=250:g=15:p=0.9"

// ListenLevels is EvenLevels for listening while cutting (previews, players):
// up to 40 dB of gain instead of 20, as raw recordings can be very quiet.
// The master does not need it: its own gain step sets the final level.
const ListenLevels = EvenLevels + ":m=100"

// LevelingNames and CompressionNames are the strengths of the two, weakest first.
var (
	LevelingNames    = []string{"off", "gentle", "normal", "strong"}
	CompressionNames = []string{"off", "light", "normal", "strong"}
)

var levelingFilters = map[string]string{
	"off":    "",
	"gentle": "dynaudnorm=f=500:g=31:p=0.8:m=5",
	"normal": EvenLevels,
	"strong": "dynaudnorm=f=150:g=9:p=0.95:m=20",
}

// The compressor runs on a recording already brought near the target loudness, so its
// thresholds (linear) are about the level of speech there: light takes the loudest
// parts down a little, strong more of the range.
var compressionFilters = map[string]string{
	"off":    "",
	"light":  "acompressor=threshold=0.125:ratio=2:attack=20:release=250",
	"normal": "acompressor=threshold=0.0631:ratio=2:attack=20:release=250",
	"strong": "acompressor=threshold=0.0794:ratio=3:attack=20:release=250",
}

// LevelingName is the leveling, "normal" when none was chosen.
func (o Options) LevelingName() string {
	if o.Leveling == "" {
		return "normal"
	}
	return o.Leveling
}

// NoiseMethodName is the way noise is taken out, "standard" when none was chosen.
func (o Options) NoiseMethodName() string {
	if o.NoiseMethod == "" {
		return "standard"
	}
	return o.NoiseMethod
}

// CompressionName is the compression, "off" when none was chosen.
func (o Options) CompressionName() string {
	if o.Compression == "" {
		return "off"
	}
	return o.Compression
}

// tidyFilter is the first part of the master chain: the channels that get published, the noise
// taken out, the levels evened out. It does not set the level: that is done by measuring (finish).
func (p *Pipeline) tidyFilter() (string, error) {
	// Convert first, whatever the input has (mono, stereo, more), so the rest
	// of the chain works on the channels that get published. A mono recording
	// made stereo is the same signal on both sides.
	f := []string{"aformat=channel_layouts=" + p.Opt.Channels()}
	if p.Opt.NoiseMethodName() == "speech" {
		speech, err := p.speechFilter()
		if err != nil {
			return "", err
		}
		f = append(f, speech)
	} else if p.Opt.NoiseReduction > 0 {
		nr := fmt.Sprintf("afftdn=nr=%d", p.Opt.NoiseReduction)
		if p.Opt.RoomSound > 0 {
			nr += fmt.Sprintf(":rf=%.1f", -38+0.18*float64(p.Opt.RoomSound)) // the default floor is -38 dB
		}
		f = append(f, nr)
	}
	if lv := levelingFilters[p.Opt.LevelingName()]; lv != "" {
		f = append(f, lv)
	}
	return strings.Join(f, ","), nil
}

// finalFilter sets the level and ends the chain: gain (dB), a limiter that keeps the peaks under the
// true-peak ceiling (with half a dB to spare for what resampling adds), and the fades, last, so nothing
// lifts them again. length (seconds) is only needed for a fade-out.
func (p *Pipeline) finalFilter(gain, length float64) string {
	f := []string{fmt.Sprintf("volume=%.2fdB", gain), p.limiter(), "aresample=48000"}
	if p.Opt.FadeIn > 0 {
		f = append(f, fmt.Sprintf("afade=t=in:d=%g", p.Opt.FadeIn))
	}
	if p.Opt.FadeOut > 0 {
		f = append(f, fmt.Sprintf("afade=t=out:st=%.3f:d=%g", length-p.Opt.FadeOut, p.Opt.FadeOut))
	}
	return strings.Join(f, ",")
}

// limiter keeps the peaks under the true-peak ceiling, with half a dB to spare for what resampling adds.
func (p *Pipeline) limiter() string {
	return fmt.Sprintf("alimiter=limit=%.4f:attack=5:release=50:level=0", math.Pow(10, (p.Opt.TruePeak-0.5)/20))
}

// tidyName is the hidden temporary file a master is made from (in the folder of dst).
func tidyName(dst, stage string) string {
	// Short, so it fits Windows' paths wherever the outputs do (folderFits in web).
	name := filepath.Base(dst)
	return filepath.Join(filepath.Dir(dst), "."+strings.TrimSuffix(name, filepath.Ext(name))+".t"+stage+".flac")
}

const lossyPeakCeiling = -1.0 // dBTP

// correction computes the gain needed after encoding to a lossy format.
// The gain is limited so that the true peak ends up at most lossyPeakCeiling.
func correction(target float64, m Loudness) (gain float64, needed bool) {
	gain = math.Min(target-m.LUFS, lossyPeakCeiling-m.TruePeak)
	needed = math.Abs(gain) >= 0.3 || m.TruePeak > lossyPeakCeiling
	return gain, needed
}
