package normalizer

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Transcription is what a transcript is made with: a whisper.cpp model file (ggml) and the language
// spoken ("nb", "nn", "sv", "en" ...: whisper's codes).
type Transcription struct {
	Model    string
	Language string
	// Words are names and words whisper should know (a church's: its name, its pastors, places), at the start of
	// every part's prompt.
	Words string
	// Key names what it is made from and with (the sound, the model's version, the language, the words): the
	// parts of one stopped earlier are used again only for the same key.
	Key string
	// Check, when set, is asked before each part whether it can go on: an error stops Transcribe with it (the
	// sound it is made from changed).
	Check func() error
}

// Segment is a piece of a transcript: what is said from Start to End.
type Segment struct {
	Start, End time.Duration
	Text       string
}

// vadModel is Silero VAD (v5.1.2, MIT; see models/README.md) as whisper.cpp reads it: it finds where
// there is speech, so whisper gets the speech in pieces cut at pauses, and no silence.
//
//go:embed models/silero-vad.bin
var vadModel []byte

// Whisper runs whisper.cpp's whisper-cli (never through a shell), which makes the transcripts. The image
// has Alpine's, whose CPU code comes in variants for each processor generation and loads the best one this
// processor has (AVX2 and FMA on one from 2013 or later), so it runs on any x86-64 or arm64.
type Whisper struct {
	Bin string
}

// Works tells whether whisper-cli is there and runs.
func (w Whisper) Works(ctx context.Context) bool {
	if w.Bin == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, w.Bin, "-h").Run() == nil
}

// partSeconds is about how long a part of the sermon is: Transcribe saves the transcript part by part, so one
// stopped (a restart of the server) goes on from the first part not done. partWindow is how far from the mark
// a part may end, at the pause nearest to it.
var (
	partSeconds = 600.0
	partWindow  = 120.0
)

// partsPlan is <parts>/plan.json: what the parts are of, and where each one after the first begins.
type partsPlan struct {
	Key    string    `json:"key"`
	Length float64   `json:"length"` // seconds
	Starts []float64 `json:"starts"` // seconds, after 0
}

// Transcribe is what is said in the sound of in, with whisper-cli. It goes part by part (about ten minutes,
// cut at a pause), each saved in the folder parts as it is done: a call for the same t.Key goes on from the
// first part not done; parts of another key are thrown away. The caller removes parts when it has kept the
// transcript, or wants no more of it. ffmpeg (f) gives whisper-cli each part as 16 kHz mono WAV; each part
// starts with the last words of the one before as whisper's prompt, so it goes on in the same way.
// progress gets how much is done, 0 to 1. On a small server it takes several times as long as the sound,
// so all of it runs at the lowest priority; ctx stops it.
func (w Whisper) Transcribe(ctx context.Context, f FFmpeg, in, parts string, t Transcription, progress func(done float64)) ([]Segment, error) {
	if progress == nil {
		progress = func(float64) {}
	}
	vad, err := embeddedFile("vad", ".bin", vadModel)
	if err != nil {
		return nil, fmt.Errorf("the speech detection model: %w", err)
	}
	f.background = true
	plan, err := f.planParts(ctx, in, parts, t.Key)
	if err != nil {
		return nil, err
	}
	bounds := append(append([]float64{0}, plan.Starts...), plan.Length)
	var all []Segment
	prompt := ""
	for i := 0; i+1 < len(bounds); i++ {
		start, end := bounds[i], bounds[i+1]
		srt := filepath.Join(parts, fmt.Sprintf("%03d.srt", i))
		if _, err := os.Stat(srt); err != nil {
			if t.Check != nil {
				if err := t.Check(); err != nil {
					return nil, err
				}
			}
			report := func(done float64) { progress((start + done*(end-start)) / plan.Length) }
			report(0)
			if err := w.transcribePart(ctx, f, in, srt, start, end-start, t, vad, partPrompt(t.Words, prompt), report); err != nil {
				return nil, err
			}
		}
		data, err := os.ReadFile(srt)
		if err != nil {
			return nil, err
		}
		segs, err := ReadSRT(data)
		if err != nil {
			return nil, err
		}
		offset := time.Duration(start * float64(time.Second))
		texts := make([]string, 0, len(segs))
		for _, sg := range segs {
			sg.Start, sg.End = sg.Start+offset, sg.End+offset
			all = append(all, sg)
			texts = append(texts, sg.Text)
		}
		prompt = lastWords(strings.Join(texts, " "), promptWords)
		progress(end / plan.Length)
	}
	return all, nil
}

