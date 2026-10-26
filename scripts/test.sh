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
#   scripts/test.sh changed    the checks for what this branch changes (since it left origin/main, uncommitted too),
#                              cheapest first, stopping at the first that fails; prints which it runs
#                              (PLAN_ONLY=1: only that)
#
# ffmpeg comes in images built once from scripts/test-images/ (kept, retagged when they change).
#
# Go modules and build cache live in named volumes, so repeat runs are fast.
set -eu
cd "$(dirname "$0")/.."
. scripts/lib.sh

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
changed)
	base=$(git merge-base origin/main HEAD)
	files=$( (git diff --name-only "$base"; git ls-files --others --exclude-standard) | sort -u)
	has() { printf '%s\n' "$files" | grep -Eq "$1"; }
	plan=unit
	has '\.md$' && plan="$plan links"
	has '^(web/static/|site/.*\.js$|jsconfig\.json$|web/js\.d\.ts$)' && plan="$plan js"
	has '^(site/|web/e2e/site/)' && plan="$plan site"
	has '^(normalizer/|go\.(mod|sum)$)' && plan="$plan normalizer"
	has '^cmd/sermon-studio-desktop/' && plan="$plan desktop"
	# The browser tests: the page, the tests, or the server code behind them.
	if has '^(web/static/|web/e2e/(tests/|run\.sh$|package))' ||
		printf '%s\n' "$files" | grep -E '^(web|cmd/sermon-studio)/[^/]+\.go$' | grep -vq '_test\.go$'; then
		plan="$plan e2e"
	fi
	echo "$(printf '%s\n' "$files" | grep -c .) files changed since origin/main; running: $plan"
	[ -n "${PLAN_ONLY:-}" ] && exit 0
	for step in $plan; do
		echo "== $step"
		case $step in
		links) scripts/check-links.sh && echo "links ok" ;;
		normalizer) "$0" all ./normalizer/ ;;
		*) "$0" "$step" ;;
		esac
	done
	;;
*)
	sed -n '2,21p' "$0"
	exit 2
	;;
esac
