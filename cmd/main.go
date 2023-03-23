package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/mivinci/cli"
	"github.com/xoolab/one"
)

const (
	banner = `
 ___  ___  ___ 
/ _ \/ _ \/ -_)
\___/_//_/\__/

A hybrid customizable blog platform.
`
	example = `one . -c one.yml
  one . -c https://example.com/one.yml`
)

var (
	cmd = cli.Command{
		Name:    "one",
		Usage:   banner,
		Example: example,
		Run:     run,
		Args:    []string{"path"},
		Flags: []*cli.Flag{
			{
				Name:  "config",
				Short: 'c',
				Usage: "location to config file",
				Value: cli.String("config"),
			},
			{
				Name:  "theme",
				Usage: "location to theme",
				Value: cli.String("scratch"),
			},
			{
				Name:  "output",
				Short: 'o',
				Usage: "location to output",
				Value: cli.String("dist"),
			},
			{
				Name:  "entry",
				Usage: "location to entry files",
				Value: cli.String("lib"),
			},
			{
				Name:  "template",
				Usage: "location to template",
				Value: cli.String("pages"),
			},
			{
				Name:  "feed",
				Usage: "location to a RSS feed template",
				Value: cli.String("atom.xml"),
			},
		},
	}
)

func run(ctx *cli.Context) error {
	oh := one.New(
		one.WithSourceDir(ctx.Args()[0]),
		one.WithThemeDir(filepath.Join("themes", ctx.String("theme"))),
		one.WithOutputDir(ctx.String("output")),
		one.WithEntryDir(ctx.String("entry")),
		one.WithTemplateDir(ctx.String("template")),
		one.WithRSSFeed(ctx.String("feed")),
	)

	now := time.Now()

	err := oh.Bundle(ctx)
	if err != nil {
		panic(err)
	}

	err = oh.Generate(ctx)
	if err != nil {
		panic(err)
	}

	log.Printf("Finished in %s", time.Since(now))
	return nil
}

func main() {
	log.SetFlags(log.Lmsgprefix)

	if err := cmd.Exec(os.Args); err != nil {
		log.Fatal(err)
	}
}
