package normalizer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every file input gets the whitelists, and only file inputs: not lavfi's, whose options have -f.
func TestGuardInputs(t *testing.T) {
	guard := []string{"-protocol_whitelist", "file", "-format_whitelist", inputFormats}
	got := GuardInputs([]string{"-y", "-ss", "1", "-i", "a.flac", "-f", "lavfi", "-i", "anullsrc", "-i", "b.mp3", "-f", "null", "-"})
	want := slices.Concat([]string{"-y", "-ss", "1"}, guard, []string{"-i", "a.flac", "-f", "lavfi", "-i", "anullsrc"}, guard,
		[]string{"-i", "b.mp3", "-f", "null", "-"})
	if !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// ffmpeg reads a file as what it is, whatever its extension, but never as a playlist or script that names other
// files: those are refused, even named like a recording.
func TestGuardedInputs(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := t.TempDir()
	ctx := context.Background()
	real := filepath.Join(dir, "real.m4a")
	if err := ff.RunTo(ctx, real, "-f", "lavfi", "-i", "sine=d=1", "-c:a", "aac"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(real)
	mislabeled := filepath.Join(dir, "mislabeled.aac")
	os.WriteFile(mislabeled, b, 0o644)
	if res, err := ff.Run(ctx, "-i", mislabeled, "-f", "null", "-"); err != nil || !strings.Contains(res.Stderr, "Input #0, mov") {
		t.Errorf("an MP4 named .aac: %v %s", err, res.Stderr)
	}
	for name, body := range map[string]string{
		"playlist.m3u8":   "#EXTM3U\n#EXT-X-TARGETDURATION:10\n#EXTINF:10,\nreal.m4a\n#EXT-X-ENDLIST\n",
		"script.ffconcat": "ffconcat version 1.0\nfile real.m4a\n",
	} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(body), 0o644)
		if _, err := ff.Run(ctx, "-i", path, "-f", "null", "-"); err == nil {
			t.Errorf("%s was read", name)
		}
	}
}
