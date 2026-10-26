#!/bin/sh
# Runs the studio from this checkout, to try it in a browser or with curl: no login, one church with President
# Kennedy's inaugural address in its inbox, ffmpeg included. It runs in a container (the Go test image) until stopped;
# every start builds anew and begins with fresh data.
#
#   scripts/dev.sh [--demo] [--port 8090]   start (or start again); waits until it answers, prints its address
#   scripts/dev.sh logs                     the server's log
#   scripts/dev.sh stop
#
# --demo runs it as the public demo does: --visitor-role demo, the church demo-church ("Demo"), and its intro and outro
# when ~/.cache/sermon-studio-samples/ has them (demo/make-jingles.sh). The recording is service.flac from there
# (demo/make-recording.sh: with quiet and room noise around the speech) if it is there, else the speech as it is,
# downloaded once.
set -eu
cd "$(dirname "$0")/.."
. scripts/lib.sh
name=sermon-studio-dev
case ${1:-} in
stop) "$run" rm -f "$name" >/dev/null 2>&1 || true; echo "stopped"; exit 0 ;;
logs) exec "$run" logs "$name" ;;
esac
demo=false port=8090
while [ $# -gt 0 ]; do
	case $1 in
	--demo) demo=true ;;
	--port) port=$2; shift ;;
	*) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
	esac
	shift
done

samples=$HOME/.cache/sermon-studio-samples
mkdir -p "$samples"
if [ ! -s "$samples/service.flac" ] && [ ! -s "$samples/jfk-inaugural.ogg" ]; then
	echo "downloading the speech (6 MB, once)" >&2
	curl -fsSL -A 'sermon-studio-dev (https://sermon-studio.app)' -o "$samples/jfk-inaugural.ogg.part" \
		https://upload.wikimedia.org/wikipedia/commons/d/d5/JFK_inaugural_address.ogg
	mv "$samples/jfk-inaugural.ogg.part" "$samples/jfk-inaugural.ogg"
fi
if $demo; then church=demo-church title=Demo role=demo; else church=dev-church title="Dev Church" role=admin; fi

"$run" rm -f "$name" >/dev/null 2>&1 || true
image=$(testimage go "$golang")
# In the container: build, make the church (as the demo's deployment does), put the recording in its inbox with an
# old time (so it is taken at once), and serve.
"$run" run -d --name "$name" -p "127.0.0.1:$port:8080" -v "$PWD":/src:ro,Z -w /src \
	-v gomod:/go/pkg/mod -v gocache:/root/.cache/go-build -v "$samples":/samples:ro,z -e CGO_ENABLED=0 \
	-e CHURCH="$church" -e TITLE="$title" -e ROLE="$role" -e DEMO="$demo" "$image" sh -c '
set -e
go build -o /tmp/sermon-studio ./cmd/sermon-studio
c=/data/$CHURCH
mkdir -p "$c/Taler" "$c/Innboks"
lib=""
if $DEMO && [ -s /samples/intro.flac ] && [ -s /samples/outro.flac ]; then
	mkdir -p "$c/clips"
	now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	clip() {
		cp /samples/$2.flac "$c/clips/$1.flac"
		s=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$c/clips/$1.flac")
		v=$(sha256sum "$c/clips/$1.flac" | cut -c1-12)
		printf "{\"id\":\"%s\",\"name\":\"%s\",\"seconds\":%s,\"version\":\"%s\",\"added\":\"%s\"}" "$1" "$3" "$s" "$v" "$now"
	}
	lib=$(printf ",\"clips\":{\"clips\":[%s,%s],\"intro\":\"00000000d001\",\"outro\":\"00000000d002\"}" \
		"$(clip 00000000d001 intro "Demo intro")" "$(clip 00000000d002 outro "Demo outro")")
fi
printf "{\"name\":\"%s\"%s}" "$TITLE" "$lib" >"$c/church.json"
if [ -s /samples/service.flac ]; then rec=/samples/service.flac; else rec=/samples/jfk-inaugural.ogg; fi
cp "$rec" "$c/Innboks/1961-01-20.${rec##*.}"
touch -d "1961-01-20 12:00:00" "$c/Innboks/1961-01-20.${rec##*.}"
exec /tmp/sermon-studio --listen=:8080 --data-dir=/data --cache-dir=/data/cache --no-login --visitor-role="$ROLE"
' >/dev/null

url=http://127.0.0.1:$port/
for i in $(seq 180); do
	curl -sf "${url}healthz" >/dev/null 2>&1 && break
	if [ "$("$run" inspect -f '{{.State.Running}}' "$name" 2>/dev/null)" != true ] || [ "$i" = 180 ]; then
		"$run" logs "$name" 2>&1 | tail -20 >&2
		echo "the studio did not start" >&2
		exit 1
	fi
	sleep 1
done
echo "$url  (church $church, role $role; the inbox files the recording within ~10 s; scripts/dev.sh stop)"
