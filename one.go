package one

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/russross/blackfriday/v2"
)

type One struct {
	http.Handler
	options Options
	site    Site
	funcs   template.FuncMap
	entries map[string][]*Post
}

func (o One) Load(r io.Reader) error {
	dec := json.NewDecoder(r)
	return dec.Decode(&o.site.Metadata)
}

func (o One) Dump(w io.Writer) error {
	enc := json.NewEncoder(w)
	return enc.Encode(&o.site.Metadata)
}

func (o *One) Bundle(ctx context.Context) error {
	log.Printf("Building %s", o.options.EntryDir)

	entries := []string{
		filepath.Join(o.options.ThemeDir, o.options.EntryDir, "index.css"),
		filepath.Join(o.options.ThemeDir, o.options.EntryDir, "index.ts"),
	}

	result := api.Build(api.BuildOptions{
		EntryPoints:       entries,
		EntryNames:        "[dir]/[name]-[hash]",
		Outdir:            o.options.OutputDir,
		Bundle:            true,
		Write:             true,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		TreeShaking:       api.TreeShakingTrue,
		Loader: map[string]api.Loader{
			".ttf": api.LoaderFile,
			".png": api.LoaderBase64,
		},
	})
	if len(result.Errors) > 0 {
		for _, err := range result.Errors {
			log.Print(err) // TODO: log error correctly
		}
		return errors.New("bundle error")
	}
	for _, file := range result.OutputFiles {
		path := filepath.Join("/", filepath.Base(file.Path))
		if strings.HasSuffix(file.Path, ".css") {
			o.site.Styles = append(o.site.Styles, path)
		}
		if strings.HasSuffix(file.Path, ".js") {
			o.site.Scripts = append(o.site.Scripts, path)
		}
	}
	return nil
}

func (o One) Generate(ctx context.Context) error {
	err := filepath.WalkDir(o.options.SourceDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			outdir := strings.Replace(path, o.options.SourceDir, o.options.OutputDir, 1)
			return os.MkdirAll(outdir, 0744)
		}
		return o.generate(ctx, path, entry)
	})
	if err != nil {
		return err
	}
	if err = o.generateList(ctx); err != nil {
		return err
	}
	if len(o.options.RSSFeed) > 0 {
		return o.generateRSSFeed(ctx)
	}
	return nil
}

func (o *One) generate(ctx context.Context, path string, entry fs.DirEntry) error {
	log.Printf("Generating %s", path)

	opts := o.options
	name := entry.Name()
	use := "post.html"
	escape := includes(name, opts.Escape)
	if escape {
		use = strings.Replace(name, "md", "html", 1)
	}
	layout := filepath.Join(opts.ThemeDir, "layout.html")
	page := filepath.Join(opts.ThemeDir, "pages", use)
	t, err := template.New("layout").Funcs(o.funcs).ParseFiles(layout, page)
	if err != nil {
		return err
	}

	ext := filepath.Ext(path)
	outdir := strings.Replace(path[:len(path)-len(ext)], opts.SourceDir, opts.OutputDir, 1)
	outfile := outdir + ".html"
	if !escape {
		if err = os.MkdirAll(outdir, 0744); err != nil {
			return err
		}
		outfile = filepath.Join(outdir, "index.html")
	}

	out, err := os.Create(outfile)
	if err != nil {
		return err
	}
	defer out.Close()

	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := entry.Info()
	if err != nil {
		return err
	}

	rawMD, err := io.ReadAll(in)
	if err != nil {
		return err
	}

	// TODO: split out front matter from rawMD

	rawHTML := blackfriday.Run(rawMD)

	post := &Post{
		Site:     o.site,
		Name:     info.Name(),
		Size:     info.Size(),
		Path:     path,
		HTML:     template.HTML(rawHTML),
		Markdown: string(rawMD),
		Metadata: Metadata{}, // TODO: extract from frontmatter
	}

	dir := filepath.Dir(path)
	o.entries[dir] = append(o.entries[dir], post)
	o.site.Posts = append(o.site.Posts, post)
	return t.ExecuteTemplate(out, "layout", post)
}

