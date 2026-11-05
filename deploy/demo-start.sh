#!/bin/sh
# The public demo's start (quadlet/sermon-studio-demo.container), inside its container: makes the church in the
# empty /data (a tmpfs, so every start begins afresh), puts the recording in its inbox, then runs the studio
# without the login, every visitor in the role demo. "demo" is a reserved church id, so the church is
# demo-church.
#
# The recording is John F. Kennedy's inaugural address (1961-01-20, about 14 minutes), a US government work in
# the public domain, from Wikimedia Commons: downloaded once into /seed, never kept in a repository. It is
# made once into a "service" (service-v2.flac, lossless): a minute of quiet before and after the speech, with
# room noise under all of it, as demo/make-recording.sh makes it (brown noise, 60 Hz-4 kHz, about -62 dBFS:
# below the speech's own noise, so the studio still finds the first and last minute as silences and proposes
# the speech as the sermon). A new recipe gets a new name. The file keeps an old time, so the inbox takes it
# at once instead of waiting for it to settle.
#
# The church's default intro and outro, when they are in /seed/clips (intro.flac and outro.flac, made by
# demo/make-jingles.sh and copied there by hand): put in as an upload would, in church.json's "clips".
set -e
church=/data/demo-church
mkdir -p "$church/Taler" "$church/Innboks" /data/cache

lib=''
if [ -s /seed/clips/intro.flac ] && [ -s /seed/clips/outro.flac ]; then
  mkdir -p "$church/clips"
  now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  clip() { # clip <id> <file> <name>: copies it in and prints its entry
    cp /seed/clips/$2.flac "$church/clips/$1.flac"
    # Its length from ffmpeg's Duration line (the image has ffmpeg but no ffprobe).
    s=$(ffmpeg -hide_banner -i "$church/clips/$1.flac" 2>&1 | sed -n 's/.*Duration: \([0-9:.]*\),.*/\1/p' | awk -F: '{print $1*3600+$2*60+$3}')
    v=$(sha256sum "$church/clips/$1.flac" | cut -c1-12)
    printf '{"id":"%s","name":"%s","seconds":%s,"version":"%s","added":"%s"}' "$1" "$3" "$s" "$v" "$now"
  }
  lib=$(printf ',"clips":{"clips":[%s,%s],"intro":"00000000d001","outro":"00000000d002"}' \
    "$(clip 00000000d001 intro 'Demo intro')" "$(clip 00000000d002 outro 'Demo outro')")
fi
printf '{"name":"Demo"%s}' "$lib" > "$church/church.json"

if [ ! -s /seed/speech.ogg ]; then
  wget -q -U 'sermon-studio-demo (https://github.com/gunnaringe/sermon-studio)' -O /seed/speech.ogg.part \
    https://upload.wikimedia.org/wikipedia/commons/d/d5/JFK_inaugural_address.ogg
  touch -d '1961-01-20 12:00:00' /seed/speech.ogg.part
  mv /seed/speech.ogg.part /seed/speech.ogg
fi
if [ ! -s /seed/service-v2.flac ]; then
  ffmpeg -v error -y -i /seed/speech.ogg \
    -f lavfi -i 'anoisesrc=color=brown:amplitude=0.5:sample_rate=44100:duration=960:seed=1961' \
    -filter_complex '[0:a]adelay=delays=60000:all=1,apad=pad_dur=60[s];[1:a]highpass=f=60,lowpass=f=4000,volume=0.0098[n];[s][n]amix=inputs=2:duration=first:normalize=0' \
    -c:a flac -f flac /seed/service-v2.flac.part
  touch -d '1961-01-20 12:00:00' /seed/service-v2.flac.part
  mv /seed/service-v2.flac.part /seed/service-v2.flac
  rm -f /seed/service.flac
fi
cp -p /seed/service-v2.flac "$church/Innboks/1961-01-20.flac"

exec sermon-studio --listen=:8080 --data-dir=/data --cache-dir=/data/cache --no-login --visitor-role=demo --parallel-runs=1
