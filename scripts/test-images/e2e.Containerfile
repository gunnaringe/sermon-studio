# The browser tests' image: Playwright with ffmpeg in it, built once by scripts/test.sh
# (instead of installing ffmpeg on every run). BASE is the Playwright image test.sh names. ffmpeg is the
# image's (FFMPEG_URL, checked by FFMPEG_SHA256: scripts/lib.sh testimage); Ubuntu's is here only for ffprobe.
ARG BASE
FROM ${BASE}
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends ffmpeg >/dev/null && rm -rf /var/lib/apt/lists/*
ARG FFMPEG_URL FFMPEG_SHA256
ADD --chmod=755 --checksum=sha256:${FFMPEG_SHA256} ${FFMPEG_URL} /usr/local/bin/ffmpeg