// planParts reads the plan in parts, or (none, or for another key) empties the folder and makes one: the
// sound's length, and a cut at the pause nearest to every partSeconds.
func (f FFmpeg) planParts(ctx context.Context, in, parts, key string) (partsPlan, error) {
	planFile := filepath.Join(parts, "plan.json")
	var plan partsPlan
	if data, err := os.ReadFile(planFile); err == nil && json.Unmarshal(data, &plan) == nil && plan.Key == key && plan.Length > 0 {
		return plan, nil
	}
	if err := os.RemoveAll(parts); err != nil {
		return plan, err
	}
	if err := os.MkdirAll(parts, 0o755); err != nil {
		return plan, err
	}
	length, err := f.Duration(ctx, in)
	if err != nil {
		return plan, err
	}
	var pauses []Span
	if length > partSeconds+partWindow {
		if pauses, err = f.pauses(ctx, in); err != nil {
			return plan, err
		}
	}
	plan = partsPlan{Key: key, Length: length, Starts: partStarts(length, pauses)}
	data, _ := json.Marshal(plan)
	if err := os.WriteFile(planFile, data, 0o644); err != nil {
		return plan, err
	}
	return plan, nil
}

// partStarts are where the parts after the first begin: at the middle of the pause nearest to each
// partSeconds since the last, if one is within partWindow, else at the mark. The last part is no shorter
// than partWindow.
func partStarts(length float64, pauses []Span) []float64 {
	var starts []float64
	last := 0.0
	for {
		mark := last + partSeconds
		if mark+partWindow >= length {
			return starts
		}
		cut, best := mark, partWindow
		for _, p := range pauses {
			mid := (p.Start + p.End) / 2
			if d := math.Abs(mid - mark); d <= best && mid > last+partWindow {
				cut, best = mid, d
			}
		}
		starts = append(starts, cut)
		last = cut
	}
}

// pauses are the quiet stretches of in of at least 0.3 s (ffmpeg's silencedetect, at -40 dB: the sermon's
// files are loudness-normalised).
func (f FFmpeg) pauses(ctx context.Context, in string) ([]Span, error) {
	res, err := f.Run(ctx, "-i", in, "-map", "0:a:0", "-af", "silencedetect=noise=-40dB:d=0.3", "-f", "null", "-")
	if err != nil {
		return nil, err
	}
	var spans []Span
	start := -1.0
	for line := range strings.SplitSeq(res.Stderr, "\n") {
		if _, v, ok := strings.Cut(line, "silence_start: "); ok {
			if n, err := strconv.ParseFloat(strings.Fields(v)[0], 64); err == nil {
				start = n
			}
		} else if _, v, ok := strings.Cut(line, "silence_end: "); ok && start >= 0 {
			if n, err := strconv.ParseFloat(strings.Fields(v)[0], 64); err == nil {
				spans = append(spans, Span{max(start, 0), n})
			}
			start = -1
		}
	}
	return spans, nil
}

// transcribePart makes the SubRip of seconds of in from start, at srt (written whole or not at all).
func (w Whisper) transcribePart(ctx context.Context, f FFmpeg, in, srt string, start, seconds float64, t Transcription,
	vad, prompt string, progress func(done float64)) error {
	base := strings.TrimSuffix(srt, ".srt")
	wav := base + ".wav"
	defer Remove(wav)
	sec := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
	if err := f.RunTo(ctx, wav, "-ss", sec(start), "-i", in, "-t", sec(seconds), "-map", "0:a:0", "-ac", "1", "-ar", "16000",
		"-c:a", "pcm_s16le"); err != nil {
		return fmt.Errorf("the sound for whisper: %w", err)
	}
	out := base + ".whisper" // whisper-cli adds .srt
	defer Remove(out + ".srt")
	// As many threads as the cores it may use (in a container, its limit): whisper.cpp's threads wait by
	// spinning, so one too many makes it several times slower. -bs 1: one candidate at a time (greedy): beam
	// search took 75 % longer for no better text with NB-Whisper large.
	args := []string{"-np", "-pp", "-t", strconv.Itoa(runtime.GOMAXPROCS(0)), "-m", t.Model, "-l", t.Language,
		"-bs", "1", "--vad", "-vm", vad, "-osrt", "-of", out, "-f", wav}
	if prompt != "" {
		args = append(args, "--prompt", prompt)
	}
	cmd := command(ctx, w.Bin, args, true)
	stderr := &whisperProgress{fn: progress}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("whisper-cli failed (%w):\n%s", err, lastLines(stderr.text.String(), 15))
	}
	if _, err := os.Stat(out + ".srt"); errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(srt, nil, 0o644) // nothing was said
	}
	return Rename(out+".srt", srt)
}

// promptWords is how many of the last words of a part go on to the next one's prompt. whisper reads about 220
// tokens of prompt and keeps its end; with a church's words (at most MaxWords characters) before them, both fit.
const promptWords = 30

// MaxWords is how long a church's words may be (Transcription.Words), in characters.
const MaxWords = 300

