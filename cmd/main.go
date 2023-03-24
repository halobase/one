package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mivinci/cli"
	"github.com/xoolab/one"
)

const (
	banner = `
 ___  ___  ___ 
/ _ \/ _ \/ -_)
\___/_//_/\__/

A hybrid customizable blog platform.`

	example = `one . -c one.yml
  one . -c https://example.com/one.yml
  one README.md`
)

var (
	cmd = cli.Command{
		Name:    "one",
		Usage:   banner,
		Example: example,
		Run:     Gen,
		Args:    []string{"path"},
		Flags: []*cli.Flag{
			{
				Name:  "config",
				Short: 'c',
				Usage: "specify config file",
				Value: cli.String(""),
			},
			{
				Name:  "output",
				Short: 'o',
				Usage: "specify output",
				Value: cli.String("dist"),
			},
			{
				Name:  "theme",
				Short: 't',
				Usage: "specify theme",
				Value: cli.String("scratch"),
			},
			{
				Name:  "force",
				Usage: "force rewrite output",
				Value: cli.Bool(false),
			},
			{
				Name:  "disable-minify",
				Usage: "disable minifying output files",
				Value: cli.Bool(false),
			},
			{
				Name:  "disable-hash",
				Usage: "disable using hash output files names",
				Value: cli.Bool(false),
			},
			{
				Name:  "disable-treeshaking",
				Usage: "disable purging used code",
				Value: cli.Bool(false),
			},
		},
	}

	dev = cli.Command{
		Name:  "dev",
		Usage: "run in development mode",
		Run:   Dev,
		Flags: []*cli.Flag{},
	}

	serve = cli.Command{
		Name:  "serve",
		Usage: "run in production mode",
	}
)

func init() {
	cmd.Add(&dev, &serve)
}

func Gen(ctx *cli.Context) error {
	output := ctx.String("output")
	force := ctx.Bool("force")
	if _, err := os.Stat(output); err == nil {
		// output directory exists
		if !force {
			return os.ErrExist
		}
		if err = os.RemoveAll(output); err != nil {
			return err
		}
	}
	cfg, err := openConfig(ctx)
	if err != nil {
		return err
	}
	defer cfg.Close()

	theme := ctx.String("theme")
	oh := one.New(
		one.WithSourceDir(ctx.Args()[0]),
		one.WithThemeDir(theme),
		one.WithOutputDir(output),
		one.WithMinify(!ctx.Bool("disable-minify")),
		one.WithHash(!ctx.Bool("disable-hash")),
		one.WithTreeShaking(!ctx.Bool("disable-treeshaking")),
	)

	log.Printf("Building with %s ...", theme)

	now := time.Now()

	if err := oh.Load(cfg); err != nil {
		return err
	}
	if err = oh.Bundle(ctx); err != nil {
		return err
	}
	if err = oh.Generate(ctx); err != nil {
		return err
	}

	log.Printf("Finished in %s", time.Since(now))
	return nil
}

func openConfig(ctx *cli.Context) (io.ReadCloser, error) {
	theme := ctx.String("theme")
	custom := ctx.String("config")
	if len(custom) > 0 {
		if strings.HasPrefix(custom, "https://") || strings.HasPrefix(custom, "http://") {
			res, err := http.Get(custom)
			if err != nil {
				return nil, err
			}
			return res.Body, nil
		}
		return os.Open(custom)
	}

	var chosen string
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	paths := []string{
		"one.yml",
		"one.yaml",
		filepath.Join(home, ".one", "one.yml"),
		filepath.Join(home, ".one", "one.yaml"),
		filepath.Join(theme, "one.yml"),
		filepath.Join(theme, "one.yaml"),
	}

	for _, path := range paths {
		if _, err = os.Stat(path); err == nil {
			chosen = path
			break
		}
	}
	if len(chosen) == 0 {
		return nil, os.ErrNotExist
	}
	return os.Open(chosen)
}

func Dev(ctx *cli.Context) error {
	return nil
}

func main() {
	log.SetFlags(log.Lmsgprefix)

	if err := cmd.Exec(os.Args); err != nil {
		log.Fatal(err)
	}
}
