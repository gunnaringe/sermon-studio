package normalizer

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
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

// Transcribe writes what is said in the sound of in to dst as SubRip (.srt). ffmpeg (f) decodes the sound
// to a 16 kHz mono WAV file next to dst, which whisper-cli reads, then it is removed. progress gets how much
// is done, 0 to 1. On a small server it takes several times as long as the sound with a medium model, so
// it runs at the lowest priority; ctx stops it.
func (w Whisper) Transcribe(ctx context.Context, f FFmpeg, in, dst string, t Transcription, progress func(done float64)) error {
	vad, err := embeddedFile("vad", ".bin", vadModel)
	if err != nil {
		return fmt.Errorf("the speech detection model: %w", err)
	}
	base := filepath.Join(filepath.Dir(dst), "."+strings.TrimSuffix(filepath.Base(dst), filepath.Ext(dst)))
	wav := base + ".whisper.wav"
	defer Remove(wav)
	f.background = true
	if err := f.RunTo(ctx, wav, "-i", in, "-map", "0:a:0", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le"); err != nil {
		return fmt.Errorf("the sound for whisper: %w", err)
	}
	out := base + ".whisper" // whisper-cli adds .srt
	defer Remove(out + ".srt")
	// As many threads as the cores it may use (in a container, its limit): whisper.cpp's threads wait by
	// spinning, so one too many makes it several times slower. -bs 1: one candidate at a time (greedy), not
	// whisper-cli's beam search of five, which takes longer.
	args := []string{"-np", "-pp", "-t", strconv.Itoa(runtime.GOMAXPROCS(0)), "-m", t.Model, "-l", t.Language,
		"-bs", "1", "--vad", "-vm", vad, "-osrt", "-of", out, "-f", wav}
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
		// Nothing was said.
		if err := os.WriteFile(out+".srt", nil, 0o644); err != nil {
			return err
		}
	}
	return Rename(out+".srt", dst)
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
