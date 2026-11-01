package normalizer

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NoiseMethods are the ways noise can be taken out: "standard" (afftdn, by the noise's spectrum) and
// "speech" (arnndn, a small neural network trained to keep speech; see models/README.md).
var NoiseMethods = []string{"standard", "speech"}

// speechModel is the network arnndn uses for "speech": rnnoise-nu's somnolent-hogwash, for speech in
// a recording with steady noise (fans, ventilation, hum).
//
//go:embed models/speech.rnnn
var speechModel []byte

// speechFilter is arnndn with the speech model. RoomSound mixes that much of the sound before it back in.
func (p *Pipeline) speechFilter() (string, error) {
	model, err := speechModelFile()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("arnndn=m=%s:mix=%.2f", filterPath(model), 1-float64(p.Opt.RoomSound)/100), nil
}

// speechModelFile is the speech model as a file, as arnndn reads it (embeddedFile).
func speechModelFile() (string, error) {
	path, err := embeddedFile("speech", ".rnnn", speechModel)
	if err != nil {
		return "", fmt.Errorf("the speech model: %w", err)
	}
	return path, nil
}

// embeddedFile is a model built into the program as a file, as ffmpeg's filters read them: written once
// into the temporary folder, under a name from its content (sermon-studio-<name>-<hash><ext>).
func embeddedFile(name, ext string, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	path := filepath.Join(os.TempDir(), "sermon-studio-"+name+"-"+hex.EncodeToString(sum[:6])+ext)
	if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, data) {
		return path, nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".sermon-studio-*"+ext)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = Rename(f.Name(), path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return path, nil
}

// filterPath escapes a path for an option of a filter in a filter graph: once for the option (':' ends
// it) and once for the graph (',' ';' '[' ']'). Forward slashes, which Windows takes too, keep it short.
func filterPath(path string) string {
	path = filepath.ToSlash(path)
	option := strings.NewReplacer(`\`, `\\`, `'`, `\'`, `:`, `\:`).Replace(path)
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`, `,`, `\,`, `;`, `\;`, `[`, `\[`, `]`, `\]`).Replace(option)
}
