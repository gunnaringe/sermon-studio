package normalizer

import "fmt"

// Options controls the processing.
type Options struct {
	Mp3Bitrate     string
	OpusBitrate    string
	Stereo         bool    // keep two channels; default mono
	TargetLUFS     float64 // mono: -19, stereo: -16
	TruePeak       float64
	NoiseReduction int // afftdn nr in dB; 0 = off (with the standard method)
	// NoiseMethod is how noise is taken out: one of NoiseMethods ("" = standard). With "off" and
	// "speech", NoiseReduction does not apply; with "off", RoomSound neither.
	NoiseMethod string
	// Repair is what is taken out before the noise: the low end and clicks.
	Repair
	// RoomSound is how much of the room's own sound (reverb, ambience) is kept when noise is taken
	// out, 0 to 100: it raises the floor the noise reduction works down to (standard), or mixes that
	// much of the sound before it back in (speech).
	RoomSound int
	// Leveling evens out quiet speech and loud music: one of LevelingNames ("" = normal).
	Leveling string
	// Compression narrows the loudness range further: one of CompressionNames ("" = off).
	Compression string
	// Expander makes the pauses quieter, so the hiss between words is heard less: one of ExpanderNames ("" = off).
	Expander string
	Force    bool
	// MasterFresh, when set, tells whether the master at a path is of these options, so MP3 and Opus can be made
	// from it, also with Force; one it says no to is never used. Unset, an existing master is used unless Force.
	MasterFresh func(path string) bool
	// OriginalName is the name (no extension) the recording is kept under in original/: the name it
	// arrived with. "" = the name it has in the folder.
	OriginalName string
	Start, End   float64 // cut raw to edit (seconds); 0 = from the beginning / to the end
	Cuts         []Span  // parts of Start..End to leave out (seconds in the recording), e.g. a song
	Pauses       []Pause // silences put into the sermon, at times in the recording
	FadeIn       float64 // seconds at the start of the sermon; 0 = off
	FadeOut      float64 // seconds at the end of the sermon; 0 = off
	// Intro and Outro are clips put before and after the sermon (paths; "" = none). They are made
	// elsewhere, often with music, so only their level, rate and channels are set; ClipPause is the
	// silence (seconds) at each join. With either, a run makes two versions of each output: with the
	// clips (the main files) and the sermon only (Paths.Sermon*).
	Intro, Outro string
	ClipPause    float64
	Outputs      Outputs
	Tags         Tags // written into every output; empty fields are left out
}

// Repair takes out what a noise reduction does not: rumble under the voice (a low cut) and clicks and crackle
// (adeclick). The zero value does nothing. It is a part of the sound kept with a sermon, as it is in the JSON.
type Repair struct {
	LowCut      int  `json:"lowCut"`      // Hz below which the sound is cut (highpass); 0 = off
	LowCutSteep bool `json:"lowCutSteep"` // 24 dB per octave instead of 12
	// Declick is adeclick's threshold: clicks are found where the sound jumps more than that many times what
	// is expected (lower finds more, also what is not a click); 0 = off.
	Declick       float64 `json:"declick"`
	DeclickWindow int     `json:"declickWindow"` // ms adeclick looks at around a click; 0 = its own (DefaultDeclickWindow)
}

// The low cut and declick limits, as Validate checks them.
const (
	MinLowCut, MaxLowCut               = 20, 300
	MinDeclick, MaxDeclick             = 1.0, 20.0
	MinDeclickWindow, MaxDeclickWindow = 10, 100
	DefaultDeclickWindow               = 55 // adeclick's own
)

// filters are the filters of r, in the order they go in: the low cut first (rumble also throws off the
// click finding and the noise reduction), then the clicks.
func (r Repair) filters() []string {
	var f []string
	if r.LowCut > 0 {
		hp := fmt.Sprintf("highpass=f=%d", r.LowCut)
		f = append(f, hp)
		if r.LowCutSteep { // two in a row: 24 dB per octave
			f = append(f, hp)
		}
	}
	if r.Declick > 0 {
		dc := fmt.Sprintf("adeclick=t=%g", r.Declick)
		if r.DeclickWindow > 0 {
			dc += fmt.Sprintf(":w=%d", r.DeclickWindow)
		}
		f = append(f, dc)
	}
	return f
}

