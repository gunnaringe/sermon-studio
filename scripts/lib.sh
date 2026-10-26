# Shared by scripts/test.sh and scripts/dev.sh (sourced, from the repository's top folder): the container
# runner, the images, and the test images with ffmpeg.
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
