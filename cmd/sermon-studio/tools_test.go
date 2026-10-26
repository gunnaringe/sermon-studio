package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The Go version is named in several places: mise.toml (the host), go.mod, the images (scripts/lib.sh, the Dockerfile)
// and CI. They name the same minor version, so a test on the host means what it means in CI and in the image.
func TestToolVersions(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "mise.toml")); err != nil {
		t.Skip("not in a whole checkout (the image build copies only the code)")
	}
	minorOf := func(file, pattern string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(pattern).FindStringSubmatch(string(b))
		if m == nil {
			t.Fatalf("%s: no Go version (%s)", file, pattern)
		}
		return m[1]
	}
	want := minorOf("mise.toml", `(?m)^go = "(\d+\.\d+)\.\d+"`)
	for _, c := range []struct{ file, pattern string }{
		{"go.mod", `(?m)^go (\d+\.\d+)\.\d+$`},
		{"scripts/lib.sh", `golang:(\d+\.\d+)-alpine`},
		{"Dockerfile", `golang:(\d+\.\d+)-alpine`},
		{".github/workflows/test.yml", `go-version: '(\d+\.\d+)\.x'`},
		{".github/workflows/release.yml", `go-version: '(\d+\.\d+)\.x'`},
	} {
		if got := minorOf(c.file, c.pattern); got != want {
			t.Errorf("%s has Go %s, mise.toml %s", c.file, got, want)
		}
	}
}
