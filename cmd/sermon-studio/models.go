package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/gunnaringe/sermon-studio/models"
)

// modelsCmd keeps the models folder (package models), apart from the studio: as a step before it, or
// beside it with --every (a second container), so a download never holds up the studio.
type modelsCmd struct {
	Pull modelsPullCmd `cmd:"" help:"Download the models given, and keep them up to date with Hugging Face."`
	List modelsListCmd `cmd:"" help:"List the models in the folder."`
}

type modelsPullCmd struct {
	Dir   string        `required:"" help:"The models folder (the studio's --models-dir)."`
	Model []string      `required:"" placeholder:"LANGS=MODEL" help:"A model and its languages: nb,nn=NbAiLab/nb-whisper-medium/ggml-model-q5_0.bin (owner/name/file, at main), or the file's URL on Hugging Face (…/resolve/<revision>/<file>). * as the language: any (whisper finds it). Again for more; the first for a language is used."`
	Every time.Duration `help:"Look again this often (e.g. 24h) and keep running; a model that has changed is downloaded and used from the next transcript."`

	out io.Writer // os.Stderr; tests set it
}

func (c *modelsPullCmd) Run() error {
	var specs []models.Spec
	for _, s := range c.Model {
		sp, err := models.ParseSpec(s)
		if err != nil {
			return err
		}
		specs = append(specs, sp)
	}
	out := c.out
	if out == nil {
		out = os.Stderr
	}
	p := &models.Puller{Dir: c.Dir, Logger: log.New(out, "", log.LstdFlags), Client: &http.Client{Timeout: 0}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		err := p.Pull(ctx, specs)
		if c.Every <= 0 {
			return err
		}
		if err != nil {
			p.Logger.Printf("%v (again in %s)", err, c.Every)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(c.Every):
		}
	}
}

type modelsListCmd struct {
	Dir  string `required:"" help:"The models folder (the studio's --models-dir)."`
	JSON bool   `name:"json" help:"Print models.json."`

	out io.Writer // os.Stdout; tests set it
}

func (c *modelsListCmd) Run() error {
	out := c.out
	if out == nil {
		out = os.Stdout
	}
	m, err := models.Read(c.Dir)
	if err != nil {
		return err
	}
	if c.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(m)
	}
	installed := map[string]bool{}
	for _, x := range m.Installed(c.Dir) {
		installed[x.File] = true
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tLANGUAGES\tDATE\tSIZE\tFILE")
	for _, x := range m.Models {
		date, size := "", ""
		if x.Date != nil {
			date = x.Date.Format("2006-01-02")
		}
		if x.Size > 0 {
			size = fmt.Sprintf("%d MB", x.Size>>20)
		}
		file := x.File
		if !installed[x.File] {
			file += " (missing)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", x.Name, strings.Join(x.Languages, ","), date, size, file)
	}
	return tw.Flush()
}
