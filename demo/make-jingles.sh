#!/bin/sh
# Makes the demo's intro and outro (intro.flac, outro.flac here; see README.md): music synthesized with ffmpeg
# (jingle.py), a line read by Piper's "joe" voice over it, the music ducked under the voice, at about -22 LUFS.
# Needs python3, piper (pip install piper-tts) and ffmpeg, or podman, which runs ffmpeg in an Alpine container.
# The voice is downloaded once into ~/.cache/sermon-studio-samples/. A new run
# sounds the same, though the files may differ byte for byte. OUT=<folder> writes them elsewhere.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=${OUT:-$here}
work=$(mktemp -d)
chmod 777 "$work"
python3 "$here/jingle.py" "$work"

voice=$HOME/.cache/sermon-studio-samples/en_US-joe-medium.onnx
if [ ! -s "$voice" ]; then
	mkdir -p "$(dirname "$voice")"
	for f in onnx onnx.json; do
		curl -fsSL -o "$voice.part" "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/joe/medium/en_US-joe-medium.$f"
		mv "$voice.part" "${voice%.onnx}.$f"
	done
fi
echo "Welcome to Demo Church. Here is this Sunday's sermon." | piper -m "$voice" -f "$work/voice-intro.wav"
echo "Thank you for listening. You will find more sermons from Demo Church online." | piper -m "$voice" -f "$work/voice-outro.wav"
chmod 666 "$work"/*

# The music (12 and 14 seconds, a little echo, faded out), then the voice over it from 1 second.
cat >"$work/ff.sh" <<'FF'
set -e
cd "$1"
for w in intro:12 outro:14; do
	n=${w%%:*}; d=${w##*:}
	ffmpeg -v error -y -f lavfi -i "aevalsrc=exprs='$(cat $n.expr)':s=44100:d=$d" \
		-af "aecho=0.8:0.6:70|140|260:0.25|0.15|0.08,afade=t=out:st=$((d-2)):d=2,loudnorm=I=-20:TP=-2:LRA=11" \
		-ar 44100 -c:a flac $n-music.flac
	ffmpeg -v error -y -i $n-music.flac -i voice-$n.wav -filter_complex "
		[1:a]aresample=44100,adelay=delays=1000:all=1,apad,asplit[v1][v2];
		[0:a][v1]sidechaincompress=threshold=0.015:ratio=5:attack=40:release=600[m];
		[m][v2]amix=inputs=2:duration=first:normalize=0:weights=1 1.6,loudnorm=I=-20:TP=-2:LRA=11" \
		-ar 44100 -c:a flac $n.flac
done
FF
if command -v ffmpeg >/dev/null; then
	sh "$work/ff.sh" "$work"
else
	podman run --rm -v "$work:/j:Z" docker.io/library/alpine:3.22 sh -c 'apk add -q --no-cache ffmpeg && sh /j/ff.sh /j'
fi
cp "$work/intro.flac" "$work/outro.flac" "$out/"
echo "made $out/intro.flac and $out/outro.flac (work files in $work)"
