package web

import (
	"strings"

	"github.com/gunnaringe/sermon-studio/normalizer"
	"testing"
)

func TestCleanSourceName(t *testing.T) {
	for in, want := range map[string]string{
		"R_20261018-104506.wav":      "R_20261018-104506.wav",
		`C:\Recorder\R_1.WAV`:        "R_1.WAV",
		"/tmp/../x/Preken 1 (a).mp3": "Preken 1 (a).mp3",
		"høst: prekenen?.flac":       "høst_ prekenen_.flac",
		".hidden.flac":               "hidden.flac",
		"x.partial.wav":              "x_partial.wav",
		"noextension":                "",
		".wav":                       "",
		"":                           "",
	} {
		if got := cleanSourceName(in); got != want {
			t.Errorf("cleanSourceName(%q) = %q, want %q", in, got, want)
		}
	}
	long := cleanSourceName(strings.Repeat("a", 300) + ".wav")
	if len([]rune(long)) != maxSourceName || !strings.HasSuffix(long, ".wav") {
		t.Errorf("long name: %d runes, %q", len([]rune(long)), long[len(long)-8:])
	}
}

// The noise method is in the fingerprint only when it is not the standard one, so files made before it
// was a choice are not out of date.
func TestMadeFromNoiseMethod(t *testing.T) {
	o := normalizer.DefaultOptions()
	before := madeFrom(o, "mp3", "")
	o.NoiseMethod = "standard"
	if madeFrom(o, "mp3", "") != before {
		t.Error("standard changed the fingerprint")
	}
	o.NoiseMethod = "speech"
	if madeFrom(o, "mp3", "") == before {
		t.Error("speech did not change the fingerprint")
	}
}
