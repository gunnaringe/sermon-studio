# Transcripts

A sermon can have a transcript, as text to read and captions (WebVTT): **Transcript** in the editor, which starts as the
church's default (**Settings → Processing → Transcripts**). It is made on the server after the files, from the files of
the cut sermon only (no intro or outro, never the recording), by whisper.cpp's `whisper-cli` with a speech model, on the CPU. Names and Bible references can come
out wrong: read it before you publish it.

## Speech models

The studio only reads a models folder, `--models-dir` (default `<data-dir>/.models`): `models.json`, which lists the
models and their languages, and the files it names. It reads it each time it needs it, so a model that arrives is used
from the next transcript, with no restart. Without the folder, or without whisper.cpp's `whisper-cli` (`--whisper-cli`,
default `whisper-cli` in `PATH`; the image has Alpine's), the studio makes no transcripts and says why in its log;
`--no-transcripts` turns them off. The desktop app has none.

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

**Speed.** One transcript is made at a time, on all the cores the studio may use, after the files, in the order
they were asked for (across all churches). It shares the cores with runs, at the lowest priority (`nice 19`): a run, or
a page, while a transcript is being made goes first, and the transcript waits. In a container this only ranks the
container's own work; against other containers, give the studio's a CPU limit if transcripts take too much of the
machine (whisper then uses as many threads as the limit allows). Alpine's whisper.cpp loads the CPU code for the
processor it runs on (AVX2 and FMA on one from 2013 or later), so it runs on any x86-64 or arm64, faster on a newer one.

On the studio's server (3 shared cores of an AMD EPYC from 2017), measured on a sermon:

| Model | Time, as many times the sermon | |
|---|---|---|
| NB-Whisper large | 7.5 | the clearest text: the default for Bokmål and Nynorsk there |
| NB-Whisper medium | 5 | sentences missing and garbled in places |
| NB-Whisper small | about 1.5 (from medium; not measured) | |

So a sermon of 40 minutes takes about 5 hours with large: it is there the next morning. Beam search (five candidates
instead of one) took 75 % longer with large for the same text, so whisper takes one (greedy).

**In parts, kept across restarts.** The sermon is transcribed in parts of about ten minutes, cut at a pause, each saved
as it is done (in a hidden `.<date>.transcript.parts` folder next to it, removed when the transcript is there); each part
starts with the last words of the one before as whisper's prompt. A transcript asked for is kept in the recording's
`recording.json` until it is made, stopped or fails: when the studio stops (an update, a restart of the server), the
ones waiting are queued again when it starts, in their order, and the one being made goes on from its last part done.

## When it is made

- **Save** in the editor makes it when **Transcript** is on and the one there is not of this cut, language and model:
  after the files, when Save makes them, else at once. Transcript off deletes it, as a format turned off is. Nothing
  makes one by itself.
- **Transcript** starts as the recording has it (on, in its language and with its model) when it has one; else it is on
  when the church has a language (**Settings → Processing → Transcripts**, *No transcripts* = off), in that language.
- **The model** is the church's for the language. Settings shows the model for the church's language on one line, and
  folded away under *Models for other languages*, one line for each other language; until the church chooses another,
  it is the first for the language in `models.json`. **Change…** opens a dialog with every installed model for the
  language, filtered by size (tiny … large-v3-turbo, from the model's name) and by name; *Use the default* goes back. A
  church's choice is kept by the model's repository and file, so a new version of it is taken, and is marked
  *changed*. The editor shows the model's name, and Change… there makes this recording's transcript with another; the
  church's is marked *default*.
- **Make transcript** / **Make again** on the recordings page (a sermon's details), in the transcript's language, else
  the church's, with the church's model for it.
- While one is made, a bar shows how far it is, with ✕ to stop it.
- A transcript made **from an earlier cut** (the sermon was cut again since) or **with another version of its model**
  says so; it is not made again by itself.

The files, next to the sermon's (see [outputs](outputs.md)):

```
2026-10-04/
  2026-10-04.transcript.json  the transcript: its pieces with their times, the model and its version, the language
  2026-10-04.txt              the text, a paragraph at each longer pause
  2026-10-04.vtt              captions (WebVTT)
```

The desktop app makes no transcripts.
