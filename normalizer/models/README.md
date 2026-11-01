# Models

`speech.rnnn` is the noise reduction for speech (the processing's noise reduction "Speech", ffmpeg's
`arnndn`): the model `somnolent-hogwash-2018-09-01/sh.rnnn` from
[GregorR/rnnoise-models](https://github.com/GregorR/rnnoise-models), trained for speech in a recording
with steady noise (fans, ventilation, computers). Its README says the models are not creative work
and so not subject to copyright. Unchanged; sha256
`70bb6685eb0c2a1d18e2918dca3fbfbd39317010b1802eb1b6ea73a92f3fdec0`.

`silero-vad.bin` finds where there is speech, for transcripts (the whisper filter's `vad_model`, `transcribe.go`):
Silero VAD v5.1.2 from [snakers4/silero-vad](https://github.com/snakers4/silero-vad) (MIT, `LICENSE-silero-vad`), converted to
whisper.cpp's format: `ggml-silero-v5.1.2.bin` from
[ggml-org/whisper-vad](https://huggingface.co/ggml-org/whisper-vad). Unchanged; sha256
`29940d98d42b91fbd05ce489f3ecf7c72f0a42f027e4875919a28fb4c04ea2cf`.

The speech recognition models themselves (whisper, NB-Whisper, KB-Whisper ...) are not part of Sermon Studio:
`sermon-studio models pull` downloads the ones a studio is given, under their own licences.
