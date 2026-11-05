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

**The queue, kept across restarts.** The transcripts asked for wait in the studio's queue, shared by its churches:
`<data-dir>/.transcripts/queue.json` (with `--root`, `.transcripts` in the models folder), first asked first made. The
page says how many are ahead. The sermon is transcribed in parts of about ten minutes, cut at a pause, each saved as it
is done (in `.transcripts/<job>/`, removed with the job). When the studio stops (an update, a restart of the server),
the queue stays: the ones waiting wait again in their order, and the one being made goes on from its last part done.
A recording renamed or moved takes its place in the queue along; one deleted, or ✕, takes it out. Nothing about the
queue is in the recording's folder.

**Made from the files there.** Before each part the studio checks that the sermon's files are the ones it began with;
if they were made again (the sermon cut again and saved), it starts over from the new ones.

**When it fails.** A transcript that fails is tried once more, at its turn; the second time, it leaves the queue and
its page says why (with **Make again**). One that cannot be made (no files to make it from, no model for its
language) is not tried again.

**Names and words.** Whisper writes what it hears, and spells names as it guesses. **Names and words** in **Settings →
Processing → Transcripts** are the church's names, places and words (at most 300), spelled as they should be: they start
whisper's prompt for every part, followed by the last words of the part before, which helps it spell them right. A
transcript waiting keeps the words it was asked with.

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
- While one waits, it says how many are ahead; while one is made, a bar shows how far it is. ✕ stops it or takes it
  out of the queue.
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
