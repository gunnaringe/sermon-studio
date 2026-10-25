package normalizer

// Options controls the processing.
type Options struct {
	Mp3Bitrate     string
	OpusBitrate    string
	Stereo         bool    // keep two channels; default mono
	TargetLUFS     float64 // mono: -19, stereo: -16
	TruePeak       float64
	NoiseReduction int // afftdn nr in dB; 0 = off
	// RoomSound is how much of the room's own sound (reverb, ambience) is kept when noise is taken
	// out, 0 to 100: it raises the floor the noise reduction works down to.
	RoomSound int
	// Leveling evens out quiet speech and loud music: one of LevelingNames ("" = normal).
	Leveling string
	// Compression narrows the loudness range further: one of CompressionNames ("" = off).
	Compression string
	Force       bool
	// OriginalName is the name (no extension) the recording is kept under in original/: the name it
	// arrived with. "" = <date>_raw.
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
