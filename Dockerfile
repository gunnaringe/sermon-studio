# Build: docker build -t sermon-studio .
# Run:   docker run --rm -p 8080:8080 -u "$(id -u):$(id -g)" -v /path/to/Taler:/data/Taler -v /path/to/cache:/cache \
#          sermon-studio --no-login --root /data/Taler --cache-dir /cache

# The build stage runs on the builder's own platform: the tests run once, natively, and Go cross-compiles
# for each target (linux/amd64, linux/arm64). Only the final stage is per platform.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine3.24 AS build
# Alpine's ffmpeg, the same as in the image: the tests run against it (with ffprobe).
RUN apk add --no-cache ffmpeg
WORKDIR /src
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

FROM alpine:3.24 AS image
# Alpine's ffmpeg, and whisper.cpp's whisper-cli for transcripts: its CPU code comes in a variant per processor
# generation, and it loads the best one there (haswell, with AVX2 and FMA, on a processor from 2013 or later).
# Not pinned: every build of main installs them afresh (image.yml), with Alpine's fixes.
RUN apk add --no-cache ffmpeg whisper.cpp
# The encoders the studio uses are there, and whisper-cli runs (on arm64, this runs emulated in CI).
RUN ffmpeg -hide_banner -encoders 2>/dev/null | grep -E "libopus|libmp3lame" | wc -l | grep -qx 2 && whisper-cli -h >/dev/null 2>&1
COPY --from=build /sermon-studio /usr/local/bin/
WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["sermon-studio"]
CMD ["--help"]
