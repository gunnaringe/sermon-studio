#!/bin/sh
# Makes the demo's recording (README.md): President Kennedy's inaugural address (1961-01-20, about 14 minutes, public
# domain, from Wikimedia Commons) as a service, with a minute of quiet before and after it and faint room noise under
# all of it. The tools come from mise.toml here (mise install). The speech is downloaded once into
# ~/.cache/sermon-studio-samples/, and service.flac goes there too (OUT=<folder> for another), not into the repository.
# The demo's deployment makes the same file the same way (clusters: kate/norkirken/sermon-studio-demo/).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
cache=$HOME/.cache/sermon-studio-samples
out=${OUT:-$cache}
mkdir -p "$cache" "$out"
speech=$cache/jfk-inaugural.ogg
if [ ! -s "$speech" ]; then
	curl -fsSL -A 'sermon-studio-demo (https://sermon-studio.app; contact@sermon-studio.app)' -o "$speech.part" \
		https://upload.wikimedia.org/wikipedia/commons/d/d5/JFK_inaugural_address.ogg
	mv "$speech.part" "$speech"
fi
# The room noise: brown noise, 60 Hz-4 kHz, at about -62 dBFS. That is below the speech's own noise (about -58), so the
# studio still finds the first and last minute as silences and proposes the speech as the sermon; louder, they break up.
mise exec -C "$here" -- ffmpeg -v error -y -i "$speech" \
	-f lavfi -i 'anoisesrc=color=brown:amplitude=0.5:sample_rate=44100:duration=960:seed=1961' \
	-filter_complex '[0:a]adelay=delays=60000:all=1,apad=pad_dur=60[s];[1:a]highpass=f=60,lowpass=f=4000,volume=0.0098[n];[s][n]amix=inputs=2:duration=first:normalize=0' \
	-c:a flac -f flac "$out/service.flac"
echo "made $out/service.flac"
