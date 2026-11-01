package normalizer

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// FFmpeg calls ffmpeg with an argument list (never through a shell).
type FFmpeg struct {
	Bin string
}

// Result holds what ffmpeg wrote to stdout and stderr.
type Result struct {
	Stdout string
	Stderr string
}

// Run runs ffmpeg. -nostdin stops ffmpeg from waiting for key presses.
func (f FFmpeg) Run(ctx context.Context, args ...string) (Result, error) {
	return f.run(ctx, nil, args)
}

// run runs ffmpeg; with progress set, ffmpeg reports how many seconds of
// output it has written, which are passed to progress as they arrive.
func (f FFmpeg) run(ctx context.Context, progress func(seconds float64), args []string) (Result, error) {
	full := []string{"-nostdin", "-hide_banner", "-nostats"}
	if progress != nil {
		// Every 0.1 s instead of 0.5: on a fast machine a step can be over before the first report.
		full = append(full, "-progress", "pipe:1", "-stats_period", "0.1")
	}
	cmd := exec.CommandContext(ctx, f.Bin, append(full, GuardInputs(args)...)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	if progress != nil {
		cmd.Stdout = &progressWriter{fn: progress}
	}
	cmd.Stderr = &errb
	// ffmpeg on Windows ends its lines with \r\n; the parsers expect \n.
	res := func() Result {
		return Result{Stdout: out.String(), Stderr: strings.ReplaceAll(errb.String(), "\r\n", "\n")}
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return res(), ctx.Err()
		}
		return res(), fmt.Errorf("ffmpeg failed (%w):\n%s", err, lastLines(res().Stderr, 15))
	}
	return res(), nil
}

// HasFilter tells whether this ffmpeg has the filter (the whisper filter needs ffmpeg built with whisper.cpp).
func (f FFmpeg) HasFilter(ctx context.Context, name string) bool {
	res, err := f.Run(ctx, "-filters")
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(res.Stdout, "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[1] == name {
			return true
		}
	}
	return false
}

// inputFormats are the only containers ffmpeg may read a file as (-format_whitelist): what a recording, a
// clip or a file the studio made can be, and none of the playlists and scripts (HLS, concat ...) that name
// other files. ffmpeg still finds the format from the content, so a file with the wrong extension is read as
// what it is.
const inputFormats = "wav,aiff,flac,mp3,mov,aac,ogg,asf,caf,matroska"

// GuardInputs puts -protocol_whitelist file and -format_whitelist before every file input in args (an -i
// whose options have no -f, which lavfi's have): ffmpeg then reads that file as audio or video, and never
// another file or an address the file names. The files come from the public, and ffmpeg reads many formats.
func GuardInputs(args []string) []string {
	out := make([]string, 0, len(args)+4)
	start := 0 // where the options of the next input begin
	for i := 0; i < len(args); i++ {
		if args[i] == "-i" && i+1 < len(args) {
			if !slices.Contains(args[start:i], "-f") {
				out = append(out, "-protocol_whitelist", "file", "-format_whitelist", inputFormats)
			}
			out = append(out, args[i], args[i+1])
			i++
			start = i + 1
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// RunTo runs ffmpeg with dst as the output file. ffmpeg writes to a temporary
// file next to dst, which is renamed only on success, so an interrupted or
// failed run never leaves a half-written dst that a later run would skip.
func (f FFmpeg) RunTo(ctx context.Context, dst string, args ...string) error {
	return f.RunToProgress(ctx, dst, nil, args...)
}

// RunToProgress is RunTo that reports the seconds of output written so far.
func (f FFmpeg) RunToProgress(ctx context.Context, dst string, progress func(seconds float64), args ...string) error {
	ext := filepath.Ext(dst) // ffmpeg picks the format from the extension
	tmp := strings.TrimSuffix(dst, ext) + ".partial" + ext
	if _, err := f.run(ctx, progress, append(append([]string{"-y"}, args...), tmp)); err != nil {
		Remove(tmp)
		return err
	}
	return Rename(tmp, dst)
}

// progressWriter reads ffmpeg's -progress output (key=value lines) and
// passes out_time_us on as seconds.
type progressWriter struct {
	fn  func(seconds float64)
	buf []byte
}

func (w *progressWriter) Write(b []byte) (int, error) {
	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(b), nil
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if v, ok := strings.CutPrefix(line, "out_time_us="); ok {
			if us, err := strconv.ParseInt(v, 10, 64); err == nil && us >= 0 {
				w.fn(float64(us) / 1e6)
			}
		}
	}
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
