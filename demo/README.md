# The demo's intro and outro

The public demo at <https://demo.sermon-studio.app> (docs/access.md, [A public demo](../docs/access.md#a-public-demo))
has a church with a default intro and outro, so visitors can hear a join and make files with and without them:

| File | Length | What it says |
|---|---|---|
| `intro.flac` | 12 s | "Welcome to Demo Church. Here is this Sunday's sermon." |
| `outro.flac` | 14 s | "Thank you for listening. You will find more sermons from Demo Church online." |

Both are about −22 LUFS, 44.1 kHz mono FLAC. The demo's deployment downloads them from here when its volume
does not have them yet, and puts them in the church as an upload would.

## How they are made

`make-jingles.sh` makes them again (see the script for what it needs):

- **The music** is synthesized with ffmpeg from the expressions `jingle.py` writes: plucked chords, two seconds
  each (the intro C, Am, F, G to C; the outro F, G, Am, F to C), over a quiet pad, with a little echo and a fade.
- **The voice** is [Piper](https://github.com/OHF-Voice/piper1-gpl) with the
  [`en_US-joe-medium`](https://huggingface.co/rhasspy/piper-voices/tree/main/en/en_US/joe/medium) voice, whose
  dataset is CC0. It starts after a second, and the music is ducked under it.

## Licence

The music is made here, and the voice's dataset is CC0, so the clips have no terms of their own beyond this
repository's licence. Piper's licence (GPL) covers Piper, not what it reads aloud.

The demo's recording, President Kennedy's inaugural address (public domain, from Wikimedia Commons), is not kept
here: the deployment downloads it.
