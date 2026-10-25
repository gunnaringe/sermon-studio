#!/bin/sh
# Makes the demo's intro and outro (README.md): music synthesized with ffmpeg (jingle.py), a line read by Piper's
# "joe" voice over it from the first second, the music ducked under the voice, at about -22 LUFS. The tools come
# from mise.toml here (mise install). The voice is downloaded once into ~/.cache/sermon-studio-samples/; the clips go
# there too (OUT=<folder> for another), not into the repository.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
cache=$HOME/.cache/sermon-studio-samples
out=${OUT:-$cache}
mkdir -p "$cache" "$out"
work=$(mktemp -d)
run() { mise exec -C "$here" -- "$@"; }

run python3 "$here/jingle.py" "$work"
voice=$cache/en_US-joe-medium.onnx
for f in onnx onnx.json; do
	[ -s "${voice%.onnx}.$f" ] && continue
	curl -fsSL -o "$voice.part" "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/joe/medium/en_US-joe-medium.$f"
	mv "$voice.part" "${voice%.onnx}.$f"
done
echo "This is a speech by John F. Kennedy." | run piper -m "$voice" -f "$work/voice-intro.wav"
echo "Thank you for listening." | run piper -m "$voice" -f "$work/voice-outro.wav"

for w in intro:12 outro:14; do
	n=${w%%:*} d=${w##*:}
	# The music: a little echo, faded out over the last two seconds.
	run ffmpeg -v error -y -f lavfi -i "aevalsrc=exprs='$(cat "$work/$n.expr")':s=44100:d=$d" \
		-af "aecho=0.8:0.6:70|140|260:0.25|0.15|0.08,afade=t=out:st=$((d - 2)):d=2,loudnorm=I=-20:TP=-2:LRA=11" \
		-ar 44100 -c:a flac "$work/$n-music.flac"
	# The voice over it, from the first second; the music goes down while it speaks.
	run ffmpeg -v error -y -i "$work/$n-music.flac" -i "$work/voice-$n.wav" -filter_complex "
		[1:a]aresample=44100,adelay=delays=1000:all=1,apad,asplit[v1][v2];
		[0:a][v1]sidechaincompress=threshold=0.015:ratio=5:attack=40:release=600[m];
		[m][v2]amix=inputs=2:duration=first:normalize=0:weights=1 1.6,loudnorm=I=-20:TP=-2:LRA=11" \
		-ar 44100 -c:a flac "$out/$n.flac"
done
echo "made $out/intro.flac and $out/outro.flac"
