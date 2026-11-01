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

// speechModelFile is the speech model as a file, as arnndn reads it: written once into the temporary
// folder, under a name from its content.
func speechModelFile() (string, error) {
	path := filepath.Join(os.TempDir(), "sermon-studio-"+modelKey()+".rnnn")
	if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, speechModel) {
		return path, nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".sermon-studio-*.rnnn")
	if err != nil {
		return "", fmt.Errorf("the speech model: %w", err)
	}
	_, err = f.Write(speechModel)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = Rename(f.Name(), path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("the speech model: %w", err)
	}
	return path, nil
}

// modelKey names the model's file: the start of its hash.
func modelKey() string {
	sum := sha256.Sum256(speechModel)
	return hex.EncodeToString(sum[:6])
}

// filterPath escapes a path for an option of a filter in a filter graph: once for the option (':' ends
// it) and once for the graph (',' ';' '[' ']'). Forward slashes, which Windows takes too, keep it short.
func filterPath(path string) string {
	path = filepath.ToSlash(path)
	option := strings.NewReplacer(`\`, `\\`, `'`, `\'`, `:`, `\:`).Replace(path)
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`, `,`, `\,`, `;`, `\;`, `[`, `\[`, `]`, `\]`).Replace(option)
}
