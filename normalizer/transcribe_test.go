package normalizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// fakeWhisper writes a whisper-cli that notes its arguments (one per line, after "call <-of>"), its priority
// and keeps the WAV it got (seen-<part>.wav); it says how far it is and writes one cue, "Del <part>.", 0-1.5 s. It
// fails for a part while dir/fail-<part> is there.
func fakeWhisper(t *testing.T, dir string) string {
	t.Helper()
	notes := filepath.Join(dir, "notes")
	bin := filepath.Join(dir, "whisper-cli")
	script := `#!/bin/sh
out= wav=
for a in "$@"; do prev=$cur; cur=$a; [ "$prev" = -of ] && out=$a; [ "$prev" = -f ] && wav=$a; done
part=$(basename "$out" .whisper)
echo "call $part" >>` + notes + `
for a in "$@"; do echo "arg $a" >>` + notes + `; done
if [ -r /proc/$$/stat ]; then echo "nice $(cut -d' ' -f19 /proc/$$/stat)" >>` + notes + `; fi
cp "$wav" ` + dir + `/seen-$part.wav
[ -e ` + dir + `/fail-$part ] && { echo "error: no" >&2; exit 1; }
echo "whisper_print_progress_callback: progress =  50%" >&2
echo "whisper_print_progress_callback: progress = 100%" >&2
printf '1\n00:00:00,000 --> 00:00:01,500\nDel %s.\n\n' "$part" >"$out.srt"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// Transcribe gives whisper-cli the sound as 16 kHz mono WAV, at the lowest priority, with the model, the
// language and the VAD, and passes its progress on; what is left is the parts folder (the caller's to remove).
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
	bin := fakeWhisper(t, dir)
	var progress []float64
	parts := filepath.Join(dir, ".parts")
	segs, err := Whisper{Bin: bin}.Transcribe(context.Background(), ff, in, parts, Transcription{Model: "/m/nb.bin", Language: "no", Key: "k"},
		func(done float64) { progress = append(progress, done) })
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 || segs[0].Text != "Del 000." || segs[0].End != 1500*time.Millisecond {
		t.Errorf("transcript: %+v", segs)
	}
	if strings.Trim(fmt.Sprint(progress), "[]") != "0 0.5 1 1" {
		t.Errorf("progress: %v", progress)
	}
	n := string(must(os.ReadFile(filepath.Join(dir, "notes"))))
	for _, want := range []string{"arg -m\narg /m/nb.bin", "arg -l\narg no", "arg --vad", "arg -osrt", "arg -bs\narg 1"} {
		if !strings.Contains(n, want) {
			t.Errorf("whisper-cli got no %q:\n%s", want, n)
		}
	}
	if strings.Contains(n, "--prompt") {
		t.Errorf("a prompt for the first part:\n%s", n)
	}
	if p, err := ff.ProbeFile(context.Background(), filepath.Join(dir, "seen-000.wav")); err != nil || p.SampleRate != 16000 || p.Channels != 1 {
		t.Errorf("the sound whisper-cli got: %+v %v", p, err)
	}
	if _, err := exec.LookPath("nice"); err == nil && runtime.GOOS == "linux" && !strings.Contains(n, "nice 19") {
		t.Errorf("not at nice 19:\n%s", n)
	}
	if left := listFiles(t, parts); strings.Join(left, " ") != "000.srt plan.json" {
		t.Errorf("in the parts folder: %v", left)
	}
}

// A long sermon goes in parts cut at pauses, each with the end of the one before as the prompt; one stopped
// goes on from the first part not done, and parts made for another key are made again.
func TestTranscribeParts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake whisper-cli is a shell script")
	}
	ff := requireFFmpeg(t)
	defer func(s, w float64) { partSeconds, partWindow = s, w }(partSeconds, partWindow)
	partSeconds, partWindow = 3, 1
	dir := t.TempDir()
	in := filepath.Join(dir, "sermon.flac")
	// 2 s of sound, 1 s of silence, ten seconds: pauses at 2-3, 5-6 and 8-9, so four parts.
	if _, err := ff.Run(context.Background(), "-f", "lavfi", "-i", "aevalsrc='if(lt(mod(t,3),2),0.5*sin(2*PI*440*t),0)':d=10:s=44100", in); err != nil {
		t.Fatal(err)
	}
	bin := fakeWhisper(t, dir)
	parts := filepath.Join(dir, ".parts")
	tr := Transcription{Model: "m", Language: "no", Key: "one"}
	os.WriteFile(filepath.Join(dir, "fail-001"), nil, 0o644)
	if _, err := (Whisper{Bin: bin}).Transcribe(context.Background(), ff, in, parts, tr, nil); err == nil {
		t.Fatal("no error from the failing part")
	}
	os.Remove(filepath.Join(dir, "fail-001"))
	segs, err := Whisper{Bin: bin}.Transcribe(context.Background(), ff, in, parts, tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	var plan partsPlan
	json.Unmarshal(must(os.ReadFile(filepath.Join(parts, "plan.json"))), &plan)
	if len(plan.Starts) != 3 || !near(plan.Starts[0], 2.5, 0.2) || !near(plan.Starts[1], 5.5, 0.2) || !near(plan.Starts[2], 8.5, 0.2) ||
		!near(plan.Length, 10, 0.1) {
		t.Fatalf("plan: %+v", plan)
	}
	if len(segs) != 4 || segs[0].Text != "Del 000." || segs[3].Text != "Del 003." || !near(segs[1].Start.Seconds(), plan.Starts[0], 0.01) {
		t.Errorf("transcript: %+v", segs)
	}
	if d, err := ff.Duration(context.Background(), filepath.Join(dir, "seen-003.wav")); err != nil || !near(d, 10-plan.Starts[2], 0.1) {
		t.Errorf("the last part is %v s (%v)", d, err)
	}
	n := string(must(os.ReadFile(filepath.Join(dir, "notes"))))
	if strings.Count(n, "call 000") != 1 || strings.Count(n, "call 001") != 2 || strings.Count(n, "call 003") != 1 {
		t.Errorf("the parts made:\n%s", n)
	}
	if !strings.Contains(n, "arg --prompt\narg Del 000.") || !strings.Contains(n, "arg --prompt\narg Del 001.") {
		t.Errorf("no prompt from the part before:\n%s", n)
	}
	// Another key: all of it again. The church's words start every part's prompt; Check is asked before each
	// part, and its error stops it.
	os.Remove(filepath.Join(dir, "notes"))
	tr.Key, tr.Words = "two", " Melhus  Peder "
	stop := errors.New("changed")
	checks := 0
	tr.Check = func() error {
		if checks++; checks == 3 {
			return stop
		}
		return nil
	}
	if _, err := (Whisper{Bin: bin}).Transcribe(context.Background(), ff, in, parts, tr, nil); !errors.Is(err, stop) {
		t.Fatalf("not stopped by Check: %v", err)
	}
	n = string(must(os.ReadFile(filepath.Join(dir, "notes"))))
	if strings.Count(n, "call 000") != 1 || strings.Count(n, "call 001") != 1 || strings.Contains(n, "call 002") {
		t.Errorf("parts of another key used, or made after Check:\n%s", n)
	}
	if !strings.Contains(n, "arg --prompt\narg Melhus  Peder\n") || !strings.Contains(n, "arg --prompt\narg Melhus  Peder Del 000.") {
		t.Errorf("the words are not in the prompt:\n%s", n)
	}
}

// Cuts go at the pause nearest each mark within the window, else at the mark; the last part is not tiny.
func TestPartStarts(t *testing.T) {
	pauses := []Span{{590, 592}, {650, 651}, {1150, 1152}}
	got := partStarts(1800, pauses)
	if fmt.Sprint(got) != "[591 1151]" {
		t.Errorf("starts: %v", got)
	}
	if got := partStarts(700, pauses); len(got) != 0 {
		t.Errorf("a short sermon: %v", got)
	}
	if got := partStarts(1500, nil); fmt.Sprint(got) != "[600 1200]" {
		t.Errorf("no pauses: %v", got)
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
	parts := filepath.Join(dir, "parts")
	_, err := Whisper{Bin: filepath.Join(dir, "no-whisper-cli")}.Transcribe(context.Background(), ff, in, parts, Transcription{Key: "k"}, nil)
	if err == nil {
		t.Fatal("no error without whisper-cli")
	}
	if left := listFiles(t, dir); strings.Join(left, " ") != "in.flac parts/plan.json" {
		t.Errorf("left behind: %v", left)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
