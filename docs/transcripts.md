# Transcripts

A church can have a transcript of every sermon, as text to read and captions (WebVTT): **Settings → Processing →
Transcripts**, the language of its sermons. It is made on the server after the files, from the sermon only (no intro or
outro), by ffmpeg's `whisper` filter (whisper.cpp) with a speech model, on the CPU. Names and Bible references can come
out wrong: read it before you publish it.

## Speech models

The studio only reads a models folder, `--models-dir` (default `<data-dir>/.models`): `models.json`, which lists the
models and their languages, and the files it names. It reads it each time it needs it, so a model that arrives is used
from the next transcript, with no restart. Without the folder, without a whisper filter in ffmpeg, or on an x86 processor
without AVX and F16C (before 2012), the studio makes no transcripts and says why in its log; `--no-transcripts` turns
them off.

`sermon-studio models pull` fills the folder from Hugging Face:

```bash
sermon-studio models pull --dir /data/.models \
  --model nb,nn=NbAiLab/nb-whisper-medium/ggml-model-q5_0.bin \
  --model sv=KBLab/kb-whisper-medium/ggml-model-q5_0.bin \
  --model '*=ggerganov/whisper.cpp/ggml-medium-q5_0.bin' \
  --every 24h
```

- `--model LANGS=owner/name/file` follows the repository's `main`; the file's URL on Hugging Face
  (`https://huggingface.co/<owner>/<name>/resolve/<revision>/<file>`) follows that revision instead. The languages are
  whisper's codes (`nb` is Bokmål); `*` is any language, which whisper finds itself. For a language, the first model
  given that has it is used, else the first for `*`.
- A file is downloaded only when Hugging Face's own scans call it safe, and is checked against the SHA-256 Hugging Face
  gives for it. One that fails keeps the version already there.
- `--every` keeps it running and looks again that often: a model that changed is downloaded beside the old one, then
  `models.json` names the new and the old is removed. Without it, it pulls once and stops.
- `models.json` can also be written by hand: `{"models": [{"name": "…", "file": "…", "languages": ["nb"]}]}`, with the
  file in the folder. `pull` keeps such entries (after its own) and the files it did not download.
- `sermon-studio models list --dir …` shows what is there.

On Kubernetes, run `pull --every 24h` as a second container in the studio's pod, sharing the models folder: the studio
starts at once, and transcripts are offered once the first model is there.

```yaml
- name: models
  image: ghcr.io/gunnaringe/sermon-studio:…
  args: [models, pull, --dir, /data/.models, --every, 24h,
         --model, "nb,nn=NbAiLab/nb-whisper-medium/ggml-model-q5_0.bin"]
  volumeMounts: [{name: data, mountPath: /data}]
```

**Speed.** One transcript is made at a time, on all the cores the studio may use, after the files. It shares them
with runs: a run while a transcript is being made slows both, the transcript most. On four cores of a desktop processor from 2014, whisper's small model takes a little longer than the sermon
(half as long again when it finds the language itself), and a medium model, which makes fewer mistakes, about three
times as long as small. A newer server is several times faster. Pick the model by what the server can keep up with.

## When it is made

- **After a run**, when the church has a language and the sermon has no transcript yet. Never again by itself.
- **Make transcript** / **Make again** on the recordings page (a sermon's details), for admins and members.
- A transcript made **from an earlier cut** (the sermon was cut again since) or **with another model** than the one there
  now says so, with **Make again**; it is not made again by itself.

The files, next to the sermon's (see [outputs](outputs.md)):

```
2026-10-04/
  2026-10-04.transcript.json  the transcript: its pieces with their times, the model and its version, the language
  2026-10-04.txt              the text, a paragraph at each longer pause
  2026-10-04.vtt              captions (WebVTT)
```

The desktop app makes no transcripts.
