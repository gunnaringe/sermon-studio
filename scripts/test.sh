#!/bin/sh
# One entry point for checks, with short output (ok lines and failures only).
#
#   scripts/test.sh            unit tests (no ffmpeg; seconds; host Go or container)
#   scripts/test.sh all [args] vet + all tests with ffmpeg, in a container;
#                              args go to "go test" (default ./...), e.g.
#                              scripts/test.sh all ./web/ -run Inbox
#   scripts/test.sh desktop    the desktop program as released (ffmpeg built in, Linux amd64) from the
#                              outside: start, upload, make a sermon, stop by itself (TestEndToEnd)
#   scripts/test.sh js         the check of the JavaScript (web/static, site): names, imports, calls (tsc, jsconfig.json)
#   scripts/test.sh e2e [args] browser tests (Playwright); args go to playwright,
#                              e.g. scripts/test.sh e2e -g upload
#   scripts/test.sh guide      the pictures of the documentation (docs/img), from a real speech;
#                              fetch it first with scripts/fetch-sample.sh
#   scripts/test.sh site [args] the website in site/ (Playwright, served as files): pages, languages, links
#   scripts/test.sh site-shots [shots|video]  the website's pictures and video (use scripts/site-shots.sh)
#   scripts/test.sh image      the Docker image (vet + all tests inside)
#
# ffmpeg comes in images built once from scripts/test-images/ (kept, retagged when they change).
#
# Go modules and build cache live in named volumes, so repeat runs are fast.
set -eu
cd "$(dirname "$0")/.."
run=$(command -v podman || command -v docker) || { echo "needs podman or docker" >&2; exit 1; }
golang=docker.io/library/golang:1.25-alpine3.22
playwright=mcr.microsoft.com/playwright:v1.64.1-noble # keep equal to web/e2e/package.json
quiet_npm="-e NPM_CONFIG_UPDATE_NOTIFIER=false" # no "new version of npm" notice in the output
# testimage <name> <base>: the image scripts/test-images/<name>.Containerfile makes from base (ffmpeg added),
# built the first time and kept; its tag changes with the file and the base, so either changing builds it anew.
testimage() {
	file=scripts/test-images/$1.Containerfile
	tag="localhost/sermon-studio-test-$1:$( (cat "$file"; echo "$2") | sha256sum | cut -c1-12)"
	if ! "$run" image inspect "$tag" >/dev/null 2>&1; then
		echo "building $tag (once)" >&2
		"$run" build -q -t "$tag" --build-arg BASE="$2" -f "$file" scripts/test-images >/dev/null || exit 1
	fi
	echo "$tag"
}
incontainer() { # incontainer <script> [args for the script's "$@"]; IMAGE overrides the Go image
	script=$1
	shift
	"$run" run --rm -v "$PWD":/src:Z -w /src -v gomod:/go/pkg/mod -v gocache:/root/.cache/go-build \
		-e CGO_ENABLED=0 "${IMAGE:-$golang}" sh -c "$script" sh "$@"
}

mode=${1:-unit}
[ $# -gt 0 ] && shift
case $mode in
unit)
	if command -v go >/dev/null; then
		test -z "$(gofmt -l cmd normalizer web)" || { gofmt -l cmd normalizer web; echo "gofmt needed" >&2; exit 1; }
		go vet ./... && go test -short ./...
	else
		incontainer 'test -z "$(gofmt -l cmd normalizer web)" || { gofmt -l cmd normalizer web; exit 1; }; go vet ./... && go test -short ./...'
	fi
	;;
all)
	[ $# -eq 0 ] && set -- ./...
	IMAGE=$(testimage go "$golang") incontainer 'test -z "$(gofmt -l cmd normalizer web)" && go vet ./... && FFMPEG_REQUIRED=1 go test "$@"' "$@"
	;;
desktop)
	TARGETS=linux-amd64 scripts/build-desktop.sh test >/dev/null
	IMAGE=$(testimage go "$golang") incontainer 'DESKTOP_BIN=/src/dist/sermon-studio-desktop-linux-amd64 DESKTOP_EMBEDDED=1 go test -count=1 -run EndToEnd ./cmd/sermon-studio-desktop/'
	;;
js)
	image=$(testimage e2e "$playwright")
	"$run" run --rm $quiet_npm -v "$PWD":/src:Z -w /src/web/e2e "$image" sh -c \
		'lock=$(sha256sum package-lock.json | cut -c1-64)
		[ "$(cat node_modules/.lock-sha 2>/dev/null)" = "$lock" ] || { npm ci --silent --no-audit --no-fund && echo "$lock" >node_modules/.lock-sha; }
		npx tsc -p ../../jsconfig.json && echo "js ok"'
	;;
e2e)
	incontainer 'go build -o web/e2e/bin/sermon-studio ./cmd/sermon-studio'
	image=$(testimage e2e "$playwright")
	"$run" run --rm $quiet_npm --ipc=host -v "$PWD/web/e2e":/work:Z -w /work "$image" sh run.sh "$@" 2>&1 |
		grep -v -e '^debconf' -e '^$'
	;;
guide)
	mkdir -p docs/img
	incontainer 'go build -o web/e2e/bin/sermon-studio ./cmd/sermon-studio'
	image=$(testimage e2e "$playwright")
	"$run" run --rm $quiet_npm --ipc=host -v "$PWD/web/e2e":/work:Z -v "$PWD/docs/img":/out:Z -w /work "$image" sh guide.sh "$@" 2>&1 |
		grep -v -e '^debconf' -e '^$'
	;;
site)
	image=$(testimage e2e "$playwright")
	mkdir -p web/e2e/screenshots
	"$run" run --rm $quiet_npm --ipc=host -v "$PWD/web/e2e":/work:Z -v "$PWD/site":/site:ro,z -w /work "$image" sh site/run.sh "$@"
	;;
site-shots)
	incontainer 'go build -o web/e2e/bin/sermon-studio ./cmd/sermon-studio'
	image=$(testimage e2e "$playwright")
	mkdir -p site/img/shots site/media
	"$run" run --rm $quiet_npm --ipc=host -v "$PWD/web/e2e":/work:Z -v "$PWD/site":/site:z -v "$PWD/web/static":/static:ro,z -w /work \
		"$image" sh site-shots/run.sh "$@"
	;;
image)
	"$run" build -q -t sermon-studio .
	;;
*)
	sed -n '2,18p' "$0"
	exit 2
	;;
esac
