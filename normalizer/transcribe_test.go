package normalizer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// What the whisper filter writes (format=srt), with a cue of nothing between two of speech.
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
	niceness := func(f FFmpeg) string {
		res, err := f.run(context.Background(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(res.Stdout)
	}
	if got := niceness(FFmpeg{Bin: bin, background: true}); got != "19" {
		t.Errorf("background: nice %s, want 19", got)
	}
	if got := niceness(FFmpeg{Bin: bin}); got == "19" {
		t.Errorf("a run: nice %s, want the studio's own", got)
	}
}
