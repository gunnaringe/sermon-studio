# The Go tests' image with ffmpeg in it, built once by scripts/test.sh (instead of installing
# ffmpeg on every "scripts/test.sh all"). BASE is the Go image test.sh names. ffmpeg is the image's
# (FFMPEG_URL, checked by FFMPEG_SHA256: scripts/lib.sh testimage); Alpine's is here only for ffprobe.
ARG BASE
FROM ${BASE}
RUN apk add -q --no-cache ffmpeg
ARG FFMPEG_URL FFMPEG_SHA256
# Checked in its own step: ADD --checksum is not in the podman of GitHub's runners.
ADD --chmod=755 ${FFMPEG_URL} /usr/local/bin/ffmpeg
RUN echo "${FFMPEG_SHA256}  /usr/local/bin/ffmpeg" | sha256sum -c - >/dev/null
