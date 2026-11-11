# The browser tests' image: Playwright with ffmpeg in it, built once by scripts/test.sh
# (instead of installing ffmpeg on every run). BASE is the Playwright image test.sh names. ffmpeg is the
# image's (FFMPEG_URL, checked by FFMPEG_SHA256: scripts/lib.sh testimage); Ubuntu's is here for ffprobe and for the
# website's pictures and video (site-shots/run.sh), which need encoders the small one has not. PulseAudio is the sound
# card the video's sound is recorded from.
ARG BASE
FROM ${BASE}
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends ffmpeg pulseaudio pulseaudio-utils >/dev/null && rm -rf /var/lib/apt/lists/*
ARG FFMPEG_URL FFMPEG_SHA256
# Checked in its own step: ADD --checksum is not in the podman of GitHub's runners.
ADD --chmod=755 ${FFMPEG_URL} /usr/local/bin/ffmpeg
RUN echo "${FFMPEG_SHA256}  /usr/local/bin/ffmpeg" | sha256sum -c - >/dev/null