func (o One) generateList(ctx context.Context) error {
	var outdir string
	var post *Post // for xxx/index.md

	opts := o.options
	layout := filepath.Join(o.options.ThemeDir, "layout.html")
	page := filepath.Join(o.options.ThemeDir, opts.TemplateDir, "list.html")
	t, err := template.New("layout").Funcs(o.funcs).ParseFiles(layout, page)
	if err != nil {
		return err
	}

	for k, v := range o.entries {
		if k == o.options.SourceDir {
			// we don't need to generate a list page for where the home page is.
			continue
		}
		outdir = strings.Replace(k, o.options.SourceDir, o.options.OutputDir, 1)
		out, err := os.Create(filepath.Join(outdir, "index.html"))
		if err != nil {
			return err
		}

		for _, p := range v {
			if p.Name == "index.md" {
				post = p
				break
			}
		}

		if post == nil {
			post = &Post{
				Name: filepath.Base(k),
				Path: k,
			}
		}

		list := &List{
			Site:  o.site,
			Post:  post,
			Posts: v,
		}
		if err = t.ExecuteTemplate(out, "layout", list); err != nil {
			return err
		}
	}
	return nil
}

func (o *One) generateRSSFeed(ctx context.Context) error {
	t, err := template.ParseFiles(filepath.Join(o.options.ThemeDir, o.options.RSSFeed))
	if err != nil {
		return err
	}
	w, err := os.Create(filepath.Join(o.options.OutputDir, o.options.RSSFeed))
	if err != nil {
		return err
	}
	defer w.Close()
	return t.Execute(w, o.site)
}

func (o One) partial(path string, args interface{}) template.HTML {
	w := bytes.Buffer{}
	t := template.Must(template.ParseFiles(filepath.Join(o.options.ThemeDir, path)))
	if err := t.Execute(&w, args); err != nil {
		panic(err)
	}
	return template.HTML(w.Bytes())
}

func New(opts ...Option) *One {
	options := Options{
		Escape: []string{
			"index.md",
		},
	}
	for _, o := range opts {
		o(&options)
	}

	o := &One{
		options: options,
		site:    Site{},
		entries: make(map[string][]*Post),
		Handler: http.FileServer(http.Dir(options.OutputDir)),
	}

	o.funcs = template.FuncMap{
		"partial": o.partial,
	}
	return o
}

func includes(s string, a []string) bool {
	for _, v := range a {
		if s == v {
			return true
		}
	}
	return false
}

type Metadata map[string]interface{}

type Site struct {
	Styles   []string `json:"styles"`
	Scripts  []string `json:"scripts"`
	Posts    []*Post  `json:"posts"`
	Metadata Metadata `json:"metadata"` // extracted from config file like `one.yml`
}

type Post struct {
	Site     Site          `json:"site"`
	Name     string        `json:"name"`
	Path     string        `json:"path"`
	HTML     template.HTML `json:"html"`
	Markdown string        `json:"markdown"`
	Metadata Metadata      `json:"metadata"`
	Size     int64         `json:"size"`
}

type List struct {
	*Post
	Site  Site    `json:"site"`
	Posts []*Post `json:"posts"`
}

type Options struct {
	SourceDir   string
	ThemeDir    string
	OutputDir   string
	TemplateDir string   // e.g. pages
	EntryDir    string   // e.g. lib
	RSSFeed     string   // e.g. index.xml
	Escape      []string // e.g. index.md
}

type Option func(*Options)

func WithSourceDir(path string) Option {
	return func(o *Options) {
		o.SourceDir = path
	}
}

func WithThemeDir(path string) Option {
	return func(o *Options) {
		o.ThemeDir = path
	}
}

func WithOutputDir(path string) Option {
	return func(o *Options) {
		o.OutputDir = path
	}
}

func WithEscape(names ...string) Option {
	return func(o *Options) {
		o.Escape = names
	}
}

func WithEntryDir(path string) Option {
	return func(o *Options) {
		o.EntryDir = path
	}
}

func WithTemplateDir(path string) Option {
	return func(o *Options) {
		o.TemplateDir = path
	}
}

func WithRSSFeed(name string) Option {
	return func(o *Options) {
		o.RSSFeed = name
	}
}