// partPrompt is a part's prompt: the church's words, then the last words of the part before.
func partPrompt(words, before string) string {
	return strings.TrimSpace(strings.TrimSpace(words) + " " + before)
}

// lastWords is the last n words of s.
func lastWords(s string, n int) string {
	words := strings.Fields(s)
	return strings.Join(words[max(len(words)-n, 0):], " ")
}

// whisperProgress reads whisper-cli's stderr: it keeps the text (for an error) and passes its
// "progress = 42%" lines on as 0.42.
type whisperProgress struct {
	fn   func(done float64)
	text bytes.Buffer
	line []byte
}

var whisperPercent = regexp.MustCompile(`progress =\s*(\d+)%`)

func (p *whisperProgress) Write(b []byte) (int, error) {
	if p.text.Len() < 1<<20 {
		p.text.Write(b)
	}
	p.line = append(p.line, b...)
	for {
		i := bytes.IndexByte(p.line, '\n')
		if i < 0 {
			break
		}
		if m := whisperPercent.FindSubmatch(p.line[:i]); m != nil && p.fn != nil {
			if n, err := strconv.Atoi(string(m[1])); err == nil {
				p.fn(min(float64(n)/100, 1))
			}
		}
		p.line = p.line[i+1:]
	}
	return len(b), nil
}

// languageCode checks a whisper language code: two or three small letters.
var languageCode = regexp.MustCompile(`^[a-z]{2,3}$`)

// ValidLanguage tells whether code can be given to whisper as the language.
func ValidLanguage(code string) bool { return languageCode.MatchString(code) }

// ReadSRT reads a SubRip file, as the whisper filter writes it.
func ReadSRT(data []byte) ([]Segment, error) {
	var segs []Segment
	sc := bufio.NewScanner(bytes.NewReader(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var cur *Segment
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			if cur != nil && cur.Text != "" {
				segs = append(segs, *cur)
			}
			cur = nil
		case cur == nil && strings.Contains(line, "-->"):
			a, b, _ := strings.Cut(line, "-->")
			start, err1 := srtTime(strings.TrimSpace(a))
			end, err2 := srtTime(strings.TrimSpace(b))
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("not a transcript: %q", line)
			}
			cur = &Segment{Start: start, End: end}
		case cur == nil:
			// the number of the cue
		default:
			cur.Text = strings.TrimSpace(cur.Text + " " + line)
		}
	}
	if cur != nil && cur.Text != "" {
		segs = append(segs, *cur)
	}
	out := segs[:0]
	for _, s := range segs {
		if s.Text = cleanText(s.Text); s.Text != "" {
			out = append(out, s)
		}
	}
	return out, sc.Err()
}

// nonSpeech is what whisper writes for sound that is not speech: [Pause], [Music], (applause) ... A piece in
// square brackets goes wherever it is; one in parentheses only when it is all there is (it can be said).
var nonSpeech = regexp.MustCompile(`\s*\[[^\]]*\]|^\([^)]*\)$`)

func cleanText(s string) string { return strings.TrimSpace(nonSpeech.ReplaceAllString(s, "")) }

// srtTime reads 01:02:03,456.
func srtTime(s string) (time.Duration, error) {
	hms, ms, ok := strings.Cut(s, ",")
	parts := strings.Split(hms, ":")
	if !ok || len(parts) != 3 {
		return 0, errors.New("bad time")
	}
	var d time.Duration
	for i, unit := range []time.Duration{time.Hour, time.Minute, time.Second} {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return 0, err
		}
		d += time.Duration(n) * unit
	}
	n, err := strconv.Atoi(ms)
	if err != nil {
		return 0, err
	}
	return d + time.Duration(n)*time.Millisecond, nil
}

// WebVTT is the segments as captions (.vtt), what a browser's <track> and a podcast app read.
func WebVTT(segs []Segment) []byte {
	var b bytes.Buffer
	b.WriteString("WEBVTT\n")
	for _, s := range segs {
		fmt.Fprintf(&b, "\n%s --> %s\n%s\n", vttTime(s.Start), vttTime(s.End), strings.ReplaceAll(s.Text, "-->", "->"))
	}
	return b.Bytes()
}

func vttTime(d time.Duration) string {
	ms := d.Milliseconds()
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// paragraphPause is the pause in the speech that starts a new paragraph of the text.
const paragraphPause = 2 * time.Second

// PlainText is the segments as text to read: one paragraph per stretch of speech without a long pause.
func PlainText(segs []Segment) []byte {
	var b bytes.Buffer
	for i, s := range segs {
		switch {
		case i == 0:
		case s.Start-segs[i-1].End >= paragraphPause:
			b.WriteString("\n\n")
		default:
			b.WriteString(" ")
		}
		b.WriteString(s.Text)
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	return b.Bytes()
}
