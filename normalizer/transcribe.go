package normalizer

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
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
// there is speech, so the whisper filter cuts the sound into pieces at pauses instead of in words.
//
//go:embed models/silero-vad.bin
var vadModel []byte

// queueSeconds is how much sound the whisper filter gathers before it transcribes: whisper's own window.
// ffmpeg-lgpl's filter waits until half of it is there, then cuts at the last pause (the VAD), so whisper
// gets 15-30 s at a time; a piece per sentence, as ffmpeg's own filter does, is about six times slower.
const queueSeconds = 30

// Transcribe writes what is said in the sound of in to dst as SubRip (.srt), with ffmpeg's whisper
// filter (whisper.cpp, on the CPU). progress gets the seconds of sound done so far. It takes about as
// long as the sound with a medium model on four cores, so it is slow work, done at the lowest priority;
// ctx stops it. It runs on the
// CPU, which needs AVX and F16C on x86 (CanTranscribe).
func (f FFmpeg) Transcribe(ctx context.Context, in, dst string, t Transcription, progress func(seconds float64)) error {
	vad, err := embeddedFile("vad", ".bin", vadModel)
	if err != nil {
		return fmt.Errorf("the speech detection model: %w", err)
	}
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".partial")
	filter := fmt.Sprintf("whisper=model=%s:language=%s:queue=%d:use_gpu=false:vad_model=%s:destination=%s:format=srt",
		filterPath(t.Model), t.Language, queueSeconds, filterPath(vad), filterPath(tmp))
	// As many threads as the cores it may use (in a container, its limit): ffmpeg's default is one more,
	// and whisper.cpp's threads wait by spinning, so one too many makes it several times slower.
	threads := strconv.Itoa(runtime.GOMAXPROCS(0))
	f.background = true
	args := []string{"-y", "-filter_threads", threads, "-i", in, "-map", "0:a:0", "-af", filter, "-f", "null", "-"}
	if _, err := f.run(ctx, progress, args); err != nil {
		Remove(tmp)
		return err
	}
	if _, err := os.Stat(tmp); errors.Is(err, os.ErrNotExist) {
		// Nothing was said: whisper's filter opens the file only with something to write.
		if err := os.WriteFile(tmp, nil, 0o644); err != nil {
			return err
		}
	}
	return Rename(tmp, dst)
}

// CanTranscribe tells whether this processor can run the whisper filter: ffmpeg-lgpl builds whisper.cpp for
// x86 with AVX and F16C (2012 and later); on an older one it would stop with "Illegal instruction".
func CanTranscribe() bool { return canTranscribe() }

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
