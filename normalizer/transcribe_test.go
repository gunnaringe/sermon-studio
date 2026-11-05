package normalizer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// What whisper-cli writes (-osrt), with a cue of nothing between two of speech.
const whisperSRT = "1\n00:00:00,000 --> 00:00:04,500\nI de dager\n\n2\n00:00:04,500 --> 00:00:07,250\nkom et bud fra keiser Augustus.\n\n" +
	"3\n00:00:12,000 --> 00:01:02,040\nOg dette er den første innskrivningen.\n\n"

func TestReadSRT(t *testing.T) {
	segs, err := ReadSRT([]byte(strings.ReplaceAll(whisperSRT, "\n", "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 3 || segs[1].Text != "kom et bud fra keiser Augustus." || segs[2].Start != 12*time.Second ||
		segs[2].End != time.Minute+2040*time.Millisecond {
		t.Errorf("segments: %+v", segs)
	}
	if segs, err := ReadSRT(nil); err != nil || len(segs) != 0 {
		t.Errorf("nothing said: %v %v", segs, err)
	}
	// What is not speech goes: a piece of only that, and square brackets in a piece.
	segs, _ = ReadSRT([]byte("1\n00:00:00,000 --> 00:00:10,000\n[Pause]\n\n2\n00:00:10,000 --> 00:00:12,000\n(applause)\n\n" +
		"3\n00:00:12,000 --> 00:00:14,000\nAmen. [Music] Amen (twice).\n"))
	if len(segs) != 1 || segs[0].Text != "Amen. Amen (twice)." {
		t.Errorf("not speech: %+v", segs)
	}
	if _, err := ReadSRT([]byte("1\n00:00 --> x\nhi\n")); err == nil {
		t.Error("a bad time is not an error")
	}
}

func TestWebVTTAndPlainText(t *testing.T) {
	segs, _ := ReadSRT([]byte(whisperSRT))
	vtt := string(WebVTT(segs))
	if !strings.HasPrefix(vtt, "WEBVTT\n\n00:00:00.000 --> 00:00:04.500\nI de dager\n") || !strings.Contains(vtt, "\n00:00:12.000 --> 00:01:02.040\n") {
		t.Errorf("vtt:\n%s", vtt)
	}
	if got := string(WebVTT([]Segment{{Text: "a --> b"}})); strings.Count(got, "-->") != 1 {
		t.Errorf("an arrow in the text ends the cue: %q", got)
	}
	// A pause of two seconds or more starts a paragraph.
	if got, want := string(PlainText(segs)), "I de dager kom et bud fra keiser Augustus.\n\nOg dette er den første innskrivningen.\n"; got != want {
		t.Errorf("text %q, want %q", got, want)
	}
	if got := PlainText(nil); len(got) != 0 {
		t.Errorf("nothing said: %q", got)
	}
}

func TestValidLanguage(t *testing.T) {
	for code, ok := range map[string]bool{"nb": true, "nn": true, "haw": true, "": false, "NB": false, "n:b": false, "nb,x": false} {
		if ValidLanguage(code) != ok {
			t.Errorf("%q: %v", code, !ok)
		}
	}
}

// A transcript's ffmpeg runs at the lowest priority, and only that one.
func TestBackgroundNice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no nice on Windows")
	}
	if _, err := exec.LookPath("nice"); err != nil {
		t.Skip("no nice here")
	}
	// An ffmpeg that says its priority (macOS has no /proc).
	bin := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\nif [ -r /proc/$$/stat ]; then cut -d' ' -f19 /proc/$$/stat; else ps -o nice= -p $$; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	niceness := func(f FFmpeg) int {
		res, err := f.run(context.Background(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		n, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	// 19 on Linux; macOS's ps shows its own scale, so: lower than a run's, which is the studio's own.
	if bg, own := niceness(FFmpeg{Bin: bin, background: true}), niceness(FFmpeg{Bin: bin}); bg <= own || runtime.GOOS == "linux" && bg != 19 {
		t.Errorf("nice: background %d, a run %d", bg, own)
	}
}

// Transcribe gives whisper-cli the sound as 16 kHz mono WAV, at the lowest priority, passes its progress on and
// puts its SubRip at dst; the WAV and whisper-cli's own file are gone afterwards.
func TestTranscribeWhisperCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake whisper-cli is a shell script")
	}
	ff := requireFFmpeg(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "sermon.flac")
	if _, err := ff.Run(context.Background(), "-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-ac", "2", "-ar", "44100", in); err != nil {
		t.Fatal(err)
	}
	// A whisper-cli that notes its arguments, priority and the WAV's format, says how far it is and writes a transcript.
	notes := filepath.Join(dir, "notes")
	bin := filepath.Join(dir, "whisper-cli")
	script := `#!/bin/sh
out= wav=
while [ $# -gt 0 ]; do
	case $1 in -of) out=$2; shift ;; -f) wav=$2; shift ;; esac
	echo "arg $1" >>` + notes + `
	shift
done
if [ -r /proc/$$/stat ]; then echo "nice $(cut -d' ' -f19 /proc/$$/stat)" >>` + notes + `; fi
cp "$wav" ` + filepath.Join(dir, "seen") + `
echo "whisper_print_progress_callback: progress =  50%" >&2
echo "whisper_print_progress_callback: progress = 100%" >&2
printf '1\n00:00:00,000 --> 00:00:01,500\nI de dager\n\n' >"$out.srt"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var progress []float64
	dst := filepath.Join(dir, ".2026-11-01.transcript.srt")
	err := Whisper{Bin: bin}.Transcribe(context.Background(), ff, in, dst, Transcription{Model: "/m/nb.bin", Language: "no"},
		func(done float64) { progress = append(progress, done) })
	if err != nil {
		t.Fatal(err)
	}
	if segs, err := ReadSRT(must(os.ReadFile(dst))); err != nil || len(segs) != 1 || segs[0].Text != "I de dager" {
		t.Errorf("transcript: %+v %v", segs, err)
	}
	if len(progress) != 2 || progress[0] != 0.5 || progress[1] != 1 {
		t.Errorf("progress: %v", progress)
	}
	n := string(must(os.ReadFile(notes)))
	for _, want := range []string{"arg -m\narg /m/nb.bin", "arg -l\narg no", "arg --vad", "arg -osrt"} {
		if !strings.Contains(n, want) {
			t.Errorf("whisper-cli got no %q:\n%s", want, n)
		}
	}
	if p, err := ff.ProbeFile(context.Background(), filepath.Join(dir, "seen")); err != nil || p.SampleRate != 16000 || p.Channels != 1 {
		t.Errorf("the sound whisper-cli got: %+v %v", p, err)
	}
	if _, err := exec.LookPath("nice"); err == nil && runtime.GOOS == "linux" && !strings.Contains(n, "nice 19") {
		t.Errorf("not at nice 19:\n%s", n)
	}
	if left := listFiles(t, dir); strings.Join(left, " ") != ".2026-11-01.transcript.srt notes seen sermon.flac whisper-cli" {
		t.Errorf("left behind: %v", left)
	}
}

// Without whisper-cli, Transcribe fails and leaves no WAV behind.
func TestTranscribeNoWhisperCLI(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "in.flac")
	if _, err := ff.Run(context.Background(), "-f", "lavfi", "-i", "sine=duration=1", "-ac", "2", "-ar", "48000", in); err != nil {
		t.Fatal(err)
	}
	err := Whisper{Bin: filepath.Join(dir, "no-whisper-cli")}.Transcribe(context.Background(), ff, in, filepath.Join(dir, "t.srt"), Transcription{}, nil)
	if err == nil {
		t.Fatal("no error without whisper-cli")
	}
	if left := listFiles(t, dir); strings.Join(left, " ") != "in.flac" {
		t.Errorf("left behind: %v", left)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
