# Build: docker build -t sermon-studio .
# Run:   docker run --rm -p 8080:8080 -u "$(id -u):$(id -g)" -v /path/to/Taler:/data/Taler -v /path/to/cache:/cache \
#          sermon-studio --no-login --root /data/Taler --cache-dir /cache

# The build stage runs on the builder's own platform: the tests run once, natively, and Go cross-compiles
# for each target (linux/amd64, linux/arm64). Only the final stage is per platform.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine3.24 AS build
# ffmpeg is the desktop app's: the release of github.com/gunnaringe/ffmpeg-lgpl in packaging/ffmpeg-release,
# checked against packaging/ffmpeg.sha256 (a small static LGPL build, without network protocols). The tests
# run against it; Alpine's ffmpeg is here only for ffprobe, which the tests also use (/usr/local/bin comes
# first in PATH).
RUN apk add --no-cache ffmpeg
WORKDIR /src
COPY packaging/ffmpeg-release packaging/ffmpeg.sha256 ./packaging/
# Both systems' builds, so this and the tests are the same layers for every target; the tests use this machine's.
RUN . packaging/ffmpeg-release && mkdir /ffmpeg && cd /ffmpeg \
 && for f in ffmpeg-linux-amd64 ffmpeg-linux-arm64 LICENSES.txt; do \
      wget -q "https://github.com/$FFMPEG_REPO/releases/download/$FFMPEG_RELEASE/$f" || exit 1; \
      grep "  $f\$" /src/packaging/ffmpeg.sha256 | sha256sum -c -s || { echo "$f does not match packaging/ffmpeg.sha256" >&2; exit 1; }; \
    done \
 && chmod +x ffmpeg-linux-* && cp "ffmpeg-linux-$(case $(uname -m) in aarch64) echo arm64 ;; *) echo amd64 ;; esac)" /usr/local/bin/ffmpeg
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY models ./models
COPY normalizer ./normalizer
COPY web ./web
RUN go vet ./... && FFMPEG_REQUIRED=1 go test ./...
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /sermon-studio ./cmd/sermon-studio

FROM alpine:3.24
ARG TARGETARCH
COPY --from=build /ffmpeg/ffmpeg-linux-$TARGETARCH /usr/local/bin/ffmpeg
COPY --from=build /ffmpeg/LICENSES.txt /usr/local/share/ffmpeg/LICENSES.txt
# The encoders the studio uses are there (on arm64, this runs emulated in CI).
RUN ffmpeg -hide_banner -encoders 2>/dev/null | grep -E "libopus|libmp3lame" | wc -l | grep -qx 2
COPY --from=build /sermon-studio /usr/local/bin/
WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["sermon-studio"]
CMD ["--help"]