// validate checks r's values against the limits.
func (r Repair) validate() error {
	switch {
	case r.LowCut != 0 && (r.LowCut < MinLowCut || r.LowCut > MaxLowCut):
		return fmt.Errorf("the low cut can be off or from %d to %d Hz", MinLowCut, MaxLowCut)
	case r.Declick != 0 && (r.Declick < MinDeclick || r.Declick > MaxDeclick):
		return fmt.Errorf("the click threshold can be off or from %g to %g", MinDeclick, MaxDeclick)
	case r.DeclickWindow != 0 && (r.DeclickWindow < MinDeclickWindow || r.DeclickWindow > MaxDeclickWindow):
		return fmt.Errorf("the click window can be from %d to %d ms", MinDeclickWindow, MaxDeclickWindow)
	}
	return nil
}

// Tags are the metadata written into the outputs (FLAC, MP3, Opus).
type Tags struct {
	Title, Artist, Album string
	Date                 string // recording date, 2006-01-02
}

// args are the ffmpeg options that set the tags, for the output that follows.
// Zero Tags leave the source's tags alone; otherwise all four are set, and an
// empty one removes the tag (so a recorder's own tags do not leak through).
func (t Tags) args() []string {
	if t == (Tags{}) {
		return nil
	}
	var a []string
	for _, kv := range [][2]string{{"title", t.Title}, {"artist", t.Artist}, {"album", t.Album}, {"date", t.Date}} {
		a = append(a, "-metadata", kv[0]+"="+kv[1])
	}
	return a
}

// clips reports whether an intro or outro goes around the sermon.
func (o Options) clips() bool { return o.Intro != "" || o.Outro != "" }

// MaxClipPause is the longest pause at a join with a clip, in seconds.
const MaxClipPause = 10.0

// cutting reports whether a sermon is selected in the recording.
func (o Options) cutting() bool { return o.Start > 0 || o.End > 0 }

// Pause is silence put into the sermon: Seconds long, where the recording is at At (seconds in the
// recording). At is where a kept part ends (before a cut or a gap), or inside a part, which it
// then splits. A pause at the end of the sermon, or outside it, has no effect.
type Pause struct {
	At, Seconds float64
}

// MaxPause is the longest pause, in seconds.
const MaxPause = 30.0

// ChannelDefaults are the defaults that depend on mono or stereo: the usual
// podcast loudness targets, and bitrates that sound the same per channel.
type ChannelDefaults struct {
	TargetLUFS  float64
	Mp3Bitrate  string
	OpusBitrate string
}

func DefaultsFor(stereo bool) ChannelDefaults {
	if stereo {
		return ChannelDefaults{TargetLUFS: -16, Mp3Bitrate: "128k", OpusBitrate: "96k"}
	}
	return ChannelDefaults{TargetLUFS: -19, Mp3Bitrate: "96k", OpusBitrate: "48k"}
}

// SetStereo switches between mono and stereo, moving the loudness target and
// bitrates along unless they were changed from the old defaults.
func (o *Options) SetStereo(stereo bool) {
	old, def := DefaultsFor(o.Stereo), DefaultsFor(stereo)
	if o.TargetLUFS == old.TargetLUFS {
		o.TargetLUFS = def.TargetLUFS
	}
	if o.Mp3Bitrate == old.Mp3Bitrate {
		o.Mp3Bitrate = def.Mp3Bitrate
	}
	if o.OpusBitrate == old.OpusBitrate {
		o.OpusBitrate = def.OpusBitrate
	}
	o.Stereo = stereo
}

// Channels is "mono" or "stereo".
func (o Options) Channels() string {
	if o.Stereo {
		return "stereo"
	}
	return "mono"
}

func DefaultOptions() Options {
	mono := DefaultsFor(false)
	return Options{
		Mp3Bitrate:     mono.Mp3Bitrate,
		OpusBitrate:    mono.OpusBitrate,
		TargetLUFS:     mono.TargetLUFS,
		TruePeak:       -1.5,
		NoiseReduction: 6,
		Leveling:       "normal",
		Compression:    "off",
		Outputs:        AllOutputs,
	}
}
