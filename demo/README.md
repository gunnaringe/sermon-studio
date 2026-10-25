# The demo's recording, intro and outro

The public demo at <https://demo.sermon-studio.app> ([A public demo](../docs/access.md#a-public-demo)) has one
church with one recording and a default intro and outro. These scripts make them; the audio is not kept in the
repository. The tools come from `mise.toml` here: run `mise install` in this folder first.

| Script | Makes | |
|---|---|---|
| `make-recording.sh` | `service.flac`, 16 min | President Kennedy's inaugural address as a service: a minute of quiet before and after it, and faint room noise under all of it |
| `make-jingles.sh` | `intro.flac`, 12 s | music, and "This is a speech by John F. Kennedy." |
| | `outro.flac`, 14 s | music, and "Thank you for listening." |

They write to `~/.cache/sermon-studio-samples/` (`OUT=<folder>` for another), where the speech and the voice are
downloaded once too.

## How they are made

- **The recording:** the speech (1961-01-20, about 14 minutes, public domain, from Wikimedia Commons), with a minute
  of quiet added before and after it. Under all of it is generated room noise: brown noise, 60 Hz–4 kHz, at about
  −62 dBFS. That is below the speech's own noise, so the studio still finds the first and last minute as silences
  and proposes the speech as the sermon; louder, the silences break up. The demo's deployment makes the same file
  the same way on its own volume.
- **The music** is synthesized with ffmpeg from the expressions `jingle.py` writes: plucked chords, two seconds
  each (the intro C, Am, F, G to C; the outro F, G, Am, F to C), over a quiet pad, with a little echo and a fade.
- **The voice** is [Piper](https://github.com/OHF-Voice/piper1-gpl) with the
  [`en_US-joe-medium`](https://huggingface.co/rhasspy/piper-voices/tree/main/en/en_US/joe/medium) voice, whose
  dataset is CC0. It starts after a second, and the music is ducked under it. The clips are about −21 LUFS.

The demo's deployment does not have Piper: the intro and outro are copied onto its volume by hand
(`clips/intro.flac`, `clips/outro.flac`), and it puts them in the church as an upload would.

## Licence

The speech is in the public domain, the music and the room noise are made here, and the voice's dataset is CC0, so
the files have no terms of their own. Piper's licence (GPL) covers Piper, not what it reads aloud.
